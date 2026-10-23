import { redirect } from "next/navigation";

// 资产入口已改为二级菜单：/function/assets/<type>。旧链接（书签、外部引用、
// 浏览器历史）落到这里时转到默认视图，避免 404。
//
// 用 redirect() 而不是客户端跳转：静态导出会把这条路由预渲染成一个带
// meta refresh 的页面，直接访问也能立刻跳走。
export default function AssetsIndexPage() {
  redirect("/function/assets/company");
}
