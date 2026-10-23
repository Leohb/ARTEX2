"use client";

import * as React from "react";

import Link from "next/link";
import { useSearchParams } from "next/navigation";

import { ArrowLeftIcon, ExternalLinkIcon, ServerIcon } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api";
import type { WebAssetDetail } from "@/lib/types";
import { cn } from "@/lib/utils";

import { Chips, MethodBadge, statusTone } from "../_components/asset-table";
import { SeverityBadge } from "../_components/web-assets-panel";

// WEB 资产详情页：一个域名的完整暴露面。
//
// 列表页以域名为主键，这里回答「这个站点下面到底有什么」：跑了哪些服务、
// 每个服务下有哪些 URL 路径（接口）、关联了哪些漏洞、解析到哪个 IP、
// 这个 IP 上又是哪台主机（DMZ/内网）。
//
// 用 ?domain= 而不是动态路由段：静态导出（output: export）要求动态路由在构建期
// 枚举全部取值，而域名是运行期数据，不可能预生成。项目里其它详情页（任务、
// 发现）也是同一做法。
function WebAssetDetailInner() {
  const searchParams = useSearchParams();
  const domain = searchParams.get("domain") ?? "";

  const [data, setData] = React.useState<WebAssetDetail | null>(null);
  const [err, setErr] = React.useState("");
  const [loading, setLoading] = React.useState(true);

  React.useEffect(() => {
    if (!domain) {
      setLoading(false);
      setErr("缺少 domain 参数");
      return;
    }
    setLoading(true);
    api
      .webAssetDetail(domain)
      .then((d) => {
        setData(d);
        setErr("");
      })
      .catch((e) => setErr(String((e as Error)?.message ?? e)))
      .finally(() => setLoading(false));
  }, [domain]);

  const back = (
    <Button variant="outline" size="sm" asChild>
      <Link prefetch={false} href="/function/assets/web">
        <ArrowLeftIcon className="size-3.5" /> 返回 WEB 资产
      </Link>
    </Button>
  );

  if (loading) {
    return <div className="p-6 text-sm text-muted-foreground">加载中…</div>;
  }
  if (err || !data) {
    return (
      <div className="space-y-3 p-6">
        {back}
        <div className="text-sm text-destructive">{err || "该域名下没有 WEB 资产"}</div>
      </div>
    );
  }

  return (
    <div className="flex h-[calc(100vh-6rem)] min-h-0 flex-col gap-4">
      <div className="flex items-center justify-between gap-4">
        <div className="flex items-center gap-3">
          {back}
          <ServerIcon className="size-5 text-muted-foreground" />
          <h1 className="font-mono text-xl font-semibold tracking-tight">{data.domain}</h1>
        </div>
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <Badge variant="secondary">{data.services.length} 个服务</Badge>
          <Badge variant="secondary">{data.endpoints.length} 条 URL 路径</Badge>
          <Badge variant="secondary">{data.findings.length} 个漏洞</Badge>
        </div>
      </div>

      <div className="min-h-0 flex-1 space-y-4 overflow-auto">
        <Section title="服务" count={data.services.length}>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>URL</TableHead>
                <TableHead className="w-20">状态码</TableHead>
                <TableHead>标题</TableHead>
                <TableHead>指纹</TableHead>
                <TableHead className="w-24">IP</TableHead>
                <TableHead className="w-16">端口</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.services.map((s) => (
                <TableRow key={s.id}>
                  <TableCell className="max-w-sm truncate font-mono text-xs">
                    {s.url ? (
                      <a
                        href={s.url}
                        target="_blank"
                        rel="noreferrer"
                        className="text-blue-600 hover:underline dark:text-blue-400"
                      >
                        {s.url} <ExternalLinkIcon className="inline size-3" />
                      </a>
                    ) : (
                      "—"
                    )}
                  </TableCell>
                  <TableCell>
                    {s.status_code != null ? (
                      <span className={cn("font-mono text-xs font-semibold tabular-nums", statusTone(s.status_code))}>
                        {s.status_code}
                      </span>
                    ) : (
                      "—"
                    )}
                  </TableCell>
                  <TableCell className="max-w-[14rem] truncate text-xs">{s.page_title || "—"}</TableCell>
                  <TableCell>
                    <Chips items={s.technologies ?? []} />
                  </TableCell>
                  <TableCell className="font-mono text-xs">{s.ip || "—"}</TableCell>
                  <TableCell className="font-mono text-xs tabular-nums">{s.port ?? "—"}</TableCell>
                </TableRow>
              ))}
              {data.services.length === 0 && <EmptyRow cols={6} text="暂无服务" />}
            </TableBody>
          </Table>
        </Section>

        <Section title="URL 路径" count={data.endpoints.length}>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-20">方法</TableHead>
                <TableHead>URL</TableHead>
                <TableHead>参数</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.endpoints.map((e) => (
                <TableRow key={e.id}>
                  <TableCell>
                    <MethodBadge method={e.method || ""} />
                  </TableCell>
                  <TableCell className="max-w-lg truncate font-mono text-xs" title={e.url}>
                    {e.url || "—"}
                  </TableCell>
                  <TableCell>
                    <Chips
                      items={(e.params ?? []).map((p) => {
                        const item = p as Record<string, string>;
                        return item.name ? `${item.name}(${item.location || "?"})` : "?";
                      })}
                      mono
                    />
                  </TableCell>
                </TableRow>
              ))}
              {data.endpoints.length === 0 && <EmptyRow cols={3} text="暂无 URL 路径" />}
            </TableBody>
          </Table>
        </Section>

        <Section title="关联漏洞" count={data.findings.length}>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-24">严重度</TableHead>
                <TableHead>名称</TableHead>
                <TableHead className="w-28">状态</TableHead>
                <TableHead className="w-16 text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.findings.map((f) => (
                <TableRow key={f.id}>
                  <TableCell>
                    <SeverityBadge severity={f.severity} />
                  </TableCell>
                  <TableCell className="text-xs">{f.name || "—"}</TableCell>
                  <TableCell className="text-xs">{f.status}</TableCell>
                  <TableCell className="text-right">
                    <Button variant="ghost" size="sm" className="h-7" asChild>
                      <Link prefetch={false} href={`/function/findings/detail?id=${f.id}`}>
                        详情
                      </Link>
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
              {data.findings.length === 0 && <EmptyRow cols={4} text="暂无关联漏洞" />}
            </TableBody>
          </Table>
        </Section>

        <Section title="解析与承载" count={data.ips.length + data.hosts.length}>
          <div className="space-y-2 p-3 text-xs">
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-muted-foreground">域名</span>
              <Badge variant="outline" className="font-mono">
                {data.domain}
              </Badge>
              <span className="text-muted-foreground">→ 解析到</span>
              {data.ips.length === 0 ? (
                <span className="text-muted-foreground">（未记录 IP）</span>
              ) : (
                data.ips.map((ip) => (
                  <Badge key={ip.id} variant="secondary" className="font-mono">
                    {ip.label}
                  </Badge>
                ))
              )}
            </div>
            {data.hosts.length > 0 && (
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-muted-foreground">承载主机</span>
                {data.hosts.map((h) => (
                  <Badge key={h.id} variant="secondary" className="font-mono">
                    {h.label}
                  </Badge>
                ))}
              </div>
            )}
            {data.ips.length === 0 && data.hosts.length === 0 && (
              <div className="text-muted-foreground">暂无解析记录。可在「公网IP」或「主机资产」页补充。</div>
            )}
          </div>
        </Section>
      </div>
    </div>
  );
}

function Section({ title, count, children }: { title: string; count: number; children: React.ReactNode }) {
  return (
    <Card className="overflow-hidden py-0">
      <div className="flex items-center gap-2 border-b px-3 py-2">
        <span className="text-sm font-medium">{title}</span>
        <Badge variant="secondary" className="tabular-nums">
          {count}
        </Badge>
      </div>
      <div className="overflow-auto">{children}</div>
    </Card>
  );
}

function EmptyRow({ cols, text }: { cols: number; text: string }) {
  return (
    <TableRow>
      <TableCell colSpan={cols} className="py-8 text-center text-sm text-muted-foreground">
        {text}
      </TableCell>
    </TableRow>
  );
}

// useSearchParams 在静态导出下必须包在 Suspense 里，否则 build 直接报错。
export default function WebAssetDetailPage() {
  return (
    <React.Suspense fallback={<div className="p-6 text-sm text-muted-foreground">加载中…</div>}>
      <WebAssetDetailInner />
    </React.Suspense>
  );
}
