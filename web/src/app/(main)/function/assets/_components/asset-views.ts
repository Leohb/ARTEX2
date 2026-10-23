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
  nat: {
    key: "nat",
    title: "NAT映射",
    dbType: null,
    placeholderReason:
      "assets 表的 type 约束目前只包含 root_domain / ip / subdomain / app / service / endpoint，没有 NAT 映射记录。需要先扩展数据模型（内网地址、外网地址、协议、端口映射关系）才能展示。",
  },
  dmzip: {
    key: "dmzip",
    title: "DMZIP",
    dbType: null,
    placeholderReason:
      "assets 表没有 DMZ 网段标记字段，无法区分 DMZ 区与普通内网 IP。需要先给资产增加区域/网段属性，或引入独立的 DMZ 资产表。",
  },
  host: {
    key: "host",
    title: "主机资产",
    dbType: null,
    placeholderReason:
      "assets 表没有主机（操作系统级）资产类型。现有 ip 类型记录的是网络地址与开放端口，不包含主机名、操作系统、补丁等主机维度信息。",
  },
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
