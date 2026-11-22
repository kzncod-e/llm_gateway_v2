"use client";

import { keepPreviousData, useQuery } from "@tanstack/react-query";

import type { TimeRange } from "@/components/common/range-select";
import { api } from "@/lib/api";
import type { BreakdownRow, Summary, TimePoint } from "@/lib/types";

export function useSummary(range: TimeRange, teamId?: string) {
  return useQuery({
    queryKey: ["analytics", "summary", range.from, range.to, teamId],
    queryFn: () => api<{ summary: Summary }>("/analytics/summary", { query: { from: range.from, to: range.to, team_id: teamId } }).then((r) => r.summary),
    placeholderData: keepPreviousData,
    refetchInterval: 30_000,
  });
}

export function useTimeseries(range: TimeRange, teamId?: string) {
  return useQuery({
    queryKey: ["analytics", "timeseries", range.from, range.to, range.interval, teamId],
    queryFn: () =>
      api<{ data: TimePoint[] }>("/analytics/timeseries", { query: { from: range.from, to: range.to, interval: range.interval, team_id: teamId } }).then((r) => r.data),
    placeholderData: keepPreviousData,
    refetchInterval: 30_000,
  });
}

export function useBreakdown(range: TimeRange, by: "provider" | "model" | "team", teamId?: string) {
  return useQuery({
    queryKey: ["analytics", "breakdown", by, range.from, range.to, teamId],
    queryFn: () => api<{ data: BreakdownRow[] }>("/analytics/breakdown", { query: { from: range.from, to: range.to, by, team_id: teamId } }).then((r) => r.data),
    placeholderData: keepPreviousData,
  });
}
