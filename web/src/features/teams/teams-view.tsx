"use client";

import { zodResolver } from "@hookform/resolvers/zod";
import type { ColumnDef } from "@tanstack/react-table";
import { Plus } from "lucide-react";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { z } from "zod";

import { DataTable } from "@/components/common/data-table";
import { Field } from "@/components/common/field";
import { PageHeader, Section } from "@/components/common/page-header";
import { EmptyState } from "@/components/common/states";
import { Pill } from "@/components/common/status-badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { useMe, usePermissions } from "@/features/auth/api";
import { ApiError } from "@/lib/api";
import { formatDate } from "@/lib/format";
import type { Team } from "@/lib/types";

import { useCreateTeam, useTeams } from "./api";

const optionalInt = z.preprocess((v) => (v === "" || v === undefined ? undefined : Number(v)), z.number().int().min(1).max(1_000_000).optional());
const schema = z.object({
  name: z.string().trim().min(1, "Name is required").max(100),
  description: z.string().max(500).optional(),
  rpm_limit: optionalInt,
  concurrency_limit: optionalInt,
});
type Values = z.input<typeof schema>;

function CreateTeamDialog() {
  const [open, setOpen] = useState(false);
  const create = useCreateTeam();
  const router = useRouter();
  const form = useForm<Values, unknown, z.output<typeof schema>>({ resolver: zodResolver(schema), defaultValues: { name: "", description: "" } });
  const e = form.formState.errors;
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus /> New team
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>New team</DialogTitle>
          <DialogDescription>Teams own API keys, budgets and rate limits. Usage is attributed per team.</DialogDescription>
        </DialogHeader>
        <form
          noValidate
          className="space-y-4"
          onSubmit={form.handleSubmit((v) =>
            create.mutate(v, {
              onSuccess: (t) => {
                toast.success(`Team ${t.name} created`);
                setOpen(false);
                router.push(`/teams/${t.id}`);
              },
              onError: (err) => toast.error(err instanceof ApiError ? err.message : "Failed to create team"),
            }),
          )}
        >
          <Field label="Name" htmlFor="t-name" error={e.name?.message}>
            <Input id="t-name" {...form.register("name")} />
          </Field>
          <Field label="Description" htmlFor="t-desc" error={e.description?.message}>
            <Textarea id="t-desc" rows={2} {...form.register("description")} />
          </Field>
          <div className="grid grid-cols-2 gap-3">
            <Field label="Requests / minute" htmlFor="t-rpm" error={e.rpm_limit?.message} hint="Empty = gateway default">
              <Input id="t-rpm" type="number" {...form.register("rpm_limit")} />
            </Field>
            <Field label="Concurrent requests" htmlFor="t-conc" error={e.concurrency_limit?.message} hint="Empty = gateway default">
              <Input id="t-conc" type="number" {...form.register("concurrency_limit")} />
            </Field>
          </div>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending ? "Creating…" : "Create team"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function TeamsView() {
  const router = useRouter();
  const teams = useTeams();
  const me = useMe();
  const { isAdmin } = usePermissions();
  const roleOf = (id: string) => (isAdmin ? "admin" : me.data?.teams.find((t) => t.id === id)?.role ?? "—");

  const columns: ColumnDef<Team, unknown>[] = [
    {
      accessorKey: "name",
      header: "Team",
      cell: ({ row }) => (
        <div>
          <div className="font-medium text-foreground">{row.original.name}</div>
          <div className="font-mono text-[11px] text-muted-foreground">{row.original.slug}</div>
        </div>
      ),
    },
    { accessorKey: "description", header: "Description", cell: ({ row }) => <span className="text-muted-foreground">{row.original.description || "—"}</span> },
    { accessorKey: "rpm_limit", header: "RPM", meta: { align: "right" }, cell: ({ row }) => row.original.rpm_limit ?? <span className="text-muted-foreground">default</span> },
    { accessorKey: "concurrency_limit", header: "Concurrency", meta: { align: "right" }, cell: ({ row }) => row.original.concurrency_limit ?? <span className="text-muted-foreground">default</span> },
    { id: "role", header: "Your role", cell: ({ row }) => <span className="text-muted-foreground">{roleOf(row.original.id)}</span> },
    { accessorKey: "created_at", header: "Created", cell: ({ row }) => <span className="text-muted-foreground">{formatDate(row.original.created_at)}</span> },
    { id: "state", header: "", cell: ({ row }) => (row.original.archived_at ? <Pill>archived</Pill> : null) },
  ];

  return (
    <div className="space-y-6">
      <PageHeader title="Teams" description="Tenants of the gateway. Each team has its own keys, budget and limits." actions={isAdmin ? <CreateTeamDialog /> : null} />
      <Section title="All teams">
        <DataTable
          columns={columns}
          data={teams.data}
          loading={teams.isLoading}
          error={teams.error}
          onRetry={() => teams.refetch()}
          onRowClick={(t) => router.push(`/teams/${t.id}`)}
          empty={<EmptyState title={isAdmin ? "No teams yet" : "You are not a member of any team"} description={isAdmin ? "Create a team to issue API keys." : "Ask an administrator to add you to a team."} action={isAdmin ? <CreateTeamDialog /> : null} />}
        />
      </Section>
    </div>
  );
}
