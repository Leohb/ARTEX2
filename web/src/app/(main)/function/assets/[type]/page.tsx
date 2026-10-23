import { notFound } from "next/navigation";

import AssetsPage from "../_components/asset-view-page";
import { ASSET_VIEW_KEYS, ASSET_VIEWS, isAssetViewKey } from "../_components/asset-views";

// 静态导出（output: "export"）要求动态路由在构建期枚举全部取值，
// 否则 next build 会拒绝生成该路由。
export function generateStaticParams() {
  return ASSET_VIEW_KEYS.map((type) => ({ type }));
}

// Next 16 的页面参数是 Promise，需要 await。
export default async function AssetTypePage({ params }: { params: Promise<{ type: string }> }) {
  const { type } = await params;
  if (!isAssetViewKey(type)) notFound();
  return <AssetsPage view={ASSET_VIEWS[type]} />;
}
