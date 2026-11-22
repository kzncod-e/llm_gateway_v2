import { cn } from "@/lib/utils";
import type { CircuitState, CostStatus, RequestStatus } from "@/lib/types";

type Tone = "success" | "warning" | "danger" | "info" | "neutral";

const toneClass: Record<Tone, string> = {
  success: "border-success/30 bg-success/10 text-success",
  warning: "border-warning/30 bg-warning/10 text-warning",
  danger: "border-danger/30 bg-danger/10 text-danger",
  info: "border-info/30 bg-info/10 text-info",
  neutral: "border-border bg-muted text-muted-foreground",
};

const dotClass: Record<Tone, string> = {
  success: "bg-success",
  warning: "bg-warning",
  danger: "bg-danger",
  info: "bg-info",
  neutral: "bg-muted-foreground",
};

export function Pill({ tone = "neutral", children, className }: { tone?: Tone; children: React.ReactNode; className?: string }) {
  return (
    <span className={cn("inline-flex h-5 items-center gap-1.5 rounded border px-1.5 text-[11px] font-medium whitespace-nowrap", toneClass[tone], className)}>
      <span className={cn("size-1.5 rounded-full", dotClass[tone])} />
      {children}
    </span>
  );
}

const requestTone: Record<RequestStatus, Tone> = {
  success: "success",
  error: "danger",
  interrupted: "warning",
  client_cancelled: "neutral",
  rejected: "info",
};

export function RequestStatusBadge({ status }: { status: RequestStatus | string }) {
  return <Pill tone={requestTone[status as RequestStatus] ?? "neutral"}>{status.replace("_", " ")}</Pill>;
}

const circuitTone: Record<CircuitState, Tone> = { closed: "success", half_open: "warning", open: "danger" };
const circuitLabel: Record<CircuitState, string> = { closed: "Healthy", half_open: "Probing", open: "Circuit open" };

export function CircuitBadge({ state }: { state: CircuitState }) {
  return <Pill tone={circuitTone[state]}>{circuitLabel[state]}</Pill>;
}

export function CostStatusHint({ status }: { status: CostStatus }) {
  if (status === "estimated") return <span className="text-[10px] text-warning" title="Usage was not reported; charged at the reserved estimate">est.</span>;
  if (status === "unknown") return <span className="text-[10px] text-muted-foreground" title="No price configured for this model">unpriced</span>;
  return null;
}
