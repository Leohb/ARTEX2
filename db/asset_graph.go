package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
)

// =====================================================================
// 资产关系图
// =====================================================================
//
// 改造前资产之间的关系只靠字符串字段巧合表达（subdomain.root_domain 写一个域名、
// service.ip 写一个 IP），查询时用 map 拼回去。后果：
//   - 父记录改名/删除后，子记录仍指向不存在的目标
//   - 无法表达非树形结构（一个 IP 跑多个服务、一条链路多条 NAT 映射）
//   - 每次查树都要全量拉行再在内存里拼
//
// 现在关系显式存在 asset_edges 里，删节点时边级联消失。

// EdgeKind 是资产关系的类型。
type EdgeKind string

const (
	// EdgeBelongsTo：子域 → 根域、服务 → 主机、接口 → 服务
	EdgeBelongsTo EdgeKind = "belongs_to"
	// EdgeResolvesTo：子域 → IP（DNS 解析）
	EdgeResolvesTo EdgeKind = "resolves_to"
	// EdgeRunsOn：服务 → IP（监听地址）
	EdgeRunsOn EdgeKind = "runs_on"
	// EdgeMapsTo：NAT 映射，内网地址 → 外网地址
	EdgeMapsTo EdgeKind = "maps_to"
	// EdgeExposedVia：内网主机 → 公网服务（通过哪条链路暴露）
	EdgeExposedVia EdgeKind = "exposed_via"
)

func (k EdgeKind) valid() bool {
	switch k {
	case EdgeBelongsTo, EdgeResolvesTo, EdgeRunsOn, EdgeMapsTo, EdgeExposedVia:
		return true
	}
	return false
}

// AssetEdge 是资产图上的一条有向边。
type AssetEdge struct {
	ID    int64    `json:"id"`
	SrcID int64    `json:"src_id"`
	DstID int64    `json:"dst_id"`
	Kind  EdgeKind `json:"kind"`
	// Dst 是目标资产的摘要，方便前端直接渲染而不用二次查询。
	Dst *GraphNodeRef `json:"dst,omitempty"`
}

// GraphNodeRef 是资产的最小可读摘要（树/关系列表用）。
type GraphNodeRef struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Label string `json:"label"`
}

// LinkAssets 建一条边。已存在则忽略（幂等）；src == dst 直接跳过。
//
// 不存在的 id 会因外键失败而返回错误——调用方若只是"尽力连边"，可忽略错误。
func (s *AssetStore) LinkAssets(srcID, dstID int64, kind EdgeKind) error {
	if srcID <= 0 || dstID <= 0 || srcID == dstID {
		return nil
	}
	if !kind.valid() {
		return fmt.Errorf("unknown edge kind %q", kind)
	}
	_, err := s.exec(`
		INSERT INTO asset_edges (src_id, dst_id, kind) VALUES ($1,$2,$3)
		ON CONFLICT (src_id, dst_id, kind) DO NOTHING`, srcID, dstID, string(kind))
	if err != nil {
		return fmt.Errorf("link assets: %w", err)
	}
	return nil
}

// UnlinkAssets 删一条边。
func (s *AssetStore) UnlinkAssets(srcID, dstID int64, kind EdgeKind) error {
	_, err := s.exec(`DELETE FROM asset_edges WHERE src_id=$1 AND dst_id=$2 AND kind=$3`,
		srcID, dstID, string(kind))
	return err
}

// EdgesFrom 返回某资产指向的下游边（带目标摘要）。
func (s *AssetStore) EdgesFrom(id int64) ([]*AssetEdge, error) {
	return s.queryEdges(`
		SELECT e.id, e.src_id, e.dst_id, e.kind, a.id, a.type, `+graphNodeLabelSQL("a")+`
		FROM asset_edges e JOIN assets a ON a.id = e.dst_id
		WHERE e.src_id = $1
		ORDER BY e.kind, a.type, a.id`, id)
}

// EdgesTo 返回指向某资产的上游边（带来源摘要）——逐层追查「这个 IP 被谁解析到」时用。
func (s *AssetStore) EdgesTo(id int64) ([]*AssetEdge, error) {
	rows, err := s.db.Query(`
		SELECT e.id, e.src_id, e.dst_id, e.kind, a.id, a.type, `+graphNodeLabelSQL("a")+`
		FROM asset_edges e JOIN assets a ON a.id = e.src_id
		WHERE e.dst_id = $1
		ORDER BY e.kind, a.type, a.id`, id)
	if err != nil {
		return nil, fmt.Errorf("query edges to: %w", err)
	}
	defer rows.Close()
	out := []*AssetEdge{}
	for rows.Next() {
		var e AssetEdge
		var ref GraphNodeRef
		if err := rows.Scan(&e.ID, &e.SrcID, &e.DstID, &e.Kind, &ref.ID, &ref.Type, &ref.Label); err != nil {
			return nil, err
		}
		e.Dst = &ref
		out = append(out, &e)
	}
	return out, rows.Err()
}

// exec 在事务内时走 tx，否则走 db。
//
// 资产写入路径全部包在 withCompanyScopeMutation 的事务里（s.tx != nil）。建边
// 必须用同一个事务：否则边在事务外提交，而 src/dst 的新 id 在事务提交前对别的
// 连接不可见，外键会直接失败——实测表现为「资产建好了但一条边都没有」。
func (s *AssetStore) exec(q string, args ...any) (sql.Result, error) {
	if s.tx != nil {
		return s.tx.Exec(q, args...)
	}
	return s.db.Exec(q, args...)
}

func (s *AssetStore) queryEdges(q string, args ...any) ([]*AssetEdge, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("query edges: %w", err)
	}
	defer rows.Close()
	out := []*AssetEdge{}
	for rows.Next() {
		var e AssetEdge
		var ref GraphNodeRef
		if err := rows.Scan(&e.ID, &e.SrcID, &e.DstID, &e.Kind, &ref.ID, &ref.Type, &ref.Label); err != nil {
			return nil, err
		}
		e.Dst = &ref
		out = append(out, &e)
	}
	return out, rows.Err()
}

// graphNodeLabelSQL 生成一个与前端展示一致的可读标签表达式。
// 优先级与 coverageNodeLabel 保持一致：URL > domain > ip > hostname > nat 两端 > app_name。
func graphNodeLabelSQL(alias string) string {
	return fmt.Sprintf(`COALESCE(NULLIF(%[1]s.url,''),
	        NULLIF(%[1]s.domain,''),
	        NULLIF(%[1]s.ip,''),
	        NULLIF(%[1]s.hostname,''),
	        NULLIF(%[1]s.nat_external_ip || ':' || COALESCE(%[1]s.nat_external_port::text,''), ''),
	        NULLIF(%[1]s.app_name,''),
	        %[1]s.type || '#' || %[1]s.id::text)`, alias)
}

