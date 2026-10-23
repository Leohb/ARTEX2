package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/db"
)

// 本文件是「人工」侧的资产/标签接口：
//   GET    /api/assets/{id}
//   PATCH  /api/assets/{id}
//   GET    /api/tags
//   POST   /api/tags
//   PATCH  /api/tags/{id}
//   DELETE /api/tags/{id}
//
// 与 Agent 侧的 POST /api/assets（批量登记新发现）区分开：那边的字段按类型强
// 校验、参与企业自动归因；这边允许部分更新，且改了什么就只改什么。

func (s *Server) tagStore(w http.ResponseWriter) *db.TagStore {
	if s.m.pg == nil {
		writeErr(w, http.StatusServiceUnavailable, "管理后台数据源(PostgreSQL)未连接")
		return nil
	}
	return s.m.pg.Tags()
}

// GET /api/assets/{id}
func (s *Server) getAsset(w http.ResponseWriter, r *http.Request) {
	as := s.assetStore()
	if as == nil {
		writeErr(w, 503, "database unavailable")
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "invalid asset id")
		return
	}
	a, err := as.GetByID(id)
	if err != nil {
		if errors.Is(err, db.ErrAssetNotFound) {
			writeErr(w, 404, "资产不存在")
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, a)
}

// PATCH /api/assets/{id}
//
// 部分更新：只有请求里出现的字段会被改。标签会顺手登记到 tags 表，这样
// 「先在资产上打标签、再去标签页管理」也能用。
func (s *Server) patchAsset(w http.ResponseWriter, r *http.Request) {
	as := s.assetStore()
	if as == nil {
		writeErr(w, 503, "database unavailable")
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "invalid asset id")
		return
	}
	var patch db.AssetPatch
	if err := decode(r, &patch); err != nil {
		writeErr(w, 400, "invalid JSON: "+err.Error())
		return
	}
	// 标签先做基本校验：单个标签名过长/为空会让后续 EnsureTagsExist 静默跳过，
	// 这里提前给出明确错误，避免用户以为保存成功但标签没上去。
	if patch.Tags != nil {
		cleaned := make([]string, 0, len(*patch.Tags))
		for _, raw := range *patch.Tags {
			t := strings.TrimSpace(raw)
			if t == "" {
				continue
			}
			if len([]rune(t)) > db.MaxTagNameRunes {
				writeErr(w, 400, "标签名不能超过 "+strconv.Itoa(db.MaxTagNameRunes)+" 个字符")
				return
			}
			cleaned = append(cleaned, t)
		}
		patch.Tags = &cleaned
	}

	a, err := as.PatchAsset(id, patch)
	if err != nil {
		if errors.Is(err, db.ErrAssetNotFound) {
			writeErr(w, 404, "资产不存在")
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	if patch.Tags != nil && len(*patch.Tags) > 0 {
		if ts := s.tagStore(w); ts != nil {
			// 失败不影响资产保存——标签登记是附带动作，资产本身已经改好了。
			_ = ts.EnsureTagsExist(*patch.Tags)
		}
	}
	writeJSON(w, 200, a)
}

// GET /api/tags
func (s *Server) listTags(w http.ResponseWriter, r *http.Request) {
	ts := s.tagStore(w)
	if ts == nil {
		return
	}
	tags, err := ts.ListTags()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, tags)
}

// POST /api/tags
func (s *Server) createTag(w http.ResponseWriter, r *http.Request) {
	ts := s.tagStore(w)
	if ts == nil {
		return
	}
	var req struct {
		Name        string `json:"name"`
		Color       string `json:"color"`
		Description string `json:"description"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, "invalid JSON: "+err.Error())
		return
	}
	tag, err := ts.CreateTag(req.Name, req.Color, req.Description)
	if err != nil {
		if errors.Is(err, db.ErrTagNameConflict) {
			writeErr(w, 409, "标签已存在")
			return
		}
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 201, tag)
}

// PATCH /api/tags/{id}
func (s *Server) updateTag(w http.ResponseWriter, r *http.Request) {
	ts := s.tagStore(w)
	if ts == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "invalid tag id")
		return
	}
	var req struct {
		Name        string `json:"name"`
		Color       string `json:"color"`
		Description string `json:"description"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, "invalid JSON: "+err.Error())
		return
	}
	tag, err := ts.UpdateTag(id, req.Name, req.Color, req.Description)
	if err != nil {
		switch {
		case errors.Is(err, db.ErrTagNotFound):
			writeErr(w, 404, "标签不存在")
		case errors.Is(err, db.ErrTagNameConflict):
			writeErr(w, 409, "标签名已被占用")
		default:
			writeErr(w, 400, err.Error())
		}
		return
	}
	writeJSON(w, 200, tag)
}

