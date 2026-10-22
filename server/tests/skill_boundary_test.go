//go:build ignore

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skillFixture 建一个 skillDir，里面有一个合法 skill 和一个指向外部目录的符号链接。
// 返回 (server, token, outsideDir)。
func skillFixture(t *testing.T) (*Server, string, string) {
	t.Helper()
	base := t.TempDir()
	skills := filepath.Join(base, "skills")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{skills, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// 合法 skill
	if err := os.MkdirAll(filepath.Join(skills, "good"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skills, "good", "SKILL.md"), []byte("---\nname: good\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(skills, "good", "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skills, "good", "scripts", "a.py"), []byte("print(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 外部机密
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside-marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	// skill 内的逃逸链接：good/link -> outside
	if err := os.Symlink(outside, filepath.Join(skills, "good", "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	s := &Server{m: &Manager{dir: base}, skillDir: skills, jwtKey: []byte("skill-test-key-32-bytes-long-0000")}
	token, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	return s, token, outside
}

func skillReq(s *Server, token string, h http.HandlerFunc, req *http.Request) *httptest.ResponseRecorder {
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.requireAuth(h).ServeHTTP(w, req)
	return w
}

// 读：经符号链接读到外部文件必须被拒。
func TestSkillReadRejectsSymlinkEscape(t *testing.T) {
	s, token, outside := skillFixture(t)
	req := httptest.NewRequest("GET", "/api/skills/good/files/link/secret.txt", nil)
	req.SetPathValue("name", "good")
	req.SetPathValue("file", "link/secret.txt")
	w := skillReq(s, token, s.fsReadFile, req)
	if w.Code < 400 {
		t.Fatalf("symlink escape allowed: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "outside-marker") {
		t.Fatal("outside file content leaked")
	}
	// 外部文件未被改动
	if b, err := os.ReadFile(filepath.Join(outside, "secret.txt")); err != nil || string(b) != "outside-marker" {
		t.Fatalf("outside file touched: %q %v", b, err)
	}
}

// 写：经符号链接写到外部必须被拒。
func TestSkillWriteRejectsSymlinkEscape(t *testing.T) {
	s, token, outside := skillFixture(t)
	body := strings.NewReader(`{"content":"pwned"}`)
	req := httptest.NewRequest("POST", "/api/skills/good/files/link/secret.txt", body)
	req.SetPathValue("name", "good")
	req.SetPathValue("file", "link/secret.txt")
	w := skillReq(s, token, s.fsWriteFile, req)
	if w.Code < 400 {
		t.Fatalf("symlink write allowed: %d %s", w.Code, w.Body.String())
	}
	if b, err := os.ReadFile(filepath.Join(outside, "secret.txt")); err != nil || string(b) != "outside-marker" {
		t.Fatalf("outside file overwritten: %q %v", b, err)
	}
	// 也不得在外部新建文件
	if _, err := os.Stat(filepath.Join(outside, "new.txt")); !os.IsNotExist(err) {
		t.Fatal("created file outside skillDir")
	}
}

// 删：经符号链接删除外部文件/目录必须被拒，且链接本身可安全删除。
func TestSkillDeleteRejectsSymlinkEscape(t *testing.T) {
	s, token, outside := skillFixture(t)
	req := httptest.NewRequest("DELETE", "/api/skills/good/files/link/secret.txt", nil)
	req.SetPathValue("name", "good")
	req.SetPathValue("file", "link/secret.txt")
	w := skillReq(s, token, s.fsDeletePath, req)
	if w.Code < 400 {
		t.Fatalf("symlink delete allowed: %d %s", w.Code, w.Body.String())
	}
	if b, err := os.ReadFile(filepath.Join(outside, "secret.txt")); err != nil || string(b) != "outside-marker" {
		t.Fatalf("outside file deleted: %q %v", b, err)
	}
	// 删除链接本身是安全的，且不能连带删掉目标
	req2 := httptest.NewRequest("DELETE", "/api/skills/good/files/link", nil)
	req2.SetPathValue("name", "good")
	req2.SetPathValue("file", "link")
	w2 := skillReq(s, token, s.fsDeletePath, req2)
	if w2.Code != 200 {
		t.Fatalf("remove link: %d %s", w2.Code, w2.Body.String())
	}
	if b, err := os.ReadFile(filepath.Join(outside, "secret.txt")); err != nil || string(b) != "outside-marker" {
		t.Fatalf("removing link affected its target: %q %v", b, err)
	}
}

// 列表：不得因符号链接把外部目录的内容列出来。
func TestSkillListDoesNotFollowSymlink(t *testing.T) {
	s, token, _ := skillFixture(t)
	req := httptest.NewRequest("GET", "/api/skills/good/files", nil)
	req.SetPathValue("name", "good")
	w := skillReq(s, token, s.fsListFiles, req)
	if w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, f := range out.Files {
		if strings.Contains(f, "secret.txt") {
			t.Fatalf("listed file outside skillDir via symlink: %v", out.Files)
		}
	}
	// 正常文件仍应列出
	found := false
	for _, f := range out.Files {
		if f == "scripts/a.py" || f == "SKILL.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("normal files missing: %v", out.Files)
	}
}

// 正常读写仍必须工作（防止加固过头把功能打死）。
func TestSkillNormalOperationsStillWork(t *testing.T) {
	s, token, _ := skillFixture(t)

	// 读
	req := httptest.NewRequest("GET", "/api/skills/good/files/scripts/a.py", nil)
	req.SetPathValue("name", "good")
	req.SetPathValue("file", "scripts/a.py")
	w := skillReq(s, token, s.fsReadFile, req)
	if w.Code != 200 {
		t.Fatalf("read: %d %s", w.Code, w.Body.String())
	}

	// 写（含新建子目录）
	body := strings.NewReader(`{"content":"new-content"}`)
	req = httptest.NewRequest("POST", "/api/skills/good/files/references/b.md", body)
	req.SetPathValue("name", "good")
	req.SetPathValue("file", "references/b.md")
	w = skillReq(s, token, s.fsWriteFile, req)
	if w.Code != 200 {
		t.Fatalf("write: %d %s", w.Code, w.Body.String())
	}
	if b, err := os.ReadFile(filepath.Join(s.skillDir, "good", "references", "b.md")); err != nil || string(b) != "new-content" {
		t.Fatalf("write landed wrong: %q %v", b, err)
	}

	// 建目录
	body = strings.NewReader(`{"path":"assets/img"}`)
	req = httptest.NewRequest("POST", "/api/skills/good/dirs", body)
	req.SetPathValue("name", "good")
	w = skillReq(s, token, s.fsCreateDir, req)
	if w.Code != 201 {
		t.Fatalf("mkdir: %d %s", w.Code, w.Body.String())
	}

	// 删
	req = httptest.NewRequest("DELETE", "/api/skills/good/files/references/b.md", nil)
	req.SetPathValue("name", "good")
	req.SetPathValue("file", "references/b.md")
	w = skillReq(s, token, s.fsDeletePath, req)
	if w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
}

// 词法穿越仍必须被拒（skillRelPath 的既有保证不能被 os.Root 改动打破）。
func TestSkillPathTraversalStillRejected(t *testing.T) {
	s, token, _ := skillFixture(t)
	for _, bad := range []string{"../outside/secret.txt", "..", "a/../../x", "/etc/passwd"} {
		req := httptest.NewRequest("GET", "/api/skills/good/files/x", nil)
		req.SetPathValue("name", "good")
		req.SetPathValue("file", bad)
		w := skillReq(s, token, s.fsReadFile, req)
		if w.Code < 400 {
			t.Fatalf("traversal %q allowed: %d %s", bad, w.Code, w.Body.String())
		}
	}
}