// AssetTreeNode 是逐层追查树的一个节点。
type AssetTreeNode struct {
	GraphNodeRef
	// ParentID 为空表示根节点。
	ParentID *int64 `json:"parent_id,omitempty"`
	// EdgeKind 是「从父节点到本节点」的边类型。
	EdgeKind EdgeKind `json:"edge_kind,omitempty"`
	// Children 是下一层。为空时不序列化，避免前端判断 null。
	Children []*AssetTreeNode `json:"children,omitempty"`
	// Depth 从 0 开始，前端按层缩进用。
	Depth int `json:"depth"`
	// Cycle 为 true 表示这条边指回祖先（数据异常或双向关系），不再展开。
	Cycle bool `json:"cycle,omitempty"`
}

// AssetTree 从一个或多个根节点向下展开。
//
// 用递归 CTE 在库内展开，而不是把全表拉回来在内存里拼：后者在资产量大时会把
// 每次请求变成一次全表扫描，而且无法限制深度。
//
// maxDepth 限制层数（0 或负数按默认 6 处理）。一个真实的公网暴露面链路
// 企业→根域→子域→服务→接口 也只有 4 层，6 层足够且能防环。
func (s *AssetStore) AssetTree(rootIDs []int64, maxDepth int) ([]*AssetTreeNode, error) {
	if len(rootIDs) == 0 {
		return nil, nil
	}
	if maxDepth <= 0 {
		maxDepth = 6
	}
	placeholders := make([]string, len(rootIDs))
	args := make([]any, 0, len(rootIDs)+1)
	for i, id := range rootIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args = append(args, id)
	}
	args = append(args, maxDepth)

	// 同一个节点可能同时属于多个父（例如服务既挂在根域也挂在子域，子域自己
	// 又挂在根域）。树里必须只出现一次，否则读起来像重复节点。用 visited
	// 数组在递归过程中全局去重：先被展开的父「认领」这个节点，后来的边跳过。
	q := fmt.Sprintf(`
		WITH RECURSIVE walk AS (
			SELECT a.id, a.type, %s AS label, NULL::bigint AS parent_id,
			       NULL::text AS edge_kind, 0 AS depth,
			       ARRAY[a.id] AS path, ARRAY[a.id] AS visited
			FROM assets a WHERE a.id IN (%s)
			UNION ALL
			-- 边的方向是 src 属于 dst（子 → 父）。树从父向下展开，所以找
			-- 「dst_id = 自己」的边，取 src 当子节点。
			SELECT a.id, a.type, %s, e.dst_id, e.kind, w.depth + 1,
			       w.path || a.id, w.visited || a.id
			FROM walk w
			JOIN asset_edges e ON e.dst_id = w.id
			JOIN assets a ON a.id = e.src_id
			-- 只沿层级边展开；resolves_to/runs_on/maps_to/exposed_via 是横向
			-- 关系，当层级展开会把同一个 IP 复制到每个解析它的子域下。
			AND e.kind = 'belongs_to'
			WHERE w.depth < $%d
			  AND NOT (a.id = ANY(w.path))     -- 防环
			  AND NOT (a.id = ANY(w.visited))  -- 全局去重：每个节点只出现一次
		)
		SELECT id, type, label, parent_id, edge_kind, depth FROM walk
		ORDER BY depth, id`,
		graphNodeLabelSQL("a"), strings.Join(placeholders, ","), graphNodeLabelSQL("a"), len(args))

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("asset tree: %w", err)
	}
	defer rows.Close()

	type flat struct {
		node *AssetTreeNode
	}
	var ordered []*AssetTreeNode
	for rows.Next() {
		n := &AssetTreeNode{}
		var parent sql.NullInt64
		var kind sql.NullString
		if err := rows.Scan(&n.ID, &n.Type, &n.Label, &parent, &kind, &n.Depth); err != nil {
			return nil, err
		}
		if parent.Valid {
			pid := parent.Int64
			n.ParentID = &pid
		}
		if kind.Valid {
			n.EdgeKind = EdgeKind(kind.String)
		}
		ordered = append(ordered, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 按 parent 挂成树。先建 id → node 索引，再一次遍历挂接。
	byID := make(map[int64]*AssetTreeNode, len(ordered))
	for _, n := range ordered {
		byID[n.ID] = n
	}
	rootSet := make(map[int64]bool, len(rootIDs))
	for _, id := range rootIDs {
		rootSet[id] = true
	}
	var roots []*AssetTreeNode
	for _, n := range ordered {
		if n.ParentID == nil {
			if rootSet[n.ID] {
				roots = append(roots, n)
			}
			continue
		}
		parent, ok := byID[*n.ParentID]
		if !ok {
			// 父节点超出深度限制时，把本节点当根返回，避免整棵子树消失。
			n.ParentID = nil
			roots = append(roots, n)
			continue
		}
		parent.Children = append(parent.Children, n)
	}
	return roots, nil
}

// BackfillEdges 为单条资产补齐它的边。资产写入路径调用它，让新建的资产
// 立刻进入图，而不是等下一次全库回填。
func (s *AssetStore) BackfillEdges(id int64) error {
	a, err := s.GetByID(id)
	if err != nil {
		if errors.Is(err, ErrAssetNotFound) {
			return nil
		}
		return err
	}
	link := func(dstID int64, kind EdgeKind) {
		if dstID > 0 && dstID != id {
			_ = s.LinkAssets(id, dstID, kind)
		}
	}

	switch a.Type {
	case "subdomain":
		// 子域 → 根域
		if a.RootDomain != "" {
			if pid := s.findByTypeAndColumn("root_domain", "domain", a.RootDomain); pid > 0 {
				link(pid, EdgeBelongsTo)
			}
		}
		// 子域 → 解析到的 IP
		for _, v := range a.RecordValue {
			if pid := s.findByTypeAndColumn("ip", "ip", v); pid > 0 {
				link(pid, EdgeResolvesTo)
			}
		}
	case "service":
		if a.RootDomain != "" {
			if pid := s.findByTypeAndColumn("root_domain", "domain", a.RootDomain); pid > 0 {
				link(pid, EdgeBelongsTo)
			}
		}
		if a.IP != "" {
			if pid := s.findByTypeAndColumn("ip", "ip", a.IP); pid > 0 {
				link(pid, EdgeRunsOn)
			}
		}
	case "endpoint":
		if a.ParentServiceID != nil {
			link(*a.ParentServiceID, EdgeBelongsTo)
		} else if a.URL != "" {
			// 唯一命中才连，一对多时不猜。
			if pid := s.findUniqueServiceByURL(a.URL); pid > 0 {
				link(pid, EdgeBelongsTo)
			}
		}
	case "nat":
		// NAT 把内网地址映射到外网地址：两端各自连边。
		if a.NatInternalIP != "" {
			if pid := s.findByTypeAndColumn("ip", "ip", a.NatInternalIP); pid > 0 {
				link(pid, EdgeMapsTo)
			}
		}
		if a.NatExternalIP != "" {
			if pid := s.findByTypeAndColumn("ip", "ip", a.NatExternalIP); pid > 0 {
				link(pid, EdgeExposedVia)
			}
		}
	}
	return nil
}

// findByTypeAndColumn 按某列等值查资产 id。列名来自本文件内的常量字面量，
// 不接受外部输入，因此直接拼接安全。
func (s *AssetStore) findByTypeAndColumn(typ, col, val string) int64 {
	if val == "" {
		return 0
	}
	var id int64
	var err error
	q := fmt.Sprintf(`SELECT id FROM assets WHERE type=$1 AND %s=$2 LIMIT 1`, col)
	if s.tx != nil {
		err = s.tx.QueryRow(q, typ, val).Scan(&id)
	} else {
		err = s.db.QueryRow(q, typ, val).Scan(&id)
	}
	if err != nil {
		return 0
	}
	return id
}

// findUniqueServiceByURL 只在「同一 URL 只有一个 http 服务」时返回其 id。
func (s *AssetStore) findUniqueServiceByURL(url string) int64 {
	if url == "" {
		return 0
	}
	var id int64
	var cnt int
	q := `SELECT COALESCE(min(id),0), count(*) FROM assets
		WHERE type='service' AND service_type='http' AND url=$1`
	var err error
	if s.tx != nil {
		err = s.tx.QueryRow(q, url).Scan(&id, &cnt)
	} else {
		err = s.db.QueryRow(q, url).Scan(&id, &cnt)
	}
	if err != nil {
		return 0
	}
	if cnt != 1 {
		return 0
	}
	return id
}

// AssetRelations 是一次详情查询需要的全部关系（上游 + 下游）。
type AssetRelations struct {
	Upstream   []*AssetEdge `json:"upstream"`
	Downstream []*AssetEdge `json:"downstream"`
}

// Relations 返回某资产的上游与下游边。
func (s *AssetStore) Relations(id int64) (*AssetRelations, error) {
	up, err := s.EdgesTo(id)
	if err != nil {
		return nil, err
	}
	down, err := s.EdgesFrom(id)
	if err != nil {
		return nil, err
	}
	if up == nil {
		up = []*AssetEdge{}
	}
	if down == nil {
		down = []*AssetEdge{}
	}
	return &AssetRelations{Upstream: up, Downstream: down}, nil
}

// =====================================================================
// 网络位置（公网 / 内网 / DMZ / 回环）
// =====================================================================

// ClassifyIPScope 按地址本身判断网络位置。
//
// 只做「地址能说明的那部分」：回环与私网可确定；DMZ 是组织概念，地址上看不出来，
// 必须由人或数据源显式标注（见 SetIPScope），因此这里不会返回 dmz。
func ClassifyIPScope(ip string) string {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return ""
	}
	switch {
	case parsed.IsLoopback():
		return "loopback"
	case parsed.IsPrivate():
		return "private"
	case parsed.IsLinkLocalUnicast(), parsed.IsLinkLocalMulticast(), parsed.IsUnspecified():
		// 链路本地/未指定地址不属于任何真实暴露面，按内网处理。
		return "private"
	default:
		return "public"
	}
}

