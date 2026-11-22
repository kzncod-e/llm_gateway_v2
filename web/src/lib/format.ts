// Formatting helpers. Money values are integer nano-USD.

export const NANOS_PER_USD = 1_000_000_000;

export function usdToNanos(usd: number): number {
  return Math.round(usd * NANOS_PER_USD);
}

export function nanosToUsd(nanos: number): number {
  return nanos / NANOS_PER_USD;
}

/** Formats nano-USD with precision suited to the magnitude. */
export function formatMoney(nanos: number | null | undefined, opts: { compact?: boolean } = {}): string {
  if (nanos === null || nanos === undefined) return "—";
  const usd = nanos / NANOS_PER_USD;
  if (usd === 0) return "$0.00";
  const abs = Math.abs(usd);
  if (opts.compact && abs >= 1000) {
    return new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", notation: "compact", maximumFractionDigits: 1 }).format(usd);
  }
  const digits = abs >= 1 ? 2 : abs >= 0.01 ? 4 : 6;
  return new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", minimumFractionDigits: 2, maximumFractionDigits: digits }).format(usd);
}

/** Price per 1M tokens, e.g. "$2.50". */
export function formatPricePerM(nanos: number): string {
  return new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", minimumFractionDigits: 2, maximumFractionDigits: 4 }).format(nanos / NANOS_PER_USD);
}

export function formatNumber(n: number | null | undefined, compact = false): string {
  if (n === null || n === undefined) return "—";
  return new Intl.NumberFormat("en-US", compact ? { notation: "compact", maximumFractionDigits: 1 } : {}).format(n);
}

export function formatPercent(ratio: number | null | undefined, digits = 1): string {
  if (ratio === null || ratio === undefined || Number.isNaN(ratio)) return "—";
  return `${(ratio * 100).toFixed(digits)}%`;
}

export function formatDuration(ms: number | null | undefined): string {
  if (ms === null || ms === undefined) return "—";
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(ms < 10_000 ? 2 : 1)} s`;
  const m = Math.floor(ms / 60_000);
  const s = Math.round((ms % 60_000) / 1000);
  return `${m}m ${s}s`;
}

export function formatDateTime(iso: string | null | undefined): string {
  if (!iso) return "—";
  return new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false }).format(new Date(iso));
}

export function formatDate(iso: string | null | undefined): string {
  if (!iso) return "—";
  return new Intl.DateTimeFormat("en-US", { year: "numeric", month: "short", day: "numeric" }).format(new Date(iso));
}

export function formatRelative(iso: string | null | undefined, now: number = Date.now()): string {
  if (!iso) return "never";
  const diff = Math.round((now - new Date(iso).getTime()) / 1000);
  if (diff < 0) return "just now";
  if (diff < 60) return `${diff}s ago`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  return `${Math.floor(diff / 86400)}d ago`;
}

export function shortId(id: string): string {
  return id.slice(-8);
}
