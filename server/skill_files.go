package server

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// validSkillName checks the agentskills.io name constraints, widened so a skill can
// also be named in Chinese (or any other script): ASCII must stay lowercase
// alphanumeric + hyphens, while non-ASCII letters/digits are accepted as-is.
// 1-64 runes, must start with a letter, no leading/trailing/consecutive hyphens.
// The name doubles as a directory name under skillDir, so nothing that could carry a
// path (separators, dots, spaces, control characters) is allowed through.
func validSkillName(name string) bool {
	if name == "" || !utf8.ValidString(name) {
		return false
	}
	rs := []rune(name)
	if len(rs) > 64 {
		return false
	}
	isLetter := func(r rune) bool {
		return (r >= 'a' && r <= 'z') || (r > unicode.MaxASCII && unicode.IsLetter(r))
	}
	if !isLetter(rs[0]) || rs[len(rs)-1] == '-' {
		return false
	}
	for _, r := range rs {
		switch {
		case isLetter(r), r >= '0' && r <= '9', r == '-':
		case r > unicode.MaxASCII && unicode.IsDigit(r):
		default:
			return false
		}
	}
	return !strings.Contains(name, "--")
}

// skillPathBlocked lists the ASCII characters a skill-relative path may not contain.
// '\' is a Windows separator; '%' keeps double-URL-encoding tricks from surviving
// (Go's net/http URL-decodes PathValue once — %2F→/ %2e→. — so an attacker sending
// %252e%252e arrives here as the literal "%2e%2e" and is rejected on the '%'); '#'
// and '?' would truncate the path when it travels back through a URL; the rest are
// reserved on Windows filesystems.
const skillPathBlocked = `\%#?*:"<>|`

// skillPathRune reports whether r may appear in a client-supplied skill path.
// It is a blacklist over Unicode rather than an ASCII whitelist so that 中文 (and any
// other script) file names work, while everything that makes path validation hard is
// still refused: control/format characters, look-alike whitespace, separators.
func skillPathRune(r rune) bool {
	switch {
	case r < 0x20, r == 0x7f, r == utf8.RuneError:
		return false // NUL & control characters, invalid UTF-8
	case strings.ContainsRune(skillPathBlocked, r):
		return false
	case unicode.Is(unicode.Cf, r), unicode.Is(unicode.Co, r), unicode.Is(unicode.Cs, r):
		return false // zero-width joiners, bidi overrides (RLO 文件名伪装), private use
	case r != ' ' && unicode.IsSpace(r):
		return false // NBSP / 全角空格 之类：看着是空格，其实不是
	}
	return true
}

// maxSkillPathLen caps a relative path so a pathological name can't reach the syscall.
const maxSkillPathLen = 512

// skillRelPath validates a relative file path supplied by the client.
// Returns the cleaned path and an empty error string on success.
// Validation order matters: character check first (before Clean) so encoding tricks
// can't survive normalization.
func skillRelPath(file string) (string, string) {
	// 1. Character check — before normalization. Defeats null bytes, backslash,
	//    '%', control characters and unicode look-alikes; CJK names pass through.
	if file == "" || len(file) > maxSkillPathLen {
		return "", "invalid path: empty or too long"
	}
	if !utf8.ValidString(file) {
		return "", "invalid path: not valid UTF-8"
	}
	for _, r := range file {
		if !skillPathRune(r) {
			return "", "invalid path: illegal character " + strconv.QuoteRune(r)
		}
	}
	// 2. Reject ".." explicitly. With the whitelist above, encoding bypass is already
	//    impossible, but we keep this check so the intent is obvious to reviewers.
	if strings.Contains(file, "..") {
		return "", "invalid path: '..' not allowed"
	}
	// 3. Reject leading slash and empty segments (double slash).
	//    Leading slash would survive filepath.Clean as an absolute path.
	if strings.HasPrefix(file, "/") || strings.Contains(file, "//") {
		return "", "invalid path: must be relative with no empty segments"
	}
	// 4. Normalize and final safety re-check after Clean.
	//    filepath.Clean removes redundant separators and resolves single dots.
	clean := filepath.Clean(file)
	if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", "invalid path"
	}
	return filepath.ToSlash(clean), ""
}

// walkSkillFiles returns all files under root (relative to root), sorted,
// including files in subdirectories (scripts/, references/, assets/, etc.).
// walkSkillFiles returns all entries under root relative to root.
// Directories are included with a trailing "/" so the frontend can distinguish
// them from files and render empty folders in the tree.
// walkSkillFiles 列出 root 下的所有条目。root 是一个 *os.Root，遍历时同时做两件事：
// (1) 用 Lstat 不跟随链接，符号链接只作为一条记录出现、不会被展开到树外；
// (2) 链接目标越出 root 时跳过它。
//
// 早先这里用 filepath.WalkDir：它对指向目录的软链接会跟进去，把 skill 目录外
// （甚至宿主机上）的整棵子树列进文件列表。
func walkSkillFiles(root *os.Root) ([]string, error) {
	var entries []string
	err := fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if p == "." {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			// 不展开链接：越界的直接丢掉，越界判定交给 root.Lstat 解析。
			if _, lerr := root.Lstat(p); lerr != nil {
				return nil
			}
			entries = append(entries, path.Clean(p))
			return nil
		}
		if d.IsDir() {
			entries = append(entries, path.Clean(p)+"/")
		} else {
			entries = append(entries, path.Clean(p))
		}
		return nil
	})
	return entries, err
}