// setIPScopeIfUnset 只在当前值缺失或仍是自动推断的 private 时才写。
// 用于「自动补默认值」的场景，避免后续流程把 dmz 之类的显式标注覆盖掉。
func (s *AssetStore) setIPScopeIfUnset(id int64, scope string) error {
	_, err := s.exec(`UPDATE assets SET ip_scope=$2
		WHERE id=$1 AND (ip_scope IS NULL OR ip_scope = '')`, id, scope)
	return err
}

// SetIPScope 显式设置网络位置（人可以把某个私网 IP 标成 DMZ）。
func (s *AssetStore) SetIPScope(id int64, scope string) error {
	switch scope {
	case "public", "private", "dmz", "loopback":
	default:
		return fmt.Errorf("invalid ip_scope %q", scope)
	}
	// 走 exec：主机/NAT 写入发生在事务内，用 s.db 会查不到未提交的行。
	_, err := s.exec(`UPDATE assets SET ip_scope=$2 WHERE id=$1`, id, scope)
	return err
}

// =====================================================================
// host（内网 / DMZ 主机）与 nat（NAT 映射）
// =====================================================================

// UpsertHostReq 是内网/DMZ 主机的登记参数。
type UpsertHostReq struct {
	Hostname string
	IP       string // 主机地址（可空，靠 NAT 映射时用）
	OS       string
	Role     string // web / db / dc / jump …
	Scope    string // private / dmz；空则按 IP 自动判断
	TaskID   int64
}

