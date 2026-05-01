"use client";

import { useCallback, useEffect, useMemo, useRef } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { workspaceListOptions } from "@multica/core/workspace";
import { crossWorkspaceIssueKeys } from "@multica/core/issues/queries";
import { useExtraWorkspaceWSEvents } from "@multica/core/realtime";
import type { WSEventType } from "@multica/core/types";

/**
 * Issue lifecycle events that should refresh the cross-workspace board.
 * `created` / `updated` / `deleted` are the obvious triggers. `_labels:changed`
 * is included because labels are denormalized onto each Issue row, so a label
 * rename or recolor in any workspace mutates the cross-workspace cache.
 */
const ISSUE_EVENTS: readonly WSEventType[] = Object.freeze([
  "issue:created",
  "issue:updated",
  "issue:deleted",
  "issue_labels:changed",
] as const);

/**
 * Coalesce window for refetches. The acceptance criterion is "appears on
 * /global within 2s"; 250ms throttling keeps us well inside the budget while
 * absorbing 10+ events/sec without thrashing the API on bursty traffic.
 */
const INVALIDATE_THROTTLE_MS = 250;

/**
 * Subscribe to issue events across every workspace the current user belongs
 * to and invalidate the cross-workspace issue list cache on any event.
 * Throttled at most one invalidation per `INVALIDATE_THROTTLE_MS`.
 *
 * Mounted by `<GlobalKanban>`; unmount tears down all extra connections via
 * `useExtraWorkspaceWSEvents`.
 */
export function useCrossWorkspaceIssueRealtime() {
  const qc = useQueryClient();
  const { data: workspaces } = useQuery(workspaceListOptions());

  const slugs = useMemo(
    () => workspaces?.map((w) => w.slug) ?? [],
    [workspaces],
  );

  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    return () => {
      if (timerRef.current) {
        clearTimeout(timerRef.current);
        timerRef.current = null;
      }
    };
  }, []);

  const handler = useCallback(() => {
    if (timerRef.current) return;
    timerRef.current = setTimeout(() => {
      timerRef.current = null;
      qc.invalidateQueries({ queryKey: crossWorkspaceIssueKeys.all() });
    }, INVALIDATE_THROTTLE_MS);
  }, [qc]);

  useExtraWorkspaceWSEvents(slugs, ISSUE_EVENTS, handler);
}
