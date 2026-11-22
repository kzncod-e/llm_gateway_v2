"use client";

import { ArrowLeft } from "lucide-react";
import Link from "next/link";

import { PageHeader, Section } from "@/components/common/page-header";
import { useTimeRange } from "@/components/common/range-select";
import { StatCard } from "@/components/common/stat-card";
import { ErrorState, LoadingRows } from "@/components/common/states";
import { Pill } from "@/components/common/status-badge";
import { Button } from "@/components/ui/button";
import { useSummary } from "@/features/analytics/api";
import { usePermissions } from "@/features/auth/api";
import { useGatewaySettings } from "@/features/settings/api";
import { formatMoney, formatNumber, formatPercent } from "@/lib/format";

import { useBudget, useTeam } from "./api";
import { ApiKeysSection } from "./api-keys-section";
import { BudgetCard, BudgetForm, LimitsForm, MembersSection } from "./team-settings";

export function TeamDetailView({ id }: { id: string }) {
  const team = useTeam(id);
  const budget = useBudget(id);
  const [range] = useTimeRange("30d");
  const usage = useSummary(range, id);
  const settings = useGatewaySettings();
  const { isAdmin, canManage, canDevelop } = usePermissions();

  if (team.isLoading) return <LoadingRows rows={8} />;
  if (team.error || !team.data) return <ErrorState error={team.error} onRetry={() => team.refetch()} />;
  const t = team.data.team;
  const u = usage.data;

  return (
    <div className="space-y-6">
      <PageHeader
        title={t.name}
        description={
          <span>
            <span className="font-mono text-xs">{t.slug}</span>
            {t.description ? ` · ${t.description}` : ""}
          </span>
        }
        actions={
          <>
            {t.archived_at ? <Pill tone="warning">archived</Pill> : null}
            <Pill>{team.data.role}</Pill>
            <Button variant="ghost" size="sm" asChild>
              <Link href="/teams">
                <ArrowLeft /> Back
              </Link>
            </Button>
          </>
        }
      />

      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <StatCard label="Spend (30d)" loading={usage.isLoading} value={formatMoney(u?.cost_nanos)} />
        <StatCard label="Requests (30d)" loading={usage.isLoading} value={formatNumber(u?.requests)} />
        <StatCard label="Tokens (30d)" loading={usage.isLoading} value={formatNumber(u?.total_tokens, true)} />
        <StatCard label="Success rate" loading={usage.isLoading} value={formatPercent(u?.success_rate)} />
      </div>

      <div className="grid gap-4 lg:grid-cols-5">
        <div className="space-y-4 lg:col-span-3">
          <ApiKeysSection teamId={id} canDevelop={canDevelop(id)} />
          <MembersSection teamId={id} canManage={canManage(id)} />
        </div>
        <div className="space-y-4 lg:col-span-2">
          <Section title="Budget" description="Enforced with reservations before each provider call">
            {budget.isLoading ? <LoadingRows rows={3} /> : budget.error || !budget.data ? <ErrorState error={budget.error} /> : <BudgetCard budget={budget.data} />}
            {isAdmin && budget.data ? <BudgetForm key={`${budget.data.monthly_limit_nanos}-${budget.data.daily_limit_nanos}`} teamId={id} budget={budget.data} /> : null}
          </Section>
          <Section title="Rate limits" description="Per team, shared across all of its keys">
            {isAdmin ? (
              <LimitsForm key={t.updated_at} team={t} />
            ) : (
              <dl className="space-y-1.5 p-4 text-xs">
                <div className="flex justify-between">
                  <dt className="text-muted-foreground">Requests per minute</dt>
                  <dd>{t.rpm_limit ?? `${settings.data?.default_team_rpm ?? "—"} (default)`}</dd>
                </div>
                <div className="flex justify-between">
                  <dt className="text-muted-foreground">Concurrent requests</dt>
                  <dd>{t.concurrency_limit ?? `${settings.data?.default_team_concurrency ?? "—"} (default)`}</dd>
                </div>
              </dl>
            )}
          </Section>
        </div>
      </div>
    </div>
  );
}
