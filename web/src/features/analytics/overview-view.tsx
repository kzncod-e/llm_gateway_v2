"use client";

import { Activity, CircleDollarSign, Coins, Gauge, Zap } from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/navigation";

import { DonutChart, TrendChart, CHART_COLORS } from "@/components/common/charts";
import { DataTable } from "@/components/common/data-table";
import { PageHeader, Section } from "@/components/common/page-header";
import { RangeSelect, useTimeRange } from "@/components/common/range-select";
import { StatCard } from "@/components/common/stat-card";
import { EmptyState, ErrorState, LoadingRows } from "@/components/common/states";
import { CircuitBadge } from "@/components/common/status-badge";
import { Button } from "@/components/ui/button";
import { useProviders } from "@/features/providers/api";
import { requestColumns } from "@/features/requests/request-columns";
import { useRecentRequests } from "@/features/requests/api";
import { formatDuration, formatMoney, formatNumber, formatPercent } from "@/lib/format";

import { useBreakdown, useSummary, useTimeseries } from "./api";
import { bucketLabel } from "./labels";

export function OverviewView() {
  const [range, setRange] = useTimeRange("7d");
  const summary = useSummary(range);
  const series = useTimeseries(range);
  const byProvider = useBreakdown(range, "provider");
  const providers = useProviders();
  const recent = useRecentRequests(8);
  const router = useRouter();
  const s = summary.data;

  const points = (series.data ?? []).map((p) => ({ ...p, cost_usd: p.cost_nanos / 1e9, succeeded: p.requests - p.failed }));
  const fmtBucket = (v: string) => bucketLabel(v, range.interval);

  return (
    <div className="space-y-6">
      <PageHeader title="Overview" description="Gateway traffic, spend and provider health across all teams you can access." actions={<RangeSelect value={range.key} onChange={setRange} />} />

      {summary.error ? (
        <ErrorState error={summary.error} onRetry={() => summary.refetch()} />
      ) : (
        <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-5">
          <StatCard
            label="Total spend"
            icon={<CircleDollarSign />}
            loading={summary.isLoading}
            value={formatMoney(s?.cost_nanos)}
            hint={s && s.estimated_cost_nanos > 0 ? `${formatMoney(s.estimated_cost_nanos)} estimated` : "provider-reported usage"}
          />
          <StatCard label="Requests" icon={<Zap />} loading={summary.isLoading} value={formatNumber(s?.requests)} hint={s ? `${formatNumber(s.stream_requests)} streamed` : undefined} />
          <StatCard
            label="Tokens"
            icon={<Coins />}
            loading={summary.isLoading}
            value={formatNumber(s?.total_tokens, true)}
            hint={s ? `${formatNumber(s.input_tokens, true)} in · ${formatNumber(s.output_tokens, true)} out` : undefined}
          />
          <StatCard
            label="Success rate"
            icon={<Activity />}
            loading={summary.isLoading}
            value={formatPercent(s?.success_rate)}
            tone={s && s.requests > 0 ? (s.success_rate >= 0.98 ? "success" : s.success_rate >= 0.9 ? "warning" : "danger") : "default"}
            hint={s ? `${formatNumber(s.failed)} failed · ${formatNumber(s.fallbacks)} fallbacks` : undefined}
          />
          <StatCard label="p95 latency" icon={<Gauge />} loading={summary.isLoading} value={formatDuration(s?.p95_latency_ms)} hint={s ? `avg ${formatDuration(s.avg_latency_ms)}` : undefined} />
        </div>
      )}

      <div className="grid gap-4 xl:grid-cols-2">
        <Section title="Spending trend" description="Cost per bucket, including estimates for unknown usage">
          <div className="p-4">
            {series.isLoading ? (
              <LoadingRows rows={4} />
            ) : series.error ? (
              <ErrorState error={series.error} onRetry={() => series.refetch()} />
            ) : (
              <TrendChart data={points} xKey="bucket" xFormat={fmtBucket} format={(v) => formatMoney(v * 1e9, { compact: true })} series={[{ key: "cost_usd", name: "Spend" }]} />
            )}
          </div>
        </Section>
        <Section title="Request volume" description="Succeeded and failed requests">
          <div className="p-4">
            {series.isLoading ? (
              <LoadingRows rows={4} />
            ) : series.error ? (
              <ErrorState error={series.error} />
            ) : (
              <TrendChart
                data={points}
                xKey="bucket"
                xFormat={fmtBucket}
                format={(v) => formatNumber(v, true)}
                series={[
                  { key: "succeeded", name: "Succeeded", color: CHART_COLORS[1] },
                  { key: "failed", name: "Failed", color: CHART_COLORS[4] },
                ]}
              />
            )}
          </div>
        </Section>
      </div>

      <div className="grid gap-4 xl:grid-cols-3">
        <Section title="Provider distribution" description="Share of spend by provider">
          <div className="p-4">
            {byProvider.isLoading ? (
              <LoadingRows rows={3} />
            ) : !byProvider.data?.length ? (
              <EmptyState title="No usage in this range" />
            ) : (
              <div className="flex items-center gap-4">
                <div className="w-36 shrink-0">
                  <DonutChart data={byProvider.data.map((r) => ({ label: r.label, value: r.cost_nanos }))} format={(v) => formatMoney(v)} height={140} />
                </div>
                <ul className="min-w-0 flex-1 space-y-2 text-xs">
                  {byProvider.data.map((r, i) => (
                    <li key={r.key} className="flex items-center gap-2">
                      <span className="size-2 shrink-0 rounded-sm" style={{ background: CHART_COLORS[i % CHART_COLORS.length] }} />
                      <span className="truncate text-foreground">{r.label}</span>
                      <span className="tabular ml-auto text-muted-foreground">{formatPercent(r.share, 0)}</span>
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </div>
        </Section>
        <Section
          title="Provider health"
          description="Live circuit breaker state"
          actions={
            <Button variant="ghost" size="sm" asChild>
              <Link href="/reliability">Details</Link>
            </Button>
          }
        >
          {providers.isLoading ? (
            <LoadingRows rows={3} />
          ) : providers.error ? (
            <ErrorState error={providers.error} />
          ) : !providers.data?.length ? (
            <EmptyState title="No providers configured" action={<Button size="sm" asChild><Link href="/providers">Add provider</Link></Button>} />
          ) : (
            <ul className="divide-y divide-border/60">
              {providers.data.map((p) => (
                <li key={p.id} className="flex items-center justify-between px-4 py-2.5 text-[13px]">
                  <div className="min-w-0">
                    <div className="truncate text-foreground">{p.name}</div>
                    <div className="text-[11px] text-muted-foreground">{p.enabled ? p.type : "disabled"}</div>
                  </div>
                  <CircuitBadge state={p.circuit_state ?? "closed"} />
                </li>
              ))}
            </ul>
          )}
        </Section>
        <Section title="Budget guardrails" description="How spend is controlled">
          <div className="space-y-2 p-4 text-xs text-muted-foreground">
            <p>Each request reserves its worst-case cost before reaching a provider, so concurrent requests cannot overspend a team budget.</p>
            <p>Unknown usage (timeouts, interrupted streams) is charged at the reserved estimate, never as zero.</p>
            <Button variant="outline" size="sm" className="mt-1" asChild>
              <Link href="/teams">Manage team budgets</Link>
            </Button>
          </div>
        </Section>
      </div>

      <Section
        title="Recent activity"
        actions={
          <Button variant="ghost" size="sm" asChild>
            <Link href="/requests">All requests</Link>
          </Button>
        }
      >
        <DataTable
          columns={requestColumns}
          data={recent.data}
          loading={recent.isLoading}
          error={recent.error}
          onRetry={() => recent.refetch()}
          onRowClick={(r) => router.push(`/requests/${r.id}`)}
          empty={<EmptyState title="No requests yet" description="Send a request with a team API key or try the Playground." action={<Button size="sm" asChild><Link href="/playground">Open playground</Link></Button>} />}
        />
      </Section>
    </div>
  );
}
