import { AlertTriangle, Inbox, RotateCw } from "lucide-react";
import type { ReactNode } from "react";

import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { ApiError } from "@/lib/api";

export function LoadingRows({ rows = 5, className = "" }: { rows?: number; className?: string }) {
  return (
    <div className={`space-y-2 p-4 ${className}`} aria-busy="true" aria-label="Loading">
      {Array.from({ length: rows }).map((_, i) => (
        <Skeleton key={i} className="h-8 w-full bg-muted" />
      ))}
    </div>
  );
}

export function EmptyState({ title, description, action, icon }: { title: string; description?: ReactNode; action?: ReactNode; icon?: ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 px-6 py-12 text-center">
      <div className="mb-1 rounded-md border border-border bg-muted/60 p-2 text-muted-foreground">{icon ?? <Inbox className="size-4" />}</div>
      <p className="text-sm font-medium text-foreground">{title}</p>
      {description ? <p className="max-w-sm text-xs text-muted-foreground">{description}</p> : null}
      {action ? <div className="mt-2">{action}</div> : null}
    </div>
  );
}

export function ErrorState({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const message = error instanceof ApiError ? error.message : error instanceof Error ? error.message : "Something went wrong.";
  const forbidden = error instanceof ApiError && (error.status === 403 || error.status === 404);
  return (
    <div role="alert" className="flex flex-col items-center justify-center gap-2 px-6 py-10 text-center">
      <div className="mb-1 rounded-md border border-danger/30 bg-danger/10 p-2 text-danger">
        <AlertTriangle className="size-4" />
      </div>
      <p className="text-sm font-medium text-foreground">{forbidden ? "Not available" : "Could not load data"}</p>
      <p className="max-w-md text-xs text-muted-foreground">{message}</p>
      {onRetry && !forbidden ? (
        <Button variant="outline" size="sm" className="mt-2" onClick={onRetry}>
          <RotateCw /> Retry
        </Button>
      ) : null}
    </div>
  );
}
