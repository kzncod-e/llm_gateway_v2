"use client";

import type { ColumnDef } from "@tanstack/react-table";
import { ArrowLeft, KeyRound, Plus, RotateCcw, Trash2 } from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { toast } from "sonner";

import { DataTable } from "@/components/common/data-table";
import { Field } from "@/components/common/field";
import { PageHeader, Section } from "@/components/common/page-header";
import { EmptyState, ErrorState, LoadingRows } from "@/components/common/states";
import { CircuitBadge, Pill } from "@/components/common/status-badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { usePermissions } from "@/features/auth/api";
import { useResetBreaker } from "@/features/reliability/api";
import { ApiError } from "@/lib/api";
import { formatDateTime, formatPricePerM, formatRelative } from "@/lib/format";
import type { Model, Provider } from "@/lib/types";

import { useCreateModel, useDeleteModel, useDeleteProvider, useModels, useProvider, useUpdateModel, useUpdateProvider } from "./api";
import { PricingDialog } from "./pricing-dialog";

const errMsg = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);

function SettingsForm({ provider }: { provider: Provider }) {
  const update = useUpdateProvider(provider.id);
  const [name, setName] = useState(provider.name);
  const [baseUrl, setBaseUrl] = useState(provider.base_url);
  const [priority, setPriority] = useState(provider.priority);
  const [timeout, setTimeoutMs] = useState(provider.timeout_ms);
  const [key, setKey] = useState("");

  const save = (e: React.FormEvent) => {
    e.preventDefault();
    update.mutate(
      { name, base_url: baseUrl, priority, timeout_ms: timeout, ...(key ? { api_key: key } : {}) },
      {
        onSuccess: () => {
          toast.success(key ? "Settings saved and credential rotated" : "Settings saved");
          setKey("");
        },
        onError: (err) => toast.error(errMsg(err, "Save failed")),
      },
    );
  };

  return (
    <form onSubmit={save} className="space-y-4 p-4">
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Display name" htmlFor="s-name">
          <Input id="s-name" value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field label="Base URL" htmlFor="s-url">
          <Input id="s-url" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} className="font-mono text-xs" />
        </Field>
        <Field label="Priority" htmlFor="s-prio" hint="Lower is tried first">
          <Input id="s-prio" type="number" value={priority} onChange={(e) => setPriority(Number(e.target.value))} />
        </Field>
        <Field label="Timeout (ms)" htmlFor="s-to">
          <Input id="s-to" type="number" value={timeout} onChange={(e) => setTimeoutMs(Number(e.target.value))} />
        </Field>
      </div>
      <Field
        label="Rotate API key"
        htmlFor="s-key"
        hint={provider.has_api_key ? `Current key ends in ${provider.api_key_last4} (version ${provider.key_version}). Leave empty to keep it.` : "No credential stored."}
      >
        <Input id="s-key" type="password" autoComplete="off" value={key} onChange={(e) => setKey(e.target.value)} placeholder="New API key" className="font-mono text-xs" />
      </Field>
      <div className="flex justify-end">
        <Button type="submit" size="sm" disabled={update.isPending}>
          {update.isPending ? "Saving…" : "Save changes"}
        </Button>
      </div>
    </form>
  );
}

function AddModelForm({ providerId }: { providerId: string }) {
  const create = useCreateModel();
  const [name, setName] = useState("");
  const [display, setDisplay] = useState("");
  return (
    <form
      className="flex flex-wrap items-end gap-2 border-t border-border/60 p-4"
      onSubmit={(e) => {
        e.preventDefault();
        if (!name.trim()) return;
        create.mutate(
          { provider_id: providerId, name: name.trim(), display_name: display.trim() || undefined },
          {
            onSuccess: () => {
              toast.success(`Model ${name} added`, { description: "Add pricing so budgeted teams can use it." });
              setName("");
              setDisplay("");
            },
            onError: (err) => toast.error(errMsg(err, "Failed to add model")),
          },
        );
      }}
    >
      <Field label="Upstream model name" htmlFor="m-name">
        <Input id="m-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="gpt-4o" className="w-56 font-mono text-xs" />
      </Field>
      <Field label="Display name" htmlFor="m-display">
        <Input id="m-display" value={display} onChange={(e) => setDisplay(e.target.value)} placeholder="GPT-4o" className="w-48" />
      </Field>
      <Button type="submit" size="sm" variant="outline" disabled={create.isPending}>
        <Plus /> Add model
      </Button>
    </form>
  );
}

