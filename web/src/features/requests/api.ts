"use client";

import { keepPreviousData, useInfiniteQuery, useQuery } from "@tanstack/react-query";

import { api } from "@/lib/api";
import type { ListResponse, RequestDetail, RequestRow } from "@/lib/types";

export interface RequestFilters {
  q?: string;
  status?: string;
  team_id?: string;
  provider_id?: string;
  source?: string;
}

export function useRequests(filters: RequestFilters, limit = 50) {
  return useInfiniteQuery({
    queryKey: ["requests", filters, limit],
    queryFn: ({ pageParam }) => api<ListResponse<RequestRow>>("/requests", { query: { ...filters, limit, cursor: pageParam } }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    placeholderData: keepPreviousData,
  });
}

export function useRecentRequests(limit = 8) {
  return useQuery({
    queryKey: ["requests", "recent", limit],
    queryFn: () => api<ListResponse<RequestRow>>("/requests", { query: { limit } }).then((r) => r.data),
    refetchInterval: 15_000,
  });
}

export function useRequest(id: string) {
  return useQuery({ queryKey: ["requests", "detail", id], queryFn: () => api<RequestDetail>(`/requests/${id}`) });
}
