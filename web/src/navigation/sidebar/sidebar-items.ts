import {
  Activity,
  AppWindow,
  Ban,
  BellRing,
  Bot,
  Boxes,
  Brain,
  Bug,
  Building2,
  ClipboardList,
  FolderOpen,
  FolderSync,
  Globe,
  Layers,
  LayoutDashboard,
  Link2,
  type LucideIcon,
  MessageSquare,
  Monitor,
  Network,
  Plug,
  Radio,
  Router,
  ScrollText,
  Server,
  Settings2,
  ShieldAlert,
  Sparkles,
  Target,
  Terminal,
  Waypoints,
  Wrench,
} from "lucide-react";

export type NavBadge = "new" | "soon";

export interface NavSubItem {
  id: string;
  title: string;
  url: string;
  icon?: LucideIcon;
  badge?: NavBadge;
  disabled?: boolean;
  newTab?: boolean;
}

interface NavItemBase {
  id: string;
  title: string;
  icon?: LucideIcon;
  badge?: NavBadge;
  disabled?: boolean;
  newTab?: boolean;
}

export interface NavMainLinkItem extends NavItemBase {
  url: string;
  subItems?: never;
}

export interface NavMainParentItem extends NavItemBase {
  subItems: NavSubItem[];
}

export type NavMainItem = NavMainLinkItem | NavMainParentItem;

export interface NavGroup {
  id: number;
  label?: string;
  items: NavMainItem[];
}

export const sidebarItems: NavGroup[] = [
  {
    id: 1,
    label: "功能",
    items: [
      { id: "dashboard", title: "仪表盘", url: "/dashboard", icon: LayoutDashboard },
      { id: "chat", title: "对话", url: "/chat", icon: MessageSquare },
      { id: "tasks", title: "任务", url: "/function/tasks", icon: Target },
      { id: "findings", title: "发现", url: "/function/findings", icon: Bug },
      { id: "traffic", title: "流量", url: "/function/traffic", icon: Activity },
      { id: "commands", title: "工具执行", url: "/function/commands", icon: Terminal },
      { id: "llm-records", title: "LLM 录制", url: "/function/llm-records", icon: Radio },
      {
        // 资产从单一入口改为可展开的二级菜单：每类资产有独立页面，可深链、
        // 可前进后退，也能在侧边栏一眼看出共有哪些维度。
        id: "assets",
        title: "资产",
        icon: Network,
        subItems: [
          { id: "assets-company", title: "企业", url: "/function/assets/company", icon: Building2 },
          { id: "assets-root-domain", title: "主域名", url: "/function/assets/root_domain", icon: Globe },
          { id: "assets-subdomain", title: "子域名", url: "/function/assets/subdomain", icon: Waypoints },
          { id: "assets-ip", title: "公网IP", url: "/function/assets/ip", icon: Network },
          { id: "assets-service", title: "公网服务", url: "/function/assets/service", icon: Server },
          { id: "assets-web", title: "WEB资产", url: "/function/assets/web", icon: Monitor },
          { id: "assets-endpoint", title: "API接口", url: "/function/assets/endpoint", icon: Link2 },
          { id: "assets-app", title: "应用清单", url: "/function/assets/app", icon: AppWindow },
          { id: "assets-nat", title: "NAT映射", url: "/function/assets/nat", icon: Router },
          { id: "assets-dmzip", title: "DMZIP", url: "/function/assets/dmzip", icon: Layers },
          { id: "assets-host", title: "主机资产", url: "/function/assets/host", icon: Boxes },
        ],
      },
      { id: "sync", title: "资产同步", url: "/function/sync", icon: FolderSync },
      { id: "workspace", title: "工作空间", url: "/function/workspace", icon: FolderOpen },
    ],
  },
  {
    id: 2,
    label: "系统",
    items: [
      { id: "llm", title: "LLM", url: "/system/llm", icon: Brain },
      { id: "agents", title: "Agent", url: "/system/agents", icon: Bot },
      { id: "mcp", title: "MCP", url: "/system/mcp", icon: Plug },
      { id: "skills", title: "Skill", url: "/system/skills", icon: Sparkles },
      { id: "tools", title: "工具", url: "/system/tools", icon: Wrench },
      { id: "notify", title: "通知推送", url: "/system/notify", icon: BellRing },
      { id: "intercept", title: "拦截规则", url: "/system/intercept", icon: ShieldAlert },
      { id: "asset-intercept", title: "资产拦截", url: "/system/intercept/assets", icon: Ban },
      { id: "approvals", title: "审批记录", url: "/system/intercept/approvals", icon: ClipboardList },
      { id: "logs", title: "日志", url: "/system/logs", icon: ScrollText },
      { id: "settings", title: "系统配置", url: "/system/settings", icon: Settings2 },
    ],
  },
];
