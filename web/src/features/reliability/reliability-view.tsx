"use client";

import type { ColumnDef } from "@tanstack/react-table";
import { RotateCcw } from "lucide-react";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { toast } from "sonner";

import { TrendChart } from "@/components/common/charts";
import { DataTable } from "@/components/common/data-table";
import { PageHeader, Section } from "@/components/common/page-header";
import { StatCard } from "@/components/common/stat-card";
import { EmptyState, ErrorState, LoadingRows } from "@/components/common/states";
import { CircuitBadge, Pill } from "@/components/common/status-badge";
import { Button } from "@/components/ui/button";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { usePermissions } from "@/features/auth/api";
import { requestColumns } from "@/features/requests/request-columns";
import { formatDuration, formatNumber, formatPercent, formatRelative } from "@/lib/format";
import type { Reliability } from "@/lib/types";

import { useReliability, useResetBreaker } from "./api";

type ProviderRow = Reliability["providers"][number];

/** Pivots per-provider hourly rows into one row per bucket for charting. */
export function pivotErrorRates(series: Reliability["series"]) {
  const providers = [...new Set(series.map((s) => s.provider_slug))];
  const buckets = new Map<string, Record<string, string | number>>();
  for (const s of series) {
    const row = buckets.get(s.bucket) ?? { bucket: s.bucket };
    row[s.provider_slug] = s.attempts ? s.errors / s.attempts : 0;
    buckets.set(s.bucket, row);
  }
  const rows = [...buckets.values()].sort((a, b) => String(a.bucket).localeCompare(String(b.bucket)));
  for (const r of rows) for (const p of providers) r[p] ??= 0;
  return { providers, rows };
}

export function ReliabilityView() {
  const [window, setWindow] = useState<"1h" | "24h">("24h");
  const q = useReliability(window);
  const reset = useResetBreaker();
  const { isAdmin } = usePermissions();
  const router = useRouter();
  const data = q.data;

  const columns: ColumnDef<ProviderRow, unknown>[] = [
    {
      accessorKey: "name",
      header: "Provider",
      cell: ({ row }) => (
        <div>
          <div className="text-foreground">{row.original.name}</div>
          <div className="font-mono text-[11px] text-muted-foreground">{row.original.slug}</div>
        </div>
      ),
    },
    { id: "circuit", header: "Circuit", cell: ({ row }) => (row.original.enabled ? <CircuitBadge state={row.original.circuit.state} /> : <Pill>disabled</Pill>) },
    { id: "attempts", header: "Attempts", meta: { align: "right" }, cell: ({ row }) => formatNumber(row.original.stats.attempts) },
    {
      id: "error_rate",
      header: "Error rate",
      meta: { align: "right" },
      cell: ({ row }) => {
        const r = row.original.stats.error_rate;
        return <span className={r > 0.1 ? "text-danger" : r > 0.02 ? "text-warning" : "text-foreground"}>{formatPercent(r)}</span>;
      },
    },
    { id: "skipped", header: "Circuit skips", meta: { align: "right" }, cell: ({ row }) => formatNumber(row.original.stats.skipped) },
    { id: "p50", header: "p50", meta: { align: "right" }, cell: ({ row }) => formatDuration(row.original.stats.p50_ms) },
    { id: "p95", header: "p95", meta: { align: "right" }, cell: ({ row }) => formatDuration(row.original.stats.p95_ms) },
    { id: "ttfb", header: "Avg first byte", meta: { align: "right" }, cell: ({ row }) => (row.original.stats.avg_ttfb_ms ? formatDuration(row.original.stats.avg_ttfb_ms) : "—") },
    {
      id: "last_error",
      header: "Last error",
      cell: ({ row }) =>
        row.original.stats.last_error ? (
          <div className="max-w-64">
            <div className="truncate font-mono text-[11px] text-danger" title={row.original.stats.last_error}>
              {row.original.stats.last_error}
            </div>
            <div className="text-[11px] text-muted-foreground">{formatRelative(row.original.stats.last_error_at)}</div>
          </div>
        ) : (
          <span className="text-muted-foreground">—</span>
        ),
    },
    {
      id: "actions",
      header: "",
      cell: ({ row }) =>
        isAdmin && row.original.circuit.state !== "closed" ? (
          <Button variant="outline" size="sm" onClick={() => reset.mutate(row.original.provider_id, { onSuccess: () => toast.success(`Circuit for ${row.original.slug} closed`) })}>
            <RotateCcw /> Reset
          </Button>
        ) : null,
    },
  ];

  const pivot = data ? pivotErrorRates(data.series) : { providers: [], rows: [] };
  const openCircuits = data?.providers.filter((p) => p.circuit.state !== "closed").length ?? 0;
  const s = data?.summary;

  return (
    <div className="space-y-6">
      <PageHeader
        title="Reliability"
        description="Circuit breakers, provider error rates, latency and fallbacks. Refreshes every 10 seconds."
        actions={
          <Tabs value={window} onValueChange={(v) => setWindow(v as "1h" | "24h")}>
            <TabsList className="h-8">
              <TabsTrigger value="1h" className="text-xs">1 hour</TabsTrigger>
              <TabsTrigger value="24h" className="text-xs">24 hours</TabsTrigger>
            </TabsList>
          </Tabs>
        }
      />
      {q.error ? <ErrorState error={q.error} onRetry={() => q.refetch()} /> : null}
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <StatCard label="Open circuits" loading={q.isLoading} value={openCircuits} tone={openCircuits ? "danger" : "success"} hint={`${data?.providers.length ?? 0} providers`} />
        <StatCard label="Success rate" loading={q.isLoading} value={formatPercent(s?.success_rate)} hint={`${formatNumber(s?.requests)} requests`} />
        <StatCard label="Failed requests" loading={q.isLoading} value={formatNumber(s?.failed)} tone={s?.failed ? "warning" : "default"} />
        <StatCard label="Fallbacks" loading={q.isLoading} value={formatNumber(s?.fallbacks)} hint="provider switches after failure" />
      </div>
      <Section title="Providers">
        <DataTable columns={columns} data={data?.providers} loading={q.isLoading} empty={<EmptyState title="No providers" />} />
      </Section>
      <Section title="Error rate by provider" description="Failed attempts / attempts, per hour">
        <div className="p-4">
          {q.isLoading ? (
            <LoadingRows rows={4} />
          ) : !pivot.rows.length ? (
            <EmptyState title="No provider attempts in this window" />
          ) : (
            <TrendChart
              data={pivot.rows}
              xKey="bucket"
              xFormat={(v) => new Intl.DateTimeFormat("en-US", { hour: "2-digit", minute: "2-digit", hour12: false }).format(new Date(v))}
              format={(v) => formatPercent(v, 0)}
              series={pivot.providers.map((p) => ({ key: p, name: p }))}
            />
          )}
        </div>
      </Section>
      <Section title="Recent failed requests">
        <DataTable
          columns={requestColumns}
          data={data?.recent_failures}
          loading={q.isLoading}
          onRowClick={(r) => router.push(`/requests/${r.id}`)}
          empty={<EmptyState title="No failed requests" description="Nice. Failures in this window will appear here." />}
        />
      </Section>
    </div>
  );
}
