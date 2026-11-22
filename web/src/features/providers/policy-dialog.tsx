"use client";

import { ArrowDown, ArrowUp, Plus, X } from "lucide-react";
import { useState, type ReactNode } from "react";
import { toast } from "sonner";

import { Field } from "@/components/common/field";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { ApiError } from "@/lib/api";
import type { Model, RoutingPolicy } from "@/lib/types";

import { useSavePolicy } from "./api";

export function PolicyDialog({ policy, models, trigger }: { policy?: RoutingPolicy; models: Model[]; trigger: ReactNode }) {
  const [open, setOpen] = useState(false);
  const save = useSavePolicy();
  const [alias, setAlias] = useState(policy?.alias ?? "");
  const [description, setDescription] = useState(policy?.description ?? "");
  const [targets, setTargets] = useState<string[]>(policy?.target_model_ids ?? []);
  const [attempts, setAttempts] = useState(policy?.max_attempts_per_target ?? 2);
  const [baseMs, setBaseMs] = useState(policy?.retry_base_ms ?? 250);
  const [maxMs, setMaxMs] = useState(policy?.retry_max_ms ?? 2000);
  const [enabled, setEnabled] = useState(policy?.enabled ?? true);
  const [error, setError] = useState<string | null>(null);

  const byId = new Map(models.map((m) => [m.id, m]));
  const available = models.filter((m) => !targets.includes(m.id));
  const move = (i: number, d: -1 | 1) => setTargets((t) => {
    const n = [...t];
    [n[i], n[i + d]] = [n[i + d], n[i]];
    return n;
  });

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    if (!/^[^\s/?#]{1,200}$/.test(alias.trim())) return setError("Alias is required and may not contain spaces or slashes.");
    if (!targets.length) return setError("Add at least one target model.");
    save.mutate(
      { id: policy?.id, alias: alias.trim(), description, target_model_ids: targets, max_attempts_per_target: attempts, retry_base_ms: baseMs, retry_max_ms: maxMs, enabled },
      {
        onSuccess: () => {
          toast.success(`Routing policy "${alias}" saved`);
          setOpen(false);
        },
        onError: (err) => setError(err instanceof ApiError ? err.message : "Failed to save policy"),
      },
    );
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{policy ? "Edit routing policy" : "New routing policy"}</DialogTitle>
          <DialogDescription>Clients request the alias as the model name. Targets are tried in order; the next one is used when a target fails or its circuit is open.</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Alias" htmlFor="pol-alias">
              <Input id="pol-alias" value={alias} onChange={(e) => setAlias(e.target.value)} placeholder="smart" className="font-mono text-xs" />
            </Field>
            <Field label="Description" htmlFor="pol-desc">
              <Input id="pol-desc" value={description} onChange={(e) => setDescription(e.target.value)} />
            </Field>
          </div>
          <div className="space-y-2">
            <div className="label-caps">Fallback chain</div>
            <ol className="space-y-1.5">
              {targets.map((id, i) => {
                const m = byId.get(id);
                return (
                  <li key={id} className="flex items-center gap-2 rounded-md border border-border bg-muted/40 px-2 py-1.5 text-xs">
                    <span className="tabular w-10 text-muted-foreground">{i === 0 ? "primary" : `#${i + 1}`}</span>
                    <span className="min-w-0 flex-1 truncate font-mono">{m ? `${m.provider_slug}/${m.name}` : id}</span>
                    {m && !m.current_price ? <span className="text-[10px] text-warning">unpriced</span> : null}
                    <Button type="button" variant="ghost" size="icon-xs" disabled={i === 0} onClick={() => move(i, -1)} aria-label="Move up">
                      <ArrowUp />
                    </Button>
                    <Button type="button" variant="ghost" size="icon-xs" disabled={i === targets.length - 1} onClick={() => move(i, 1)} aria-label="Move down">
                      <ArrowDown />
                    </Button>
                    <Button type="button" variant="ghost" size="icon-xs" onClick={() => setTargets((t) => t.filter((x) => x !== id))} aria-label="Remove">
                      <X />
                    </Button>
                  </li>
                );
              })}
            </ol>
            {available.length ? (
              <Select value="" onValueChange={(v) => setTargets((t) => [...t, v])}>
                <SelectTrigger size="sm" className="w-full text-xs" aria-label="Add target">
                  <span className="flex items-center gap-1.5 text-muted-foreground">
                    <Plus className="size-3.5" /> Add target model
                  </span>
                </SelectTrigger>
                <SelectContent>
                  {available.map((m) => (
                    <SelectItem key={m.id} value={m.id} className="font-mono text-xs">
                      {m.provider_slug}/{m.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ) : null}
          </div>
          <div className="grid grid-cols-3 gap-3">
            <Field label="Attempts / target" htmlFor="pol-att">
              <Input id="pol-att" type="number" min={1} max={5} value={attempts} onChange={(e) => setAttempts(Number(e.target.value))} />
            </Field>
            <Field label="Backoff base (ms)" htmlFor="pol-base">
              <Input id="pol-base" type="number" min={0} value={baseMs} onChange={(e) => setBaseMs(Number(e.target.value))} />
            </Field>
            <Field label="Backoff max (ms)" htmlFor="pol-max">
              <Input id="pol-max" type="number" min={0} value={maxMs} onChange={(e) => setMaxMs(Number(e.target.value))} />
            </Field>
          </div>
          <label className="flex items-center gap-2 text-xs text-muted-foreground">
            <Switch checked={enabled} onCheckedChange={setEnabled} /> Enabled
          </label>
          {error ? <p className="text-xs text-danger" role="alert">{error}</p> : null}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={save.isPending}>
              {save.isPending ? "Saving…" : "Save policy"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
