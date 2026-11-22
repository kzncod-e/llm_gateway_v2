"use client";

import { Search } from "lucide-react";
import { useRouter } from "next/navigation";
import { useDeferredValue, useState } from "react";

import { DataTable } from "@/components/common/data-table";
import { PageHeader, Section } from "@/components/common/page-header";
import { EmptyState } from "@/components/common/states";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useTeams } from "@/features/teams/api";

import { useRequests, type RequestFilters } from "./api";
import { requestColumns } from "./request-columns";

const ALL = "__all";
const STATUSES = ["success", "error", "interrupted", "client_cancelled", "rejected"];

export function RequestsView() {
  const router = useRouter();
  const teams = useTeams();
  const [q, setQ] = useState("");
  const [status, setStatus] = useState(ALL);
  const [team, setTeam] = useState(ALL);
  const [source, setSource] = useState(ALL);
  const deferredQ = useDeferredValue(q.trim());

  const filters: RequestFilters = {
    q: deferredQ || undefined,
    status: status === ALL ? undefined : status,
    team_id: team === ALL ? undefined : team,
    source: source === ALL ? undefined : source,
  };
  const query = useRequests(filters);
  const rows = query.data?.pages.flatMap((p) => p.data);
  const filtered = Object.values(filters).some(Boolean);

  return (
    <div className="space-y-6">
      <PageHeader title="Request Logs" description="Every logical request with its provider attempts. Prompts and completions are not stored." />
      <Section
        title="Requests"
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <div className="relative">
              <Search className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Request ID or model" className="h-7 w-52 pl-7 text-xs" aria-label="Search requests" />
            </div>
            <Select value={status} onValueChange={setStatus}>
              <SelectTrigger size="sm" className="w-36" aria-label="Status filter">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={ALL}>All statuses</SelectItem>
                {STATUSES.map((s) => (
                  <SelectItem key={s} value={s}>
                    {s.replace("_", " ")}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select value={team} onValueChange={setTeam}>
              <SelectTrigger size="sm" className="w-36" aria-label="Team filter">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={ALL}>All teams</SelectItem>
                {teams.data?.map((t) => (
                  <SelectItem key={t.id} value={t.id}>
                    {t.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select value={source} onValueChange={setSource}>
              <SelectTrigger size="sm" className="w-32" aria-label="Source filter">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={ALL}>All sources</SelectItem>
                <SelectItem value="api">API</SelectItem>
                <SelectItem value="playground">Playground</SelectItem>
              </SelectContent>
            </Select>
          </div>
        }
      >
        <DataTable
          columns={requestColumns}
          data={rows}
          loading={query.isLoading}
          error={query.error}
          onRetry={() => query.refetch()}
          onRowClick={(r) => router.push(`/requests/${r.id}`)}
          empty={<EmptyState title={filtered ? "No requests match these filters" : "No requests yet"} />}
        />
        {query.hasNextPage ? (
          <div className="flex justify-center border-t border-border/60 p-3">
            <Button variant="outline" size="sm" onClick={() => query.fetchNextPage()} disabled={query.isFetchingNextPage}>
              {query.isFetchingNextPage ? "Loading…" : "Load more"}
            </Button>
          </div>
        ) : null}
      </Section>
    </div>
  );
}
