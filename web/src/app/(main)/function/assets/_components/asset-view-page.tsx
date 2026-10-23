"use client";

import * as React from "react";

import { KeyRoundIcon, PencilIcon, PlusIcon, RefreshCwIcon, Trash2Icon } from "lucide-react";
import { toast } from "sonner";

import { AssetDslSearch } from "@/components/asset-dsl-search";
import { ScopeTextEditor } from "@/components/scope-text-editor";
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
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api";
import { parseCompanyScopeText } from "@/lib/company-scope";
import type { Asset, AssetRelations, Company, CompanyScopeRule } from "@/lib/types";
import { cn } from "@/lib/utils";

import { AssetCard, Chips, PlaceholderCard, statusTone } from "./asset-table";
import { type AssetView, assetViewIcon } from "./asset-views";
import { WebAssetsPanel } from "./web-assets-panel";

export default function AssetsPage({ view }: { view: AssetView }) {
  const isCompany = view.key === "company";
  const isPlaceholder = view.dbType === null && !isCompany;
  // 用哪种表格渲染（DMZIP 复用 ip 表格）。默认与 view.key 相同。
  const renderKey = view.renderAs ?? view.key;
  const ViewIcon = assetViewIcon(view.key);
  // 视图由 URL 决定，不再有页内 Tab。tab 保留为 view.key，让下面已有的取数/
  // 分页/企业分支尽量少改。
  const tab = view.key;
  const [rows, setRows] = React.useState<Asset[]>([]);
  const [total, setTotal] = React.useState(0);
  const [companies, setCompanies] = React.useState<Company[]>([]);
  const [counts, setCounts] = React.useState<Record<string, number>>({});
  const [query, setQuery] = React.useState("");
  const [loaded, setLoaded] = React.useState(false);
  const [loading, setLoading] = React.useState(false);
  const [page, setPage] = React.useState(0);
  const [size, setSize] = React.useState(50);
  const [dslError, setDslError] = React.useState("");
  const [refreshKey, setRefreshKey] = React.useState(0);
  const assetsRequestRef = React.useRef(0);
  const [rowsTab, setRowsTab] = React.useState("");

  // asset selection & delete
  const [selected, setSelected] = React.useState<Set<number>>(new Set());
  const [deleteIds, setDeleteIds] = React.useState<number[]>([]);
  const [deleteOpen, setDeleteOpen] = React.useState(false);
  const [deleting, setDeleting] = React.useState(false);

  // company delete
  const [companyDeleteTarget, setCompanyDeleteTarget] = React.useState<Company | null>(null);
  const [companyDeleteAssets, setCompanyDeleteAssets] = React.useState(false);
  const [companyDeleting, setCompanyDeleting] = React.useState(false);

  // Companies + per-type counts (tab badges) — loaded on demand, no background polling.
  const loadMeta = React.useCallback(() => {
    api
      .companies()
      .then(setCompanies)
      .catch(() => {
        /* Keep the last successful company snapshot on a transient failure. */
      });
    api
      .assetCounts()
      .then(setCounts)
      .catch(() => {
        /* Keep the last successful counters on a transient failure. */
      });
  }, []);

  // Manual refresh: reload counts/companies and re-fetch the current page.
  const refresh = React.useCallback(() => {
    loadMeta();
    setRefreshKey((k) => k + 1);
    setSelected(new Set());
  }, [loadMeta]);

  const toggleSelect = (id: number) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const toggleSelectAll = (ids: number[]) => {
    setSelected((prev) => {
      const allSelected = ids.every((id) => prev.has(id));
      const next = new Set(prev);
      if (allSelected) {
        ids.forEach((id) => {
          next.delete(id);
        });
      } else {
        ids.forEach((id) => {
          next.add(id);
        });
      }
      return next;
    });
  };

  const openDelete = (ids: number[]) => {
    setDeleteIds(ids);
    setDeleteOpen(true);
  };

  const confirmDelete = async () => {
    setDeleting(true);
    try {
      const res = await api.deleteAssets(deleteIds);
      toast.success(`已删除 ${res.deleted} 条资产`);
      setSelected(new Set());
      refresh();
    } catch (e) {
      toast.error(`删除失败：${String((e as Error)?.message ?? e)}`);
    } finally {
      setDeleting(false);
      setDeleteOpen(false);
    }
  };

  const confirmDeleteCompany = async () => {
    if (!companyDeleteTarget) return;
    setCompanyDeleting(true);
    try {
      const res = await api.deleteCompany(companyDeleteTarget.id, companyDeleteAssets);
      const msg =
        companyDeleteAssets && res.assets_deleted > 0
          ? `已删除企业，同时删除 ${res.assets_deleted} 条资产`
          : "已删除企业";
      toast.success(msg);
      refresh();
    } catch (e) {
      toast.error(`删除失败：${String((e as Error)?.message ?? e)}`);
    } finally {
      setCompanyDeleting(false);
      setCompanyDeleteTarget(null);
      setCompanyDeleteAssets(false);
    }
  };

  React.useEffect(() => {
    loadMeta();
  }, [loadMeta]);

  // Reset query + page + selection when switching tabs
  // biome-ignore lint/correctness/useExhaustiveDependencies: tab changes intentionally reset tab-local controls.
  React.useEffect(() => {
    setQuery("");
    setDslError("");
    setSelected(new Set());
    setRows([]);
    setRowsTab("");
    setTotal(0);
    setLoaded(false);
    setPage(0);
  }, [view.key]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: these controls intentionally reset server pagination.
  React.useEffect(() => setPage(0), [size, query]);

  const dslMode = query.trim() !== "";

  // Server-side paginated page loader for the active data tab (company tab excluded).
  // biome-ignore lint/correctness/useExhaustiveDependencies: refreshKey is an explicit manual-reload trigger.
  React.useEffect(() => {
    if (isCompany || isPlaceholder) return;
    const request = ++assetsRequestRef.current;
    const dsl = combineDsl(view.dsl, query.trim());
    setLoading(true);
    const offset = page * size;
    const run = async () => {
      try {
        const r = dsl
          ? await api.searchAssets(dsl, view.dbType ?? "", size, offset)
          : await api.assets(view.dbType ?? "", size, offset);
        if (assetsRequestRef.current !== request) return;
        setRows(r.assets);
        setRowsTab(renderKey);
        setTotal(r.total);
        setDslError("");
      } catch (e) {
        if (assetsRequestRef.current !== request) return;
        setDslError(String((e as Error)?.message ?? e));
        setRows([]);
        setRowsTab(renderKey);
        setTotal(0);
      } finally {
        if (assetsRequestRef.current === request) {
          setLoading(false);
          setLoaded(true);
        }
      }
    };
    const tid = setTimeout(run, query.trim() ? 400 : 0);
    return () => {
      clearTimeout(tid);
      if (assetsRequestRef.current === request) assetsRequestRef.current++;
    };
  }, [view.key, view.dbType, view.dsl, renderKey, page, size, query, refreshKey, isCompany, isPlaceholder]);

  React.useEffect(() => {
    if (isCompany || isPlaceholder) return;
    const lastPage = Math.max(0, Math.ceil(total / size) - 1);
    if (page > lastPage) setPage(lastPage);
  }, [page, size, total, isCompany, isPlaceholder]);

  const companyById = React.useMemo(() => {
    const m = new Map<number, string>();
    for (const c of companies) m.set(c.id, c.name);
    return m;
  }, [companies]);

  const companyName = (id?: number) => (id ? (companyById.get(id) ?? "") : "");

  const tabCounts: Record<string, number> = { ...counts, company: companies.length };
  const totalAssets = Object.values(counts).reduce((a, b) => a + b, 0);

  // 当前视图的条目数。
  //
  // 带固定过滤条件（view.dsl）的视图必须用它自己的 total，不能用
  // tabCounts[dbType]：公网IP 和 DMZIP 的 dbType 都是 ip，后者若拿 ip 总数
  // 就会显示「标题 2、表格 1 行」，看起来像统计错了。
  const headerCount = isCompany
    ? companies.length
    : view.dsl
      ? total
      : view.dbType
        ? (tabCounts[view.dbType] ?? total)
        : 0;

  // 页头那句「共 N 项资产」：无过滤时是全局总数（帮助判断整体规模），
  // 有过滤时改成当前视图的命中数，避免与标题徽标互相矛盾。
  const headerTotalLabel = view.dsl ? headerCount : totalAssets;

  // rows are already the current server-side page.
  const tabData = (type: string): Asset[] => (rowsTab === type ? rows : []);
  const currentRows = tabData(renderKey);
  const slice = <T,>(list: T[]) => list;

  const searchBox = (
    <AssetDslSearch
      query={query}
      onChange={setQuery}
      loading={loading}
      error={dslError}
      count={dslMode ? total : undefined}
    />
  );

  return (
    <div className="flex h-[calc(100vh-6rem)] min-h-0 flex-col gap-4">
      <div className="flex items-center justify-between gap-4">
        <div className="flex items-center gap-2">
          <ViewIcon className="size-5 text-muted-foreground" />
          <h1 className="font-semibold text-xl tracking-tight">{view.title}</h1>
          <Badge variant="secondary" className="tabular-nums">
            {headerCount}
          </Badge>
        </div>
        <div className="flex items-center gap-3">
          {!isPlaceholder && (
            <span className="text-muted-foreground text-sm">
              共 <span className="tabular-nums">{headerTotalLabel}</span> 项{view.dsl ? view.title : "资产"}
            </span>
          )}
          {selected.size > 0 && (
            <Button variant="destructive" size="sm" onClick={() => openDelete(Array.from(selected) as number[])}>
              <Trash2Icon className="size-3.5" /> 删除已选 ({selected.size})
            </Button>
          )}
          {!isPlaceholder && (
            <Button variant="outline" size="sm" onClick={refresh} disabled={loading}>
              <RefreshCwIcon className={cn("size-4", loading && "animate-spin")} /> 刷新
            </Button>
          )}
          {!isPlaceholder && !isCompany && <CreateAssetButton view={view} onSaved={refresh} />}
          {isCompany && <CompanyDialog onSaved={refresh} />}
        </div>
      </div>

      {isPlaceholder ? (
        <PlaceholderCard title={view.title} reason={view.placeholderReason ?? ""} />
      ) : (
        <div className="flex min-h-0 flex-1 flex-col gap-4">
          {/* 企业 */}
          {isCompany && (
            <div className="mt-0 flex min-h-0 flex-1 flex-col">
              <Card className="flex min-h-0 flex-1 flex-col overflow-hidden py-0">
                <div className="min-h-0 flex-1 overflow-auto">
                  <Table>
                    <TableHeader className="sticky top-0 z-10 bg-card">
                      <TableRow>
                        <TableHead>企业</TableHead>
                        <TableHead className="w-24 text-right">资产数</TableHead>
                        <TableHead>资产范围</TableHead>
                        <TableHead className="w-36 text-right">操作</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {companies.map((c) => (
                        <TableRow key={c.id}>
                          <TableCell>
                            <div className="flex items-center gap-2">
                              <CompanyAvatar name={c.name} logo={c.logo} />
                              <span className="font-medium">{c.name}</span>
                            </div>
                          </TableCell>
                          <TableCell className="text-right text-sm tabular-nums">{c.asset_count}</TableCell>
                          <TableCell>
                            {c.scope?.length ? (
                              <div className="flex flex-wrap gap-1">
                                {c.scope.map((s, i) => (
                                  <Badge key={i} variant="secondary" className="font-mono text-[11px]">
                                    {s.raw}
                                  </Badge>
                                ))}
                              </div>
                            ) : (
                              <span className="text-muted-foreground text-xs">未设置范围</span>
                            )}
                          </TableCell>
                          <TableCell className="text-right">
                            <div className="flex items-center justify-end gap-1.5">
                              <EditScopeDialog company={c} onSaved={refresh} />
                              <AppendScopeDialog company={c} onSaved={refresh} />
                              <Button
                                variant="ghost"
                                size="icon"
                                className="size-7 text-muted-foreground hover:text-destructive"
                                onClick={() => {
                                  setCompanyDeleteTarget(c);
                                  setCompanyDeleteAssets(false);
                                }}
                                aria-label={`删除企业 ${c.name}`}
                              >
                                <Trash2Icon className="size-3.5" />
                              </Button>
                            </div>
                          </TableCell>
                        </TableRow>
                      ))}
                      {companies.length === 0 && (
                        <TableRow>
                          <TableCell colSpan={4} className="py-10 text-center text-muted-foreground text-sm">
                            还没有企业。点击右上角「新增企业」并填写资产范围，系统会自动认领命中的资产。
                          </TableCell>
                        </TableRow>
                      )}
                    </TableBody>
                  </Table>
                </div>
              </Card>
            </div>
          )}

          {/* 主域名 */}
          {renderKey === "root_domain" && (
            <div className="mt-0 flex min-h-0 flex-1 flex-col gap-2">
              {searchBox}
              <AssetCard
                cols={["", "域名", "ICP 备案", "归属企业", "标签", ""]}
                loaded={loaded}
                total={total}
                page={page}
                size={size}
                onSize={setSize}
                onPage={setPage}
                rows={currentRows}
                selected={selected}
                onToggleAll={(ids) => toggleSelectAll(ids)}
              >
                {slice(tabData("root_domain")).map((a) => (
                  <TableRow key={a.id} className={selected.has(a.id) ? "bg-muted/40" : undefined}>
                    <TableCell className="w-8 pr-0">
                      <Checkbox checked={selected.has(a.id)} onCheckedChange={() => toggleSelect(a.id)} />
                    </TableCell>
                    <TableCell className="font-medium font-mono text-xs">{a.domain}</TableCell>
                    <TableCell className="text-xs">{a.icp || "—"}</TableCell>
                    <TableCell className="text-xs">
                      {companyName(a.company_id) || <span className="text-muted-foreground">未归属</span>}
                    </TableCell>
                    <TableCell>
                      <TagsCell tags={a.tags} />
                    </TableCell>
                    <TableCell className="w-8 pl-0">
                      <EditAssetButton a={a} onSaved={refresh} />
                      <Button
                        variant="ghost"
                        size="icon"
                        className="size-7 text-muted-foreground hover:text-destructive"
                        onClick={() => openDelete([a.id])}
                        aria-label={`删除资产 ${a.domain || a.id}`}
                      >
                        <Trash2Icon className="size-3.5" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </AssetCard>
            </div>
          )}

          {/* 公网IP（只含 ip_scope=public；DMZ 见下面的独立表格） */}
          {renderKey === "ip" && (
            <div className="mt-0 flex min-h-0 flex-1 flex-col gap-2">
              {searchBox}
              <AssetCard
                cols={["", "IP", "C段", "绑定域名", "开放端口", "标签", ""]}
                loaded={loaded}
                total={total}
                page={page}
                size={size}
                onSize={setSize}
                onPage={setPage}
                rows={currentRows}
                selected={selected}
                onToggleAll={(ids) => toggleSelectAll(ids)}
              >
                {slice(tabData("ip")).map((a) => (
                  <TableRow key={a.id} className={selected.has(a.id) ? "bg-muted/40" : undefined}>
                    <TableCell className="w-8 pr-0">
                      <Checkbox checked={selected.has(a.id)} onCheckedChange={() => toggleSelect(a.id)} />
                    </TableCell>
                    <TableCell className="font-medium font-mono text-xs">{a.ip}</TableCell>
                    <TableCell className="font-mono text-xs">{a.c_segment || "—"}</TableCell>
                    <TableCell>
                      <Chips items={a.bound_domains ?? []} mono />
                    </TableCell>
                    <TableCell>
                      <Chips
                        items={(a.open_ports ?? []).map((p) => (p.service ? `${p.port}/${p.service}` : String(p.port)))}
                        mono
                      />
                    </TableCell>
                    <TableCell>
                      <TagsCell tags={a.tags} />
                    </TableCell>
                    <TableCell className="w-8 pl-0">
                      <EditAssetButton a={a} onSaved={refresh} />
                      <Button
                        variant="ghost"
                        size="icon"
                        className="size-7 text-muted-foreground hover:text-destructive"
                        onClick={() => openDelete([a.id])}
                        aria-label={`删除资产 ${a.ip || a.id}`}
                      >
                        <Trash2Icon className="size-3.5" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </AssetCard>
            </div>
          )}

          {/* DMZIP：与「公网IP」是两张独立表格，不共用渲染分支 */}
          {renderKey === "dmzip" && (
            <div className="mt-0 flex min-h-0 flex-1 flex-col gap-2">
              {searchBox}
              <AssetCard
                cols={["", "DMZ 地址", "C段", "承载主机", "绑定域名", "开放端口", "标签", ""]}
                loaded={loaded}
                total={total}
                page={page}
                size={size}
                onSize={setSize}
                onPage={setPage}
                rows={currentRows}
                selected={selected}
                onToggleAll={(ids) => toggleSelectAll(ids)}
                emptyHint="暂无 DMZ 地址。可在「公网IP」或「主机资产」页把某条 IP 的区域标注为 DMZ。"
              >
                {slice(tabData("dmzip")).map((a) => (
                  <TableRow key={a.id} className={selected.has(a.id) ? "bg-muted/40" : undefined}>
                    <TableCell className="w-8 pr-0">
                      <Checkbox checked={selected.has(a.id)} onCheckedChange={() => toggleSelect(a.id)} />
                    </TableCell>
                    <TableCell className="font-mono text-xs font-medium">{a.ip}</TableCell>
                    <TableCell className="font-mono text-xs">{a.c_segment || "—"}</TableCell>
                    <TableCell className="font-mono text-xs">
                      <HostForIP ip={a.ip} />
                    </TableCell>
                    <TableCell>
                      <Chips items={a.bound_domains ?? []} mono />
                    </TableCell>
                    <TableCell>
                      <Chips
                        items={(a.open_ports ?? []).map((p) => (p.service ? `${p.port}/${p.service}` : String(p.port)))}
                        mono
                      />
                    </TableCell>
                    <TableCell>
                      <TagsCell tags={a.tags} />
                    </TableCell>
                    <TableCell className="w-8 pl-0">
                      <EditAssetButton a={a} onSaved={refresh} />
                      <Button
                        variant="ghost"
                        size="icon"
                        className="size-7 text-muted-foreground hover:text-destructive"
                        onClick={() => openDelete([a.id])}
                        aria-label={`删除资产 ${a.ip || a.id}`}
                      >
                        <Trash2Icon className="size-3.5" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </AssetCard>
            </div>
          )}

          {/* 子域名 */}
          {renderKey === "subdomain" && (
            <div className="mt-0 flex min-h-0 flex-1 flex-col gap-2">
              {searchBox}
              <AssetCard
                cols={["", "域名", "根域名", "解析类型", "解析值", "标签", ""]}
                loaded={loaded}
                total={total}
                page={page}
                size={size}
                onSize={setSize}
                onPage={setPage}
                rows={currentRows}
                selected={selected}
                onToggleAll={(ids) => toggleSelectAll(ids)}
              >
                {slice(tabData("subdomain")).map((a) => (
                  <TableRow key={a.id} className={selected.has(a.id) ? "bg-muted/40" : undefined}>
                    <TableCell className="w-8 pr-0">
                      <Checkbox checked={selected.has(a.id)} onCheckedChange={() => toggleSelect(a.id)} />
                    </TableCell>
                    <TableCell className="font-medium font-mono text-xs">{a.domain}</TableCell>
                    <TableCell className="font-mono text-xs">{a.root_domain || "—"}</TableCell>
                    <TableCell className="text-xs">{a.record_type || "—"}</TableCell>
                    <TableCell className="max-w-xs truncate font-mono text-xs">
                      {(Array.isArray(a.record_value) ? a.record_value.join(", ") : a.record_value) || "—"}
                    </TableCell>
                    <TableCell>
                      <TagsCell tags={a.tags} />
                    </TableCell>
                    <TableCell className="w-8 pl-0">
                      <EditAssetButton a={a} onSaved={refresh} />
                      <Button
                        variant="ghost"
                        size="icon"
                        className="size-7 text-muted-foreground hover:text-destructive"
                        onClick={() => openDelete([a.id])}
                        aria-label={`删除资产 ${a.domain || a.id}`}
                      >
                        <Trash2Icon className="size-3.5" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </AssetCard>
            </div>
          )}

          {/* 应用清单 */}
          {renderKey === "app" && (
            <div className="mt-0 flex min-h-0 flex-1 flex-col gap-2">
              {searchBox}
              <AssetCard
                cols={["", "应用名", "Bundle ID", "分类", "ICP 备案", "标签", ""]}
                loaded={loaded}
                total={total}
                page={page}
                size={size}
                onSize={setSize}
                onPage={setPage}
                rows={currentRows}
                selected={selected}
                onToggleAll={(ids) => toggleSelectAll(ids)}
              >
                {slice(tabData("app")).map((a) => (
                  <TableRow key={a.id} className={selected.has(a.id) ? "bg-muted/40" : undefined}>
                    <TableCell className="w-8 pr-0">
                      <Checkbox checked={selected.has(a.id)} onCheckedChange={() => toggleSelect(a.id)} />
                    </TableCell>
                    <TableCell className="font-medium text-xs">{a.app_name || "—"}</TableCell>
                    <TableCell className="font-mono text-xs">{a.bundle_id || "—"}</TableCell>
                    <TableCell className="text-xs">{a.category || "—"}</TableCell>
                    <TableCell className="text-xs">{a.app_icp || "—"}</TableCell>
                    <TableCell>
                      <TagsCell tags={a.tags} />
                    </TableCell>
                    <TableCell className="w-8 pl-0">
                      <EditAssetButton a={a} onSaved={refresh} />
                      <Button
                        variant="ghost"
                        size="icon"
                        className="size-7 text-muted-foreground hover:text-destructive"
                        onClick={() => openDelete([a.id])}
                        aria-label={`删除资产 ${a.app_name || a.id}`}
                      >
                        <Trash2Icon className="size-3.5" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </AssetCard>
            </div>
          )}

          {/* 公网服务 */}
          {renderKey === "service" && (
            <div className="mt-0 flex min-h-0 flex-1 flex-col gap-2">
              {searchBox}
              <AssetCard
                cols={["", "服务", "域名", "IP", "端口", "状态码", "标题", "指纹", "认证", "标签", ""]}
                loaded={loaded}
                total={total}
                page={page}
                size={size}
                onSize={setSize}
                onPage={setPage}
                rows={currentRows}
                selected={selected}
                onToggleAll={(ids) => toggleSelectAll(ids)}
              >
                {slice(tabData("service")).map((a) => (
                  <ServiceRow
                    key={a.id}
                    a={a}
                    selected={selected}
                    onToggle={toggleSelect}
                    onDelete={openDelete}
                    onSaved={refresh}
                  />
                ))}
              </AssetCard>
            </div>
          )}

          {/* NAT 映射 */}
          {renderKey === "nat" && (
            <div className="mt-0 flex min-h-0 flex-1 flex-col gap-2">
              {searchBox}
              <AssetCard
                cols={["", "内网地址", "内网端口", "外网地址", "外网端口", "协议", "标签", ""]}
                loaded={loaded}
                total={total}
                page={page}
                size={size}
                onSize={setSize}
                onPage={setPage}
                rows={currentRows}
                selected={selected}
                onToggleAll={(ids) => toggleSelectAll(ids)}
                emptyHint="暂无 NAT 映射。可用「新增」登记，或让 Agent 在内网探测时写入。"
              >
                {slice(tabData("nat")).map((a) => (
                  <TableRow key={a.id} className={selected.has(a.id) ? "bg-muted/40" : undefined}>
                    <TableCell className="w-8 pr-0">
                      <Checkbox checked={selected.has(a.id)} onCheckedChange={() => toggleSelect(a.id)} />
                    </TableCell>
                    <TableCell className="font-mono text-xs font-medium">{a.nat_internal_ip || "—"}</TableCell>
                    <TableCell className="font-mono text-xs tabular-nums">{a.nat_internal_port ?? "—"}</TableCell>
                    <TableCell className="font-mono text-xs font-medium">{a.nat_external_ip || "—"}</TableCell>
                    <TableCell className="font-mono text-xs tabular-nums">{a.nat_external_port ?? "—"}</TableCell>
                    <TableCell className="text-xs uppercase">{a.nat_protocol || "tcp"}</TableCell>
                    <TableCell>
                      <TagsCell tags={a.tags} />
                    </TableCell>
                    <TableCell className="w-8 pl-0">
                      <EditAssetButton a={a} onSaved={refresh} />
                      <Button
                        variant="ghost"
                        size="icon"
                        className="size-7 text-muted-foreground hover:text-destructive"
                        onClick={() => openDelete([a.id])}
                        aria-label={`删除 NAT 映射 ${a.id}`}
                      >
                        <Trash2Icon className="size-3.5" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </AssetCard>
            </div>
          )}

          {/* 主机资产 */}
          {renderKey === "host" && (
            <div className="mt-0 flex min-h-0 flex-1 flex-col gap-2">
              {searchBox}
              <AssetCard
                cols={["", "主机名", "IP", "操作系统", "角色", "区域", "标签", ""]}
                loaded={loaded}
                total={total}
                page={page}
                size={size}
                onSize={setSize}
                onPage={setPage}
                rows={currentRows}
                selected={selected}
                onToggleAll={(ids) => toggleSelectAll(ids)}
                emptyHint="暂无主机资产。内网/DMZ 主机可由「新增」登记，或让 Agent 在内网探测时写入。"
              >
                {slice(tabData("host")).map((a) => (
                  <TableRow key={a.id} className={selected.has(a.id) ? "bg-muted/40" : undefined}>
                    <TableCell className="w-8 pr-0">
                      <Checkbox checked={selected.has(a.id)} onCheckedChange={() => toggleSelect(a.id)} />
                    </TableCell>
                    <TableCell className="font-mono text-xs font-medium">{a.hostname || "—"}</TableCell>
                    <TableCell className="font-mono text-xs">{a.ip || "—"}</TableCell>
                    <TableCell className="text-xs">{a.os || "—"}</TableCell>
                    <TableCell className="text-xs">{a.host_role || "—"}</TableCell>
                    <TableCell>
                      <ScopeBadge scope={a.ip_scope} />
                    </TableCell>
                    <TableCell>
                      <TagsCell tags={a.tags} />
                    </TableCell>
                    <TableCell className="w-8 pl-0">
                      <EditAssetButton a={a} onSaved={refresh} />
                      <Button
                        variant="ghost"
                        size="icon"
                        className="size-7 text-muted-foreground hover:text-destructive"
                        onClick={() => openDelete([a.id])}
                        aria-label={`删除主机 ${a.hostname || a.id}`}
                      >
                        <Trash2Icon className="size-3.5" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </AssetCard>
            </div>
          )}

          {/* WEB 资产按域名聚合，单独成组件（分页必须作用在域名上） */}
          {renderKey === "web" && <WebAssetsPanel />}
        </div>
      )}

      <AlertDialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>确认删除</AlertDialogTitle>
            <AlertDialogDescription>
              将永久删除 <span className="font-semibold tabular-nums">{deleteIds.length}</span>{" "}
              条资产记录，此操作不可撤销。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleting}>取消</AlertDialogCancel>
            <AlertDialogAction
              onClick={(e) => {
                e.preventDefault();
                void confirmDelete();
              }}
              disabled={deleting}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              {deleting ? "删除中…" : "确认删除"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog
        open={!!companyDeleteTarget}
        onOpenChange={(o) => {
          if (!o) {
            setCompanyDeleteTarget(null);
            setCompanyDeleteAssets(false);
          }
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>删除企业 · {companyDeleteTarget?.name}</AlertDialogTitle>
            <AlertDialogDescription asChild>
              <div className="space-y-3">
                <p>此操作将永久删除该企业及其资产范围配置，不可撤销。</p>
                <label
                  htmlFor="delete-assets-opt"
                  className="flex cursor-pointer items-center gap-2.5 rounded-md border p-3 hover:bg-muted/50"
                >
                  <Checkbox
                    id="delete-assets-opt"
                    checked={companyDeleteAssets}
                    onCheckedChange={(v) => setCompanyDeleteAssets(!!v)}
                  />
                  <span className="text-sm leading-snug">
                    同时删除该企业下的所有资产
                    <span className="block text-muted-foreground text-xs">不勾选则保留资产，仅取消归属关系</span>
                  </span>
                </label>
              </div>
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={companyDeleting}>取消</AlertDialogCancel>
            <AlertDialogAction
              onClick={(e) => {
                e.preventDefault();
                void confirmDeleteCompany();
              }}
              disabled={companyDeleting}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              {companyDeleting ? "删除中…" : "确认删除"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function CompanyAvatar({ name, logo }: { name: string; logo?: string }) {
  const initial = (name.trim()[0] ?? "?").toUpperCase();
  return (
    <Avatar className="size-7 shrink-0">
      {logo ? <AvatarImage src={logo} alt={name} /> : null}
      <AvatarFallback className="text-xs">{initial}</AvatarFallback>
    </Avatar>
  );
}

// 后端返回的 warnings 说的是既有数据问题（不是本次提交的行有错），保存本身已经
// 成功。给更长的停留时间，因为它需要用户去处理具体的资产，扫一眼标题不够。
function showScopeWarnings(warnings?: string[]) {
  for (const warning of warnings ?? []) {
    toast.warning(warning, { duration: 15000 });
  }
}

function savedScopeRules(company: Company): CompanyScopeRule[] {
  return (company.scope ?? [])
    .map((scope) => ({ kind: scope.kind, value: scope.raw.trim() }))
    .filter((scope) => scope.value);
}

function savedScopeText(company: Company): string {
  return savedScopeRules(company)
    .map((scope) => scope.value)
    .join("\n");
}

// 新增企业使用与任务、LLM 编辑一致的右侧抽屉。
function CompanyDialog({ onSaved }: { onSaved: () => void }) {
  const [open, setOpen] = React.useState(false);
  const [name, setName] = React.useState("");
  const [scopeText, setScopeText] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const parsedScope = React.useMemo(() => parseCompanyScopeText(scopeText), [scopeText]);

  React.useEffect(() => {
    if (!open) return;
    setName("");
    setScopeText("");
  }, [open]);

  const submit = async () => {
    if (!name.trim()) {
      toast.error("请填写企业名称");
      return;
    }
    if (parsedScope.errors.length > 0) {
      toast.error("请修正无效的资产范围");
      return;
    }
    setBusy(true);
    try {
      const res = await api.createCompany(name.trim(), parsedScope.rules);
      const added = res.scope_added ?? 0;
      const invalid = res.scope_invalid ?? 0;
      if (invalid > 0) toast.warning(`已创建企业，添加 ${added} 条范围；${invalid} 行无效`);
      else toast.success(`已创建企业，添加 ${added} 条范围`);
      setOpen(false);
      onSaved();
    } catch (e) {
      const msg = String((e as Error)?.message ?? e);
      if (/:\s*409$/.test(msg)) toast.error("企业已存在，请换个名称");
      else toast.error(`保存失败：${msg}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <Button size="sm">新增企业</Button>
      </SheetTrigger>
      <SheetContent className="w-full! max-w-none! gap-0 p-0 sm:w-[520px]! sm:max-w-[520px]!">
        <SheetHeader className="border-b p-6">
          <SheetTitle>新增企业</SheetTitle>
          <SheetDescription>配置企业及其资产范围。关键词只作为 Agent 提示，不会自动归属资产。</SheetDescription>
        </SheetHeader>
        <div className="flex-1 overflow-y-auto p-6">
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="cn-name">企业名称</FieldLabel>
              <Input
                id="cn-name"
                placeholder="如 Acme Corp（名称唯一）"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </Field>
            <ScopeTextEditor id="cn-scope" value={scopeText} onValueChange={setScopeText} parsed={parsedScope} />
          </FieldGroup>
        </div>
        <SheetFooter className="flex-row justify-end gap-2 border-t p-4">
          <Button variant="outline" onClick={() => setOpen(false)} disabled={busy}>
            取消
          </Button>
          <Button onClick={submit} disabled={busy || !name.trim() || parsedScope.errors.length > 0}>
            {busy ? "保存中…" : "保存"}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  );
}

// 编辑（覆盖）资产范围弹窗
function EditScopeDialog({ company, onSaved }: { company: Company; onSaved: () => void }) {
  const [open, setOpen] = React.useState(false);
  const [scopeText, setScopeText] = React.useState("");
  const [reason, setReason] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const preservedScopeRules = React.useMemo(() => savedScopeRules(company), [company]);
  const parsedScope = React.useMemo(
    () => parseCompanyScopeText(scopeText, { preservedRules: preservedScopeRules }),
    [preservedScopeRules, scopeText],
  );

  React.useEffect(() => {
    if (!open) return;
    setScopeText(savedScopeText(company));
    setReason("");
  }, [open, company]);

  const submit = async () => {
    if (parsedScope.errors.length > 0) {
      toast.error("请修正无效的资产范围");
      return;
    }
    setBusy(true);
    try {
      const res = await api.updateCompanyScope(company.id, parsedScope.rules, reason);
      const errCount = res.invalid ?? 0;
      if (errCount > 0) toast.warning(`已保存；${errCount} 行无效`);
      else toast.success(`范围已更新，共 ${res.added} 条`);
      showScopeWarnings(res.warnings);
      setOpen(false);
      onSaved();
    } catch (e) {
      toast.error(`保存失败：${String((e as Error)?.message ?? e)}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="outline" size="sm" className="h-7">
          编辑
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>编辑资产范围 · {company.name}</DialogTitle>
          <DialogDescription>
            编辑后将替换全部现有范围。ICP 精确匹配资产，企业关键词仅作为 Agent 提示。
          </DialogDescription>
        </DialogHeader>
        <FieldGroup className="py-2">
          <ScopeTextEditor
            id={`edit-company-scope-${company.id}`}
            value={scopeText}
            onValueChange={setScopeText}
            parsed={parsedScope}
          />
          <Field>
            <FieldLabel htmlFor="es-reason">归属依据（可选）</FieldLabel>
            <Input
              id="es-reason"
              placeholder="如 证书 / whois / ASN 佐证"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
            />
          </Field>
        </FieldGroup>
        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)} disabled={busy}>
            取消
          </Button>
          <Button onClick={submit} disabled={busy || parsedScope.errors.length > 0}>
            {busy ? "保存中…" : "覆盖保存"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// 追加资产范围弹窗
function AppendScopeDialog({ company, onSaved }: { company: Company; onSaved: () => void }) {
  const [open, setOpen] = React.useState(false);
  const [scopeText, setScopeText] = React.useState("");
  const [reason, setReason] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const parsedScope = React.useMemo(() => parseCompanyScopeText(scopeText), [scopeText]);

  React.useEffect(() => {
    if (!open) return;
    setScopeText("");
    setReason("");
  }, [open]);

  const submit = async () => {
    if (parsedScope.rules.length === 0) {
      toast.error("请填写要追加的范围");
      return;
    }
    if (parsedScope.errors.length > 0) {
      toast.error("请修正无效的资产范围");
      return;
    }
    setBusy(true);
    try {
      const res = await api.addCompanyScope(company.id, parsedScope.rules, reason);
      const errCount = res.invalid ?? 0;
      if (errCount > 0) toast.warning(`已保存；${errCount} 行无效`);
      else toast.success(`已追加 ${res.added} 条范围`);
      showScopeWarnings(res.warnings);
      setOpen(false);
      onSaved();
    } catch (e) {
      toast.error(`保存失败：${String((e as Error)?.message ?? e)}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="outline" size="sm" className="h-7">
          追加
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>追加资产范围 · {company.name}</DialogTitle>
          <DialogDescription>新范围会追加到现有范围。ICP 精确匹配资产，企业关键词仅作为 Agent 提示。</DialogDescription>
        </DialogHeader>
        <FieldGroup className="py-2">
          <ScopeTextEditor
            id={`append-company-scope-${company.id}`}
            value={scopeText}
            onValueChange={setScopeText}
            parsed={parsedScope}
          />
          <Field>
            <FieldLabel htmlFor="as-reason">归属依据（可选）</FieldLabel>
            <Input
              id="as-reason"
              placeholder="如 证书 / whois / ASN 佐证"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
            />
          </Field>
        </FieldGroup>
        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)} disabled={busy}>
            取消
          </Button>
          <Button onClick={submit} disabled={busy || parsedScope.rules.length === 0 || parsedScope.errors.length > 0}>
            {busy ? "保存中…" : "追加"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
// combineDsl 把「视图固定条件」与「用户输入条件」用 and 组合。
// 两边都加括号：用户可能写 "a or b"，不加括号会把固定条件一起 OR 掉，
// 于是 WEB 资产页混进非 http 的服务。
function combineDsl(fixed?: string, user?: string): string {
  const f = (fixed ?? "").trim();
  const u = (user ?? "").trim();
  if (!f) return u;
  if (!u) return f;
  return `(${f}) and (${u})`;
}

// ServiceRow 同时服务「公网服务」与「WEB资产」：后者只把域名列换成完整 URL，
// 其余列一致。抽出来避免两份几乎相同的 JSX 各自漂移。
function ServiceRow({
  a,
  showURL,
  selected,
  onToggle,
  onDelete,
  onSaved,
}: {
  a: Asset;
  showURL?: boolean;
  selected: Set<number>;
  onToggle: (id: number) => void;
  onDelete: (ids: number[]) => void;
  onSaved: () => void;
}) {
  const isHttp = a.service_type === "http";
  const svc = a.service_name || (isHttp ? "http" : "") || a.service_type || "";

  let linkCell: React.ReactNode = "—";
  if (isHttp && a.url) {
    linkCell = (
      <a href={a.url} target="_blank" rel="noreferrer" className="text-blue-600 hover:underline dark:text-blue-400">
        {showURL ? a.url : a.domain || a.url}
      </a>
    );
  } else if (a.domain) {
    linkCell = a.domain;
  }

  return (
    <TableRow className={selected.has(a.id) ? "bg-muted/40" : undefined}>
      <TableCell className="w-8 pr-0">
        <Checkbox checked={selected.has(a.id)} onCheckedChange={() => onToggle(a.id)} />
      </TableCell>
      <TableCell className="text-xs">
        <Badge variant={isHttp ? "default" : "secondary"} className="font-mono text-[10px]">
          {svc || "—"}
        </Badge>
      </TableCell>
      <TableCell className="max-w-[16rem] truncate font-mono text-xs" title={a.domain || a.url}>
        {linkCell}
      </TableCell>
      <TableCell className="font-mono text-xs">{a.ip || "—"}</TableCell>
      <TableCell className="font-mono text-xs tabular-nums">{a.port || "—"}</TableCell>
      <TableCell>
        {a.status_code != null ? (
          <span className={cn("font-mono font-semibold text-xs tabular-nums", statusTone(a.status_code))}>
            {a.status_code}
          </span>
        ) : (
          "—"
        )}
      </TableCell>
      <TableCell className="max-w-[12rem] truncate text-xs" title={a.page_title}>
        {a.page_title || "—"}
      </TableCell>
      <TableCell>
        <Chips items={a.technologies ?? []} />
      </TableCell>
      <TableCell>
        {(a.auth ?? []).length === 0 ? (
          <span className="text-muted-foreground text-xs">—</span>
        ) : (
          (a.auth ?? []).map((authItem, i) => {
            const item = authItem as Record<string, string>;
            const label = item.type || item.username || "认证";
            return (
              <span key={i} className="inline-flex items-center gap-1 text-[11px]">
                <KeyRoundIcon className="size-3 text-muted-foreground" />
                <span className="font-mono">{label}</span>
              </span>
            );
          })
        )}
      </TableCell>
      <TableCell>
        <TagsCell tags={a.tags} />
      </TableCell>
      <TableCell className="w-8 pl-0">
        <EditAssetButton a={a} onSaved={onSaved} />
        <Button
          variant="ghost"
          size="icon"
          className="size-7 text-muted-foreground hover:text-destructive"
          onClick={() => onDelete([a.id])}
          aria-label={`删除资产 ${a.url || a.id}`}
        >
          <Trash2Icon className="size-3.5" />
        </Button>
      </TableCell>
    </TableRow>
  );
}

// TagsCell 展示资产上的标签。标签是自由文本，没有颜色时用中性描边。
function TagsCell({ tags }: { tags?: string[] }) {
  const list = (tags ?? []).filter(Boolean);
  if (list.length === 0) return <span className="text-muted-foreground text-xs">—</span>;
  return (
    <div className="flex flex-wrap gap-1">
      {list.map((t) => (
        <Badge key={t} variant="outline" className="text-[10px]">
          {t}
        </Badge>
      ))}
    </div>
  );
}

// EditAssetButton 打开单条资产的编辑弹窗。字段按资产类型裁剪：改一条 IP 记录时
// 不该看到 URL 输入框。
function EditAssetButton({ a, onSaved }: { a: Asset; onSaved: () => void }) {
  const [open, setOpen] = React.useState(false);
  return (
    <>
      <Button
        variant="ghost"
        size="icon"
        className="size-7 text-muted-foreground hover:text-foreground"
        onClick={() => setOpen(true)}
        aria-label={`编辑资产 ${a.domain || a.ip || a.url || a.id}`}
      >
        <PencilIcon className="size-3.5" />
      </Button>
      <AssetEditDialog a={a} open={open} onOpenChange={setOpen} onSaved={onSaved} />
    </>
  );
}

// 各类型可编辑的文本字段。键同时是 db.AssetPatch 的 JSON 名。
const EDIT_FIELDS: Record<string, { key: string; label: string; placeholder?: string }[]> = {
  root_domain: [
    { key: "domain", label: "域名" },
    { key: "icp", label: "ICP 备案", placeholder: "如 京ICP备12345678号" },
  ],
  subdomain: [
    { key: "domain", label: "域名" },
    { key: "record_type", label: "解析类型", placeholder: "A / AAAA / CNAME / MX" },
  ],
  ip: [{ key: "ip", label: "IP 地址" }],
  app: [
    { key: "app_name", label: "应用名" },
    { key: "bundle_id", label: "Bundle ID" },
    { key: "category", label: "分类" },
    { key: "app_icp", label: "ICP 备案" },
  ],
  service: [
    { key: "url", label: "URL" },
    { key: "service_name", label: "服务名" },
    { key: "page_title", label: "页面标题" },
  ],
  nat: [
    { key: "nat_internal_ip", label: "内网地址" },
    { key: "nat_internal_port", label: "内网端口" },
    { key: "nat_external_ip", label: "外网地址" },
    { key: "nat_external_port", label: "外网端口" },
    { key: "nat_protocol", label: "协议" },
  ],
  host: [
    { key: "hostname", label: "主机名" },
    { key: "ip", label: "IP 地址" },
    { key: "os", label: "操作系统" },
    { key: "host_role", label: "角色" },
    { key: "ip_scope", label: "区域" },
  ],
};

function AssetEditDialog({
  a,
  open,
  onOpenChange,
  onSaved,
}: {
  a: Asset;
  open: boolean;
  onOpenChange: (o: boolean) => void;
  onSaved: () => void;
}) {
  const fields = EDIT_FIELDS[a.type] ?? EDIT_FIELDS.service;
  const [values, setValues] = React.useState<Record<string, string>>({});
  const [tagsText, setTagsText] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [knownTags, setKnownTags] = React.useState<string[]>([]);

  React.useEffect(() => {
    if (!open) return;
    const init: Record<string, string> = {};
    for (const f of fields) {
      const v = (a as unknown as Record<string, unknown>)[f.key];
      init[f.key] = v == null ? "" : String(v);
    }
    setValues(init);
    setTagsText((a.tags ?? []).join(", "));
    // 拉一次已有标签，给输入框做候选提示（失败不影响编辑）
    api
      .tags()
      .then((ts) => setKnownTags(ts.map((t) => t.name)))
      .catch(() => setKnownTags([]));
  }, [open, a, fields]);

  const submit = async () => {
    setBusy(true);
    try {
      const patch: Record<string, unknown> = {};
      for (const f of fields) {
        const cur = values[f.key] ?? "";
        const orig = (a as unknown as Record<string, unknown>)[f.key];
        const origStr = orig == null ? "" : String(orig);
        // 只提交真正改动过的字段：部分更新语义下，无谓的字段覆盖会误改并发编辑。
        if (cur !== origStr) patch[f.key] = cur;
      }
      const tags = tagsText
        .split(/[,，]/)
        .map((t) => t.trim())
        .filter(Boolean);
      const origTags = a.tags ?? [];
      if (tags.join("\u0000") !== origTags.join("\u0000")) patch.tags = tags;

      if (Object.keys(patch).length === 0) {
        toast.info("没有改动");
        onOpenChange(false);
        return;
      }
      await api.patchAsset(a.id, patch as Partial<Asset>);
      toast.success("已保存");
      onOpenChange(false);
      onSaved();
    } catch (e) {
      toast.error(`保存失败：${String((e as Error)?.message ?? e)}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>编辑资产</DialogTitle>
          <DialogDescription>只提交改动过的字段。标签用逗号分隔。</DialogDescription>
        </DialogHeader>
        <FieldGroup className="py-2">
          {fields.map((f) => (
            <Field key={f.key}>
              <FieldLabel htmlFor={`ae-${f.key}`}>{f.label}</FieldLabel>
              <Input
                id={`ae-${f.key}`}
                value={values[f.key] ?? ""}
                placeholder={f.placeholder}
                onChange={(e) => setValues((v) => ({ ...v, [f.key]: e.target.value }))}
              />
            </Field>
          ))}
          <Field>
            <FieldLabel htmlFor="ae-tags">标签</FieldLabel>
            <Input
              id="ae-tags"
              value={tagsText}
              placeholder="逗号分隔，如 核心系统, 待复测"
              onChange={(e) => setTagsText(e.target.value)}
            />
            {knownTags.length > 0 && (
              <div className="mt-1.5 flex flex-wrap gap-1">
                {knownTags.slice(0, 12).map((t) => (
                  <button
                    key={t}
                    type="button"
                    className="rounded border px-1.5 py-0.5 text-[10px] text-muted-foreground hover:bg-muted"
                    onClick={() => {
                      const cur = tagsText
                        .split(/[,，]/)
                        .map((x) => x.trim())
                        .filter(Boolean);
                      if (!cur.includes(t)) setTagsText([...cur, t].join(", "));
                    }}
                  >
                    + {t}
                  </button>
                ))}
              </div>
            )}
          </Field>
        </FieldGroup>
        <AssetRelationsPanel assetId={a.id} />
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

// AssetRelationsPanel 展示这条资产在图上的上下游，用于逐层追查：
// 从公网 IP 反查「谁解析到它 / 哪条 NAT 暴露它」，从服务下钻「它有哪些接口」。
function AssetRelationsPanel({ assetId }: { assetId: number }) {
  const [rel, setRel] = React.useState<AssetRelations | null>(null);
  const [err, setErr] = React.useState("");

  React.useEffect(() => {
    let live = true;
    api
      .assetRelations(assetId)
      .then((r) => {
        if (live) setRel(r);
      })
      .catch((e) => {
        if (live) setErr(String((e as Error)?.message ?? e));
      });
    return () => {
      live = false;
    };
  }, [assetId]);

  if (err) return <div className="text-xs text-destructive">关系加载失败：{err}</div>;
  if (!rel) return <div className="text-xs text-muted-foreground">加载关系中…</div>;

  const hasAny = (rel.upstream?.length ?? 0) + (rel.downstream?.length ?? 0) > 0;
  if (!hasAny) {
    return <div className="text-xs text-muted-foreground">这条资产还没有关联到其它资产。</div>;
  }

  const EDGE_LABEL: Record<string, string> = {
    belongs_to: "属于",
    resolves_to: "解析到",
    runs_on: "运行于",
    maps_to: "映射到",
    exposed_via: "暴露于",
  };

  return (
    <div className="space-y-2 rounded-md border p-3 text-xs">
      {rel.upstream?.length > 0 && (
        <div>
          <div className="mb-1 font-medium text-muted-foreground">上游（谁指向它）</div>
          <ul className="space-y-0.5">
            {rel.upstream.map((e) => (
              <li key={e.id} className="flex items-center gap-1.5">
                <Badge variant="secondary" className="text-[10px]">
                  {e.dst?.type}
                </Badge>
                <span className="font-mono">{e.dst?.label}</span>
                <span className="text-muted-foreground">—{EDGE_LABEL[e.kind] ?? e.kind}→ 本条</span>
              </li>
            ))}
          </ul>
        </div>
      )}
      {rel.downstream?.length > 0 && (
        <div>
          <div className="mb-1 font-medium text-muted-foreground">下游（本条指向谁）</div>
          <ul className="space-y-0.5">
            {rel.downstream.map((e) => (
              <li key={e.id} className="flex items-center gap-1.5">
                <span className="text-muted-foreground">本条 —{EDGE_LABEL[e.kind] ?? e.kind}→</span>
                <Badge variant="secondary" className="text-[10px]">
                  {e.dst?.type}
                </Badge>
                <span className="font-mono">{e.dst?.label}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

// CreateAssetButton 是人工新增资产的入口。只暴露当前视图对应的类型，避免在
// 「公网IP」页里手滑建出一条域名记录。
function CreateAssetButton({ view, onSaved }: { view: AssetView; onSaved: () => void }) {
  const [open, setOpen] = React.useState(false);
  const fields = CREATE_FIELDS[view.key] ?? CREATE_FIELDS.ip;
  const [values, setValues] = React.useState<Record<string, string>>({});
  const [tagsText, setTagsText] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => {
    if (!open) return;
    const init: Record<string, string> = {};
    for (const f of fields) init[f.key] = "";
    setValues(init);
    setTagsText("");
  }, [open, fields]);

  const submit = async () => {
    const required = fields.find((f) => f.required);
    if (required && !(values[required.key] ?? "").trim()) {
      toast.error(`${required.label}不能为空`);
      return;
    }
    const payload: Record<string, unknown> = { type: view.dbType };
    for (const f of fields) {
      const v = (values[f.key] ?? "").trim();
      if (v !== "") payload[f.key] = f.number ? Number(v) : v;
    }
    const tags = tagsText
      .split(/[,，]/)
      .map((t) => t.trim())
      .filter(Boolean);
    if (tags.length > 0) payload.tags = tags;

    setBusy(true);
    try {
      const res = await api.insertAssets([payload]);
      if (res.errors?.length) {
        toast.error(`新增失败：${res.errors[0].error}`);
        return;
      }
      toast.success("已新增");
      setOpen(false);
      onSaved();
    } catch (e) {
      toast.error(`新增失败：${String((e as Error)?.message ?? e)}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <Button size="sm" onClick={() => setOpen(true)}>
        <PlusIcon className="size-3.5" /> 新增
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>新增{view.title}</DialogTitle>
            <DialogDescription>手工登记一条记录。必填项标有 *。</DialogDescription>
          </DialogHeader>
          <FieldGroup className="py-2">
            {fields.map((f) => (
              <Field key={f.key}>
                <FieldLabel htmlFor={`ca-${f.key}`}>
                  {f.label}
                  {f.required ? " *" : ""}
                </FieldLabel>
                <Input
                  id={`ca-${f.key}`}
                  value={values[f.key] ?? ""}
                  placeholder={f.placeholder}
                  onChange={(e) => setValues((v) => ({ ...v, [f.key]: e.target.value }))}
                />
              </Field>
            ))}
            <Field>
              <FieldLabel htmlFor="ca-tags">标签</FieldLabel>
              <Input
                id="ca-tags"
                value={tagsText}
                placeholder="逗号分隔，如 核心系统, 待复测"
                onChange={(e) => setTagsText(e.target.value)}
              />
            </Field>
          </FieldGroup>
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)} disabled={busy}>
              取消
            </Button>
            <Button onClick={() => void submit()} disabled={busy}>
              {busy ? "保存中…" : "保存"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

// 各视图新增时需要的字段。type 由 view.dbType 决定，不在这里重复。
const CREATE_FIELDS: Record<
  string,
  { key: string; label: string; required?: boolean; placeholder?: string; number?: boolean }[]
> = {
  root_domain: [{ key: "domain", label: "域名", required: true, placeholder: "example.com" }],
  subdomain: [
    { key: "domain", label: "域名", required: true, placeholder: "api.example.com" },
    { key: "record_type", label: "解析类型", placeholder: "A / CNAME" },
  ],
  ip: [{ key: "ip", label: "IP 地址", required: true, placeholder: "203.0.113.10" }],
  app: [{ key: "app_name", label: "应用名", required: true }],
  service: [
    { key: "url", label: "URL（HTTP 服务）", placeholder: "https://example.com" },
    { key: "service_name", label: "服务名（非 HTTP 服务）", placeholder: "ssh / mysql" },
    { key: "port", label: "端口（非 HTTP 服务）", placeholder: "22", number: true },
  ],
  web: [{ key: "url", label: "URL", required: true, placeholder: "https://example.com" }],
  nat: [
    { key: "nat_internal_ip", label: "内网地址", required: true, placeholder: "10.10.1.20" },
    { key: "nat_internal_port", label: "内网端口", placeholder: "8080", number: true },
    { key: "nat_external_ip", label: "外网地址", placeholder: "203.0.113.50" },
    { key: "nat_external_port", label: "外网端口", placeholder: "443", number: true },
    { key: "nat_protocol", label: "协议", placeholder: "tcp / udp" },
  ],
  host: [
    { key: "hostname", label: "主机名", required: true, placeholder: "dmz-web-01" },
    { key: "ip", label: "IP 地址", placeholder: "10.10.1.20" },
    { key: "os", label: "操作系统", placeholder: "Ubuntu 22.04" },
    { key: "host_role", label: "角色", placeholder: "web / db / dc / jump" },
    { key: "ip_scope", label: "区域", placeholder: "dmz / private / public" },
  ],
};

// ScopeBadge 显示网络位置。DMZ 需要人工标注，所以颜色上与公网/内网区分开。
function ScopeBadge({ scope }: { scope?: string }) {
  if (!scope) return <span className="text-xs text-muted-foreground">—</span>;
  const tone: Record<string, string> = {
    public: "border-emerald-500/40 text-emerald-600",
    dmz: "border-amber-500/40 text-amber-600",
    private: "border-slate-400/40 text-slate-600",
    loopback: "border-slate-400/40 text-slate-500",
  };
  const label: Record<string, string> = {
    public: "公网",
    dmz: "DMZ",
    private: "内网",
    loopback: "回环",
  };
  return (
    <Badge variant="outline" className={cn("text-[10px]", tone[scope])}>
      {label[scope] ?? scope}
    </Badge>
  );
}

// HostForIP 显示这个 DMZ 地址上承载的主机名。
//
// 数据来自资产图的 runs_on 边（主机 → IP）。这里按需查询而不是把主机名塞进
// ip 资产的行里：一台机器可能换 IP，反向依赖关系以边为准更可靠。
function HostForIP({ ip }: { ip?: string }) {
  const [label, setLabel] = React.useState<string | null>(null);

  React.useEffect(() => {
    if (!ip) return;
    let live = true;
    // 列表行里只有 ip 文本、没有 ip 资产的 id，所以直接按 ip 搜主机列表。
    api
      .assets("host", 200, 0)
      .then((r) => {
        if (!live) return;
        const hit = r.assets.find((h) => h.ip === ip);
        setLabel(hit?.hostname ?? "");
      })
      .catch(() => {
        if (live) setLabel("");
      });
    return () => {
      live = false;
    };
  }, [ip]);

  if (label === null) return <span className="text-muted-foreground">…</span>;
  if (!label) return <span className="text-muted-foreground">—</span>;
  return <span>{label}</span>;
}
