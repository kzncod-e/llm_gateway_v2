export function bucketLabel(iso: string, interval: "hour" | "day"): string {
  const d = new Date(iso);
  return interval === "hour"
    ? new Intl.DateTimeFormat("en-US", { hour: "2-digit", minute: "2-digit", hour12: false }).format(d)
    : new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric" }).format(d);
}
