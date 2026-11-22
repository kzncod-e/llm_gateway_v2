"use client";

import { flexRender, getCoreRowModel, getSortedRowModel, useReactTable, type ColumnDef, type SortingState } from "@tanstack/react-table";
import { ArrowDown, ArrowUp } from "lucide-react";
import { useState, type ReactNode } from "react";

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { cn } from "@/lib/utils";

import { EmptyState, ErrorState, LoadingRows } from "./states";

export function DataTable<T>({
  columns,
  data,
  loading,
  error,
  onRetry,
  empty,
  onRowClick,
  dense = true,
}: {
  columns: ColumnDef<T, unknown>[];
  data: T[] | undefined;
  loading?: boolean;
  error?: unknown;
  onRetry?: () => void;
  empty?: ReactNode;
  onRowClick?: (row: T) => void;
  dense?: boolean;
}) {
  const [sorting, setSorting] = useState<SortingState>([]);
  // eslint-disable-next-line react-hooks/incompatible-library -- TanStack Table returns non-memoizable functions by design
  const table = useReactTable({
    data: data ?? [],
    columns,
    state: { sorting },
    onSortingChange: setSorting,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
  });

  if (error) return <ErrorState error={error} onRetry={onRetry} />;
  if (loading && !data) return <LoadingRows />;
  if (!data?.length) return <>{empty ?? <EmptyState title="Nothing here yet" />}</>;

  return (
    <div className="overflow-x-auto">
      <Table>
        <TableHeader>
          {table.getHeaderGroups().map((hg) => (
            <TableRow key={hg.id} className="border-border/70 hover:bg-transparent">
              {hg.headers.map((h) => {
                const sortable = h.column.getCanSort() && h.column.columnDef.enableSorting !== false;
                const dir = h.column.getIsSorted();
                return (
                  <TableHead key={h.id} className={cn("label-caps h-9 px-4 font-medium", (h.column.columnDef.meta as { align?: string })?.align === "right" && "text-right")}>
                    {h.isPlaceholder ? null : sortable ? (
                      <button type="button" className="inline-flex items-center gap-1 hover:text-foreground" onClick={h.column.getToggleSortingHandler()}>
                        {flexRender(h.column.columnDef.header, h.getContext())}
                        {dir === "asc" ? <ArrowUp className="size-3" /> : dir === "desc" ? <ArrowDown className="size-3" /> : null}
                      </button>
                    ) : (
                      flexRender(h.column.columnDef.header, h.getContext())
                    )}
                  </TableHead>
                );
              })}
            </TableRow>
          ))}
        </TableHeader>
        <TableBody>
          {table.getRowModel().rows.map((row) => (
            <TableRow
              key={row.id}
              className={cn("border-border/50", onRowClick && "cursor-pointer")}
              onClick={onRowClick ? () => onRowClick(row.original) : undefined}
            >
              {row.getVisibleCells().map((cell) => (
                <TableCell
                  key={cell.id}
                  className={cn("px-4 text-[13px]", dense ? "py-2" : "py-3", (cell.column.columnDef.meta as { align?: string })?.align === "right" && "tabular text-right")}
                >
                  {flexRender(cell.column.columnDef.cell, cell.getContext())}
                </TableCell>
              ))}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