// UpsertHost 登记一台内网/DMZ 主机。以 hostname 为幂等键（没有 hostname 时退回 ip）。
func (s *AssetStore) UpsertHost(req UpsertHostReq) (int64, error) {
	hostname := strings.TrimSpace(req.Hostname)
	ip := strings.TrimSpace(req.IP)
	if hostname == "" && ip == "" {
		return 0, fmt.Errorf("hostname 与 ip 至少填一个")
	}
	if err := ValidateAssetIP(ip); err != nil {
		return 0, err
	}
	scope := req.Scope
	if scope == "" {
		scope = ClassifyIPScope(ip)
	}
	if scope == "" {
		scope = "private" // 主机默认在内网
	}

	var id int64
	var err error
	if hostname != "" {
		err = s.db.QueryRow(`
			INSERT INTO assets (type, hostname, ip, os, host_role, ip_scope, task_ids)
			VALUES ('host',$1,$2,$3,$4,$5, ARRAY[]::bigint[])
			ON CONFLICT (hostname) WHERE type='host' AND hostname IS NOT NULL
			DO UPDATE SET
				ip = COALESCE(NULLIF(EXCLUDED.ip,''), assets.ip),
				os = COALESCE(NULLIF(EXCLUDED.os,''), assets.os),
				host_role = COALESCE(NULLIF(EXCLUDED.host_role,''), assets.host_role),
				updated_at = now()
			RETURNING id`, hostname, ip, req.OS, req.Role, scope).Scan(&id)
	} else {
		// 没有主机名：按 ip 幂等。
		err = s.db.QueryRow(`
			INSERT INTO assets (type, ip, os, host_role, ip_scope, task_ids)
			VALUES ('host',$1,$2,$3,$4, ARRAY[]::bigint[])
			ON CONFLICT (ip) WHERE type='ip'
			DO NOTHING
			RETURNING id`, ip, req.OS, req.Role, scope).Scan(&id)
		if err != nil {
			// 已存在同名 ip 记录时直接复用。
			if e := s.db.QueryRow(`SELECT id FROM assets WHERE type='host' AND ip=$1 LIMIT 1`, ip).Scan(&id); e != nil {
				return 0, fmt.Errorf("upsert host: %w", err)
			}
		}
	}
	if err != nil {
		return 0, fmt.Errorf("upsert host: %w", err)
	}
	if req.TaskID > 0 {
		_, _ = s.db.Exec(`UPDATE assets SET task_ids = (
			SELECT array_agg(DISTINCT x) FROM unnest(task_ids || ARRAY[$2]::bigint[]) AS x
		) WHERE id=$1`, id, req.TaskID)
	}
	// 主机若带 IP，确保 ip 资产存在并连边——否则「从公网 IP 反查到内网主机」
	// 这条链路断在第一跳。
	if ip != "" {
		pid := s.findByTypeAndColumn("ip", "ip", ip)
		if pid == 0 {
			if newID, e := s.UpsertIP(UpsertIPReq{IP: ip, TaskID: req.TaskID}); e == nil {
				pid = newID
			}
		}
		if pid > 0 {
			_ = s.LinkAssets(id, pid, EdgeRunsOn)
			// 内网 IP 的网络位置同步到 ip 资产，前端才能按 scope 过滤。
			// 用「仅当未标注」的版本：ip 资产可能先被扫描登记成 private，
			// 这里要能把它升级成 dmz。
			if scope != "" {
				_ = s.SetIPScope(pid, scope)
			}
		}
	}
	return id, nil
}

// UpsertNATReq 是一条 NAT 映射规则。
type UpsertNATReq struct {
	InternalIP   string
	InternalPort int
	ExternalIP   string
	ExternalPort int
	Protocol     string // tcp / udp；空按 tcp
	TaskID       int64
}

// UpsertNAT 登记一条 NAT 映射。以「内网地址+端口+协议」为幂等键。
func (s *AssetStore) UpsertNAT(req UpsertNATReq) (int64, error) {
	inIP := strings.TrimSpace(req.InternalIP)
	exIP := strings.TrimSpace(req.ExternalIP)
	if inIP == "" && exIP == "" {
		return 0, fmt.Errorf("nat_internal_ip 与 nat_external_ip 至少填一个")
	}
	if err := ValidateAssetIP(inIP); err != nil {
		return 0, err
	}
	if err := ValidateAssetIP(exIP); err != nil {
		return 0, err
	}
	proto := strings.ToLower(strings.TrimSpace(req.Protocol))
	if proto == "" {
		proto = "tcp"
	}

	var id int64
	err := s.db.QueryRow(`
		INSERT INTO assets (type, nat_internal_ip, nat_internal_port, nat_external_ip,
		                    nat_external_port, nat_protocol, ip_scope, task_ids)
		VALUES ('nat',$1,NULLIF($2,0),$3,NULLIF($4,0),$5,$6, ARRAY[]::bigint[])
		ON CONFLICT (nat_internal_ip, nat_internal_port, nat_protocol)
			WHERE type='nat' AND nat_internal_ip IS NOT NULL
		DO UPDATE SET
			nat_external_ip = COALESCE(NULLIF(EXCLUDED.nat_external_ip,''), assets.nat_external_ip),
			nat_external_port = COALESCE(EXCLUDED.nat_external_port, assets.nat_external_port),
			updated_at = now()
		RETURNING id`,
		inIP, req.InternalPort, exIP, req.ExternalPort, proto,
		// NAT 规则本身描述的是暴露链路，外网侧是 public；没有外网地址时按内网。
		map[bool]string{true: "public", false: "private"}[exIP != ""]).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert nat: %w", err)
	}
	if req.TaskID > 0 {
		_, _ = s.db.Exec(`UPDATE assets SET task_ids = (
			SELECT array_agg(DISTINCT x) FROM unnest(task_ids || ARRAY[$2]::bigint[]) AS x
		) WHERE id=$1`, id, req.TaskID)
	}
	// 两端连边：内网 IP ←maps_to— NAT —exposed_via→ 外网 IP。
	// 两端的 ip 资产若不存在就建出来，否则 NAT 会成为图上的孤岛。
	ensureIP := func(addr, scope string) int64 {
		if addr == "" {
			return 0
		}
		pid := s.findByTypeAndColumn("ip", "ip", addr)
		if pid == 0 {
			if newID, e := s.UpsertIP(UpsertIPReq{IP: addr, TaskID: req.TaskID}); e == nil {
				pid = newID
			}
		}
		// 只在尚未标注时补默认值。NAT 的内网侧想标 private，但如果这台机器已经
		// 被人工/主机登记标成 dmz，不能被覆盖回 private——DMZ 是更强的信息。
		if pid > 0 && scope != "" {
			_ = s.setIPScopeIfUnset(pid, scope)
		}
		return pid
	}
	if pid := ensureIP(inIP, "private"); pid > 0 {
		_ = s.LinkAssets(id, pid, EdgeMapsTo)
		// 内网 IP 上有主机登记时，把 NAT 与主机也连起来，形成
		// 公网IP ←exposed_via— NAT —maps_to→ 内网IP ←runs_on— 主机 的完整链路。
		var hid int64
		if e := s.db.QueryRow(`SELECT id FROM assets WHERE type='host' AND ip=$1 LIMIT 1`, inIP).Scan(&hid); e == nil && hid > 0 {
			_ = s.LinkAssets(id, hid, EdgeMapsTo)
		}
	}
	if pid := ensureIP(exIP, "public"); pid > 0 {
		_ = s.LinkAssets(id, pid, EdgeExposedVia)
	}
	return id, nil
}

