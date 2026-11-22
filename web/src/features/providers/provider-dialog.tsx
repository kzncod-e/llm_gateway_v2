"use client";

import { zodResolver } from "@hookform/resolvers/zod";
import { Plus } from "lucide-react";
import { useState } from "react";
import { Controller, useForm } from "react-hook-form";
import { toast } from "sonner";
import { z } from "zod";

import { Field } from "@/components/common/field";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { ApiError } from "@/lib/api";

import { useCreateProvider } from "./api";

const DEFAULT_URLS = { openai: "https://api.openai.com", anthropic: "https://api.anthropic.com", gemini: "https://generativelanguage.googleapis.com" };

export const providerSchema = z.object({
  name: z.string().trim().min(1, "Name is required").max(100),
  type: z.enum(["openai", "anthropic", "gemini"]),
  base_url: z.url("Enter an http(s) URL").refine((v) => /^https?:\/\//.test(v), "Must start with http:// or https://"),
  api_key: z.string().trim().min(8, "API key must be at least 8 characters").max(500),
  priority: z.coerce.number<number>().int().min(0).max(10000),
  timeout_ms: z.coerce.number<number>().int().min(1000).max(600000),
});
type Values = z.infer<typeof providerSchema>;

export function AddProviderDialog() {
  const [open, setOpen] = useState(false);
  const create = useCreateProvider();
  const form = useForm<Values>({
    resolver: zodResolver(providerSchema),
    defaultValues: { name: "", type: "openai", base_url: DEFAULT_URLS.openai, api_key: "", priority: 100, timeout_ms: 60000 },
  });
  const errors = form.formState.errors;

  const submit = form.handleSubmit((v) =>
    create.mutate(v, {
      onSuccess: (p) => {
        toast.success(`Provider ${p.name} added`, { description: "Add models and pricing on the provider page." });
        setOpen(false);
        form.reset();
      },
      onError: (e) => toast.error(e instanceof ApiError ? e.message : "Failed to add provider"),
    }),
  );

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus /> Add provider
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Add provider</DialogTitle>
          <DialogDescription>The API key is encrypted at rest (AES-256-GCM) and is never shown again.</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4" noValidate>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Name" htmlFor="p-name" error={errors.name?.message}>
              <Input id="p-name" placeholder="OpenAI production" {...form.register("name")} />
            </Field>
            <Field label="Type" error={errors.type?.message}>
              <Controller
                control={form.control}
                name="type"
                render={({ field }) => (
                  <Select
                    value={field.value}
                    onValueChange={(v) => {
                      field.onChange(v);
                      form.setValue("base_url", DEFAULT_URLS[v as keyof typeof DEFAULT_URLS]);
                    }}
                  >
                    <SelectTrigger className="w-full" aria-label="Provider type">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="openai">OpenAI</SelectItem>
                      <SelectItem value="anthropic">Anthropic</SelectItem>
                      <SelectItem value="gemini">Google Gemini</SelectItem>
                    </SelectContent>
                  </Select>
                )}
              />
            </Field>
          </div>
          <Field label="Base URL" htmlFor="p-url" error={errors.base_url?.message}>
            <Input id="p-url" className="font-mono text-xs" {...form.register("base_url")} />
          </Field>
          <Field label="API key" htmlFor="p-key" error={errors.api_key?.message}>
            <Input id="p-key" type="password" autoComplete="off" className="font-mono text-xs" {...form.register("api_key")} />
          </Field>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Priority" htmlFor="p-prio" error={errors.priority?.message} hint="Lower is tried first for model-based routing">
              <Input id="p-prio" type="number" {...form.register("priority")} />
            </Field>
            <Field label="Timeout (ms)" htmlFor="p-timeout" error={errors.timeout_ms?.message} hint="Per attempt / time to first chunk">
              <Input id="p-timeout" type="number" {...form.register("timeout_ms")} />
            </Field>
          </div>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending ? "Adding…" : "Add provider"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
