import type { ReactNode } from "react";

import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";

export function StatCard({
  label,
  value,
  hint,
  icon,
  loading,
  tone,
}: {
  label: string;
  value: ReactNode;
  hint?: ReactNode;
  icon?: ReactNode;
  loading?: boolean;
  tone?: "default" | "success" | "warning" | "danger";
}) {
  return (
    <div className="rounded-lg border border-border bg-card px-4 py-3.5">
      <div className="flex items-center justify-between">
        <span className="label-caps">{label}</span>
        {icon ? <span className="text-muted-foreground [&_svg]:size-3.5">{icon}</span> : null}
      </div>
      {loading ? (
        <Skeleton className="mt-2 h-7 w-24 bg-muted" />
      ) : (
        <div
          className={cn(
            "tabular mt-1.5 text-2xl font-semibold tracking-tight",
            tone === "success" && "text-success",
            tone === "warning" && "text-warning",
            tone === "danger" && "text-danger",
          )}
        >
          {value}
        </div>
      )}
      {hint ? <div className="mt-1 text-xs text-muted-foreground">{hint}</div> : null}
    </div>
  );
}
