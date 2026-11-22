"use client";

import type { ColumnDef } from "@tanstack/react-table";
import { KeyRound, Pencil, Plus, Trash2 } from "lucide-react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";

import { DataTable } from "@/components/common/data-table";
import { PageHeader, Section } from "@/components/common/page-header";
import { EmptyState } from "@/components/common/states";
import { CircuitBadge, Pill } from "@/components/common/status-badge";
import { Button } from "@/components/ui/button";
import { usePermissions } from "@/features/auth/api";
import { ApiError } from "@/lib/api";
import type { Model, Provider, RoutingPolicy } from "@/lib/types";

import { useDeletePolicy, useModels, usePolicies, useProviders } from "./api";
import { PolicyDialog } from "./policy-dialog";
import { AddProviderDialog } from "./provider-dialog";

const typeLabel = { openai: "OpenAI", anthropic: "Anthropic", gemini: "Gemini" } as const;

const columns: ColumnDef<Provider, unknown>[] = [
  {
    accessorKey: "name",
    header: "Provider",
    cell: ({ row }) => (
      <div>
        <div className="font-medium text-foreground">{row.original.name}</div>
        <div className="font-mono text-[11px] text-muted-foreground">{row.original.slug}</div>
      </div>
    ),
  },
  { accessorKey: "type", header: "Type", cell: ({ row }) => typeLabel[row.original.type] },
  { accessorKey: "base_url", header: "Endpoint", cell: ({ row }) => <span className="font-mono text-[11px] text-muted-foreground">{row.original.base_url}</span> },
  {
    id: "credential",
    header: "Credential",
    cell: ({ row }) =>
      row.original.has_api_key ? (
        <span className="inline-flex items-center gap-1 font-mono text-[11px] text-muted-foreground">
          <KeyRound className="size-3" /> ••••{row.original.api_key_last4}
        </span>
      ) : (
        <Pill tone="warning">missing</Pill>
      ),
  },
  { accessorKey: "priority", header: "Priority", meta: { align: "right" } },
  {
    id: "status",
    header: "Status",
    cell: ({ row }) => (row.original.enabled ? <CircuitBadge state={row.original.circuit_state ?? "closed"} /> : <Pill>disabled</Pill>),
  },
];

function PolicyList({ policies, models, isAdmin }: { policies: RoutingPolicy[]; models: Model[]; isAdmin: boolean }) {
  const del = useDeletePolicy();
  const byId = new Map(models.map((m) => [m.id, m]));
  if (!policies.length) return <EmptyState title="No routing policies" description="Create an alias such as “smart” that falls back across providers." />;
  return (
    <ul className="divide-y divide-border/60">
      {policies.map((p) => (
        <li key={p.id} className="flex flex-wrap items-center gap-3 px-4 py-3">
          <div className="w-40 min-w-0">
            <div className="font-mono text-[13px] text-foreground">{p.alias}</div>
            <div className="truncate text-[11px] text-muted-foreground">{p.description || "—"}</div>
          </div>
          <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5 text-[11px]">
            {p.target_model_ids.map((id, i) => {
              const m = byId.get(id);
              return (
                <span key={id} className="inline-flex items-center gap-1.5">
                  {i > 0 ? <span className="text-muted-foreground">→</span> : null}
                  <span className="rounded border border-border bg-muted/50 px-1.5 py-0.5 font-mono text-foreground">{m ? `${m.provider_slug}/${m.name}` : "deleted model"}</span>
                </span>
              );
            })}
          </div>
          <span className="text-[11px] text-muted-foreground">
            {p.max_attempts_per_target}× per target · {p.retry_base_ms}–{p.retry_max_ms} ms
          </span>
          {!p.enabled ? <Pill>disabled</Pill> : null}
          {isAdmin ? (
            <div className="flex gap-1">
              <PolicyDialog
                policy={p}
                models={models}
                trigger={
                  <Button variant="ghost" size="icon-sm" aria-label={`Edit ${p.alias}`}>
                    <Pencil />
                  </Button>
                }
              />
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={`Delete ${p.alias}`}
                onClick={() => {
                  if (!confirm(`Delete routing policy "${p.alias}"? Clients requesting it will get model_not_found.`)) return;
                  del.mutate(p.id, {
                    onSuccess: () => toast.success("Policy deleted"),
                    onError: (e) => toast.error(e instanceof ApiError ? e.message : "Delete failed"),
                  });
                }}
              >
                <Trash2 />
              </Button>
            </div>
          ) : null}
        </li>
      ))}
    </ul>
  );
}

export function ProvidersView() {
  const router = useRouter();
  const { isAdmin } = usePermissions();
  const providers = useProviders();
  const models = useModels();
  const policies = usePolicies();

  return (
    <div className="space-y-6">
      <PageHeader title="Providers" description="Provider registry, credentials, models and routing." actions={isAdmin ? <AddProviderDialog /> : null} />
      <Section title="Registry" description="Model-based routing tries providers that serve a model in priority order">
        <DataTable
          columns={columns}
          data={providers.data}
          loading={providers.isLoading}
          error={providers.error}
          onRetry={() => providers.refetch()}
          onRowClick={(p) => router.push(`/providers/${p.id}`)}
          empty={<EmptyState title="No providers yet" description="Add an OpenAI, Anthropic or Gemini provider to start routing traffic." action={isAdmin ? <AddProviderDialog /> : null} />}
        />
      </Section>
      <Section
        title="Routing & fallback policies"
        description="Aliases resolve to an ordered chain of targets"
        actions={
          isAdmin && models.data ? (
            <PolicyDialog
              models={models.data}
              trigger={
                <Button size="sm" variant="outline">
                  <Plus /> New policy
                </Button>
              }
            />
          ) : null
        }
      >
        {policies.data && models.data ? <PolicyList policies={policies.data} models={models.data} isAdmin={isAdmin} /> : <div className="p-4 text-xs text-muted-foreground">Loading…</div>}
      </Section>
      <Section title="How requests are routed">
        <ol className="list-decimal space-y-1 py-3 pr-4 pl-9 text-xs text-muted-foreground">
          <li>
            A <span className="font-mono text-foreground">model</span> matching a routing policy alias uses that policy&apos;s fallback chain.
          </li>
          <li>
            <span className="font-mono text-foreground">provider-slug/model</span> or the <span className="font-mono text-foreground">X-Gateway-Provider</span> header selects one provider explicitly.
          </li>
          <li>Otherwise every enabled provider serving that model name is tried in priority order.</li>
        </ol>
      </Section>
    </div>
  );
}
