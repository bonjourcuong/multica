"use client";

import { useCallback, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { ListTodo } from "lucide-react";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { crossWorkspaceIssueListOptions } from "@multica/core/issues/queries";
import type { CrossWorkspaceIssue, IssueStatus } from "@multica/core/types";
import { useNavigation } from "../../navigation";
import { GlobalKanbanColumn } from "./global-kanban-column";
import { GlobalKanbanFilters } from "./global-kanban-filters";

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

const GLOBAL_BOARD_STATUS_SET = new Set<IssueStatus>(GLOBAL_BOARD_STATUSES);

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
 * Parse a comma-separated `workspace_ids` URL search param into a clean
 * `string[]`. Empty / missing values both collapse to `[]`, which the
 * hook reads as "no filter — show everything".
 *
 * Exported for testability so the URL ↔ filter contract can be
 * verified without rendering the whole page.
 */
export function parseWorkspaceIdsParam(
  raw: string | null | undefined,
): string[] {
  if (!raw) return [];
  return raw
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

/**
 * Parse a comma-separated `status` URL search param into a clean
 * `IssueStatus[]`. Same shape contract as `workspace_ids`: empty / missing
 * collapse to `[]` ("no filter"). Values that aren't part of the global
 * board are dropped silently — `/global` only surfaces five statuses, so
 * a stray `?status=cancelled` from someone hand-editing the URL must not
 * leak into the request.
 */
export function parseStatusParam(
  raw: string | null | undefined,
): IssueStatus[] {
  if (!raw) return [];
  const out: IssueStatus[] = [];
  for (const part of raw.split(",")) {
    const value = part.trim();
    if (value && GLOBAL_BOARD_STATUS_SET.has(value as IssueStatus)) {
      out.push(value as IssueStatus);
    }
  }
  return out;
}

/**
 * Cross-workspace Kanban (ADR 0001 / MUL-6). Aggregates issues from every
 * workspace the current user belongs to.
 *
 * Filters: `workspace_ids` and `status` are round-tripped through URL
 * search params so the view is shareable. Toggling a chip updates the URL
 * via `NavigationAdapter.replace()` (no history entry per click — pile of
 * undo entries on a filter UI is hostile).
 *
 * `assignee_ids`, `priority` filters and cross-workspace realtime fan-out
 * remain tracked as v2 follow-ups (see PR description).
 */
export function GlobalKanban() {
  const nav = useNavigation();
  const workspaceIds = useMemo(
    () => parseWorkspaceIdsParam(nav.searchParams.get("workspace_ids")),
    [nav.searchParams],
  );
  const statuses = useMemo(
    () => parseStatusParam(nav.searchParams.get("status")),
    [nav.searchParams],
  );

  const setWorkspaceIds = useCallback(
    (next: string[]) => {
      const params = new URLSearchParams(nav.searchParams);
      if (next.length > 0) params.set("workspace_ids", next.join(","));
      else params.delete("workspace_ids");
      const qs = params.toString();
      nav.replace(qs ? `${nav.pathname}?${qs}` : nav.pathname);
    },
    [nav],
  );

  const setStatuses = useCallback(
    (next: IssueStatus[]) => {
      const params = new URLSearchParams(nav.searchParams);
      if (next.length > 0) params.set("status", next.join(","));
      else params.delete("status");
      const qs = params.toString();
      nav.replace(qs ? `${nav.pathname}?${qs}` : nav.pathname);
    },
    [nav],
  );

  const { data: issues, isPending, isError, error, refetch } = useQuery(
    crossWorkspaceIssueListOptions({
      workspace_ids: workspaceIds,
      status: statuses,
    }),
  );

  const buckets = useMemo(
    () => (issues ? bucketByStatus(issues) : null),
    [issues],
  );

  const hasFilter = workspaceIds.length > 0 || statuses.length > 0;
  const clearAllFilters = useCallback(() => {
    const params = new URLSearchParams(nav.searchParams);
    params.delete("workspace_ids");
    params.delete("status");
    const qs = params.toString();
    nav.replace(qs ? `${nav.pathname}?${qs}` : nav.pathname);
  }, [nav]);

  return (
    <div className="flex flex-1 min-h-0 flex-col" data-testid="global-kanban">
      <GlobalKanbanFilters
        selectedWorkspaceIds={workspaceIds}
        onWorkspaceChange={setWorkspaceIds}
        boardStatuses={GLOBAL_BOARD_STATUSES}
        selectedStatuses={statuses}
        onStatusChange={setStatuses}
      />
      <GlobalKanbanBody
        isPending={isPending}
        isError={isError}
        error={error}
        issues={issues}
        buckets={buckets}
        onRetry={refetch}
        hasFilter={hasFilter}
        onClearFilter={clearAllFilters}
      />
    </div>
  );
}

function GlobalKanbanBody({
  isPending,
  isError,
  error,
  issues,
  buckets,
  onRetry,
  hasFilter,
  onClearFilter,
}: {
  isPending: boolean;
  isError: boolean;
  error: unknown;
  issues: CrossWorkspaceIssue[] | undefined;
  buckets: Record<IssueStatus, CrossWorkspaceIssue[]> | null;
  onRetry: () => void;
  hasFilter: boolean;
  onClearFilter: () => void;
}) {
  if (isPending) return <GlobalKanbanSkeleton />;
  if (isError) return <GlobalKanbanError message={errorMessage(error)} onRetry={onRetry} />;
  if (!issues || issues.length === 0) {
    return hasFilter ? (
      <GlobalKanbanFilteredEmpty onClearFilter={onClearFilter} />
    ) : (
      <GlobalKanbanEmpty />
    );
  }

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

function GlobalKanbanFilteredEmpty({
  onClearFilter,
}: {
  onClearFilter: () => void;
}) {
  return (
    <div className="flex flex-1 min-h-0 flex-col items-center justify-center gap-2 px-6 text-center text-muted-foreground">
      <ListTodo className="h-10 w-10 text-muted-foreground/40" />
      <p className="text-sm">No issues match the current filter</p>
      <button
        type="button"
        onClick={onClearFilter}
        className="rounded-md border border-border bg-background px-3 py-1.5 text-xs font-medium hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        Clear filter
      </button>
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
