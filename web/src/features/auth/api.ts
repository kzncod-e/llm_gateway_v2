"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/lib/api";
import type { Me, User } from "@/lib/types";

export const meKey = ["me"] as const;

export function useMe() {
  return useQuery({ queryKey: meKey, queryFn: () => api<Me>("/auth/me"), staleTime: 60_000 });
}

export function useLogin() {
  return useMutation({
    mutationFn: (body: { email: string; password: string }) => api<{ user: User }>("/auth/login", { method: "POST", body }),
  });
}

export function useLogout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api<void>("/auth/logout", { method: "POST", body: {} }),
    onSettled: () => {
      qc.clear();
      // Full reload on purpose so no client state from the old session survives.
      // eslint-disable-next-line @next/next/no-location-assign-relative-destination
      window.location.assign("/login");
    },
  });
}

export function useUpdateProfile() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { name: string }) => api<void>("/auth/me", { method: "PATCH", body }),
    onSuccess: () => qc.invalidateQueries({ queryKey: meKey }),
  });
}

export function useChangePassword() {
  return useMutation({
    mutationFn: (body: { current_password: string; new_password: string }) => api<void>("/auth/password", { method: "POST", body }),
  });
}

/** Convenience flags derived from the session. */
export function usePermissions() {
  const { data } = useMe();
  const isAdmin = data?.user.role === "admin";
  const roleFor = (teamId: string) => (isAdmin ? "owner" : data?.teams.find((t) => t.id === teamId)?.role);
  return {
    isAdmin,
    canManage: (teamId: string) => roleFor(teamId) === "owner",
    canDevelop: (teamId: string) => ["owner", "developer"].includes(roleFor(teamId) ?? ""),
  };
}
