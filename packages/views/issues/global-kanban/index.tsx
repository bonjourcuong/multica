"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { ListTodo } from "lucide-react";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { crossWorkspaceIssueListOptions } from "@multica/core/issues/queries";
import type { CrossWorkspaceIssue, IssueStatus } from "@multica/core/types";
import { GlobalKanbanColumn } from "./global-kanban-column";

/**
 * Five status columns shown on the cross-workspace Kanban. `blocked` and
 * `cancelled` are intentionally hidden — the global view is for
 * triaging active work across workspaces, not auditing dead branches.
 * (See ADR 0001 acceptance criteria.)
 */
const GLOBAL_BOARD_STATUSES: IssueStatus[] = [
  "backlog",
  "todo",
  "in_progress",
  "in_review",
  "done",
];

/**
 * Bucket issues by status using a single pass — order within a bucket is
 * preserved from the server response (sorted by `created_at DESC`, see
 * ADR 0001 §1.5). We intentionally do not re-sort client-side; the server
 * is authoritative on cross-workspace ordering.
 */
function bucketByStatus(
  issues: CrossWorkspaceIssue[],
): Record<IssueStatus, CrossWorkspaceIssue[]> {
  const out: Record<IssueStatus, CrossWorkspaceIssue[]> = {
    backlog: [],
    todo: [],
    in_progress: [],
    in_review: [],
    done: [],
    blocked: [],
    cancelled: [],
  };
  for (const issue of issues) {
    out[issue.status].push(issue);
  }
  return out;
}

/**
 * Cross-workspace Kanban (ADR 0001 / MUL-6). Aggregates issues from every
 * workspace the current user belongs to. Filters (workspace_ids,
 * assignee_ids, priority, status) and realtime fan-out are tracked as v2
 * follow-ups; v1 ships an unfiltered view + skeleton + empty state +
 * per-card workspace badge.
 */
export function GlobalKanban() {
  const { data: issues, isPending, isError, error, refetch } = useQuery(
    crossWorkspaceIssueListOptions({}),
  );

  const buckets = useMemo(
    () => (issues ? bucketByStatus(issues) : null),
    [issues],
  );

  if (isPending) return <GlobalKanbanSkeleton />;
  if (isError) return <GlobalKanbanError message={errorMessage(error)} onRetry={refetch} />;
  if (!issues || issues.length === 0) return <GlobalKanbanEmpty />;

  return (
    <div className="flex flex-1 min-h-0 gap-4 overflow-x-auto p-4">
      {GLOBAL_BOARD_STATUSES.map((status) => (
        <GlobalKanbanColumn
          key={status}
          status={status}
          issues={buckets?.[status] ?? []}
        />
      ))}
    </div>
  );
}

function errorMessage(err: unknown): string {
  if (err instanceof Error && err.message) return err.message;
  return "Could not load cross-workspace issues.";
}

function GlobalKanbanSkeleton() {
  return (
    <div
      className="flex flex-1 min-h-0 gap-4 overflow-x-auto p-4"
      role="status"
      aria-label="Loading cross-workspace Kanban"
    >
      {GLOBAL_BOARD_STATUSES.map((status) => (
        <div key={status} className="flex w-[280px] shrink-0 flex-col gap-2">
          <Skeleton className="h-5 w-24" />
          <Skeleton className="h-24 w-full rounded-lg" />
          <Skeleton className="h-24 w-full rounded-lg" />
        </div>
      ))}
    </div>
  );
}

function GlobalKanbanEmpty() {
  return (
    <div className="flex flex-1 min-h-0 flex-col items-center justify-center gap-2 px-6 text-center text-muted-foreground">
      <ListTodo className="h-10 w-10 text-muted-foreground/40" />
      <p className="text-sm">No issues across your workspaces yet</p>
      <p className="text-xs">
        Open a workspace from the rail to create your first issue — it will
        appear here automatically.
      </p>
    </div>
  );
}

function GlobalKanbanError({
  message,
  onRetry,
}: {
  message: string;
  onRetry: () => void;
}) {
  return (
    <div className="flex flex-1 min-h-0 flex-col items-center justify-center gap-3 px-6 text-center">
      <p className="text-sm text-destructive">{message}</p>
      <button
        type="button"
        onClick={onRetry}
        className="rounded-md border border-border bg-background px-3 py-1.5 text-xs font-medium hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        Retry
      </button>
    </div>
  );
}
