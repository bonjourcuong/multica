import { queryOptions, useQuery } from "@tanstack/react-query";
import { api } from "../api";
import type {
  IssueStatus,
  IssuePriority,
  ListIssuesParams,
  ListIssuesCache,
  CrossWorkspaceIssue,
} from "../types";
import { BOARD_STATUSES } from "./config";

export const issueKeys = {
  all: (wsId: string) => ["issues", wsId] as const,
  list: (wsId: string) => [...issueKeys.all(wsId), "list"] as const,
  /** All "my issues" queries — use for bulk invalidation. */
  myAll: (wsId: string) => [...issueKeys.all(wsId), "my"] as const,
  /** Per-scope "my issues" list with filter identity baked into the key. */
  myList: (wsId: string, scope: string, filter: MyIssuesFilter) =>
    [...issueKeys.myAll(wsId), scope, filter] as const,
  detail: (wsId: string, id: string) =>
    [...issueKeys.all(wsId), "detail", id] as const,
  children: (wsId: string, id: string) =>
    [...issueKeys.all(wsId), "children", id] as const,
  childProgress: (wsId: string) =>
    [...issueKeys.all(wsId), "child-progress"] as const,
  timeline: (issueId: string) => ["issues", "timeline", issueId] as const,
  reactions: (issueId: string) => ["issues", "reactions", issueId] as const,
  subscribers: (issueId: string) =>
    ["issues", "subscribers", issueId] as const,
  usage: (issueId: string) => ["issues", "usage", issueId] as const,
};

/**
 * Filters that can be expressed in the cross-workspace query key. Kept
 * separate from {@link ListCrossWorkspaceIssuesParams} on the API side
 * because the cache key needs to be stable for `JSON.stringify`-style
 * equality (sorted, no `undefined` keys).
 */
export interface CrossWorkspaceIssuesFilters {
  workspace_ids?: string[];
  assignee_ids?: string[];
  priority?: IssuePriority[];
  status?: IssueStatus[];
  open_only?: boolean;
}

/**
 * Normalize filters so two callers passing logically-equivalent filter sets
 * (different array order, undefined vs empty) hit the same TQ cache entry.
 * Empty / undefined values are dropped — they are equivalent to "no filter".
 */
function normalizeCrossWorkspaceFilters(
  filters: CrossWorkspaceIssuesFilters | undefined,
): CrossWorkspaceIssuesFilters {
  if (!filters) return {};
  const out: CrossWorkspaceIssuesFilters = {};
  if (filters.workspace_ids?.length) out.workspace_ids = [...filters.workspace_ids].sort();
  if (filters.assignee_ids?.length) out.assignee_ids = [...filters.assignee_ids].sort();
  if (filters.priority?.length) out.priority = [...filters.priority].sort();
  if (filters.status?.length) out.status = [...filters.status].sort();
  if (filters.open_only) out.open_only = true;
  return out;
}

export const crossWorkspaceIssueKeys = {
  all: () => ["issues", "cross-workspace"] as const,
  list: (filters: CrossWorkspaceIssuesFilters) =>
    [...crossWorkspaceIssueKeys.all(), normalizeCrossWorkspaceFilters(filters)] as const,
};

export type MyIssuesFilter = Pick<
  ListIssuesParams,
  "assignee_id" | "assignee_ids" | "creator_id" | "project_id"
>;

/** Page size per status column. */
export const ISSUE_PAGE_SIZE = 50;

/** Statuses the issues/my-issues pages paginate. Cancelled is intentionally excluded — it has never been surfaced in the list/board views. */
export const PAGINATED_STATUSES: readonly IssueStatus[] = BOARD_STATUSES;

/** Flatten a bucketed response to a single Issue[] for consumers that want the whole list. */
export function flattenIssueBuckets(data: ListIssuesCache) {
  const out = [];
  for (const status of PAGINATED_STATUSES) {
    const bucket = data.byStatus[status];
    if (bucket) out.push(...bucket.issues);
  }
  return out;
}

async function fetchFirstPages(filter: MyIssuesFilter = {}): Promise<ListIssuesCache> {
  const responses = await Promise.all(
    PAGINATED_STATUSES.map((status) =>
      api.listIssues({ status, limit: ISSUE_PAGE_SIZE, offset: 0, ...filter }),
    ),
  );
  const byStatus: ListIssuesCache["byStatus"] = {};
  PAGINATED_STATUSES.forEach((status, i) => {
    const res = responses[i]!;
    byStatus[status] = { issues: res.issues, total: res.total };
  });
  return { byStatus };
}

