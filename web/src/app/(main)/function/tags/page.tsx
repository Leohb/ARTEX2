"use client";

import * as React from "react";

import { PencilIcon, PlusIcon, RefreshCwIcon, Trash2Icon } from "lucide-react";
import { toast } from "sonner";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
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
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api";
import type { Tag } from "@/lib/types";
import { cn } from "@/lib/utils";

// 自定义标签管理页。标签定义（名字/颜色/说明）在这里维护；资产上挂的是标签
// 名字，所以改名会同步重写所有资产上的旧名（后端在一个事务里做）。
export default function TagsPage() {
  const [tags, setTags] = React.useState<Tag[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [loaded, setLoaded] = React.useState(false);
  const [editing, setEditing] = React.useState<Tag | null>(null);
  const [creating, setCreating] = React.useState(false);
  const [deleteTarget, setDeleteTarget] = React.useState<Tag | null>(null);
  const [busy, setBusy] = React.useState(false);

  const load = React.useCallback(() => {
    setLoading(true);
    api
      .tags()
      .then(setTags)
      .catch((e) => toast.error(`加载标签失败：${String((e as Error)?.message ?? e)}`))
      .finally(() => {
        setLoading(false);
        setLoaded(true);
      });
  }, []);

  React.useEffect(() => {
    load();
  }, [load]);

  const confirmDelete = async () => {
    if (!deleteTarget) return;
    setBusy(true);
    try {
      await api.deleteTag(deleteTarget.id);
      toast.success(`已删除标签「${deleteTarget.name}」`);
      setDeleteTarget(null);
      load();
    } catch (e) {
      toast.error(`删除失败：${String((e as Error)?.message ?? e)}`);
    } finally {
      setBusy(false);
    }
  };

  const totalUsed = tags.reduce((n, t) => n + t.asset_count, 0);

  return (
    <div className="flex h-[calc(100vh-6rem)] min-h-0 flex-col gap-4">
      <div className="flex items-center justify-between gap-4">
        <div className="flex items-center gap-2">
          <h1 className="font-semibold text-xl tracking-tight">自定义标签</h1>
          <Badge variant="secondary" className="tabular-nums">
            {tags.length}
          </Badge>
        </div>
        <div className="flex items-center gap-3">
          <span className="text-muted-foreground text-sm">
            共 <span className="tabular-nums">{totalUsed}</span> 处标注
          </span>
          <Button variant="outline" size="sm" onClick={load} disabled={loading}>
            <RefreshCwIcon className={cn("size-4", loading && "animate-spin")} /> 刷新
          </Button>
          <Button size="sm" onClick={() => setCreating(true)}>
            <PlusIcon className="size-3.5" /> 新增标签
          </Button>
        </div>
      </div>

      <Card className="flex min-h-0 flex-1 flex-col overflow-hidden py-0">
        <div className="min-h-0 flex-1 overflow-auto">
          <Table>
            <TableHeader className="sticky top-0 z-10 bg-card">
              <TableRow>
                <TableHead>标签</TableHead>
                <TableHead>说明</TableHead>
                <TableHead className="w-24 text-right">引用数</TableHead>
                <TableHead className="w-36 text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {tags.map((t) => (
                <TableRow key={t.id}>
                  <TableCell>
                    <span className="inline-flex items-center gap-2">
                      <span
                        className="size-2.5 rounded-full"
                        style={{ backgroundColor: t.color || "var(--muted-foreground)" }}
                      />
                      <span className="font-medium">{t.name}</span>
                    </span>
                  </TableCell>
                  <TableCell className="max-w-md truncate text-muted-foreground text-xs">
                    {t.description || "—"}
                  </TableCell>
                  <TableCell className="text-right text-sm tabular-nums">{t.asset_count}</TableCell>
                  <TableCell className="text-right">
                    <div className="flex items-center justify-end gap-1.5">
                      <Button variant="outline" size="sm" className="h-7" onClick={() => setEditing(t)}>
                        <PencilIcon className="size-3" /> 编辑
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="size-7 text-muted-foreground hover:text-destructive"
                        onClick={() => setDeleteTarget(t)}
                        aria-label={`删除标签 ${t.name}`}
                      >
                        <Trash2Icon className="size-3.5" />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
              {tags.length === 0 && (
                <TableRow>
                  <TableCell colSpan={4} className="py-12 text-center text-muted-foreground text-sm">
                    {loaded ? "还没有标签。点击右上角「新增标签」创建，或在资产编辑弹窗里直接输入标签名。" : "加载中…"}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
      </Card>

      <TagDialog
        open={creating}
        onOpenChange={setCreating}
        onSaved={() => {
          setCreating(false);
          load();
        }}
      />

      <TagDialog
        open={!!editing}
        onOpenChange={(o) => {
          if (!o) setEditing(null);
        }}
        tag={editing ?? undefined}
        onSaved={() => {
          setEditing(null);
          load();
        }}
      />

      <AlertDialog open={!!deleteTarget} onOpenChange={(o) => !o && setDeleteTarget(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>删除标签 · {deleteTarget?.name}</AlertDialogTitle>
            <AlertDialogDescription>
              将删除这个标签定义。已经打在资产上的同名标注**不会**被清除——那是历史记录，
              需要的话请在对应资产里编辑移除。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={busy}>取消</AlertDialogCancel>
            <AlertDialogAction
              onClick={(e) => {
                e.preventDefault();
                void confirmDelete();
              }}
              disabled={busy}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              {busy ? "删除中…" : "确认删除"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

const PRESET_COLORS = [
  "#ef4444",
  "#f97316",
  "#eab308",
  "#22c55e",
  "#06b6d4",
  "#3b82f6",
  "#8b5cf6",
  "#ec4899",
  "#6b7280",
];

function TagDialog({
  open,
  onOpenChange,
  tag,
  onSaved,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  tag?: Tag;
  onSaved: () => void;
}) {
  const isEdit = !!tag;
  const [name, setName] = React.useState("");
  const [color, setColor] = React.useState("");
  const [description, setDescription] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => {
    if (!open) return;
    setName(tag?.name ?? "");
    setColor(tag?.color ?? "");
    setDescription(tag?.description ?? "");
  }, [open, tag]);

  const submit = async () => {
    if (!name.trim()) {
      toast.error("标签名不能为空");
      return;
    }
    setBusy(true);
    try {
      if (isEdit) {
        await api.updateTag(tag.id, { name: name.trim(), color, description });
        toast.success("标签已更新");
      } else {
        await api.createTag({ name: name.trim(), color, description });
        toast.success("标签已创建");
      }
      onSaved();
    } catch (e) {
      const msg = String((e as Error)?.message ?? e);
      if (/:\s*409$/.test(msg)) toast.error("标签名已存在");
      else toast.error(`保存失败：${msg}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{isEdit ? "编辑标签" : "新增标签"}</DialogTitle>
          <DialogDescription>
            {isEdit
              ? "改名会同步更新所有已使用该标签的资产。"
              : "标签用于给资产做自定义分类，可在各资产页的编辑弹窗里挂上。"}
          </DialogDescription>
        </DialogHeader>
        <FieldGroup className="py-2">
          <Field>
            <FieldLabel htmlFor="tag-name">名称</FieldLabel>
            <Input
              id="tag-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="如 核心系统 / 待复测 / 已修复"
            />
          </Field>
          <Field>
            <FieldLabel>颜色</FieldLabel>
            <div className="flex flex-wrap items-center gap-1.5">
              {PRESET_COLORS.map((c) => (
                <button
                  key={c}
                  type="button"
                  onClick={() => setColor(c)}
                  className={cn(
                    "size-6 rounded-full ring-offset-2 ring-offset-background transition",
                    color === c ? "ring-2 ring-foreground" : "ring-0 hover:scale-110",
                  )}
                  style={{ backgroundColor: c }}
                  aria-label={`选择颜色 ${c}`}
                />
              ))}
              <Button variant="ghost" size="sm" className="h-6 px-2 text-xs" onClick={() => setColor("")}>
                清除
              </Button>
            </div>
          </Field>
          <Field>
            <FieldLabel htmlFor="tag-desc">说明（可选）</FieldLabel>
            <Input
              id="tag-desc"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="这个标签代表什么"
            />
          </Field>
        </FieldGroup>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            取消
          </Button>
          <Button onClick={() => void submit()} disabled={busy || !name.trim()}>
            {busy ? "保存中…" : "保存"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
