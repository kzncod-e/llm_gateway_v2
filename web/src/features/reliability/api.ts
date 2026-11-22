"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/lib/api";
import type { Reliability } from "@/lib/types";

export function useReliability(window: "1h" | "24h") {
  return useQuery({
    queryKey: ["reliability", window],
    queryFn: () => api<Reliability>("/reliability", { query: { window } }),
    refetchInterval: 10_000,
  });
}

export function useResetBreaker() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (providerId: string) => api<void>(`/reliability/breakers/${providerId}/reset`, { method: "POST", body: {} }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["reliability"] });
      qc.invalidateQueries({ queryKey: ["providers"] });
    },
  });
}
