package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// =====================================================================
// 自定义标签
// =====================================================================
//
// 设计取舍：assets.tags 存的是标签「名字」数组，不是 tags.id。
//
// 存 id 的话，删掉一个标签会让所有资产上留下一堆指向不存在行的 id，展示时
// 要么显示空洞要么得 LEFT JOIN 兜底；存名字则历史值天然可读。代价是改名
// 要同时改两张表——那一步在 RenameTag 里用一次事务完成。

// Tag 是 tags 表的一行。
type Tag struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color,omitempty"`
	Description string `json:"description,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	// AssetCount 是该标签当前被多少条资产引用，列表页用来显示用量。
	AssetCount int `json:"asset_count"`
}

// TagStore operates on the tags table (and the assets.tags array it labels).
type TagStore struct{ db *DB }

var (
	ErrTagNameConflict = errors.New("tag name already exists")
	ErrTagNotFound     = errors.New("tag not found")
)

// 标签名长度上限。下限 1 由 TrimSpace 后非空保证；上限防止把整段说明塞进标签名。
const MaxTagNameRunes = 32

// normalizeTagName 去掉首尾空白并做长度校验。
// 返回 (规范化后的名字, 空串表示通过, 错误原因)。
func normalizeTagName(name string) (string, string) {
	n := strings.TrimSpace(name)
	if n == "" {
		return "", "标签名不能为空"
	}
	if len([]rune(n)) > MaxTagNameRunes {
		return "", fmt.Sprintf("标签名不能超过 %d 个字符", MaxTagNameRunes)
	}
	return n, ""
}

// ListTags 返回全部标签，按名字排序，并带上各自被引用的资产数。
func (s *TagStore) ListTags() ([]*Tag, error) {
	rows, err := s.db.Query(`
		SELECT t.id, t.name, COALESCE(t.color,''), COALESCE(t.description,''),
		       t.created_at::text, t.updated_at::text,
		       (SELECT count(*) FROM assets a WHERE t.name = ANY(a.tags)) AS asset_count
		FROM tags t
		ORDER BY t.name`)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	defer rows.Close()
	out := []*Tag{}
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Color, &t.Description, &t.CreatedAt, &t.UpdatedAt, &t.AssetCount); err != nil {
			return nil, fmt.Errorf("scan tag: %w", err)
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

// CreateTag 新建标签。名字冲突返回 ErrTagNameConflict。
func (s *TagStore) CreateTag(name, color, description string) (*Tag, error) {
	n, msg := normalizeTagName(name)
	if msg != "" {
		return nil, errors.New(msg)
	}
	var id int64
	err := s.db.QueryRow(`
		INSERT INTO tags (name, color, description) VALUES ($1, $2, $3)
		ON CONFLICT (name) DO NOTHING
		RETURNING id`, n, nullIfEmpty(color), nullIfEmpty(description)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTagNameConflict
	}
	if err != nil {
		return nil, fmt.Errorf("create tag: %w", err)
	}
	return s.GetTag(id)
}

// GetTag 按 id 取单个标签。
func (s *TagStore) GetTag(id int64) (*Tag, error) {
	var t Tag
	err := s.db.QueryRow(`
		SELECT t.id, t.name, COALESCE(t.color,''), COALESCE(t.description,''),
		       t.created_at::text, t.updated_at::text,
		       (SELECT count(*) FROM assets a WHERE t.name = ANY(a.tags))
		FROM tags t WHERE t.id = $1`, id).
		Scan(&t.ID, &t.Name, &t.Color, &t.Description, &t.CreatedAt, &t.UpdatedAt, &t.AssetCount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTagNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get tag: %w", err)
	}
	return &t, nil
}

// UpdateTag 改颜色/说明；name 非空时同时改名，并把所有资产上的旧名替换成新名。
// 改名与数组重写在同一事务里，避免出现「标签表已改名、资产还挂着旧名」的中间态。
func (s *TagStore) UpdateTag(id int64, name, color, description string) (*Tag, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("update tag: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var oldName string
	if err := tx.QueryRow(`SELECT name FROM tags WHERE id = $1`, id).Scan(&oldName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTagNotFound
		}
		return nil, fmt.Errorf("update tag: %w", err)
	}

	newName := oldName
	if strings.TrimSpace(name) != "" {
		n, msg := normalizeTagName(name)
		if msg != "" {
			return nil, errors.New(msg)
		}
		if n != oldName {
			var exists bool
			if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM tags WHERE name = $1 AND id <> $2)`, n, id).Scan(&exists); err != nil {
				return nil, fmt.Errorf("update tag: %w", err)
			}
			if exists {
				return nil, ErrTagNameConflict
			}
			newName = n
		}
	}

	if _, err := tx.Exec(`
		UPDATE tags SET name = $1, color = $2, description = $3, updated_at = now()
		WHERE id = $4`,
		newName, nullIfEmpty(color), nullIfEmpty(description), id); err != nil {
		return nil, fmt.Errorf("update tag: %w", err)
	}

	// 资产上挂的是旧名字，改名后同步替换。array_replace 只替换命中的元素，
	// 不会重排或去重；这里额外用子查询去掉可能产生的重复项。
	if newName != oldName {
		if _, err := tx.Exec(`
			UPDATE assets
			SET tags = (
				SELECT COALESCE(array_agg(DISTINCT x), '{}')
				FROM unnest(array_replace(tags, $1, $2)) AS x
			)
			WHERE $1 = ANY(tags)`, oldName, newName); err != nil {
			return nil, fmt.Errorf("rename tag on assets: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("update tag: %w", err)
	}
	return s.GetTag(id)
}

// DeleteTag 删除标签定义。资产上的同名值保留——它是历史标注，不该因为标签
// 管理页删了一行就从资产上消失（用户要清资产上的标签可以在资产编辑里做）。
func (s *TagStore) DeleteTag(id int64) error {
	res, err := s.db.Exec(`DELETE FROM tags WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete tag: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrTagNotFound
	}
	return nil
}

// EnsureTagsExist 在资产写入时按需登记标签名，让「先给资产打标签、后去标签页
// 管理」也能工作。已存在的名字忽略。
func (s *TagStore) EnsureTagsExist(names []string) error {
	for _, raw := range names {
		n, msg := normalizeTagName(raw)
		if msg != "" {
			continue // 非法名字直接跳过：资产写入不该因为一个坏标签名整批失败
		}
		if _, err := s.db.Exec(`INSERT INTO tags (name) VALUES ($1) ON CONFLICT (name) DO NOTHING`, n); err != nil {
			return fmt.Errorf("ensure tag %q: %w", n, err)
		}
	}
	return nil
}
