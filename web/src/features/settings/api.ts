"use client";

import { useQuery } from "@tanstack/react-query";

import { api } from "@/lib/api";
import type { AuditLog, GatewaySettings, ListResponse } from "@/lib/types";

export function useGatewaySettings() {
  return useQuery({ queryKey: ["settings"], queryFn: () => api<GatewaySettings>("/settings"), staleTime: 300_000 });
}

export function useAuditLogs(enabled: boolean) {
  return useQuery({ queryKey: ["audit"], queryFn: () => api<ListResponse<AuditLog>>("/audit-logs", { query: { limit: 100 } }).then((r) => r.data), enabled });
}
