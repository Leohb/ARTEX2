"use client";

import * as React from "react";

import Link from "next/link";

import { ChevronLeftIcon, ChevronRightIcon, ExternalLinkIcon, RefreshCwIcon } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api";
import type { WebAssetSummary } from "@/lib/types";
import { cn } from "@/lib/utils";

import { Chips, PAGE_SIZES, statusTone } from "./asset-table";

// WebAssetsPanel 是「WEB资产」列表：一行 = 一个域名。
//
// 与其它资产页不同，这里的主键是域名而不是 assets.id。一个域名上通常跑多个
// 服务、每个服务下多个接口，按行展示会让同一个站点散成很多条。分页必须作用
// 在域名上，所以聚合在服务端做（见 GET /api/web-assets）。
export function WebAssetsPanel() {
  const [rows, setRows] = React.useState<WebAssetSummary[]>([]);
  const [total, setTotal] = React.useState(0);
  const [page, setPage] = React.useState(0);
  const [size, setSize] = React.useState(50);
  const [loading, setLoading] = React.useState(false);
  const [loaded, setLoaded] = React.useState(false);

  const load = React.useCallback(() => {
    setLoading(true);
    api
      .webAssets(size, page * size)
      .then((r) => {
        setRows(r.assets);
        setTotal(r.total);
      })
      .catch((e) => toast.error("加载 WEB 资产失败：" + String((e as Error)?.message ?? e)))
      .finally(() => {
        setLoading(false);
        setLoaded(true);
      });
  }, [size, page]);

  React.useEffect(() => {
    load();
  }, [load]);

  const pageCount = Math.max(1, Math.ceil(total / size));
  const start = total === 0 ? 0 : page * size + 1;
  const end = page * size + rows.length;

  return (
    <div className="mt-0 flex min-h-0 flex-1 flex-col gap-2">
      <div className="flex items-center justify-between">
        <span className="text-xs text-muted-foreground">
          按域名聚合，共 <span className="tabular-nums">{total}</span> 个 WEB 资产
        </span>
        <Button variant="outline" size="sm" onClick={load} disabled={loading}>
          <RefreshCwIcon className={cn("size-4", loading && "animate-spin")} /> 刷新
        </Button>
      </div>

      <Card className="flex min-h-0 flex-1 flex-col overflow-hidden py-0">
        <div className="min-h-0 flex-1 overflow-auto">
          <Table>
            <TableHeader className="sticky top-0 z-10 bg-card">
              <TableRow>
                <TableHead>域名</TableHead>
                <TableHead className="w-24 text-right">服务</TableHead>
                <TableHead className="w-24 text-right">URL路径</TableHead>
                <TableHead className="w-24 text-right">漏洞</TableHead>
                <TableHead>IP</TableHead>
                <TableHead className="w-20">状态码</TableHead>
                <TableHead>指纹</TableHead>
                <TableHead className="w-16 text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((w) => (
                <TableRow key={w.domain} className="cursor-pointer hover:bg-muted/40">
                  <TableCell>
                    <Link
                      prefetch={false}
                      href={`/function/assets/web-detail?domain=${encodeURIComponent(w.domain)}`}
                      className="font-mono text-xs font-medium text-blue-600 hover:underline dark:text-blue-400"
                    >
                      {w.domain}
                    </Link>
                    {w.titles.length > 0 && (
                      <div
                        className="mt-0.5 max-w-md truncate text-[11px] text-muted-foreground"
                        title={w.titles.join(" / ")}
                      >
                        {w.titles.join(" / ")}
                      </div>
                    )}
                  </TableCell>
                  <TableCell className="text-right tabular-nums text-sm">{w.service_count}</TableCell>
                  <TableCell className="text-right tabular-nums text-sm">{w.endpoint_count}</TableCell>
                  <TableCell className="text-right tabular-nums text-sm">
                    {w.finding_count > 0 ? (
                      <span className="font-medium text-amber-600">{w.finding_count}</span>
                    ) : (
                      <span className="text-muted-foreground">0</span>
                    )}
                  </TableCell>
                  <TableCell>
                    <Chips items={w.ips} mono />
                  </TableCell>
                  <TableCell>
                    <div className="flex flex-wrap gap-1">
                      {w.status_codes.length === 0 ? (
                        <span className="text-xs text-muted-foreground">—</span>
                      ) : (
                        w.status_codes.map((c) => (
                          <span key={c} className={cn("font-mono text-xs font-semibold tabular-nums", statusTone(c))}>
                            {c}
                          </span>
                        ))
                      )}
                    </div>
                  </TableCell>
                  <TableCell>
                    <Chips items={w.technologies.slice(0, 4)} />
                  </TableCell>
                  <TableCell className="text-right">
                    <Button variant="ghost" size="sm" className="h-7" asChild>
                      <Link
                        prefetch={false}
                        href={`/function/assets/web-detail?domain=${encodeURIComponent(w.domain)}`}
                      >
                        详情 <ExternalLinkIcon className="size-3" />
                      </Link>
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
              {rows.length === 0 && (
                <TableRow>
                  <TableCell colSpan={8} className="py-12 text-center text-sm text-muted-foreground">
                    {loaded ? "暂无 WEB 资产（HTTP 协议的公网服务）。" : "加载中…"}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
        {total > 0 && (
          <div className="flex shrink-0 items-center gap-2 border-t px-3 py-1.5 text-xs text-muted-foreground">
            <Select value={String(size)} onValueChange={(v) => setSize(Number(v))}>
              <SelectTrigger size="sm" className="h-7 w-24">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {PAGE_SIZES.map((n) => (
                    <SelectItem key={n} value={String(n)}>
                      {n} / 页
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
            <span className="tabular-nums">
              {start}–{end} / {total}
            </span>
            {pageCount > 1 && (
              <div className="flex items-center gap-2">
                <Button
                  variant="outline"
                  size="icon"
                  className="size-7"
                  disabled={page <= 0}
                  onClick={() => setPage((p) => Math.max(0, p - 1))}
                >
                  <ChevronLeftIcon />
                </Button>
                <span className="tabular-nums">
                  {page + 1} / {pageCount}
                </span>
                <Button
                  variant="outline"
                  size="icon"
                  className="size-7"
                  disabled={page + 1 >= pageCount}
                  onClick={() => setPage((p) => Math.min(pageCount - 1, p + 1))}
                >
                  <ChevronRightIcon />
                </Button>
              </div>
            )}
          </div>
        )}
      </Card>
    </div>
  );
}

// WebAssetBadge 供详情页复用严重度着色。
export function SeverityBadge({ severity }: { severity: string }) {
  const s = severity.toLowerCase();
  const tone =
    s === "critical"
      ? "border-red-500/50 text-red-600"
      : s === "high"
        ? "border-orange-500/50 text-orange-600"
        : s === "medium"
          ? "border-amber-500/50 text-amber-600"
          : "border-slate-400/40 text-slate-600";
  return (
    <Badge variant="outline" className={cn("text-[10px]", tone)}>
      {severity || "未评级"}
    </Badge>
  );
}
