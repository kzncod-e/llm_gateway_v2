"use client";

import { CornerDownLeft, Eraser, Loader2, Square } from "lucide-react";
import Link from "next/link";
import { useMemo, useRef, useState } from "react";

import { Field } from "@/components/common/field";
import { PageHeader, Section } from "@/components/common/page-header";
import { EmptyState } from "@/components/common/states";
import { Pill } from "@/components/common/status-badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { useMe, usePermissions } from "@/features/auth/api";
import { useModels, usePolicies } from "@/features/providers/api";
import { ApiError } from "@/lib/api";
import { formatDuration, formatMoney, formatNumber } from "@/lib/format";
import { cn } from "@/lib/utils";

import { runChat, type ChatMessage, type ChatResult } from "./stream";

function Stat({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-3 py-1.5 text-xs">
      <span className="text-muted-foreground">{label}</span>
      <span className="tabular min-w-0 truncate text-right text-foreground">{value}</span>
    </div>
  );
}

export function PlaygroundView() {
  const me = useMe();
  const { canDevelop } = usePermissions();
  const models = useModels();
  const policies = usePolicies();
  const teams = useMemo(() => (me.data?.teams ?? []).filter((t) => !t.archived_at && canDevelop(t.id)), [me.data, canDevelop]);

  const [teamId, setTeamId] = useState<string>("");
  const [model, setModel] = useState<string>("");
  const [stream, setStream] = useState(true);
  const [temperature, setTemperature] = useState("0.7");
  const [maxTokens, setMaxTokens] = useState("512");
  const [system, setSystem] = useState("You are a concise assistant.");
  const [history, setHistory] = useState<ChatMessage[]>([]);
  const [input, setInput] = useState("Explain microservices architecture in three sentences.");
  const [output, setOutput] = useState("");
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<ChatResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const abort = useRef<AbortController | null>(null);

  const effectiveTeam = teamId || teams[0]?.id || "";
  const enabledModels = (models.data ?? []).filter((m) => m.enabled);
  const aliases = (policies.data ?? []).filter((p) => p.enabled);
  const effectiveModel = model || aliases[0]?.alias || enabledModels[0]?.name || "";

  const send = async () => {
    if (!input.trim() || !effectiveTeam || !effectiveModel) return;
    const userMsg: ChatMessage = { role: "user", content: input.trim() };
    const messages: ChatMessage[] = [...(system.trim() ? [{ role: "system" as const, content: system.trim() }] : []), ...history, userMsg];
    setRunning(true);
    setError(null);
    setResult(null);
    setOutput("");
    const ctrl = new AbortController();
    abort.current = ctrl;
    try {
      const res = await runChat(
        effectiveTeam,
        {
          model: effectiveModel,
          messages,
          stream,
          temperature: temperature === "" ? undefined : Number(temperature),
          max_tokens: maxTokens === "" ? undefined : Number(maxTokens),
        },
        (d) => setOutput((o) => o + d),
        ctrl.signal,
      );
      setResult(res);
      setHistory((h) => [...h, userMsg, { role: "assistant", content: res.content }]);
      setInput("");
    } catch (e) {
      if ((e as Error).name === "AbortError") setError("Request cancelled.");
      else setError(e instanceof ApiError ? `${e.code}: ${e.message}` : (e as Error).message);
    } finally {
      setRunning(false);
      abort.current = null;
    }
  };

  if (me.data && !teams.length) {
    return (
      <div className="space-y-6">
        <PageHeader title="Playground" />
        <Section title="No team available">
          <EmptyState title="You need developer access to a team" description="The playground runs requests on behalf of a team so its budget and rate limits apply." />
        </Section>
      </div>
    );
  }

  const usage = result?.usage;

  return (
    <div className="space-y-6">
      <PageHeader title="Playground" description="Runs through the real gateway engine: routing, fallback, budgets and rate limits apply, and requests appear in the logs." />
      <div className="grid gap-4 xl:grid-cols-[280px_minmax(0,1fr)_280px]">
        <Section title="Configuration">
          <div className="space-y-4 p-4">
            <Field label="Team">
              <Select value={effectiveTeam} onValueChange={setTeamId}>
                <SelectTrigger className="w-full" aria-label="Team">
                  <SelectValue placeholder="Select team" />
                </SelectTrigger>
                <SelectContent>
                  {teams.map((t) => (
                    <SelectItem key={t.id} value={t.id}>
                      {t.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field label="Model" hint="Aliases use their fallback chain">
              <Select value={effectiveModel} onValueChange={setModel}>
                <SelectTrigger className="w-full font-mono text-xs" aria-label="Model">
                  <SelectValue placeholder="Select model" />
                </SelectTrigger>
                <SelectContent>
                  {aliases.length ? (
                    <SelectGroup>
                      <SelectLabel>Routing policies</SelectLabel>
                      {aliases.map((p) => (
                        <SelectItem key={p.id} value={p.alias} className="font-mono text-xs">
                          {p.alias}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  ) : null}
                  <SelectGroup>
                    <SelectLabel>Provider models</SelectLabel>
                    {enabledModels.map((m) => (
                      <SelectItem key={m.id} value={`${m.provider_slug}/${m.name}`} className="font-mono text-xs">
                        {m.provider_slug}/{m.name}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </Field>
            <label className="flex items-center justify-between text-xs text-muted-foreground">
              Stream (SSE)
              <Switch checked={stream} onCheckedChange={setStream} aria-label="Stream responses" />
            </label>
            <div className="grid grid-cols-2 gap-3">
              <Field label="Temperature" htmlFor="pg-temp">
                <Input id="pg-temp" type="number" step="0.1" min={0} max={2} value={temperature} onChange={(e) => setTemperature(e.target.value)} />
              </Field>
              <Field label="Max tokens" htmlFor="pg-max">
                <Input id="pg-max" type="number" min={1} value={maxTokens} onChange={(e) => setMaxTokens(e.target.value)} />
              </Field>
            </div>
            <Field label="System prompt" htmlFor="pg-system">
              <Textarea id="pg-system" rows={4} value={system} onChange={(e) => setSystem(e.target.value)} className="text-xs" />
            </Field>
          </div>
        </Section>

        <Section
          title="Conversation"
          actions={
            <Button variant="ghost" size="sm" disabled={running || (!history.length && !output)} onClick={() => { setHistory([]); setOutput(""); setResult(null); setError(null); }}>
              <Eraser /> Clear
            </Button>
          }
        >
          <div className="flex min-h-[420px] flex-col">
            <div className="flex-1 space-y-3 overflow-y-auto p-4" aria-live="polite">
              {history.map((m, i) => (
                <div key={i} className={cn("rounded-md border px-3 py-2 text-[13px] leading-relaxed whitespace-pre-wrap", m.role === "user" ? "ml-10 border-border bg-muted/40" : "mr-10 border-border/60 bg-transparent")}>
                  <div className="label-caps mb-1">{m.role}</div>
                  {m.content}
                </div>
              ))}
              {running || (output && !result) ? (
                <div className="mr-10 rounded-md border border-sand-deep/30 px-3 py-2 text-[13px] leading-relaxed whitespace-pre-wrap" data-testid="streaming-output">
                  <div className="label-caps mb-1">assistant</div>
                  {output}
                  {running ? <span className="ml-0.5 inline-block h-3.5 w-1.5 animate-pulse bg-sand align-middle" /> : null}
                </div>
              ) : null}
              {!history.length && !running && !output ? <EmptyState title="Send a message to start" description="Responses show token usage and estimated cost." /> : null}
              {result?.interrupted ? <p className="text-xs text-warning">Stream interrupted: {result.interrupted}</p> : null}
              {error ? (
                <p className="text-xs text-danger" role="alert">
                  {error}
                </p>
              ) : null}
            </div>
            <form
              className="flex items-end gap-2 border-t border-border/60 p-3"
              onSubmit={(e) => {
                e.preventDefault();
                void send();
              }}
            >
              <Textarea
                value={input}
                onChange={(e) => setInput(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                    e.preventDefault();
                    void send();
                  }
                }}
                rows={2}
                placeholder="Message (⌘/Ctrl + Enter to send)"
                className="min-h-0 resize-none text-[13px]"
                aria-label="Message"
              />
              {running ? (
                <Button type="button" variant="outline" onClick={() => abort.current?.abort()}>
                  <Square /> Stop
                </Button>
              ) : (
                <Button type="submit" disabled={!input.trim() || !effectiveModel}>
                  <CornerDownLeft /> Send
                </Button>
              )}
            </form>
          </div>
        </Section>

        <Section title="Inspector">
          <div className="divide-y divide-border/50 px-4 py-2">
            {running ? (
              <p className="flex items-center gap-2 py-3 text-xs text-muted-foreground">
                <Loader2 className="size-3.5 animate-spin" /> Waiting for provider…
              </p>
            ) : null}
            <div className="py-2">
              <div className="label-caps mb-1">Routing</div>
              <Stat label="Provider" value={result?.meta.provider ?? "—"} />
              <Stat label="Model" value={<span className="font-mono">{result?.meta.model ?? "—"}</span>} />
              <Stat label="Attempts" value={result?.meta.attempts ? (Number(result.meta.attempts) > 1 ? <Pill tone="warning">{result.meta.attempts} (fallback)</Pill> : result.meta.attempts) : "—"} />
              <Stat label="Finish reason" value={result?.finishReason ?? "—"} />
            </div>
            <div className="py-2">
              <div className="label-caps mb-1">Latency</div>
              <Stat label="First token" value={formatDuration(result?.ttfbMs ?? null)} />
              <Stat label="Total" value={result ? formatDuration(result.totalMs) : "—"} />
            </div>
            <div className="py-2">
              <div className="label-caps mb-1">Usage</div>
              <Stat label="Prompt tokens" value={formatNumber(usage?.prompt_tokens)} />
              <Stat label="Completion tokens" value={formatNumber(usage?.completion_tokens)} />
              <Stat label="Cached input" value={formatNumber(usage?.prompt_tokens_details?.cached_tokens)} />
              <Stat label="Total tokens" value={formatNumber(usage?.total_tokens)} />
              <Stat label="Estimated cost" value={usage ? formatMoney(usage.cost_nanos ?? null) : result ? <span className="text-warning">unknown</span> : "—"} />
            </div>
            {result?.meta.requestId ? (
              <div className="py-3">
                <Button variant="outline" size="sm" className="w-full" asChild>
                  <Link href={`/requests/${result.meta.requestId}`}>View request log</Link>
                </Button>
              </div>
            ) : null}
          </div>
        </Section>
      </div>
    </div>
  );
}
