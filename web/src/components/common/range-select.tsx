"use client";

import { useMemo, useState } from "react";

import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

export const RANGES = {
  "24h": { label: "Last 24 hours", hours: 24, interval: "hour" as const },
  "7d": { label: "Last 7 days", hours: 24 * 7, interval: "day" as const },
  "30d": { label: "Last 30 days", hours: 24 * 30, interval: "day" as const },
  "90d": { label: "Last 90 days", hours: 24 * 90, interval: "day" as const },
};
export type RangeKey = keyof typeof RANGES;

export interface TimeRange {
  key: RangeKey;
  from: string;
  to: string;
  interval: "hour" | "day";
}

/** Holds a relative time range; from/to are recomputed when the key changes. */
export function useTimeRange(initial: RangeKey = "7d") {
  const [key, setKey] = useState<RangeKey>(initial);
  const range = useMemo<TimeRange>(() => {
    const to = new Date();
    // Round up to the next minute so query keys are stable within a minute.
    to.setSeconds(0, 0);
    to.setMinutes(to.getMinutes() + 1);
    const from = new Date(to.getTime() - RANGES[key].hours * 3600_000);
    return { key, from: from.toISOString(), to: to.toISOString(), interval: RANGES[key].interval };
  }, [key]);
  return [range, setKey] as const;
}

export function RangeSelect({ value, onChange }: { value: RangeKey; onChange: (k: RangeKey) => void }) {
  return (
    <Select value={value} onValueChange={(v) => onChange(v as RangeKey)}>
      <SelectTrigger size="sm" className="w-40" aria-label="Time range">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {Object.entries(RANGES).map(([k, r]) => (
          <SelectItem key={k} value={k}>
            {r.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
