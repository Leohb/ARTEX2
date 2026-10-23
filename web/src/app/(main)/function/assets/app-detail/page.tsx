"use client";

import * as React from "react";

import Link from "next/link";
import { useSearchParams } from "next/navigation";

import { ArrowLeftIcon, ExternalLinkIcon } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api";
import type { AppDetail, Asset } from "@/lib/types";

import { Chips, statusTone } from "../_components/asset-table";

// 应用详情：主数据 + 关联的全部资产。
//
// 关联口径是企业归属（资产的 company_id 指向应用所属企业）。资产没有直接
// 指向应用的字段——扫描发现的是资产，应用是人维护的业务视角。
function AppDetailInner() {
  const searchParams = useSearchParams();
  const id = Number(searchParams.get("id") ?? 0);

  const [data, setData] = React.useState<AppDetail | null>(null);
  const [err, setErr] = React.useState("");
  const [loading, setLoading] = React.useState(true);

  React.useEffect(() => {
    if (!id) {
      setLoading(false);
      setErr("缺少 id 参数");
      return;
    }
    setLoading(true);
    api
      .appDetail(id)
      .then((d) => {
        setData(d);
        setErr("");
      })
      .catch((e) => setErr(String((e as Error)?.message ?? e)))
      .finally(() => setLoading(false));
  }, [id]);

  const back = (
    <Button variant="outline" size="sm" asChild>
      <Link prefetch={false} href="/function/assets/app">
        <ArrowLeftIcon className="size-3.5" /> 返回应用清单
      </Link>
    </Button>
  );

  if (loading) return <div className="p-6 text-sm text-muted-foreground">加载中…</div>;
  if (err || !data) {
    return (
      <div className="space-y-3 p-6">
        {back}
        <div className="text-sm text-destructive">{err || "应用不存在"}</div>
      </div>
    );
  }

  const app = data.app;

  return (
    <div className="flex h-[calc(100vh-6rem)] min-h-0 flex-col gap-4">
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-1">
          <div className="flex items-center gap-3">
            {back}
            <h1 className="text-xl font-semibold tracking-tight">{app.app_name || "未命名应用"}</h1>
            {app.itam_id && (
              <Badge variant="secondary" className="font-mono">
                {app.itam_id}
              </Badge>
            )}
          </div>
          {app.app_full_name && <div className="pl-1 text-sm text-muted-foreground">{app.app_full_name}</div>}
        </div>
      </div>

      {/* 主数据 */}
      <Card className="py-0">
        <div className="grid grid-cols-2 gap-x-8 gap-y-2 p-4 text-sm md:grid-cols-4">
          <Meta label="英文短名" value={app.app_short_name} mono />
          <Meta label="归属部门" value={app.dept} />
          <Meta label="应用负责人" value={app.app_owner} />
          <Meta label="运维负责人" value={app.ops_owner} />
          <Meta label="分类" value={app.category} />
          <Meta label="关联应用" value={String(data.related_apps.length)} />
          <Meta label="说明" value={app.app_description} span2 />
        </div>
      </Card>

      <div className="min-h-0 flex-1 space-y-4 overflow-auto">
        <AssetSection
          title="主域名"
          rows={data.domains}
          cols={["域名", "ICP 备案"]}
          render={(a) => [
            <span key="d" className="font-mono text-xs">
              {a.domain}
            </span>,
            <span key="i" className="text-xs">
              {a.icp || "—"}
            </span>,
          ]}
        />

        <AssetSection
          title="WEB 资产"
          rows={data.websites}
          cols={["域名", "URL", "状态码", "指纹"]}
          render={(a) => [
            <span key="d" className="font-mono text-xs">
              {a.domain || "—"}
            </span>,
            <a
              key="u"
              href={a.url}
              target="_blank"
              rel="noreferrer"
              className="font-mono text-xs text-blue-600 hover:underline dark:text-blue-400"
            >
              {a.url || "—"} <ExternalLinkIcon className="inline size-3" />
            </a>,
            <span
              key="s"
              className={
                a.status_code != null
                  ? `font-mono text-xs font-semibold tabular-nums ${statusTone(a.status_code)}`
                  : "text-xs"
              }
            >
              {a.status_code ?? "—"}
            </span>,
            <Chips key="t" items={a.technologies ?? []} />,
          ]}
        />

        <AssetSection
          title="公网服务"
          rows={data.services}
          cols={["服务", "IP", "端口"]}
          render={(a) => [
            <span key="n" className="font-mono text-xs">
              {a.service_name || a.service_type || "—"}
            </span>,
            <span key="i" className="font-mono text-xs">
              {a.ip || "—"}
            </span>,
            <span key="p" className="font-mono text-xs tabular-nums">
              {a.port ?? "—"}
            </span>,
          ]}
        />

        <AssetSection
          title="公网 IP"
          rows={data.public_ips}
          cols={["IP", "C段", "绑定域名", "开放端口"]}
          render={(a) => [
            <span key="i" className="font-mono text-xs font-medium">
              {a.ip}
            </span>,
            <span key="c" className="font-mono text-xs">
              {a.c_segment || "—"}
            </span>,
            <Chips key="b" items={a.bound_domains ?? []} mono />,
            <Chips
              key="p"
              items={(a.open_ports ?? []).map((p) => (p.service ? `${p.port}/${p.service}` : String(p.port)))}
              mono
            />,
          ]}
        />

        <AssetSection
          title="DMZ IP"
          rows={data.dmz_ips}
          cols={["IP", "C段", "绑定域名", "开放端口"]}
          render={(a) => [
            <span key="i" className="font-mono text-xs font-medium">
              {a.ip}
            </span>,
            <span key="c" className="font-mono text-xs">
              {a.c_segment || "—"}
            </span>,
            <Chips key="b" items={a.bound_domains ?? []} mono />,
            <Chips
              key="p"
              items={(a.open_ports ?? []).map((p) => (p.service ? `${p.port}/${p.service}` : String(p.port)))}
              mono
            />,
          ]}
        />

        <AssetSection
          title="NAT 映射"
          rows={data.nats}
          cols={["内网地址", "内网端口", "外网地址", "外网端口", "协议"]}
          render={(a) => [
            <span key="i" className="font-mono text-xs">
              {a.nat_internal_ip || "—"}
            </span>,
            <span key="ip" className="font-mono text-xs tabular-nums">
              {a.nat_internal_port ?? "—"}
            </span>,
            <span key="e" className="font-mono text-xs">
              {a.nat_external_ip || "—"}
            </span>,
            <span key="ep" className="font-mono text-xs tabular-nums">
              {a.nat_external_port ?? "—"}
            </span>,
            <span key="pr" className="text-xs uppercase">
              {a.nat_protocol || "tcp"}
            </span>,
          ]}
        />

        <AssetSection
          title="主机资产"
          rows={data.hosts}
          cols={["主机名", "IP", "操作系统", "角色"]}
          render={(a) => [
            <span key="h" className="font-mono text-xs font-medium">
              {a.hostname || "—"}
            </span>,
            <span key="i" className="font-mono text-xs">
              {a.ip || "—"}
            </span>,
            <span key="o" className="text-xs">
              {a.os || "—"}
            </span>,
            <span key="r" className="text-xs">
              {a.host_role || "—"}
            </span>,
          ]}
        />

        <AssetSection
          title="关联应用"
          rows={data.related_apps}
          cols={["ITAM 编号", "应用名称", "归属部门"]}
          render={(a) => [
            <Link
              key="i"
              prefetch={false}
              href={`/function/assets/app-detail?id=${a.id}`}
              className="font-mono text-xs text-blue-600 hover:underline dark:text-blue-400"
            >
              {a.itam_id || "未编号"}
            </Link>,
            <Link
              key="n"
              prefetch={false}
              href={`/function/assets/app-detail?id=${a.id}`}
              className="text-xs hover:underline"
            >
              {a.app_name || "—"}
            </Link>,
            <span key="d" className="text-xs">
              {a.dept || "—"}
            </span>,
          ]}
        />
      </div>
    </div>
  );
}

