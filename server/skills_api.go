package server

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/skill"
)

type skillFileNode struct {
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	License       string   `json:"license,omitempty"`
	Compatibility string   `json:"compatibility,omitempty"`
	MCPs          []string `json:"mcps,omitempty"`
	Files         []string `json:"files"`
	// Usage ledger (db/skill_usage.go). Calls is 0 and LastUsed nil for a skill that
	// was never invoked — the ledger only has rows for skills agents actually loaded.
	Calls    int        `json:"calls"`
	Tasks    int        `json:"tasks"`
	Agents   []string   `json:"usage_agents"`
	LastUsed *time.Time `json:"last_used,omitempty"`
}

func (s *Server) fsListSkills(w http.ResponseWriter, r *http.Request) {
	_ = os.MkdirAll(s.skillDir, 0o755)
	allReg, _ := skill.LoadDir(s.skillDir)
	metaByDir := map[string]skill.Skill{}
	if allReg != nil {
		for _, sk := range allReg.List() {
			if sk.Dir != "" {
				metaByDir[filepath.Base(sk.Dir)] = sk
			}
		}
	}
	entries, err := os.ReadDir(s.skillDir)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// Usage is best-effort decoration: a ledger read failure leaves the counts at
	// zero rather than failing the skill list itself.
	statBySkill := map[string]db.SkillStat{}
	if s.m.pg != nil {
		stats, err := s.m.pg.SkillStats()
		if err != nil {
			log.Printf("[skills] 读取调用统计失败: %v", err)
		}
		for _, st := range stats {
			statBySkill[st.Skill] = st
		}
	}
	nodes := []skillFileNode{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dirName := e.Name()
		node := skillFileNode{Name: dirName, Agents: []string{}}
		if st, ok := statBySkill[dirName]; ok {
			node.Calls, node.Tasks, node.Agents, node.LastUsed = st.Calls, st.Tasks, st.Agents, st.LastUsed
		}
		if meta, ok := metaByDir[dirName]; ok {
			node.Description = meta.Description
			node.License = meta.License
			node.Compatibility = meta.Compatibility
			node.MCPs = meta.MCPs
		}
		// 文件列表同样走 os.Root：这里只读取展示用的文件名，但目录里若存在指向
		// 外部的符号链接，跟进去就会把宿主机上的文件名列出来。取不到就留空。
		if sr, serr := os.OpenRoot(s.skillDir); serr == nil {
			if skillRoot, rerr := sr.OpenRoot(dirName); rerr == nil {
				node.Files, _ = walkSkillFiles(skillRoot)
				skillRoot.Close()
			}
			sr.Close()
		}
		if node.Files == nil {
			node.Files = []string{}
		}
		nodes = append(nodes, node)
	}
	writeJSON(w, 200, map[string]any{"skills": nodes})
}

// fsSkillUsage returns one skill's recent invocations, newest first (detail panel).
func (s *Server) fsSkillUsage(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	name := r.PathValue("name")
	if !validSkillName(name) {
		writeErr(w, 400, "非法 skill 名")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	calls, err := pg.RecentSkillCalls(name, limit)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"calls": calls})
}

// fsMissingSkills lists skill names agents asked for that do not exist — the gap
// list, i.e. which procedures are worth writing next.
func (s *Server) fsMissingSkills(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	missing, err := pg.MissingSkillStats(limit)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"missing": missing})
}

