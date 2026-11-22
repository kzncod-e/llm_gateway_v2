"use client";

import type { ColumnDef } from "@tanstack/react-table";
import { useState } from "react";
import { toast } from "sonner";

import { DataTable } from "@/components/common/data-table";
import { Field } from "@/components/common/field";
import { PageHeader, Section } from "@/components/common/page-header";
import { EmptyState, ErrorState, LoadingRows } from "@/components/common/states";
import { Pill } from "@/components/common/status-badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useChangePassword, useMe, usePermissions, useUpdateProfile } from "@/features/auth/api";
import { useModels } from "@/features/providers/api";
import { PricingDialog } from "@/features/providers/pricing-dialog";
import { useCreateUser, useUpdateUser, useUsers } from "@/features/teams/api";
import { ApiError } from "@/lib/api";
import { formatDateTime, formatDuration, formatPricePerM, formatRelative } from "@/lib/format";
import type { AuditLog, Model, User } from "@/lib/types";

import { useAuditLogs, useGatewaySettings } from "./api";

const errMsg = (e: unknown, f: string) => (e instanceof ApiError ? e.message : f);

function General() {
  const q = useGatewaySettings();
  if (q.isLoading) return <LoadingRows rows={6} />;
  if (q.error || !q.data) return <ErrorState error={q.error} onRetry={() => q.refetch()} />;
  const s = q.data;
  const rows: [string, React.ReactNode][] = [
    ["Environment", s.environment],
    ["Default requests / minute per team", s.default_team_rpm],
    ["Default concurrent requests per team", s.default_team_concurrency],
    ["Default max output tokens", s.default_max_output_tokens],
    ["Request timeout", formatDuration(s.request_timeout_ms)],
    ["Stream timeout", formatDuration(s.stream_timeout_ms)],
    ["Budget reservation TTL", formatDuration(s.reservation_ttl_ms)],
    ["Circuit breaker threshold", `${s.breaker_failure_threshold} consecutive failures`],
    ["Circuit open duration", formatDuration(s.breaker_open_duration_ms)],
    ["Redis outage behaviour", s.rate_limit_fail_open ? <Pill tone="warning">fail open</Pill> : <Pill tone="success">fail closed</Pill>],
    ["Tracing", s.tracing_enabled ? <Pill tone="success">enabled</Pill> : <Pill>disabled</Pill>],
    ["Currency", s.currency],
  ];
  return (
    <Section title="Gateway configuration" description="Read-only; set through environment variables (see .env.example)">
      <dl className="divide-y divide-border/50">
        {rows.map(([k, v]) => (
          <div key={k} className="flex items-center justify-between px-4 py-2.5 text-[13px]">
            <dt className="text-muted-foreground">{k}</dt>
            <dd className="tabular text-foreground">{v}</dd>
          </div>
        ))}
      </dl>
    </Section>
  );
}

function Pricing({ isAdmin }: { isAdmin: boolean }) {
  const models = useModels();
  const columns: ColumnDef<Model, unknown>[] = [
    { accessorKey: "name", header: "Model", cell: ({ row }) => <span className="font-mono text-xs text-foreground">{row.original.provider_slug}/{row.original.name}</span> },
    ...(["input_per_mtok_nanos", "cached_input_per_mtok_nanos", "cache_write_per_mtok_nanos", "output_per_mtok_nanos"] as const).map(
      (k): ColumnDef<Model, unknown> => ({
        id: k,
        header: { input_per_mtok_nanos: "Input", cached_input_per_mtok_nanos: "Cached", cache_write_per_mtok_nanos: "Cache write", output_per_mtok_nanos: "Output" }[k],
        meta: { align: "right" },
        cell: ({ row }) => (row.original.current_price ? formatPricePerM(row.original.current_price[k]) : <span className="text-muted-foreground">—</span>),
      }),
    ),
    { id: "since", header: "Effective since", cell: ({ row }) => (row.original.current_price ? <span className="text-muted-foreground">{formatDateTime(row.original.current_price.effective_from)}</span> : <Pill tone="warning">unpriced</Pill>) },
    {
      id: "actions",
      header: "",
      cell: ({ row }) => (
        <div className="flex justify-end">
          <PricingDialog model={row.original} canEdit={isAdmin} trigger={<Button variant="ghost" size="sm">{isAdmin ? "Edit" : "History"}</Button>} />
        </div>
      ),
    },
  ];
  return (
    <Section title="Model pricing" description="USD per 1M tokens, current version. Costs are computed from provider-reported usage.">
      <DataTable columns={columns} data={models.data} loading={models.isLoading} error={models.error} empty={<EmptyState title="No models configured" />} />
    </Section>
  );
}