// DELETE /api/tags/{id}
func (s *Server) deleteTag(w http.ResponseWriter, r *http.Request) {
	ts := s.tagStore(w)
	if ts == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "invalid tag id")
		return
	}
	if err := ts.DeleteTag(id); err != nil {
		if errors.Is(err, db.ErrTagNotFound) {
			writeErr(w, 404, "标签不存在")
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": id})
}

// GET /api/assets/{id}/relations — 单条资产的上游/下游边。
// 用于资产详情页「这个 IP 被哪些子域解析到」「这个服务下有哪些接口」。
func (s *Server) assetRelations(w http.ResponseWriter, r *http.Request) {
	as := s.assetStore()
	if as == nil {
		writeErr(w, 503, "database unavailable")
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "invalid asset id")
		return
	}
	rel, err := as.Relations(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, rel)
}

// GET /api/assets/tree?root=<id>&root=<id>&depth=<n>
//
// 从给定根节点向下展开资产树。不传 root 时，用「所有没有上游 belongs_to 的
// 资产」当根（即企业/根域名这类顶层），这样前端打开页面就能看到全貌。
func (s *Server) assetTree(w http.ResponseWriter, r *http.Request) {
	as := s.assetStore()
	if as == nil {
		writeErr(w, 503, "database unavailable")
		return
	}
	q := r.URL.Query()
	var roots []int64
	for _, raw := range q["root"] {
		if id, err := strconv.ParseInt(raw, 10, 64); err == nil && id > 0 {
			roots = append(roots, id)
		}
	}
	depth, _ := strconv.Atoi(q.Get("depth"))
	if len(roots) == 0 {
		// 顶层 = 没有任何 belongs_to 上游的资产。
		// 注意判定的是 src_id = a.id：一条边 src→dst 表示 src 属于 dst，所以
		// 「自己是 src」意味着它挂在别人下面，不是顶层。
		rows, err := s.m.pg.Query(`
			SELECT a.id FROM assets a
			WHERE NOT EXISTS (
				SELECT 1 FROM asset_edges e WHERE e.src_id = a.id AND e.kind = 'belongs_to'
			)
			ORDER BY a.type, a.id
			LIMIT 200`)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err == nil {
				roots = append(roots, id)
			}
		}
		if len(roots) == 0 {
			writeJSON(w, 200, map[string]any{"roots": []any{}})
			return
		}
	}
	tree, err := as.AssetTree(roots, depth)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if tree == nil {
		tree = []*db.AssetTreeNode{}
	}
	writeJSON(w, 200, map[string]any{"roots": tree})
}

// PATCH /api/assets/{id}/scope — 人工标注网络位置（把私网 IP 标成 DMZ 等）。
func (s *Server) setAssetIPScope(w http.ResponseWriter, r *http.Request) {
	as := s.assetStore()
	if as == nil {
		writeErr(w, 503, "database unavailable")
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "invalid asset id")
		return
	}
	var req struct {
		Scope string `json:"ip_scope"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, "invalid JSON: "+err.Error())
		return
	}
	if err := as.SetIPScope(id, req.Scope); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "ip_scope": req.Scope})
}

// =====================================================================
// WEB 资产（按域名聚合）
// =====================================================================
//
// 一个域名上通常跑多个服务、每个服务下多个接口。列表页按「域名」聚合，一行
// 代表一个 WEB 资产的暴露面，点进去再看这个域名下的全部 URL、接口与漏洞。
//
// 之所以在服务端聚合而不是前端分组：分页必须作用在域名上。若按 service 行
// 分页再在前端 group by domain，同一域名会被拆到不同页，用户看到半截。

// GET /api/assets/web?limit=&offset=
func (s *Server) listWebAssets(w http.ResponseWriter, r *http.Request) {
	as := s.assetStore()
	if as == nil {
		writeErr(w, 503, "database unavailable")
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	rows, total, err := as.ListWebAssets(limit, offset)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"assets": rows, "total": total})
}

// GET /api/assets/web/{domain} — 单个 WEB 资产的完整暴露面。
func (s *Server) getWebAsset(w http.ResponseWriter, r *http.Request) {
	as := s.assetStore()
	if as == nil {
		writeErr(w, 503, "database unavailable")
		return
	}
	domain := strings.TrimSpace(r.PathValue("domain"))
	if domain == "" {
		writeErr(w, 400, "domain required")
		return
	}
	detail, err := as.WebAssetDetail(domain)
	if err != nil {
		if errors.Is(err, db.ErrAssetNotFound) {
			writeErr(w, 404, "该域名下没有 WEB 资产")
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, detail)
}