// findSubdomainByDomain 找同域名的 subdomain 记录（不区分 record_type）。
// 多条时优先带 record_type 的那条——它承载 DNS 解析信息，是更完整的记录。
func (s *AssetStore) findSubdomainByDomain(domain string) int64 {
	if domain == "" {
		return 0
	}
	var id int64
	q := `SELECT id FROM assets WHERE type='subdomain' AND domain=$1
	      ORDER BY (record_type IS NULL) , id LIMIT 1`
	var err error
	if s.tx != nil {
		err = s.tx.QueryRow(q, domain).Scan(&id)
	} else {
		err = s.db.QueryRow(q, domain).Scan(&id)
	}
	if err != nil {
		return 0
	}
	return id
}

// =====================================================================
// WEB 资产（按域名聚合）
// =====================================================================
//
// 列表按域名聚合，分页作用在域名上。若按 service 行分页再前端 group by，
// 同一域名会被拆到不同页，用户只能看到半截。

// WebAssetSummary 是列表的一行：一个域名的暴露面统计。
type WebAssetSummary struct {
	Domain        string   `json:"domain"`
	ServiceCount  int      `json:"service_count"`
	EndpointCount int      `json:"endpoint_count"`
	FindingCount  int      `json:"finding_count"`
	IPs           []string `json:"ips"`
	Titles        []string `json:"titles"`
	StatusCodes   []int    `json:"status_codes"`
	Technologies  []string `json:"technologies"`
	Tags          []string `json:"tags"`
	LastSeen      string   `json:"last_seen"`
}