export function ProviderDetailView({ id }: { id: string }) {
  const router = useRouter();
  const { isAdmin } = usePermissions();
  const q = useProvider(id);
  const models = useModels(id);
  const update = useUpdateProvider(id);
  const updateModel = useUpdateModel();
  const deleteModel = useDeleteModel();
  const deleteProvider = useDeleteProvider();
  const reset = useResetBreaker();

  if (q.isLoading) return <LoadingRows rows={8} />;
  if (q.error || !q.data) return <ErrorState error={q.error} onRetry={() => q.refetch()} />;
  const { provider: p, circuit } = q.data;

  const modelColumns: ColumnDef<Model, unknown>[] = [
    {
      accessorKey: "name",
      header: "Model",
      cell: ({ row }) => (
        <div>
          <div className="font-mono text-xs text-foreground">{row.original.name}</div>
          <div className="text-[11px] text-muted-foreground">{row.original.display_name || "—"}</div>
        </div>
      ),
    },
    {
      id: "price",
      header: "Price / 1M (in · out)",
      cell: ({ row }) => {
        const cp = row.original.current_price;
        return cp ? (
          <span className="tabular text-xs">
            {formatPricePerM(cp.input_per_mtok_nanos)} · {formatPricePerM(cp.output_per_mtok_nanos)}
          </span>
        ) : (
          <Pill tone="warning">unpriced</Pill>
        );
      },
    },
    {
      accessorKey: "enabled",
      header: "Enabled",
      cell: ({ row }) => (
        <Switch
          checked={row.original.enabled}
          disabled={!isAdmin}
          aria-label={`Toggle ${row.original.name}`}
          onCheckedChange={(v) => updateModel.mutate({ id: row.original.id, enabled: v }, { onError: (e) => toast.error(errMsg(e, "Update failed")) })}
        />
      ),
    },
    {
      id: "actions",
      header: "",
      cell: ({ row }) => (
        <div className="flex justify-end gap-1">
          <PricingDialog
            model={row.original}
            canEdit={isAdmin}
            trigger={
              <Button variant="ghost" size="sm">
                Pricing
              </Button>
            }
          />
          {isAdmin ? (
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={`Delete ${row.original.name}`}
              onClick={() => {
                if (!confirm(`Delete model ${row.original.name}? It is removed from routing policies.`)) return;
                deleteModel.mutate(row.original.id, { onSuccess: () => toast.success("Model deleted"), onError: (e) => toast.error(errMsg(e, "Delete failed")) });
              }}
            >
              <Trash2 />
            </Button>
          ) : null}
        </div>
      ),
    },
  ];

  return (
    <div className="space-y-6">
      <PageHeader
        title={p.name}
        description={
          <span className="font-mono text-xs">
            {p.slug} · {p.type} · created {formatDateTime(p.created_at)}
          </span>
        }
        actions={
          <>
            {isAdmin ? (
              <label className="mr-2 flex items-center gap-2 text-xs text-muted-foreground">
                <Switch checked={p.enabled} onCheckedChange={(v) => update.mutate({ enabled: v }, { onError: (e) => toast.error(errMsg(e, "Update failed")) })} aria-label="Provider enabled" />
                {p.enabled ? "Enabled" : "Disabled"}
              </label>
            ) : null}
            <Button variant="ghost" size="sm" asChild>
              <Link href="/providers">
                <ArrowLeft /> Back
              </Link>
            </Button>
          </>
        }
      />
      <div className="grid gap-4 lg:grid-cols-3">
        <div className="space-y-4 lg:col-span-2">
          {isAdmin ? (
            <Section title="Configuration">
              <SettingsForm key={p.updated_at} provider={p} />
            </Section>
          ) : null}
          <Section title="Models" description="Clients can request these by name or as provider-slug/name">
            <DataTable columns={modelColumns} data={models.data} loading={models.isLoading} error={models.error} empty={<EmptyState title="No models yet" />} />
            {isAdmin ? <AddModelForm providerId={p.id} /> : null}
          </Section>
        </div>
        <div className="space-y-4">
          <Section
            title="Health"
            actions={
              isAdmin && circuit.state !== "closed" ? (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => reset.mutate(p.id, { onSuccess: () => { toast.success("Circuit closed"); q.refetch(); } })}
                >
                  <RotateCcw /> Reset
                </Button>
              ) : null
            }
          >
            <div className="space-y-3 p-4 text-[13px]">
              <CircuitBadge state={circuit.state} />
              <dl className="space-y-1.5 text-xs">
                <div className="flex justify-between">
                  <dt className="text-muted-foreground">Consecutive failures</dt>
                  <dd className="tabular">{circuit.consecutive_failures}</dd>
                </div>
                <div className="flex justify-between">
                  <dt className="text-muted-foreground">Last failure</dt>
                  <dd>{formatRelative(circuit.last_failure_at)}</dd>
                </div>
                {circuit.opened_at ? (
                  <div className="flex justify-between">
                    <dt className="text-muted-foreground">Opened</dt>
                    <dd>{formatRelative(circuit.opened_at)}</dd>
                  </div>
                ) : null}
              </dl>
              {circuit.last_error ? <p className="rounded border border-border bg-muted/50 p-2 font-mono text-[11px] break-words text-muted-foreground">{circuit.last_error}</p> : null}
              <p className="text-[11px] text-muted-foreground">Breaker state is held per gateway instance.</p>
            </div>
          </Section>
          <Section title="Credential">
            <div className="flex items-center gap-2 p-4 text-xs text-muted-foreground">
              <KeyRound className="size-3.5" />
              {p.has_api_key ? (
                <span>
                  Encrypted key ending in <span className="font-mono text-foreground">{p.api_key_last4}</span>, version {p.key_version}
                </span>
              ) : (
                <span className="text-warning">No credential stored</span>
              )}
            </div>
          </Section>
          {isAdmin ? (
            <Section title="Danger zone">
              <div className="p-4">
                <Button
                  variant="destructive"
                  size="sm"
                  onClick={() => {
                    if (!confirm(`Delete provider ${p.name}, its models and prices? Historical request logs are kept.`)) return;
                    deleteProvider.mutate(p.id, {
                      onSuccess: () => {
                        toast.success("Provider deleted");
                        router.push("/providers");
                      },
                      onError: (e) => toast.error(errMsg(e, "Delete failed")),
                    });
                  }}
                >
                  <Trash2 /> Delete provider
                </Button>
              </div>
            </Section>
          ) : null}
        </div>
      </div>
    </div>
  );
}