function Account() {
  const me = useMe();
  const profile = useUpdateProfile();
  const password = useChangePassword();
  const [name, setName] = useState(me.data?.user.name ?? "");
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <Section title="Profile">
        <form
          className="space-y-4 p-4"
          onSubmit={(e) => {
            e.preventDefault();
            profile.mutate({ name }, { onSuccess: () => toast.success("Profile updated"), onError: (err) => toast.error(errMsg(err, "Update failed")) });
          }}
        >
          <Field label="Email">
            <Input value={me.data?.user.email ?? ""} disabled />
          </Field>
          <Field label="Display name" htmlFor="acc-name">
            <Input id="acc-name" value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Button type="submit" size="sm" disabled={profile.isPending}>
            Save
          </Button>
        </form>
      </Section>
      <Section title="Password">
        <form
          className="space-y-4 p-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (next.length < 12) return toast.error("New password must be at least 12 characters");
            password.mutate(
              { current_password: current, new_password: next },
              {
                onSuccess: () => {
                  toast.success("Password changed");
                  setCurrent("");
                  setNext("");
                },
                onError: (err) => toast.error(errMsg(err, "Change failed")),
              },
            );
          }}
        >
          <Field label="Current password" htmlFor="acc-cur">
            <Input id="acc-cur" type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />
          </Field>
          <Field label="New password" htmlFor="acc-new" hint="At least 12 characters">
            <Input id="acc-new" type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />
          </Field>
          <Button type="submit" size="sm" disabled={password.isPending}>
            Change password
          </Button>
        </form>
      </Section>
    </div>
  );
}

function Users() {
  const users = useUsers();
  const create = useCreateUser();
  const update = useUpdateUser();
  const me = useMe();
  const [form, setForm] = useState({ email: "", name: "", password: "", role: "member" as "admin" | "member" });
  const columns: ColumnDef<User, unknown>[] = [
    {
      accessorKey: "email",
      header: "User",
      cell: ({ row }) => (
        <div>
          <div className="text-foreground">{row.original.name}</div>
          <div className="text-[11px] text-muted-foreground">{row.original.email}</div>
        </div>
      ),
    },
    {
      accessorKey: "role",
      header: "Role",
      cell: ({ row }) => (
        <Select
          value={row.original.role}
          disabled={row.original.id === me.data?.user.id}
          onValueChange={(v) => update.mutate({ id: row.original.id, role: v as "admin" | "member" }, { onError: (e) => toast.error(errMsg(e, "Update failed")) })}
        >
          <SelectTrigger size="sm" className="w-28" aria-label="Role">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="admin">Admin</SelectItem>
            <SelectItem value="member">Member</SelectItem>
          </SelectContent>
        </Select>
      ),
    },
    { accessorKey: "last_login_at", header: "Last login", cell: ({ row }) => <span className="text-muted-foreground">{formatRelative(row.original.last_login_at)}</span> },
    { id: "status", header: "Status", cell: ({ row }) => (row.original.disabled_at ? <Pill tone="danger">disabled</Pill> : <Pill tone="success">active</Pill>) },
    {
      id: "actions",
      header: "",
      cell: ({ row }) =>
        row.original.id !== me.data?.user.id ? (
          <div className="flex justify-end">
            <Button variant="ghost" size="sm" onClick={() => update.mutate({ id: row.original.id, disabled: !row.original.disabled_at }, { onError: (e) => toast.error(errMsg(e, "Update failed")) })}>
              {row.original.disabled_at ? "Enable" : "Disable"}
            </Button>
          </div>
        ) : null,
    },
  ];
  return (
    <Section title="Users" description="Console accounts. Add users to teams from the team page.">
      <DataTable columns={columns} data={users.data} loading={users.isLoading} error={users.error} />
      <form
        className="grid gap-3 border-t border-border/60 p-4 sm:grid-cols-5 sm:items-end"
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate(form, {
            onSuccess: () => {
              toast.success(`User ${form.email} created`);
              setForm({ email: "", name: "", password: "", role: "member" });
            },
            onError: (err) => toast.error(errMsg(err, "Failed to create user")),
          });
        }}
      >
        <Field label="Email" htmlFor="u-email">
          <Input id="u-email" type="email" required value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} />
        </Field>
        <Field label="Name" htmlFor="u-name">
          <Input id="u-name" required value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
        </Field>
        <Field label="Initial password" htmlFor="u-pw">
          <Input id="u-pw" type="password" autoComplete="new-password" required minLength={12} value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} />
        </Field>
        <Field label="Role">
          <Select value={form.role} onValueChange={(v) => setForm({ ...form, role: v as "admin" | "member" })}>
            <SelectTrigger className="w-full" aria-label="New user role">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="member">Member</SelectItem>
              <SelectItem value="admin">Admin</SelectItem>
            </SelectContent>
          </Select>
        </Field>
        <Button type="submit" size="sm" disabled={create.isPending}>
          Create user
        </Button>
      </form>
    </Section>
  );
}

