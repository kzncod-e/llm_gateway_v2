"use client";

import { zodResolver } from "@hookform/resolvers/zod";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { z } from "zod";

import { Field } from "@/components/common/field";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ApiError } from "@/lib/api";
import { formatDateTime, formatPricePerM, usdToNanos } from "@/lib/format";
import type { Model } from "@/lib/types";

import { useAddPrice, usePrices } from "./api";

const usd = z.coerce.number<number>().min(0, "Must be ≥ 0").max(10000, "Too large");
const schema = z.object({
  input: usd,
  cached_input: usd,
  cache_write: usd,
  output: usd,
  effective_from: z.string().optional(),
});
type Values = z.infer<typeof schema>;

export function PricingDialog({ model, canEdit, trigger }: { model: Model; canEdit: boolean; trigger: React.ReactNode }) {
  const [open, setOpen] = useState(false);
  const prices = usePrices(open ? model.id : undefined);
  const add = useAddPrice(model.id);
  const cur = model.current_price;
  const toUsd = (n?: number) => (n === undefined ? 0 : n / 1e9);
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { input: toUsd(cur?.input_per_mtok_nanos), cached_input: toUsd(cur?.cached_input_per_mtok_nanos), cache_write: toUsd(cur?.cache_write_per_mtok_nanos), output: toUsd(cur?.output_per_mtok_nanos), effective_from: "" },
  });
  const e = form.formState.errors;

  const submit = form.handleSubmit((v) =>
    add.mutate(
      {
        input_per_mtok_nanos: usdToNanos(v.input),
        cached_input_per_mtok_nanos: usdToNanos(v.cached_input),
        cache_write_per_mtok_nanos: usdToNanos(v.cache_write),
        output_per_mtok_nanos: usdToNanos(v.output),
        effective_from: v.effective_from ? new Date(v.effective_from).toISOString() : undefined,
      },
      {
        onSuccess: () => toast.success("New price version added"),
        onError: (err) => toast.error(err instanceof ApiError ? err.message : "Failed to add price"),
      },
    ),
  );

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="font-mono text-sm">
            {model.provider_slug}/{model.name}
          </DialogTitle>
          <DialogDescription>Prices are USD per 1M tokens. Versions are append-only, so historical costs stay reproducible.</DialogDescription>
        </DialogHeader>
        <div className="max-h-56 overflow-y-auto rounded-md border border-border">
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead className="label-caps">Effective from</TableHead>
                <TableHead className="label-caps text-right">Input</TableHead>
                <TableHead className="label-caps text-right">Cached</TableHead>
                <TableHead className="label-caps text-right">Cache write</TableHead>
                <TableHead className="label-caps text-right">Output</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {prices.data?.length ? (
                prices.data.map((p) => (
                  <TableRow key={p.id} className="text-xs">
                    <TableCell>
                      {formatDateTime(p.effective_from)}
                      {p.id === cur?.id ? <span className="ml-2 text-[10px] text-success">current</span> : null}
                    </TableCell>
                    <TableCell className="tabular text-right">{formatPricePerM(p.input_per_mtok_nanos)}</TableCell>
                    <TableCell className="tabular text-right">{formatPricePerM(p.cached_input_per_mtok_nanos)}</TableCell>
                    <TableCell className="tabular text-right">{formatPricePerM(p.cache_write_per_mtok_nanos)}</TableCell>
                    <TableCell className="tabular text-right">{formatPricePerM(p.output_per_mtok_nanos)}</TableCell>
                  </TableRow>
                ))
              ) : (
                <TableRow>
                  <TableCell colSpan={5} className="py-6 text-center text-xs text-muted-foreground">
                    {prices.isLoading ? "Loading…" : "No price configured. Teams with a budget cannot use this model until it is priced."}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
        {canEdit ? (
          <form onSubmit={submit} className="space-y-3 border-t border-border pt-4" noValidate>
            <div className="label-caps">Add a price version</div>
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
              <Field label="Input" htmlFor="pr-in" error={e.input?.message}>
                <Input id="pr-in" type="number" step="0.0001" {...form.register("input")} />
              </Field>
              <Field label="Cached input" htmlFor="pr-cin" error={e.cached_input?.message}>
                <Input id="pr-cin" type="number" step="0.0001" {...form.register("cached_input")} />
              </Field>
              <Field label="Cache write" htmlFor="pr-cw" error={e.cache_write?.message}>
                <Input id="pr-cw" type="number" step="0.0001" {...form.register("cache_write")} />
              </Field>
              <Field label="Output" htmlFor="pr-out" error={e.output?.message}>
                <Input id="pr-out" type="number" step="0.0001" {...form.register("output")} />
              </Field>
            </div>
            <div className="flex flex-wrap items-end gap-3">
              <Field label="Effective from (optional, defaults to now)" htmlFor="pr-eff">
                <Input id="pr-eff" type="datetime-local" className="w-60" {...form.register("effective_from")} />
              </Field>
              <Button type="submit" className="ml-auto" disabled={add.isPending}>
                {add.isPending ? "Saving…" : "Add version"}
              </Button>
            </div>
          </form>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}
