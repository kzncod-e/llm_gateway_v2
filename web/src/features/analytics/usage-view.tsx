"use client";

import type { ColumnDef } from "@tanstack/react-table";
import { useState } from "react";

import { CHART_COLORS, HBarChart, TrendChart } from "@/components/common/charts";
import { DataTable } from "@/components/common/data-table";
import { PageHeader, Section } from "@/components/common/page-header";
import { RangeSelect, useTimeRange } from "@/components/common/range-select";
import { StatCard } from "@/components/common/stat-card";
import { EmptyState, ErrorState, LoadingRows } from "@/components/common/states";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useTeams } from "@/features/teams/api";
import { formatMoney, formatNumber, formatPercent } from "@/lib/format";
import type { BreakdownRow } from "@/lib/types";

import { useBreakdown, useSummary, useTimeseries } from "./api";
import { bucketLabel } from "./labels";

const ALL = "__all";

const breakdownColumns: ColumnDef<BreakdownRow, unknown>[] = [
  { accessorKey: "label", header: "Name", cell: ({ row }) => <span className="font-mono text-xs text-foreground">{row.original.label}</span> },
  { accessorKey: "attempts", header: "Attempts", meta: { align: "right" }, cell: ({ row }) => formatNumber(row.original.attempts) },
  { accessorKey: "input_tokens", header: "Input tokens", meta: { align: "right" }, cell: ({ row }) => formatNumber(row.original.input_tokens) },
  { accessorKey: "output_tokens", header: "Output tokens", meta: { align: "right" }, cell: ({ row }) => formatNumber(row.original.output_tokens) },
  {
    accessorKey: "unknown_usage",
    header: "Unknown usage",
    meta: { align: "right" },
    cell: ({ row }) => <span className={row.original.unknown_usage ? "text-warning" : "text-muted-foreground"}>{formatNumber(row.original.unknown_usage)}</span>,
  },
  { accessorKey: "cost_nanos", header: "Cost", meta: { align: "right" }, cell: ({ row }) => <span className="text-foreground">{formatMoney(row.original.cost_nanos)}</span> },
  { accessorKey: "share", header: "Share", meta: { align: "right" }, cell: ({ row }) => formatPercent(row.original.share) },
];

export function UsageView() {
  const [range, setRange] = useTimeRange("30d");
  const [team, setTeam] = useState<string>(ALL);
  const [by, setBy] = useState<"provider" | "model" | "team">("provider");
  const teamId = team === ALL ? undefined : team;
  const teams = useTeams();
  const summary = useSummary(range, teamId);
  const series = useTimeseries(range, teamId);
  const breakdown = useBreakdown(range, by, teamId);
  const s = summary.data;
  const points = (series.data ?? []).map((p) => ({ ...p, cost_usd: p.cost_nanos / 1e9 }));

  return (
    <div className="space-y-6">
      <PageHeader
        title="Usage & Analytics"
        description="Spend and token consumption from the usage ledger. Costs use the price version effective at request time."
        actions={
          <>
            <Select value={team} onValueChange={setTeam}>
              <SelectTrigger size="sm" className="w-44" aria-label="Team filter">
                <SelectValue placeholder="All teams" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={ALL}>All teams</SelectItem>
                {teams.data?.map((t) => (
                  <SelectItem key={t.id} value={t.id}>
                    {t.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <RangeSelect value={range.key} onChange={setRange} />
          </>
        }
      />

      {summary.error ? (
        <ErrorState error={summary.error} onRetry={() => summary.refetch()} />
      ) : (
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          <StatCard label="Total spend" loading={summary.isLoading} value={formatMoney(s?.cost_nanos)} hint={s?.unpriced_requests ? `${s.unpriced_requests} unpriced requests excluded` : "USD"} />
          <StatCard label="Estimated portion" loading={summary.isLoading} value={formatMoney(s?.estimated_cost_nanos)} hint="unknown usage charged at estimate" tone={s?.estimated_cost_nanos ? "warning" : "default"} />
          <StatCard label="Input tokens" loading={summary.isLoading} value={formatNumber(s?.input_tokens)} />
          <StatCard label="Output tokens" loading={summary.isLoading} value={formatNumber(s?.output_tokens)} />
        </div>
      )}

      <div className="grid gap-4 xl:grid-cols-2">
        <Section title="Spend over time">
          <div className="p-4">
            {series.isLoading ? (
              <LoadingRows rows={4} />
            ) : (
              <TrendChart data={points} xKey="bucket" xFormat={(v) => bucketLabel(v, range.interval)} format={(v) => formatMoney(v * 1e9, { compact: true })} series={[{ key: "cost_usd", name: "Spend" }]} />
            )}
          </div>
        </Section>
        <Section title="Token consumption">
          <div className="p-4">
            {series.isLoading ? (
              <LoadingRows rows={4} />
            ) : (
              <TrendChart
                data={points}
                xKey="bucket"
                xFormat={(v) => bucketLabel(v, range.interval)}
                format={(v) => formatNumber(v, true)}
                stacked
                series={[
                  { key: "input_tokens", name: "Input", color: CHART_COLORS[1] },
                  { key: "output_tokens", name: "Output", color: CHART_COLORS[0] },
                ]}
              />
            )}
          </div>
        </Section>
      </div>

      <Section
        title="Cost breakdown"
        description="Aggregated from per-attempt usage records"
        actions={
          <Tabs value={by} onValueChange={(v) => setBy(v as typeof by)}>
            <TabsList className="h-8">
              <TabsTrigger value="provider" className="text-xs">Provider</TabsTrigger>
              <TabsTrigger value="model" className="text-xs">Model</TabsTrigger>
              <TabsTrigger value="team" className="text-xs">Team</TabsTrigger>
            </TabsList>
          </Tabs>
        }
      >
        {breakdown.error ? (
          <ErrorState error={breakdown.error} onRetry={() => breakdown.refetch()} />
        ) : breakdown.isLoading ? (
          <LoadingRows />
        ) : !breakdown.data?.length ? (
          <EmptyState title="No usage in this range" description="Usage appears here once requests are processed." />
        ) : (
          <div className="grid gap-0 lg:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
            <div className="border-b border-border/60 p-4 lg:border-r lg:border-b-0">
              <HBarChart data={breakdown.data.slice(0, 10).map((r) => ({ label: r.label, value: r.cost_nanos }))} format={(v) => formatMoney(v, { compact: true })} />
            </div>
            <DataTable columns={breakdownColumns} data={breakdown.data} />
          </div>
        )}
      </Section>
    </div>
  );
}