func (s *Server) fsCreateSkill(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name          string   `json:"name"`
		Description   string   `json:"description"`   // required per agentskills.io spec
		License       string   `json:"license"`       // optional
		Compatibility string   `json:"compatibility"` // optional
		MCPs          []string `json:"mcps"`          // optional; MCP servers this skill unlocks
		Instructions  string   `json:"instructions"`  // optional; scaffolded if empty
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if !validSkillName(body.Name) {
		writeErr(w, 400, "skill name must be 1-64 lowercase alphanumeric/hyphen characters, not starting/ending/doubling hyphens")
		return
	}
	if strings.TrimSpace(body.Description) == "" {
		writeErr(w, 400, "description is required")
		return
	}
	skillRoot, ok := s.skillRoot(w)
	if !ok {
		return
	}
	defer skillRoot.Close()
	if _, err := skillRoot.Lstat(body.Name); err == nil {
		writeErr(w, 409, "skill already exists")
		return
	}
	if err := skillRoot.MkdirAll(body.Name, 0o755); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// Build a spec-compliant SKILL.md (agentskills.io format):
	//   YAML frontmatter: name (required), description (required),
	//                     license, compatibility (optional)
	//   Markdown body:    step-by-step instructions
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "name: %s\n", body.Name)
	fmt.Fprintf(&sb, "description: %s\n", body.Description)
	if body.License != "" {
		fmt.Fprintf(&sb, "license: %s\n", body.License)
	}
	if body.Compatibility != "" {
		fmt.Fprintf(&sb, "compatibility: %s\n", body.Compatibility)
	}
	// mcps: MCP servers this skill unlocks on load (skill-gated deferred tools).
	if mcps := cleanStrs(body.MCPs); len(mcps) > 0 {
		fmt.Fprintf(&sb, "mcps: %s\n", strings.Join(mcps, ", "))
	}
	sb.WriteString("---\n")
	if strings.TrimSpace(body.Instructions) != "" {
		sb.WriteString(body.Instructions)
	} else {
		// scaffold a minimal Markdown body so the file is immediately useful
		fmt.Fprintf(&sb, "## %s\n\n", body.Name)
		sb.WriteString("<!-- Describe step-by-step instructions in Markdown. -->\n\n")
		sb.WriteString("1. \n2. \n3. \n")
	}
	if err := skillRoot.WriteFile(filepath.Join(body.Name, "SKILL.md"), []byte(sb.String()), 0o644); err != nil {
		_ = skillRoot.RemoveAll(body.Name)
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{"name": body.Name})
}

// fsUpdateSkillMeta rewrites the SKILL.md frontmatter fields that are safe to
// change without touching the instruction body: mcps, license, compatibility,
// description. Only fields present in the request body are updated; omitted
// fields are left as-is (the whole frontmatter is reconstructed from the
// parsed values, so the write is a clean replace, not a line-patch).
func (s *Server) fsUpdateSkillMeta(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validSkillName(name) {
		writeErr(w, 400, "invalid skill name")
		return
	}
	var body struct {
		MCPs          *[]string `json:"mcps"`
		Description   *string   `json:"description"`
		License       *string   `json:"license"`
		Compatibility *string   `json:"compatibility"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	skillMD := filepath.Join(name, "SKILL.md")
	skillRoot, ok := s.skillRoot(w)
	if !ok {
		return
	}
	defer skillRoot.Close()
	raw, err := skillRoot.ReadFile(skillMD)
	if err != nil {
		writeErr(w, 404, "skill not found")
		return
	}
	updated, err := rewriteSkillFrontmatter(raw, body.MCPs, body.Description, body.License, body.Compatibility)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := skillRoot.WriteFile(skillMD, updated, 0o644); err != nil {
		writeErr(w, 403, "拒绝写入：路径越出 skill 目录（可能经过符号链接）")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) fsDeleteSkill(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	name := r.PathValue("name")
	if !validSkillName(name) {
		writeErr(w, 400, "invalid skill name")
		return
	}
	if err := pg.DeleteSkillVisibility(name); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	skillRoot, ok := s.skillRoot(w)
	if !ok {
		return
	}
	defer skillRoot.Close()
	if err := skillRoot.RemoveAll(name); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": name})
}

func (s *Server) pgSkillVisibility(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	agents, err := pg.SkillAgents(r.PathValue("name"))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"agents": idStrings(agents)})
}

func (s *Server) pgToggleSkillVisibility(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	var body struct {
		AgentID   int64  `json:"agent_id,string"`
		SkillName string `json:"skill_name"`
		Visible   bool   `json:"visible"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := pg.ToggleSkillVisibility(body.AgentID, body.SkillName, body.Visible); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
