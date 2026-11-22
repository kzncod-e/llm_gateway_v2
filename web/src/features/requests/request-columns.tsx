"use client";

import type { ColumnDef } from "@tanstack/react-table";
import { GitBranch, Radio } from "lucide-react";

import { CostStatusHint, RequestStatusBadge } from "@/components/common/status-badge";
import { formatDateTime, formatDuration, formatMoney, formatNumber } from "@/lib/format";
import type { RequestRow } from "@/lib/types";

export const requestColumns: ColumnDef<RequestRow, unknown>[] = [
  {
    accessorKey: "created_at",
    header: "Time",
    cell: ({ row }) => <span className="tabular text-muted-foreground">{formatDateTime(row.original.created_at)}</span>,
  },
  { accessorKey: "status", header: "Status", cell: ({ row }) => <RequestStatusBadge status={row.original.status} /> },
  {
    accessorKey: "requested_model",
    header: "Model",
    cell: ({ row }) => (
      <div className="min-w-0">
        <div className="flex items-center gap-1.5 font-mono text-xs text-foreground">
          {row.original.requested_model}
          {row.original.stream ? <Radio className="size-3 text-muted-foreground" aria-label="streaming" /> : null}
        </div>
        <div className="text-[11px] text-muted-foreground">
          {row.original.provider_slug ? `${row.original.provider_slug} / ${row.original.model_name}` : row.original.error_code || "—"}
        </div>
      </div>
    ),
  },
  { accessorKey: "team_name", header: "Team", cell: ({ row }) => <span className="text-muted-foreground">{row.original.team_name}</span> },
  {
    accessorKey: "attempt_count",
    header: "Attempts",
    meta: { align: "right" },
    cell: ({ row }) => (
      <span className="inline-flex items-center gap-1">
        {row.original.fallback_count > 0 ? <GitBranch className="size-3 text-warning" aria-label="fallback" /> : null}
        {row.original.attempt_count}
      </span>
    ),
  },
  { accessorKey: "total_tokens", header: "Tokens", meta: { align: "right" }, cell: ({ row }) => formatNumber(row.original.total_tokens) },
  {
    accessorKey: "cost_nanos",
    header: "Cost",
    meta: { align: "right" },
    cell: ({ row }) => (
      <span className="inline-flex items-center gap-1">
        {formatMoney(row.original.cost_nanos)} <CostStatusHint status={row.original.cost_status} />
      </span>
    ),
  },
  { accessorKey: "latency_ms", header: "Latency", meta: { align: "right" }, cell: ({ row }) => formatDuration(row.original.latency_ms) },
];
