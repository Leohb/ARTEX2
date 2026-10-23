"use client";

import * as React from "react";

import Link from "next/link";

import { ChevronLeftIcon, ChevronRightIcon, ExternalLinkIcon, PencilIcon, PlusIcon, RefreshCwIcon } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api";
import type { AppListItem } from "@/lib/types";
import { cn } from "@/lib/utils";

import { PAGE_SIZES } from "./asset-table";

// 应用清单：以 ITAM 编号为主键的应用主数据。
//
// 与其它资产页不同，这里的字段全部是人维护的业务属性（部门、负责人），
// 不是扫描结果。ITAM 编号、应用名、英文短名都可点进详情页。
export function AppsPanel() {
  const [rows, setRows] = React.useState<AppListItem[]>([]);
  const [total, setTotal] = React.useState(0);
  const [page, setPage] = React.useState(0);
  const [size, setSize] = React.useState(50);
  const [loading, setLoading] = React.useState(false);
  const [loaded, setLoaded] = React.useState(false);
  const [editing, setEditing] = React.useState<AppListItem | null>(null);
  const [creating, setCreating] = React.useState(false);

  const load = React.useCallback(() => {
    setLoading(true);
    api
      .apps(size, page * size)
      .then((r) => {
        setRows(r.apps);
        setTotal(r.total);
      })
      .catch((e) => toast.error("加载应用清单失败：" + String((e as Error)?.message ?? e)))
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
          共 <span className="tabular-nums">{total}</span> 个应用
        </span>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={load} disabled={loading}>
            <RefreshCwIcon className={cn("size-4", loading && "animate-spin")} /> 刷新
          </Button>
          <Button size="sm" onClick={() => setCreating(true)}>
            <PlusIcon className="size-3.5" /> 新增应用
          </Button>
        </div>
      </div>

      <Card className="flex min-h-0 flex-1 flex-col overflow-hidden py-0">
        <div className="min-h-0 flex-1 overflow-auto">
          <Table>
            <TableHeader className="sticky top-0 z-10 bg-card">
              <TableRow>
                <TableHead className="w-32">ITAM 编号</TableHead>
                <TableHead>应用名称</TableHead>
                <TableHead className="w-32">英文短名</TableHead>
                <TableHead className="w-48">英文全称</TableHead>
                <TableHead className="w-32">归属部门</TableHead>
                <TableHead className="w-24">应用负责人</TableHead>
                <TableHead className="w-24">运维负责人</TableHead>
                <TableHead className="w-20 text-right">关联应用</TableHead>
                <TableHead className="w-16 text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((a) => {
                const detailHref = `/function/assets/app-detail?id=${a.id}`;
                return (
                  <TableRow key={a.id}>
                    <TableCell>
                      {a.itam_id ? (
                        <Link
                          prefetch={false}
                          href={detailHref}
                          className="font-mono text-xs font-medium text-blue-600 hover:underline dark:text-blue-400"
                        >
                          {a.itam_id}
                        </Link>
                      ) : (
                        <span className="text-xs text-muted-foreground">未编号</span>
                      )}
                    </TableCell>
                    <TableCell>
                      <Link prefetch={false} href={detailHref} className="text-sm font-medium hover:underline">
                        {a.app_name || "—"}
                      </Link>
                      {a.category && <div className="text-[11px] text-muted-foreground">{a.category}</div>}
                    </TableCell>
                    <TableCell>
                      {a.app_short_name ? (
                        <Link
                          prefetch={false}
                          href={detailHref}
                          className="font-mono text-xs text-blue-600 hover:underline dark:text-blue-400"
                        >
                          {a.app_short_name}
                        </Link>
                      ) : (
                        <span className="text-xs text-muted-foreground">—</span>
                      )}
                    </TableCell>
                    <TableCell className="max-w-[18rem] truncate text-xs" title={a.app_full_name}>
                      {a.app_full_name || "—"}
                    </TableCell>
                    <TableCell className="text-xs">{a.dept || "—"}</TableCell>
                    <TableCell className="text-xs">{a.app_owner || "—"}</TableCell>
                    <TableCell className="text-xs">{a.ops_owner || "—"}</TableCell>
                    <TableCell className="text-right">
                      <Badge variant={a.related_apps > 0 ? "secondary" : "outline"} className="tabular-nums">
                        {a.related_apps}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="ghost"
                        size="icon"
                        className="size-7 text-muted-foreground"
                        onClick={() => setEditing(a)}
                        aria-label={`编辑应用 ${a.app_name || a.id}`}
                      >
                        <PencilIcon className="size-3.5" />
                      </Button>
                    </TableCell>
                  </TableRow>
                );
              })}
              {rows.length === 0 && (
                <TableRow>
                  <TableCell colSpan={9} className="py-12 text-center text-sm text-muted-foreground">
                    {loaded ? "还没有应用。点击右上角「新增应用」，以 ITAM 编号为主键登记。" : "加载中…"}
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

      <AppEditDialog
        open={creating}
        onOpenChange={setCreating}
        onSaved={() => {
          setCreating(false);
          load();
        }}
      />
      <AppEditDialog
        open={!!editing}
        onOpenChange={(o) => !o && setEditing(null)}
        app={editing ?? undefined}
        onSaved={() => {
          setEditing(null);
          load();
        }}
      />
    </div>
  );
}

