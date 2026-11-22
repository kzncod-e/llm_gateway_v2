"use client";

import type { ColumnDef } from "@tanstack/react-table";
import { UserPlus } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { DataTable } from "@/components/common/data-table";
import { Field } from "@/components/common/field";
import { Section } from "@/components/common/page-header";
import { EmptyState } from "@/components/common/states";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { ApiError } from "@/lib/api";
import { formatMoney, nanosToUsd, usdToNanos } from "@/lib/format";
import type { BudgetStatus, Team, TeamMember, TeamRole } from "@/lib/types";

import { useAddMember, useMembers, useRemoveMember, useSetBudget, useUpdateTeam } from "./api";

const errMsg = (e: unknown, f: string) => (e instanceof ApiError ? e.message : f);

export function BudgetCard({ budget }: { budget: BudgetStatus }) {
  const limit = budget.monthly_limit_nanos;
  const pct = limit ? Math.min(100, (budget.month_spent_nanos / Math.max(limit, 1)) * 100) : 0;
  const reservedPct = limit ? Math.min(100 - pct, (budget.month_reserved_nanos / Math.max(limit, 1)) * 100) : 0;
  const tone = budget.exhausted ? "bg-danger" : budget.warning ? "bg-warning" : "bg-sand";
  return (
    <div className="space-y-3 p-4">
      <div className="flex items-baseline justify-between">
        <div>
          <div className="label-caps">Spent in {budget.period_month}</div>
          <div className="tabular mt-1 text-2xl font-semibold">{formatMoney(budget.month_spent_nanos)}</div>
        </div>
        <div className="text-right text-xs text-muted-foreground">
          {limit !== null ? (
            <>
              of <span className="text-foreground">{formatMoney(limit)}</span>
              <div>{formatMoney(budget.month_remaining_nanos)} remaining</div>
            </>
          ) : (
            "No monthly limit"
          )}
        </div>
      </div>
      {limit !== null ? (
        <div className="flex h-2 overflow-hidden rounded-sm bg-muted" role="progressbar" aria-valuenow={Math.round(pct)} aria-valuemin={0} aria-valuemax={100} aria-label="Monthly budget used">
          <div className={tone} style={{ width: `${pct}%` }} />
          <div className="bg-sand-deep/40" style={{ width: `${reservedPct}%` }} title="Reserved by in-flight requests" />
        </div>
      ) : null}
      <dl className="grid grid-cols-2 gap-2 text-xs">
        <div>
          <dt className="text-muted-foreground">Reserved (in flight)</dt>
          <dd className="tabular text-foreground">{formatMoney(budget.month_reserved_nanos)}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Today</dt>
          <dd className="tabular text-foreground">
            {formatMoney(budget.day_spent_nanos)}
            {budget.daily_limit_nanos !== null ? ` / ${formatMoney(budget.daily_limit_nanos)}` : ""}
          </dd>
        </div>
      </dl>
      {budget.exhausted ? (
        <p className="text-xs text-danger">Budget exhausted: new requests are rejected with HTTP 402.</p>
      ) : budget.warning ? (
        <p className="text-xs text-warning">Spend has passed the {budget.warn_threshold_pct}% warning threshold.</p>
      ) : null}
    </div>
  );
}

export function BudgetForm({ teamId, budget }: { teamId: string; budget: BudgetStatus }) {
  const set = useSetBudget(teamId);
  const [monthly, setMonthly] = useState(budget.monthly_limit_nanos !== null ? String(nanosToUsd(budget.monthly_limit_nanos)) : "");
  const [daily, setDaily] = useState(budget.daily_limit_nanos !== null ? String(nanosToUsd(budget.daily_limit_nanos)) : "");
  const [warn, setWarn] = useState(String(budget.warn_threshold_pct));
  const parse = (v: string) => (v.trim() === "" ? null : usdToNanos(Number(v)));
  return (
    <form
      className="grid gap-3 border-t border-border/60 p-4 sm:grid-cols-4 sm:items-end"
      onSubmit={(e) => {
        e.preventDefault();
        const m = parse(monthly);
        const d = parse(daily);
        if ([m, d].some((x) => x !== null && (Number.isNaN(x) || x < 0))) return toast.error("Limits must be non-negative numbers");
        set.mutate({ monthly_limit_nanos: m, daily_limit_nanos: d, warn_threshold_pct: Number(warn) || 80 }, { onSuccess: () => toast.success("Budget updated"), onError: (err) => toast.error(errMsg(err, "Update failed")) });
      }}
    >
      <Field label="Monthly limit (USD)" htmlFor="b-m" hint="Empty = unlimited">
        <Input id="b-m" type="number" step="0.01" min={0} value={monthly} onChange={(e) => setMonthly(e.target.value)} />
      </Field>
      <Field label="Daily limit (USD)" htmlFor="b-d" hint="Optional">
        <Input id="b-d" type="number" step="0.01" min={0} value={daily} onChange={(e) => setDaily(e.target.value)} />
      </Field>
      <Field label="Warn at (%)" htmlFor="b-w">
        <Input id="b-w" type="number" min={1} max={100} value={warn} onChange={(e) => setWarn(e.target.value)} />
      </Field>
      <Button type="submit" size="sm" disabled={set.isPending}>
        Save budget
      </Button>
    </form>
  );
}