// ListWebAssets 按域名聚合 HTTP 服务，返回分页结果与域名总数。
func (s *AssetStore) ListWebAssets(limit, offset int) ([]*WebAssetSummary, int, error) {
	var total int
	if err := s.db.QueryRow(`
		SELECT count(DISTINCT domain) FROM assets
		WHERE type='service' AND service_type='http'
		  AND domain IS NOT NULL AND domain <> ''`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count web assets: %w", err)
	}
	if offset >= total {
		return []*WebAssetSummary{}, total, nil
	}

	rows, err := s.db.Query(`
		WITH agg AS (
			SELECT a.domain,
			       count(*)                                  AS service_count,
			       array_remove(array_agg(DISTINCT NULLIF(a.ip,'')), NULL) AS ips,
			       array_remove(array_agg(DISTINCT NULLIF(a.page_title,'')), NULL) AS titles,
			       array_agg(DISTINCT status_code) FILTER (WHERE status_code IS NOT NULL) AS codes,
			       max(last_seen)                            AS last_seen
			FROM assets a
			WHERE type='service' AND service_type='http' AND a.domain IS NOT NULL AND a.domain <> ''
			GROUP BY domain
		),
		eps AS (
			SELECT domain, count(*) AS endpoint_count
			FROM assets WHERE type='endpoint' AND domain IS NOT NULL AND domain <> ''
			GROUP BY domain
		),
		tech AS (
			SELECT domain, array_remove(array_agg(DISTINCT t), NULL) AS technologies
			FROM assets a, unnest(a.technologies) AS t
			WHERE a.type='service' AND a.service_type='http' AND a.domain IS NOT NULL
			GROUP BY domain
		),
		-- service.ip 常常为空（HTTP 探测只拿到 URL），所以 IP 列同时并上
		-- 「该域名的子域解析到的 IP」。独立成 CTE 而不是相关子查询：GROUP BY
		-- 之后原始列不可引用，而相关子查询里也不能对聚合键求值。
		resolved AS (
			SELECT sd.domain AS domain,
			       array_remove(array_agg(DISTINCT i.ip), NULL) AS ips
			FROM assets sd
			JOIN asset_edges e ON e.src_id = sd.id AND e.kind = 'resolves_to'
			JOIN assets i ON i.id = e.dst_id
			WHERE sd.type='subdomain' AND sd.domain IS NOT NULL AND sd.domain <> ''
			GROUP BY sd.domain
		),
		tg AS (
			SELECT domain, array_remove(array_agg(DISTINCT t), NULL) AS tags
			FROM assets a, unnest(a.tags) AS t
			WHERE a.type IN ('service','endpoint') AND a.domain IS NOT NULL
			GROUP BY domain
		)
		SELECT agg.domain, agg.service_count, COALESCE(eps.endpoint_count,0),
		       COALESCE(array_length(agg.ips,1),0),
		       -- 并集而不是 COALESCE：service.ip 与子域解析 IP 各有各的来源，
		       -- 取第一个非空会在「service 没 IP、子域有 IP」时把后者丢掉。
		       -- 数组列必须走 array_to_json：Postgres 的数组文本格式是 {a,b}，
		       -- 不是 JSON 的 ["a","b"]，直接 json.Unmarshal 会失败并静默留下空切片。
		       array_to_json(ARRAY(SELECT DISTINCT x FROM unnest(COALESCE(resolved.ips,'{}') || COALESCE(agg.ips,'{}')) AS x WHERE x IS NOT NULL AND x <> ''))::text AS ips,
		       array_to_json(agg.titles)::text, array_to_json(agg.codes)::text,
		       array_to_json(COALESCE(tech.technologies, '{}'))::text, array_to_json(COALESCE(tg.tags, '{}'))::text,
		       agg.last_seen::text
		FROM agg
		LEFT JOIN eps  ON eps.domain  = agg.domain
		LEFT JOIN tech ON tech.domain = agg.domain
		LEFT JOIN tg   ON tg.domain   = agg.domain
		LEFT JOIN resolved ON resolved.domain = agg.domain
		ORDER BY agg.last_seen DESC, agg.domain
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list web assets: %w", err)
	}
	defer rows.Close()

	out := []*WebAssetSummary{}
	for rows.Next() {
		var w WebAssetSummary
		var ips, titles, techs, tags []byte
		var codes []byte
		if err := rows.Scan(&w.Domain, &w.ServiceCount, &w.EndpointCount, new(int),
			&ips, &titles, &codes, &techs, &tags, &w.LastSeen); err != nil {
			return nil, 0, err
		}
		// 空数组统一成 []，避免前端拿到 null 还要判空。
		w.IPs, w.Titles, w.StatusCodes, w.Technologies, w.Tags = []string{}, []string{}, []int{}, []string{}, []string{}
		_ = json.Unmarshal(ips, &w.IPs)
		_ = json.Unmarshal(titles, &w.Titles)
		_ = json.Unmarshal(codes, &w.StatusCodes)
		_ = json.Unmarshal(techs, &w.Technologies)
		_ = json.Unmarshal(tags, &w.Tags)
		out = append(out, &w)
	}
	return out, total, rows.Err()
}

// WebAssetDetail 是单个 WEB 资产（一个域名）的完整暴露面。
type WebAssetDetail struct {
	Domain    string            `json:"domain"`
	Services  []*Asset          `json:"services"`
	Endpoints []*Asset          `json:"endpoints"`
	Findings  []WebAssetFinding `json:"findings"`
	IPs       []*GraphNodeRef   `json:"ips"`
	Hosts     []*GraphNodeRef   `json:"hosts"`
}

// WebAssetFinding 是与该域名相关的漏洞摘要。
type WebAssetFinding struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Severity string `json:"severity"`
	Status   string `json:"status"`
	AssetID  int64  `json:"asset_id"`
}

// WebAssetDetail 取一个域名下的服务、接口、关联漏洞，以及解析到的 IP / 主机。
func (s *AssetStore) WebAssetDetail(domain string) (*WebAssetDetail, error) {
	d := &WebAssetDetail{Domain: domain, Services: []*Asset{}, Endpoints: []*Asset{}, Findings: []WebAssetFinding{}, IPs: []*GraphNodeRef{}, Hosts: []*GraphNodeRef{}}

	// 服务与接口
	for _, q := range []struct {
		sql  string
		dest *[]*Asset
	}{
		{assetSelectCols + ` WHERE type='service' AND service_type='http' AND domain=$1 ORDER BY url`, &d.Services},
		{assetSelectCols + ` WHERE type='endpoint' AND domain=$1 ORDER BY url, method`, &d.Endpoints},
	} {
		rows, err := s.db.Query(q.sql, domain)
		if err != nil {
			return nil, fmt.Errorf("web asset detail: %w", err)
		}
		assets, err := scanAssets(rows)
		rows.Close()
		if err != nil {
			return nil, fmt.Errorf("web asset detail: %w", err)
		}
		*q.dest = assets
	}
	if len(d.Services) == 0 && len(d.Endpoints) == 0 {
		return nil, ErrAssetNotFound
	}

	// 关联漏洞：findings.asset_ids 是 JSONB 数组，用 @> 反查。
	ids := make([]int64, 0, len(d.Services)+len(d.Endpoints))
	for _, a := range d.Services {
		ids = append(ids, a.ID)
	}
	for _, a := range d.Endpoints {
		ids = append(ids, a.ID)
	}
	if len(ids) > 0 {
		rows, err := s.db.Query(`
			SELECT f.id, COALESCE(NULLIF(f.name,''), f.vulnclass), f.severity, f.status,
			       COALESCE((f.asset_ids->>0)::bigint, 0)
			FROM findings f
			WHERE EXISTS (
				SELECT 1 FROM jsonb_array_elements_text(f.asset_ids) AS aid
				WHERE aid::bigint = ANY($1::bigint[])
			)
			ORDER BY f.created_at DESC`, ids)
		if err == nil {
			for rows.Next() {
				var f WebAssetFinding
				if err := rows.Scan(&f.ID, &f.Name, &f.Severity, &f.Status, &f.AssetID); err == nil {
					d.Findings = append(d.Findings, f)
				}
			}
			rows.Close()
		}
	}

	// 解析到的 IP（子域 → IP）与 IP 上的主机。
	ipRows, err := s.db.Query(`
		SELECT DISTINCT i.id, i.type, COALESCE(NULLIF(i.ip,''), i.type||'#'||i.id::text)
		FROM assets sd
		JOIN asset_edges e ON e.src_id = sd.id AND e.kind = 'resolves_to'
		JOIN assets i ON i.id = e.dst_id
		WHERE sd.type='subdomain' AND sd.domain = $1`, domain)
	if err == nil {
		for ipRows.Next() {
			r := &GraphNodeRef{}
			if err := ipRows.Scan(&r.ID, &r.Type, &r.Label); err == nil {
				d.IPs = append(d.IPs, r)
			}
		}
		ipRows.Close()
	}
	for _, ip := range d.IPs {
		hRows, err := s.db.Query(`
			SELECT DISTINCT h.id, h.type, COALESCE(NULLIF(h.hostname,''), h.type||'#'||h.id::text)
			FROM asset_edges e JOIN assets h ON h.id = e.src_id
			WHERE e.dst_id = $1 AND e.kind = 'runs_on' AND h.type = 'host'`, ip.ID)
		if err != nil {
			continue
		}
		for hRows.Next() {
			r := &GraphNodeRef{}
			if err := hRows.Scan(&r.ID, &r.Type, &r.Label); err == nil {
				d.Hosts = append(d.Hosts, r)
			}
		}
		hRows.Close()
	}
	return d, nil
}

// CountsByIPScope 按网络位置统计 IP 资产数量。
//
// 「公网IP」和「DMZIP」是两个独立视图，但底层都是 type=ip。若两者都拿
// /api/assets/counts 的 ip 总数，DMZIP 页会显示全部 IP 的数量而表格里只有
// DMZ 那几条，看起来像统计错了。所以单独给一个按 ip_scope 聚合的口径。
func (s *AssetStore) CountsByIPScope() (map[string]int, error) {
	rows, err := s.db.Query(`
		SELECT COALESCE(NULLIF(ip_scope,''), 'unknown'), COUNT(*)
		FROM assets WHERE type='ip'
		GROUP BY 1`)
	if err != nil {
		return nil, fmt.Errorf("count by ip_scope: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

// CountPublicIPs 返回公网 IP 数量（不含 DMZ、内网、回环）。
func (s *AssetStore) CountPublicIPs() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM assets WHERE type='ip' AND ip_scope='public'`).Scan(&n)
	return n, err
}

// DeleteByDomains 删除一组域名下的全部 service 与 endpoint 资产。
//
// WEB 资产列表以域名为主键（一行 = 一个站点），勾选与删除的语义也应该是
// 域名而不是单条 service 行——否则删掉一个 service 后同一域名的其它记录
// 还在，列表里那一行看起来「删了但没消失」。
func (s *AssetStore) DeleteByDomains(domains []string) (int64, error) {
	if len(domains) == 0 {
		return 0, nil
	}
	res, err := s.db.Exec(`
		DELETE FROM assets
		WHERE domain = ANY($1::text[])
		  AND type IN ('service','endpoint')`, domains)
	if err != nil {
		return 0, fmt.Errorf("delete by domains: %w", err)
	}
	return res.RowsAffected()
}