/**
 * CACHE SHAPE NOTE: The raw cache stores {@link ListIssuesCache} (buckets keyed
 * by status, each with `{ issues, total }`), and `select` flattens it to
 * `Issue[]` for consumers. Mutations and ws-updaters must use
 * `setQueryData<ListIssuesCache>(...)` and preserve the byStatus shape.
 *
 * Fetches the first page of each paginated status in parallel. Use
 * {@link useLoadMoreByStatus} to paginate a specific status into the cache.
 */
export function issueListOptions(wsId: string) {
  return queryOptions({
    queryKey: issueKeys.list(wsId),
    queryFn: () => fetchFirstPages(),
    select: flattenIssueBuckets,
  });
}

/**
 * Server-filtered issue list for the My Issues page.
 * Each scope gets its own cache entry so switching tabs is instant after first load.
 */
export function myIssueListOptions(
  wsId: string,
  scope: string,
  filter: MyIssuesFilter,
) {
  return queryOptions({
    queryKey: issueKeys.myList(wsId, scope, filter),
    queryFn: () => fetchFirstPages(filter),
    select: flattenIssueBuckets,
  });
}

export function issueDetailOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: issueKeys.detail(wsId, id),
    queryFn: () => api.getIssue(id),
  });
}

export function childIssueProgressOptions(wsId: string) {
  return queryOptions({
    queryKey: issueKeys.childProgress(wsId),
    queryFn: () => api.getChildIssueProgress(),
    select: (data) => {
      const map = new Map<string, { done: number; total: number }>();
      for (const entry of data.progress) {
        map.set(entry.parent_issue_id, { done: entry.done, total: entry.total });
      }
      return map;
    },
  });
}

export function childIssuesOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: issueKeys.children(wsId, id),
    queryFn: () => api.listChildIssues(id).then((r) => r.issues),
  });
}

export function issueTimelineOptions(issueId: string) {
  return queryOptions({
    queryKey: issueKeys.timeline(issueId),
    queryFn: () => api.listTimeline(issueId),
  });
}

export function issueReactionsOptions(issueId: string) {
  return queryOptions({
    queryKey: issueKeys.reactions(issueId),
    queryFn: async () => {
      const issue = await api.getIssue(issueId);
      return issue.reactions ?? [];
    },
  });
}

export function issueSubscribersOptions(issueId: string) {
  return queryOptions({
    queryKey: issueKeys.subscribers(issueId),
    queryFn: () => api.listIssueSubscribers(issueId),
  });
}

export function issueUsageOptions(issueId: string) {
  return queryOptions({
    queryKey: issueKeys.usage(issueId),
    queryFn: () => api.getIssueUsage(issueId),
  });
}

/**
 * Cross-workspace issue list (ADR 0001). Mirrors `issueListOptions` but
 * spans every workspace the current user belongs to. The `select` flattens
 * to `CrossWorkspaceIssue[]` so consumers get a plain array, identical in
 * shape to the per-workspace flow.
 */
export function crossWorkspaceIssueListOptions(
  filters: CrossWorkspaceIssuesFilters = {},
) {
  const normalized = normalizeCrossWorkspaceFilters(filters);
  return queryOptions({
    queryKey: crossWorkspaceIssueKeys.list(normalized),
    queryFn: () =>
      api.listCrossWorkspaceIssues({
        workspace_ids: normalized.workspace_ids,
        assignee_ids: normalized.assignee_ids,
        priority: normalized.priority,
        status: normalized.status,
        open_only: normalized.open_only,
      }),
    select: (data): CrossWorkspaceIssue[] => data.issues,
  });
}

/**
 * Hook wrapper around {@link crossWorkspaceIssueListOptions} for parity with
 * the techspec (`useCrossWorkspaceIssues(filters)`). Consumers can also call
 * `useQuery(crossWorkspaceIssueListOptions(filters))` directly — both routes
 * land on the same TQ cache entry.
 */
export function useCrossWorkspaceIssues(
  filters: CrossWorkspaceIssuesFilters = {},
) {
  return useQuery(crossWorkspaceIssueListOptions(filters));
}
