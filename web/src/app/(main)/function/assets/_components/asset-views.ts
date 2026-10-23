import {
  AppWindow,
  Boxes,
  Building2,
  Globe,
  Layers,
  type LucideIcon,
  Monitor,
  Network,
  Router,
  Server,
  Waypoints,
} from "lucide-react";

// 资产二级菜单的类型注册表：路由段 → 展示信息 + 取数方式。
//
// 这里的 key 同时是 URL 段（/function/assets/<key>），必须与
// navigation/sidebar/sidebar-items.ts 里的 url 保持一致。
//
// backendType 是 /api/assets?type= 接受的取值；dbType 为 null 表示后端
// assets 表的 CHECK 约束里还没有这个类型（NAT映射 / DMZIP / 主机资产），
// 页面会渲染「尚未接入」而不是去查一个必然为空的结果。

export type AssetViewKey =
  | "company"
  | "root_domain"
  | "subdomain"
  | "ip"
  | "service"
  | "web"
  | "app"
  | "nat"
  | "dmzip"
  | "host";

// 注意：这里只存「纯数据」。图标是 React 组件，而路由页是 server component、
// 视图是 client component —— 把组件当 prop 跨这道边界传会被 Next 拒绝
// （"Functions cannot be passed directly to Client Components"）。
// 因此图标放在下面按 key 查的映射里，由 client 侧自己解析。
export interface AssetView {
  key: AssetViewKey;
  title: string;
  /** /api/assets?type= 的取值；null = 该类型暂无后端数据模型。 */
  dbType: string | null;
  /** 固定附加的 DSL 过滤条件（用于把一个 dbType 拆成多个视图）。 */
  dsl?: string;
  /** 占位页说明；仅在 dbType 为 null 时使用。 */
  placeholderReason?: string;
  /**
   * 用哪种表格渲染。默认等于 key。
   *
   * 需要它是因为「取什么数据」与「怎么展示」不一定一一对应：DMZIP 取的是
   * ip 数据（dbType=ip + ip_scope==dmz），但 view.key 是 dmzip，若直接用
   * key 匹配渲染分支就找不到表格——这正是 DMZIP 页空白的原因。
   */
  renderAs?: AssetViewKey;
}

export const ASSET_VIEWS: Record<AssetViewKey, AssetView> = {
  company: { key: "company", title: "企业", dbType: null }, // 走 /api/companies
  root_domain: { key: "root_domain", title: "主域名", dbType: "root_domain" },
  subdomain: { key: "subdomain", title: "子域名", dbType: "subdomain" },
  ip: { key: "ip", title: "公网IP", dbType: "ip" },
  service: { key: "service", title: "公网服务", dbType: "service" },
  web: {
    key: "web",
    title: "WEB资产",
    dbType: "service",
    // WEB 资产 = HTTP 协议的服务。service 表里 service_type 只有 http/other，
    // 用 DSL 精确切出 http 那一半，而不是在前端再过滤一遍分页结果（那样分页
    // 和总数都会算错）。
    //
    // 语法注意：DSL 不接受操作符两侧留空格（`a == b` 会报“意外的内容 'BARE'”），
    // 必须写成 `a==b`。值不用加引号。
    dsl: "service_type==http",
  },
  app: { key: "app", title: "应用清单", dbType: "app" },
  nat: { key: "nat", title: "NAT映射", dbType: "nat" },
  // DMZ 是组织概念，地址上看不出来，因此靠人工标注的 ip_scope 过滤。
  dmzip: { key: "dmzip", title: "DMZIP", dbType: "ip", dsl: "ip_scope==dmz", renderAs: "ip" },
  host: { key: "host", title: "主机资产", dbType: "host" },
};

const ICONS: Record<AssetViewKey, LucideIcon> = {
  company: Building2,
  root_domain: Globe,
  subdomain: Waypoints,
  ip: Network,
  service: Server,
  web: Monitor,
  app: AppWindow,
  nat: Router,
  dmzip: Layers,
  host: Boxes,
};

export function assetViewIcon(key: AssetViewKey): LucideIcon {
  return ICONS[key];
}

export const ASSET_VIEW_KEYS = Object.keys(ASSET_VIEWS) as AssetViewKey[];

export function isAssetViewKey(v: string): v is AssetViewKey {
  return Object.hasOwn(ASSET_VIEWS, v);
}