// skillRoot pins skillDir for the lifetime of one request. Every file operation
// then goes through os.Root, which refuses any name (or symlink target) that
// resolves outside the tree.
//
// 早先这些 handler 用 filepath.Join + os.ReadFile/WriteFile/RemoveAll，纯词法约束。
// skillRelPath 的字符白名单挡得住“..”穿越，但挡不住已存在的符号链接：目录里只要
// 有一个指向外部的链接，读写就穿出 skillDir。workspace.go 已经改成 os.Root，
// skill 这一套当时没跟上，这里是补齐。
func (s *Server) skillRoot(w http.ResponseWriter) (*os.Root, bool) {
	root, err := os.OpenRoot(s.skillDir)
	if err != nil {
		writeErr(w, 500, "无法打开 skill 目录："+err.Error())
		return nil, false
	}
	return root, true
}

// skillNotFound 把 os.Root 返回的“不存在”与“越界”区分成对应的 HTTP 状态。
// os.Root 对越界访问报的是 EPERM 风格的错误，直接当 404 处理会把安全拦截
// 伪装成“文件不存在”，让用户无法判断是路径写错还是被拦下。
func skillNotFound(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}

func (s *Server) fsListFiles(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validSkillName(name) {
		writeErr(w, 400, "invalid skill name")
		return
	}
	root, ok := s.skillRoot(w)
	if !ok {
		return
	}
	defer root.Close()
	if fi, err := root.Stat(name); err != nil || !fi.IsDir() {
		writeErr(w, 404, "skill not found")
		return
	}
	skill, err := root.OpenRoot(name)
	if err != nil {
		writeErr(w, 404, "skill not found")
		return
	}
	defer skill.Close()
	files, err := walkSkillFiles(skill)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if files == nil {
		files = []string{}
	}
	writeJSON(w, 200, map[string]any{"files": files})
}

func (s *Server) fsReadFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validSkillName(name) {
		writeErr(w, 400, "invalid skill name")
		return
	}
	file, errMsg := skillRelPath(r.PathValue("file"))
	if errMsg != "" {
		writeErr(w, 400, errMsg)
		return
	}
	root, ok := s.skillRoot(w)
	if !ok {
		return
	}
	defer root.Close()
	data, err := root.ReadFile(filepath.Join(name, filepath.FromSlash(file)))
	if err != nil {
		if skillNotFound(err) {
			writeErr(w, 404, "file not found")
			return
		}
		writeErr(w, 403, "拒绝访问：路径越出 skill 目录（可能经过符号链接）")
		return
	}
	writeJSON(w, 200, map[string]any{"content": string(data), "file": file})
}

func (s *Server) fsWriteFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validSkillName(name) {
		writeErr(w, 400, "invalid skill name")
		return
	}
	file, errMsg := skillRelPath(r.PathValue("file"))
	if errMsg != "" {
		writeErr(w, 400, errMsg)
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	root, ok := s.skillRoot(w)
	if !ok {
		return
	}
	defer root.Close()
	if fi, err := root.Stat(name); err != nil || !fi.IsDir() {
		writeErr(w, 404, "skill not found")
		return
	}
	full := filepath.Join(name, filepath.FromSlash(file))
	// create parent subdirectory if needed (e.g. scripts/, references/, assets/)
	if dir := filepath.Dir(full); dir != name {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			writeErr(w, 403, "拒绝写入：路径越出 skill 目录（可能经过符号链接）")
			return
		}
	}
	if err := root.WriteFile(full, []byte(body.Content), 0o644); err != nil {
		writeErr(w, 403, "拒绝写入：路径越出 skill 目录（可能经过符号链接）")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) fsCreateDir(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validSkillName(name) {
		writeErr(w, 400, "invalid skill name")
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	dir, errMsg := skillRelPath(body.Path)
	if errMsg != "" {
		writeErr(w, 400, errMsg)
		return
	}
	root, ok := s.skillRoot(w)
	if !ok {
		return
	}
	defer root.Close()
	if fi, err := root.Stat(name); err != nil || !fi.IsDir() {
		writeErr(w, 404, "skill not found")
		return
	}
	if err := root.MkdirAll(filepath.Join(name, filepath.FromSlash(dir)), 0o755); err != nil {
		writeErr(w, 403, "拒绝创建：路径越出 skill 目录（可能经过符号链接）")
		return
	}
	writeJSON(w, 201, map[string]any{"dir": dir})
}

func (s *Server) fsDeletePath(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validSkillName(name) {
		writeErr(w, 400, "invalid skill name")
		return
	}
	file, errMsg := skillRelPath(r.PathValue("file"))
	if errMsg != "" {
		writeErr(w, 400, errMsg)
		return
	}
	root, ok := s.skillRoot(w)
	if !ok {
		return
	}
	defer root.Close()
	full := filepath.Join(name, filepath.FromSlash(file))
	// Lstat（不跟随链接）先确认存在；RemoveAll 在 os.Root 上只会删 root 内的东西，
	// 链接本身被删除，不会递归到它的目标。
	if _, err := root.Lstat(full); err != nil {
		if skillNotFound(err) {
			writeErr(w, 404, "not found")
			return
		}
		writeErr(w, 403, "拒绝删除：路径越出 skill 目录（可能经过符号链接）")
		return
	}
	if err := root.RemoveAll(full); err != nil {
		writeErr(w, 403, "拒绝删除：路径越出 skill 目录（可能经过符号链接）")
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": file})
}
