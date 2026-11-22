"use client";

import { ArrowLeft, CornerDownRight } from "lucide-react";
import Link from "next/link";
import type { ReactNode } from "react";

import { PageHeader, Section } from "@/components/common/page-header";
import { ErrorState, LoadingRows } from "@/components/common/states";
import { CostStatusHint, Pill, RequestStatusBadge } from "@/components/common/status-badge";
import { Button } from "@/components/ui/button";
import { formatDateTime, formatDuration, formatMoney, formatNumber } from "@/lib/format";
import type { Attempt } from "@/lib/types";
import { cn } from "@/lib/utils";

import { useRequest } from "./api";

function KV({ k, v }: { k: string; v: ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-4 py-1.5 text-[13px]">
      <dt className="text-muted-foreground">{k}</dt>
      <dd className="tabular min-w-0 truncate text-right text-foreground">{v}</dd>
    </div>
  );
}

const attemptTone = { success: "success", error: "danger", interrupted: "warning", client_cancelled: "neutral", skipped: "neutral" } as const;

function AttemptRow({ a, prev }: { a: Attempt; prev?: Attempt }) {
  const switched = prev && prev.provider_slug !== a.provider_slug;
  return (
    <li className="px-4 py-3">
      {switched ? (
        <div className="mb-2 flex items-center gap-1.5 text-[11px] text-warning">
          <CornerDownRight className="size-3" /> Fallback from {prev.provider_slug} to {a.provider_slug}
        </div>
      ) : null}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span className="tabular w-6 text-xs text-muted-foreground">#{a.attempt_number}</span>
        <span className="font-mono text-xs text-foreground">
          {a.provider_slug} / {a.model_name}
        </span>
        <Pill tone={attemptTone[a.status]}>{a.status === "skipped" ? "skipped · circuit open" : a.status.replace("_", " ")}</Pill>
        {a.http_status ? <span className="text-[11px] text-muted-foreground">HTTP {a.http_status}</span> : null}
        <span className="tabular ml-auto text-xs text-muted-foreground">
          {formatDuration(a.duration_ms)}
          {a.ttfb_ms !== null ? ` · first byte ${formatDuration(a.ttfb_ms)}` : ""}
        </span>
      </div>
      {a.error_kind && a.status !== "success" ? (
        <div className="mt-1.5 ml-9 text-xs text-muted-foreground">
          <span className="font-mono text-danger">{a.error_kind}</span>
          {a.retryable ? <span className="ml-2 text-[11px]">(retryable)</span> : null}
          {a.error_message ? <span className="ml-2 break-words">{a.error_message}</span> : null}
        </div>
      ) : null}
      {a.usage_status ? (
        <div className="tabular mt-1.5 ml-9 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-muted-foreground">
          <span>
            usage <span className={cn(a.usage_status === "unknown" && "text-warning", a.usage_status === "reported" && "text-foreground")}>{a.usage_status}</span>
          </span>
          {a.usage_status !== "none" ? (
            <>
              <span>in {formatNumber(a.input_tokens)}</span>
              {a.cached_input_tokens ? <span>cached {formatNumber(a.cached_input_tokens)}</span> : null}
              {a.cache_write_tokens ? <span>cache write {formatNumber(a.cache_write_tokens)}</span> : null}
              <span>out {formatNumber(a.output_tokens)}</span>
              {a.reasoning_tokens ? <span>reasoning {formatNumber(a.reasoning_tokens)}</span> : null}
              <span>cost {formatMoney(a.cost_nanos)}</span>
            </>
          ) : null}
        </div>
      ) : null}
    </li>
  );
}

export function RequestDetailView({ id }: { id: string }) {
  const q = useRequest(id);
  const back = (
    <Button variant="ghost" size="sm" asChild>
      <Link href="/requests">
        <ArrowLeft /> Back
      </Link>
    </Button>
  );
  if (q.isLoading) return <LoadingRows rows={8} />;
  if (q.error || !q.data) return <ErrorState error={q.error} onRetry={() => q.refetch()} />;
  const r = q.data;

  return (
    <div className="space-y-6">
      <PageHeader
        title={`Request ${r.id.slice(0, 8)}…`}
        description={<span className="font-mono text-xs">{r.id}</span>}
        actions={
          <>
            <RequestStatusBadge status={r.status} />
            {back}
          </>
        }
      />
      {r.error_message ? (
        <div className="rounded-lg border border-danger/30 bg-danger/10 px-4 py-3 text-xs">
          <span className="font-mono text-danger">{r.error_code}</span> <span className="text-foreground">{r.error_message}</span>
        </div>
      ) : null}
      <div className="grid gap-4 lg:grid-cols-3">
        <Section title="Request">
          <dl className="divide-y divide-border/50 px-4 py-1">
            <KV k="Created" v={formatDateTime(r.created_at)} />
            <KV k="Team" v={r.team_name} />
            <KV k="Source" v={r.source === "api" ? `API key ${r.api_key_prefix ?? "(deleted)"}…` : "Playground"} />
            <KV k="Requested model" v={<span className="font-mono text-xs">{r.requested_model}</span>} />
            <KV k="Mode" v={r.stream ? "Streaming (SSE)" : "Non-streaming"} />
            <KV k="HTTP status" v={r.http_status} />
          </dl>
        </Section>
        <Section title="Routing">
          <dl className="divide-y divide-border/50 px-4 py-1">
            <KV k="Served by" v={r.provider_slug ? `${r.provider_slug} / ${r.model_name}` : "—"} />
            <KV k="Attempts" v={r.attempt_count} />
            <KV k="Fallbacks" v={r.fallback_count} />
            <KV k="Latency" v={formatDuration(r.latency_ms)} />
            <KV k="Time to first byte" v={formatDuration(r.ttfb_ms)} />
          </dl>
        </Section>
        <Section title="Usage & cost">
          <dl className="divide-y divide-border/50 px-4 py-1">
            <KV k="Usage" v={r.usage_status} />
            <KV k="Input tokens" v={formatNumber(r.input_tokens)} />
            <KV k="Output tokens" v={formatNumber(r.output_tokens)} />
            <KV
              k="Cost"
              v={
                <span className="inline-flex items-center gap-1">
                  {formatMoney(r.cost_nanos)} <CostStatusHint status={r.cost_status} />
                </span>
              }
            />
            <KV k="Budget charge" v={formatMoney(r.budget_charge_nanos)} />
          </dl>
        </Section>
      </div>
      <Section title="Provider attempts" description="Retries and fallbacks in execution order">
        {r.attempts.length ? (
          <ol className="divide-y divide-border/50">
            {r.attempts.map((a, i) => (
              <AttemptRow key={a.id} a={a} prev={r.attempts[i - 1]} />
            ))}
          </ol>
        ) : (
          <p className="px-4 py-6 text-xs text-muted-foreground">This request was rejected before any provider was called.</p>
        )}
      </Section>
    </div>
  );
}