export function LimitsForm({ team }: { team: Team }) {
  const update = useUpdateTeam(team.id);
  const [rpm, setRpm] = useState(team.rpm_limit?.toString() ?? "");
  const [conc, setConc] = useState(team.concurrency_limit?.toString() ?? "");
  return (
    <form
      className="grid gap-3 p-4 sm:grid-cols-3 sm:items-end"
      onSubmit={(e) => {
        e.preventDefault();
        update.mutate(
          {
            ...(rpm.trim() ? { rpm_limit: Number(rpm) } : { clear_rpm_limit: true }),
            ...(conc.trim() ? { concurrency_limit: Number(conc) } : { clear_concurrency_limit: true }),
          },
          { onSuccess: () => toast.success("Rate limits updated"), onError: (err) => toast.error(errMsg(err, "Update failed")) },
        );
      }}
    >
      <Field label="Requests per minute" htmlFor="l-rpm" hint="Empty = gateway default">
        <Input id="l-rpm" type="number" min={1} value={rpm} onChange={(e) => setRpm(e.target.value)} />
      </Field>
      <Field label="Concurrent requests" htmlFor="l-conc" hint="Empty = gateway default">
        <Input id="l-conc" type="number" min={1} value={conc} onChange={(e) => setConc(e.target.value)} />
      </Field>
      <Button type="submit" size="sm" disabled={update.isPending}>
        Save limits
      </Button>
    </form>
  );
}

export function MembersSection({ teamId, canManage }: { teamId: string; canManage: boolean }) {
  const members = useMembers(teamId);
  const add = useAddMember(teamId);
  const remove = useRemoveMember(teamId);
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<TeamRole>("developer");

  const columns: ColumnDef<TeamMember, unknown>[] = [
    {
      accessorKey: "name",
      header: "Member",
      cell: ({ row }) => (
        <div>
          <div className="text-foreground">{row.original.name || row.original.email}</div>
          <div className="text-[11px] text-muted-foreground">{row.original.email}</div>
        </div>
      ),
    },
    { accessorKey: "role", header: "Role", cell: ({ row }) => <span className="text-muted-foreground capitalize">{row.original.role}</span> },
    {
      id: "actions",
      header: "",
      cell: ({ row }) =>
        canManage ? (
          <div className="flex justify-end">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                if (!confirm(`Remove ${row.original.email} from the team?`)) return;
                remove.mutate(row.original.user_id, { onError: (e) => toast.error(errMsg(e, "Remove failed")) });
              }}
            >
              Remove
            </Button>
          </div>
        ) : null,
    },
  ];

  return (
    <Section title="Members" description="Owners manage keys and members; developers create keys and use the playground; viewers read">
      <DataTable columns={columns} data={members.data} loading={members.isLoading} error={members.error} empty={<EmptyState title="No members" />} />
      {canManage ? (
        <form
          className="flex flex-wrap items-end gap-2 border-t border-border/60 p-4"
          onSubmit={(e) => {
            e.preventDefault();
            add.mutate(
              { email: email.trim(), role },
              {
                onSuccess: () => {
                  toast.success("Member added");
                  setEmail("");
                },
                onError: (err) => toast.error(errMsg(err, "Failed to add member")),
              },
            );
          }}
        >
          <Field label="User email" htmlFor="mem-email">
            <Input id="mem-email" type="email" required value={email} onChange={(e) => setEmail(e.target.value)} className="w-60" />
          </Field>
          <Field label="Role">
            <Select value={role} onValueChange={(v) => setRole(v as TeamRole)}>
              <SelectTrigger className="w-32" aria-label="Role">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="owner">Owner</SelectItem>
                <SelectItem value="developer">Developer</SelectItem>
                <SelectItem value="viewer">Viewer</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Button type="submit" size="sm" variant="outline" disabled={add.isPending}>
            <UserPlus /> Add
          </Button>
        </form>
      ) : null}
    </Section>
  );
}