// =====================================================================
// 应用清单（type=app）的人工维护
// =====================================================================
//
// 与 Agent 用的 UpsertApp 分开：那条按 bundle_id / app_name 去重、服务于扫描
// 结果登记；这条以 ITAM 编号为主键，服务于人在「应用清单」页维护主数据。

// UpsertAppMasterReq 是应用主数据的写入参数。
type UpsertAppMasterReq struct {
	ItamID      string
	AppName     string // 中文名
	ShortName   string // 英文短名
	FullName    string // 英文全称
	Dept        string
	AppOwner    string
	OpsOwner    string
	Description string
	Category    string
}

// UpsertAppMaster 按 ITAM 编号写入/更新一条应用主数据。
//
// ITAM 是业务主键：有它就按它 upsert，没有则退化成按应用名匹配（允许先登记
// 应用、后补编号）。两者都空直接报错——没有主键的记录无法被关联。
func (s *AssetStore) UpsertAppMaster(req UpsertAppMasterReq) (int64, error) {
	itam := strings.TrimSpace(req.ItamID)
	name := strings.TrimSpace(req.AppName)
	if itam == "" && name == "" {
		return 0, fmt.Errorf("ITAM 编号与应用名称至少填一个")
	}

	var id int64
	var err error
	if itam != "" {
		err = s.db.QueryRow(`
			INSERT INTO assets (type, itam_id, app_name, app_short_name, app_full_name,
			                    dept, app_owner, ops_owner, app_description, category, task_ids)
			VALUES ('app',$1,$2,$3,$4,$5,$6,$7,$8,$9, ARRAY[]::bigint[])
			ON CONFLICT (itam_id) WHERE type='app' AND itam_id IS NOT NULL
			DO UPDATE SET
				app_name        = COALESCE(NULLIF(EXCLUDED.app_name,''),        assets.app_name),
				app_short_name  = COALESCE(NULLIF(EXCLUDED.app_short_name,''),  assets.app_short_name),
				app_full_name   = COALESCE(NULLIF(EXCLUDED.app_full_name,''),   assets.app_full_name),
				dept            = COALESCE(NULLIF(EXCLUDED.dept,''),            assets.dept),
				app_owner       = COALESCE(NULLIF(EXCLUDED.app_owner,''),       assets.app_owner),
				ops_owner       = COALESCE(NULLIF(EXCLUDED.ops_owner,''),       assets.ops_owner),
				app_description = COALESCE(NULLIF(EXCLUDED.app_description,''), assets.app_description),
				category        = COALESCE(NULLIF(EXCLUDED.category,''),        assets.category),
				updated_at      = now()
			RETURNING id`,
			itam, name, req.ShortName, req.FullName, req.Dept,
			req.AppOwner, req.OpsOwner, req.Description, req.Category).Scan(&id)
	} else {
		// 没有 ITAM 号：按应用名找，找不到就插一条。
		if e := s.db.QueryRow(`SELECT id FROM assets WHERE type='app' AND app_name=$1 LIMIT 1`, name).Scan(&id); e == nil {
			_, err = s.db.Exec(`
				UPDATE assets SET
					app_short_name  = COALESCE(NULLIF($2,''), app_short_name),
					app_full_name   = COALESCE(NULLIF($3,''), app_full_name),
					dept            = COALESCE(NULLIF($4,''), dept),
					app_owner       = COALESCE(NULLIF($5,''), app_owner),
					ops_owner       = COALESCE(NULLIF($6,''), ops_owner),
					app_description = COALESCE(NULLIF($7,''), app_description),
					category        = COALESCE(NULLIF($8,''), category),
					updated_at      = now()
				WHERE id=$1`,
				id, req.ShortName, req.FullName, req.Dept, req.AppOwner,
				req.OpsOwner, req.Description, req.Category)
		} else {
			err = s.db.QueryRow(`
				INSERT INTO assets (type, app_name, app_short_name, app_full_name,
				                    dept, app_owner, ops_owner, app_description, category, task_ids)
				VALUES ('app',$1,$2,$3,$4,$5,$6,$7,$8, ARRAY[]::bigint[])
				RETURNING id`,
				name, req.ShortName, req.FullName, req.Dept, req.AppOwner,
				req.OpsOwner, req.Description, req.Category).Scan(&id)
		}
	}
	if err != nil {
		return 0, fmt.Errorf("upsert app master: %w", err)
	}
	return id, nil
}

// AppListItem 是应用清单列表的一行。
type AppListItem struct {
	*Asset
	// RelatedApps 是「关联应用」数量。应用之间通过共享资产（同一域名/IP/主机）
	// 间接相关——这是 CMDB 里最常见的关联来源，不需要额外维护关系表。
	RelatedApps int `json:"related_apps"`
}

