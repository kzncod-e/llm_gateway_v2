"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/lib/api";
import type { ApiKey, BudgetStatus, CreatedApiKey, ListResponse, Team, TeamMember, TeamRole, User } from "@/lib/types";

export const teamKeys = {
  all: ["teams"] as const,
  detail: (id: string) => ["teams", id] as const,
  members: (id: string) => ["teams", id, "members"] as const,
  budget: (id: string) => ["teams", id, "budget"] as const,
  keys: (id: string) => ["teams", id, "keys"] as const,
};

export function useTeams() {
  return useQuery({ queryKey: teamKeys.all, queryFn: () => api<ListResponse<Team>>("/teams").then((r) => r.data) });
}

export function useTeam(id: string) {
  return useQuery({ queryKey: teamKeys.detail(id), queryFn: () => api<{ team: Team; role: TeamRole }>(`/teams/${id}`) });
}

export interface TeamInput {
  name?: string;
  description?: string;
  rpm_limit?: number;
  concurrency_limit?: number;
  clear_rpm_limit?: boolean;
  clear_concurrency_limit?: boolean;
  archived?: boolean;
}

export function useCreateTeam() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: TeamInput) => api<Team>("/teams", { method: "POST", body }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: teamKeys.all });
      qc.invalidateQueries({ queryKey: ["me"] });
    },
  });
}

export function useUpdateTeam(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: TeamInput) => api<Team>(`/teams/${id}`, { method: "PATCH", body }),
    onSuccess: () => qc.invalidateQueries({ queryKey: teamKeys.all }),
  });
}

export function useMembers(id: string) {
  return useQuery({ queryKey: teamKeys.members(id), queryFn: () => api<ListResponse<TeamMember>>(`/teams/${id}/members`).then((r) => r.data) });
}

export function useAddMember(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { email: string; role: TeamRole }) => api<void>(`/teams/${id}/members`, { method: "POST", body }),
    onSuccess: () => qc.invalidateQueries({ queryKey: teamKeys.members(id) }),
  });
}

export function useRemoveMember(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (userId: string) => api<void>(`/teams/${id}/members/${userId}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: teamKeys.members(id) }),
  });
}

export function useBudget(id: string) {
  return useQuery({ queryKey: teamKeys.budget(id), queryFn: () => api<BudgetStatus>(`/teams/${id}/budget`), refetchInterval: 30_000 });
}

export function useSetBudget(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { monthly_limit_nanos: number | null; daily_limit_nanos: number | null; warn_threshold_pct: number }) =>
      api<BudgetStatus>(`/teams/${id}/budget`, { method: "PUT", body }),
    onSuccess: (data) => qc.setQueryData(teamKeys.budget(id), data),
  });
}

export function useApiKeys(id: string) {
  return useQuery({ queryKey: teamKeys.keys(id), queryFn: () => api<ListResponse<ApiKey>>(`/teams/${id}/api-keys`).then((r) => r.data) });
}

export function useCreateApiKey(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { name: string; expires_in_days?: number }) => api<CreatedApiKey>(`/teams/${id}/api-keys`, { method: "POST", body }),
    onSuccess: () => qc.invalidateQueries({ queryKey: teamKeys.keys(id) }),
  });
}

export function useRevokeApiKey(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (keyId: string) => api<void>(`/teams/${id}/api-keys/${keyId}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: teamKeys.keys(id) }),
  });
}

export function useUsers(enabled = true) {
  return useQuery({ queryKey: ["users"], queryFn: () => api<ListResponse<User>>("/users").then((r) => r.data), enabled });
}

export function useCreateUser() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { email: string; name: string; password: string; role: "admin" | "member" }) => api<User>("/users", { method: "POST", body }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["users"] }),
  });
}

export function useUpdateUser() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: string; role?: "admin" | "member"; disabled?: boolean; name?: string }) => api<void>(`/users/${id}`, { method: "PATCH", body }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["users"] }),
  });
}