function Meta({ label, value, mono, span2 }: { label: string; value?: string; mono?: boolean; span2?: boolean }) {
  return (
    <div className={span2 ? "col-span-2" : undefined}>
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className={mono ? "font-mono text-sm" : "text-sm"}>{value || "—"}</div>
    </div>
  );
}

function AssetSection({
  title,
  rows,
  cols,
  render,
}: {
  title: string;
  rows: Asset[];
  cols: string[];
  render: (a: Asset) => React.ReactNode[];
}) {
  return (
    <Card className="overflow-hidden py-0">
      <div className="flex items-center gap-2 border-b px-3 py-2">
        <span className="text-sm font-medium">{title}</span>
        <Badge variant="secondary" className="tabular-nums">
          {rows.length}
        </Badge>
      </div>
      <div className="overflow-auto">
        <Table>
          <TableHeader>
            <TableRow>
              {cols.map((c) => (
                <TableHead key={c}>{c}</TableHead>
              ))}
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((a) => (
              <TableRow key={a.id}>
                {render(a).map((cell, i) => (
                  <TableCell key={i}>{cell}</TableCell>
                ))}
              </TableRow>
            ))}
            {rows.length === 0 && (
              <TableRow>
                <TableCell colSpan={cols.length} className="py-6 text-center text-xs text-muted-foreground">
                  暂无
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>
    </Card>
  );
}

// useSearchParams 在静态导出下必须包在 Suspense 里。
export default function AppDetailPage() {
  return (
    <React.Suspense fallback={<div className="p-6 text-sm text-muted-foreground">加载中…</div>}>
      <AppDetailInner />
    </React.Suspense>
  );
}
