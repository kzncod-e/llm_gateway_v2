"use client";

import type { ColumnDef } from "@tanstack/react-table";
import { KeyRound, Plus, TriangleAlert } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { CopyButton } from "@/components/common/copy-button";
import { DataTable } from "@/components/common/data-table";
import { Field } from "@/components/common/field";
import { Section } from "@/components/common/page-header";
import { EmptyState } from "@/components/common/states";
import { Pill } from "@/components/common/status-badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { ApiError } from "@/lib/api";
import { formatDate, formatRelative } from "@/lib/format";
import type { ApiKey } from "@/lib/types";

import { useApiKeys, useCreateApiKey, useRevokeApiKey } from "./api";

function CreateKeyDialog({ teamId }: { teamId: string }) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [days, setDays] = useState("");
  const [created, setCreated] = useState<string | null>(null);
  const create = useCreateApiKey(teamId);

  const close = (o: boolean) => {
    setOpen(o);
    if (!o) {
      setCreated(null); // the raw key is dropped from memory once the dialog closes
      setName("");
      setDays("");
    }
  };

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogTrigger asChild>
        <Button size="sm" variant="outline">
          <Plus /> New key
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-lg">
        {created ? (
          <>
            <DialogHeader>
              <DialogTitle>API key created</DialogTitle>
              <DialogDescription>Copy it now. This is the only time the full key is shown; the gateway stores only a hash.</DialogDescription>
            </DialogHeader>
            <div className="flex items-center gap-2 rounded-md border border-sand-deep/40 bg-muted/60 p-2">
              <code className="min-w-0 flex-1 truncate font-mono text-xs text-sand" data-testid="created-key">
                {created}
              </code>
              <CopyButton value={created} />
            </div>
            <div className="flex items-start gap-2 text-[11px] text-warning">
              <TriangleAlert className="mt-px size-3.5 shrink-0" /> Store the key in a secret manager. Use it as <span className="font-mono">Authorization: Bearer &lt;key&gt;</span>.
            </div>
            <DialogFooter>
              <Button onClick={() => close(false)}>Done</Button>
            </DialogFooter>
          </>
        ) : (
          <form
            className="space-y-4"
            onSubmit={(e) => {
              e.preventDefault();
              const expires = days ? Number(days) : undefined;
              create.mutate(
                { name: name.trim(), expires_in_days: expires },
                { onSuccess: (k) => setCreated(k.key), onError: (err) => toast.error(err instanceof ApiError ? err.message : "Failed to create key") },
              );
            }}
          >
            <DialogHeader>
              <DialogTitle>New API key</DialogTitle>
              <DialogDescription>Keys authenticate applications against the gateway&apos;s /v1 API.</DialogDescription>
            </DialogHeader>
            <Field label="Name" htmlFor="k-name">
              <Input id="k-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="checkout-service" required maxLength={100} />
            </Field>
            <Field label="Expires in days (optional)" htmlFor="k-exp">
              <Input id="k-exp" type="number" min={1} max={3650} value={days} onChange={(e) => setDays(e.target.value)} />
            </Field>
            <DialogFooter>
              <Button type="button" variant="ghost" onClick={() => close(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={create.isPending || !name.trim()}>
                {create.isPending ? "Creating…" : "Create key"}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}

export function ApiKeysSection({ teamId, canDevelop }: { teamId: string; canDevelop: boolean }) {
  const keys = useApiKeys(teamId);
  const revoke = useRevokeApiKey(teamId);
  const [now] = useState(() => Date.now());

  const columns: ColumnDef<ApiKey, unknown>[] = [
    {
      accessorKey: "name",
      header: "Name",
      cell: ({ row }) => (
        <div className="flex items-center gap-2">
          <KeyRound className="size-3.5 text-muted-foreground" />
          <span className="text-foreground">{row.original.name}</span>
        </div>
      ),
    },
    { accessorKey: "prefix", header: "Key", cell: ({ row }) => <span className="font-mono text-[11px] text-muted-foreground">{row.original.prefix}…</span> },
    { accessorKey: "last_used_at", header: "Last used", cell: ({ row }) => <span className="text-muted-foreground">{formatRelative(row.original.last_used_at, now)}</span> },
    { accessorKey: "created_at", header: "Created", cell: ({ row }) => <span className="text-muted-foreground">{formatDate(row.original.created_at)}</span> },
    {
      id: "status",
      header: "Status",
      cell: ({ row }) => {
        const k = row.original;
        if (k.revoked_at) return <Pill tone="danger">revoked</Pill>;
        if (k.expires_at && new Date(k.expires_at).getTime() < now) return <Pill tone="warning">expired</Pill>;
        return <Pill tone="success">{k.expires_at ? `expires ${formatDate(k.expires_at)}` : "active"}</Pill>;
      },
    },
    {
      id: "actions",
      header: "",
      cell: ({ row }) =>
        canDevelop && !row.original.revoked_at ? (
          <div className="flex justify-end">
            <Button
              variant="ghost"
              size="sm"
              className="text-danger hover:text-danger"
              onClick={() => {
                if (!confirm(`Revoke key "${row.original.name}"? Applications using it will get 401 immediately.`)) return;
                revoke.mutate(row.original.id, { onSuccess: () => toast.success("Key revoked"), onError: (e) => toast.error(e instanceof ApiError ? e.message : "Revoke failed") });
              }}
            >
              Revoke
            </Button>
          </div>
        ) : null,
    },
  ];

  return (
    <Section title="API keys" description="Only the prefix is stored in readable form" actions={canDevelop ? <CreateKeyDialog teamId={teamId} /> : null}>
      <DataTable columns={columns} data={keys.data} loading={keys.isLoading} error={keys.error} onRetry={() => keys.refetch()} empty={<EmptyState title="No API keys" description="Create a key to call the gateway." />} />
    </Section>
  );
}
