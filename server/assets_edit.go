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