// ListApps 返回应用清单，按关联资产数量降序。
func (s *AssetStore) ListApps(limit, offset int) ([]*AppListItem, int, error) {
	var total int
	if err := s.db.QueryRow(`SELECT count(*) FROM assets WHERE type='app'`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count apps: %w", err)
	}
	if offset >= total {
		return []*AppListItem{}, total, nil
	}
	rows, err := s.db.Query(assetSelectCols+`
		WHERE type='app'
		ORDER BY (SELECT count(*) FROM task_asset_links t WHERE t.asset_id = assets.id) DESC, id
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list apps: %w", err)
	}
	assets, err := scanAssets(rows)
	rows.Close()
	if err != nil {
		return nil, 0, err
	}

	// 关联应用数：同属一个企业的其它应用数量。
	//
	// 这是当前数据模型能给出的最强关联——资产没有直接指向应用的字段（扫描
	// 发现的是资产，应用是人维护的业务视角），所以用企业归属作为共同父节点。
	out := make([]*AppListItem, 0, len(assets))
	for _, a := range assets {
		var n int
		if a.CompanyID != nil {
			_ = s.db.QueryRow(`SELECT count(*) FROM assets
				WHERE type='app' AND company_id=$1 AND id <> $2`, *a.CompanyID, a.ID).Scan(&n)
		}
		out = append(out, &AppListItem{Asset: a, RelatedApps: n})
	}
	return out, total, nil
}

// AppDetail 是应用详情：主数据 + 关联的全部资产。
type AppDetail struct {
	App         *Asset   `json:"app"`
	Domains     []*Asset `json:"domains"`
	Websites    []*Asset `json:"websites"`
	Services    []*Asset `json:"services"`
	PublicIPs   []*Asset `json:"public_ips"`
	DmzIPs      []*Asset `json:"dmz_ips"`
	Hosts       []*Asset `json:"hosts"`
	Nats        []*Asset `json:"nats"`
	RelatedApps []*Asset `json:"related_apps"`
}

// AppDetail 取一个应用及其关联资产。
//
// 关联口径：资产表上的 company_id 指向应用所属企业时视为关联。这是当前数据
// 模型能给出的最强关联——资产没有直接指向应用的字段（扫描发现的是资产，
// 应用是人维护的业务视角）。若应用没有企业归属，则退回按应用 ICP 匹配。
func (s *AssetStore) AppDetail(id int64) (*AppDetail, error) {
	app, err := s.GetByID(id)
	if err != nil {
		return nil, err
	}
	if app.Type != "app" {
		return nil, ErrAssetNotFound
	}
	d := &AppDetail{App: app, Domains: []*Asset{}, Websites: []*Asset{}, Services: []*Asset{},
		PublicIPs: []*Asset{}, DmzIPs: []*Asset{}, Hosts: []*Asset{}, Nats: []*Asset{}, RelatedApps: []*Asset{}}

	// 取关联资产的公共条件：优先 company_id，其次 app_icp。
	load := func(where string, args ...any) []*Asset {
		rows, err := s.db.Query(assetSelectCols+` WHERE `+where, args...)
		if err != nil {
			return []*Asset{}
		}
		defer rows.Close()
		out, _ := scanAssets(rows)
		if out == nil {
			return []*Asset{}
		}
		return out
	}

	if app.CompanyID == nil {
		return d, nil
	}
	cid := *app.CompanyID
	d.Domains = load(`type='root_domain' AND company_id=$1 ORDER BY domain`, cid)
	d.Websites = load(`type='service' AND service_type='http' AND company_id=$1 ORDER BY domain, url`, cid)
	d.Services = load(`type='service' AND service_type='other' AND company_id=$1 ORDER BY ip, port`, cid)
	d.RelatedApps = load(`type='app' AND company_id=$1 AND id <> $2 ORDER BY app_name`, cid, id)

	// IP / 主机 / NAT 常常没有 company_id：企业 scope 一般只配域名，扫描出来的
	// IP 靠 scope 归因匹配不上。所以这里沿资产图扩展——从该企业的域名出发，
	// 走到解析到的 IP、IP 上跑的主机、以及两端的 NAT。这比要求用户给每个 IP
	// 手工配 scope 现实得多。
	//
	// 用递归 CTE 从企业域名集合出发，沿 belongs_to / resolves_to / runs_on /
	// maps_to / exposed_via 展开到任意深度（最多 6 层防环），再按类型分桶。
	// CTE 必须放在 SELECT 最前面，不能嵌在 IN (...) 里——那样 Postgres 会当成
	// 语法错误，而错误被忽略后表现为「关联资产永远为空」。
	//
	// 先取 id 集合，再用 assetSelectCols 查完整行：把 30+ 列的清单拼进 CTE
	// 末尾既难读又容易与 scanAssets 的列序脱节。
	// 递归 CTE 只允许一个递归项，所以正反两个方向各写一条，结果 UNION 起来。
	// 正向：沿 dst 展开（从域名走到 IP/主机）；反向：沿 src 展开（从 IP 回到
	// 引用它的子域/NAT）。两个方向都需要——只看正向会漏掉「NAT 指向本企业 IP」
	// 这种上游关系。
	idRows, idErr := s.db.Query(`
		WITH RECURSIVE
		seeds AS (
			SELECT id FROM assets
			WHERE company_id = $1 AND type IN ('root_domain','subdomain','service','ip')
		),
		down AS (
			SELECT id, ARRAY[id] AS path FROM seeds
			UNION ALL
			SELECT e.dst_id, d.path || e.dst_id
			FROM down d JOIN asset_edges e ON e.src_id = d.id
			WHERE array_length(d.path, 1) < 6 AND NOT (e.dst_id = ANY(d.path))
		),
		-- 反向遍历的种子必须包含正向到达的节点：NAT 的边是「NAT → IP」，
		-- 只有从 IP 出发才走得到 NAT，而 IP 通常是正向从子域走过来的。
		up AS (
			SELECT id, ARRAY[id] AS path FROM (SELECT id FROM seeds UNION SELECT id FROM down) s0
			UNION ALL
			SELECT e.src_id, u.path || e.src_id
			FROM up u JOIN asset_edges e ON e.dst_id = u.id
			WHERE array_length(u.path, 1) < 8 AND NOT (e.src_id = ANY(u.path))
		)
		-- 第三段：从 up 到达的节点（典型是 NAT）再正向展开一次，才能走到
		-- NAT 另一侧的内网 IP 与主机。没有这一段，DMZ/主机永远进不了应用详情。
		, down2 AS (
			SELECT id, ARRAY[id] AS path FROM (SELECT id FROM seeds UNION SELECT id FROM down UNION SELECT id FROM up) s1
			UNION ALL
			SELECT e.dst_id, d.path || e.dst_id
			FROM down2 d JOIN asset_edges e ON e.src_id = d.id
			WHERE array_length(d.path, 1) < 8 AND NOT (e.dst_id = ANY(d.path))
		)
		SELECT DISTINCT id FROM (
			SELECT id FROM down UNION SELECT id FROM up UNION SELECT id FROM down2
		) t`, cid)
	if idErr == nil {
		var ids []int64
		for idRows.Next() {
			var one int64
			if err := idRows.Scan(&one); err == nil {
				ids = append(ids, one)
			}
		}
		idRows.Close()
		if len(ids) > 0 {
			related := load(`id = ANY($1::bigint[])`, ids)
			for _, a := range related {
				switch {
				case a.Type == "ip" && a.IPScope == "dmz":
					d.DmzIPs = append(d.DmzIPs, a)
				case a.Type == "ip":
					d.PublicIPs = append(d.PublicIPs, a)
				case a.Type == "host":
					d.Hosts = append(d.Hosts, a)
				case a.Type == "nat":
					d.Nats = append(d.Nats, a)
				}
			}
		}
	}
	return d, nil
}