function Audit() {
  const q = useAuditLogs(true);
  const columns: ColumnDef<AuditLog, unknown>[] = [
    { accessorKey: "created_at", header: "Time", cell: ({ row }) => <span className="tabular text-muted-foreground">{formatDateTime(row.original.created_at)}</span> },
    { accessorKey: "actor_email", header: "Actor", cell: ({ row }) => row.original.actor_email || <span className="text-muted-foreground">system</span> },
    { accessorKey: "action", header: "Action", cell: ({ row }) => <span className="font-mono text-xs text-foreground">{row.original.action}</span> },
    { id: "target", header: "Target", cell: ({ row }) => <span className="font-mono text-[11px] text-muted-foreground">{row.original.target_type}:{row.original.target_id.slice(-8)}</span> },
    { accessorKey: "ip", header: "IP", cell: ({ row }) => <span className="text-muted-foreground">{row.original.ip || "—"}</span> },
  ];
  return (
    <Section title="Audit log" description="Administrative changes, newest first">
      <DataTable columns={columns} data={q.data} loading={q.isLoading} error={q.error} empty={<EmptyState title="No audit events" />} />
    </Section>
  );
}

export function SettingsView() {
  const { isAdmin } = usePermissions();
  return (
    <div className="space-y-6">
      <PageHeader title="Settings" description="Gateway configuration, model pricing and your account." />
      <Tabs defaultValue="general" className="space-y-4">
        <TabsList>
          <TabsTrigger value="general">General</TabsTrigger>
          <TabsTrigger value="pricing">Model pricing</TabsTrigger>
          <TabsTrigger value="account">Account</TabsTrigger>
          {isAdmin ? <TabsTrigger value="users">Users</TabsTrigger> : null}
          {isAdmin ? <TabsTrigger value="audit">Audit log</TabsTrigger> : null}
        </TabsList>
        <TabsContent value="general">
          <General />
        </TabsContent>
        <TabsContent value="pricing">
          <Pricing isAdmin={isAdmin} />
        </TabsContent>
        <TabsContent value="account">
          <Account />
        </TabsContent>
        {isAdmin ? (
          <TabsContent value="users">
            <Users />
          </TabsContent>
        ) : null}
        {isAdmin ? (
          <TabsContent value="audit">
            <Audit />
          </TabsContent>
        ) : null}
      </Tabs>
    </div>
  );
}
