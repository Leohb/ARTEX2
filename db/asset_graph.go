package db

import (
	"database/sql"
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
