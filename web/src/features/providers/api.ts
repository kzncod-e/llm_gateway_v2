"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/lib/api";
import type { Circuit, ListResponse, Model, ModelPrice, Provider, ProviderType, RoutingPolicy } from "@/lib/types";

export const providerKeys = {
  all: ["providers"] as const,
  detail: (id: string) => ["providers", id] as const,
  models: (providerId?: string) => ["models", providerId ?? "all"] as const,
  prices: (modelId: string) => ["prices", modelId] as const,
  policies: ["routing-policies"] as const,
};

export function useProviders() {
  return useQuery({ queryKey: providerKeys.all, queryFn: () => api<ListResponse<Provider>>("/providers").then((r) => r.data), refetchInterval: 15_000 });
}

export function useProvider(id: string) {
  return useQuery({ queryKey: providerKeys.detail(id), queryFn: () => api<{ provider: Provider; circuit: Circuit }>(`/providers/${id}`), refetchInterval: 10_000 });
}

export interface ProviderInput {
  name?: string;
  slug?: string;
  type?: ProviderType;
  base_url?: string;
  api_key?: string;
  enabled?: boolean;
  priority?: number;
  timeout_ms?: number;
}

function useInvalidateAll() {
  const qc = useQueryClient();
  return () => {
    qc.invalidateQueries({ queryKey: providerKeys.all });
    qc.invalidateQueries({ queryKey: ["models"] });
    qc.invalidateQueries({ queryKey: providerKeys.policies });
  };
}

export function useCreateProvider() {
  const inv = useInvalidateAll();
  return useMutation({ mutationFn: (body: ProviderInput) => api<Provider>("/providers", { method: "POST", body }), onSuccess: inv });
}

export function useUpdateProvider(id: string) {
  const inv = useInvalidateAll();
  return useMutation({ mutationFn: (body: ProviderInput) => api<Provider>(`/providers/${id}`, { method: "PATCH", body }), onSuccess: inv });
}

export function useDeleteProvider() {
  const inv = useInvalidateAll();
  return useMutation({ mutationFn: (id: string) => api<void>(`/providers/${id}`, { method: "DELETE" }), onSuccess: inv });
}

export function useModels(providerId?: string) {
  return useQuery({
    queryKey: providerKeys.models(providerId),
    queryFn: () => api<ListResponse<Model>>("/models", { query: { provider_id: providerId } }).then((r) => r.data),
  });
}

export function useCreateModel() {
  const inv = useInvalidateAll();
  return useMutation({
    mutationFn: (body: { provider_id: string; name: string; display_name?: string }) => api<Model>("/models", { method: "POST", body }),
    onSuccess: inv,
  });
}

export function useUpdateModel() {
  const inv = useInvalidateAll();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: string; enabled?: boolean; display_name?: string }) => api<Model>(`/models/${id}`, { method: "PATCH", body }),
    onSuccess: inv,
  });
}

export function useDeleteModel() {
  const inv = useInvalidateAll();
  return useMutation({ mutationFn: (id: string) => api<void>(`/models/${id}`, { method: "DELETE" }), onSuccess: inv });
}

export function usePrices(modelId: string | undefined) {
  return useQuery({
    queryKey: providerKeys.prices(modelId ?? ""),
    queryFn: () => api<ListResponse<ModelPrice>>(`/models/${modelId}/prices`).then((r) => r.data),
    enabled: !!modelId,
  });
}

export interface PriceInput {
  effective_from?: string;
  input_per_mtok_nanos: number;
  cached_input_per_mtok_nanos?: number;
  cache_write_per_mtok_nanos?: number;
  output_per_mtok_nanos: number;
}

export function useAddPrice(modelId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: PriceInput) => api<ModelPrice>(`/models/${modelId}/prices`, { method: "POST", body }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: providerKeys.prices(modelId) });
      qc.invalidateQueries({ queryKey: ["models"] });
    },
  });
}

export function usePolicies() {
  return useQuery({ queryKey: providerKeys.policies, queryFn: () => api<ListResponse<RoutingPolicy>>("/routing-policies").then((r) => r.data) });
}

export interface PolicyInput {
  alias: string;
  description?: string;
  max_attempts_per_target?: number;
  retry_base_ms?: number;
  retry_max_ms?: number;
  enabled?: boolean;
  target_model_ids: string[];
}

export function useSavePolicy() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: PolicyInput & { id?: string }) =>
      id ? api<RoutingPolicy>(`/routing-policies/${id}`, { method: "PUT", body }) : api<RoutingPolicy>("/routing-policies", { method: "POST", body }),
    onSuccess: () => qc.invalidateQueries({ queryKey: providerKeys.policies }),
  });
}

export function useDeletePolicy() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>(`/routing-policies/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: providerKeys.policies }),
  });
}