function AppEditDialog({
  open,
  onOpenChange,
  app,
  onSaved,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  app?: AppListItem;
  onSaved: () => void;
}) {
  const isEdit = !!app;
  const [form, setForm] = React.useState<Record<string, string>>({});
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => {
    if (!open) return;
    setForm({
      itam_id: app?.itam_id ?? "",
      app_name: app?.app_name ?? "",
      app_short_name: app?.app_short_name ?? "",
      app_full_name: app?.app_full_name ?? "",
      dept: app?.dept ?? "",
      app_owner: app?.app_owner ?? "",
      ops_owner: app?.ops_owner ?? "",
      category: app?.category ?? "",
      app_description: app?.app_description ?? "",
    });
  }, [open, app]);

  const set = (k: string, v: string) => setForm((f) => ({ ...f, [k]: v }));

  const submit = async () => {
    if (!form.itam_id?.trim() && !form.app_name?.trim()) {
      toast.error("ITAM 编号与应用名称至少填一个");
      return;
    }
    setBusy(true);
    try {
      await api.upsertApp(form);
      toast.success(isEdit ? "已保存" : "已新增");
      onSaved();
    } catch (e) {
      toast.error("保存失败：" + String((e as Error)?.message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const FIELDS: { key: string; label: string; placeholder?: string; required?: boolean }[] = [
    { key: "itam_id", label: "ITAM 编号", placeholder: "如 ITAM-2024-001", required: true },
    { key: "app_name", label: "应用名称（中文）", required: true },
    { key: "app_short_name", label: "英文短名", placeholder: "如 crm-portal" },
    { key: "app_full_name", label: "英文全称", placeholder: "Customer Relationship Management Portal" },
    { key: "dept", label: "归属部门", placeholder: "如 信息科技部" },
    { key: "app_owner", label: "应用负责人" },
    { key: "ops_owner", label: "运维负责人" },
    { key: "category", label: "分类", placeholder: "如 业务系统 / 支撑系统" },
    { key: "app_description", label: "说明" },
  ];

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{isEdit ? "编辑应用" : "新增应用"}</DialogTitle>
          <DialogDescription>ITAM 编号是 CMDB 业务主键，全局唯一；已存在则更新该应用。</DialogDescription>
        </DialogHeader>
        <FieldGroup className="max-h-[60vh] overflow-auto py-2">
          {FIELDS.map((f) => (
            <Field key={f.key}>
              <FieldLabel htmlFor={`app-${f.key}`}>
                {f.label}
                {f.required ? " *" : ""}
              </FieldLabel>
              <Input
                id={`app-${f.key}`}
                value={form[f.key] ?? ""}
                placeholder={f.placeholder}
                onChange={(e) => set(f.key, e.target.value)}
              />
            </Field>
          ))}
        </FieldGroup>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            取消
          </Button>
          <Button onClick={() => void submit()} disabled={busy}>
            {busy ? "保存中…" : "保存"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// AppDetailLink 供详情页复用到资产页的跳转。
export function AppDetailLink({ id, children }: { id: number; children: React.ReactNode }) {
  return (
    <Link prefetch={false} href={`/function/assets/app-detail?id=${id}`} className="hover:underline">
      {children} <ExternalLinkIcon className="inline size-3" />
    </Link>
  );
}
