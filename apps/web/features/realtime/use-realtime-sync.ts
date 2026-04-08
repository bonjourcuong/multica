"use client";

import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type { WSClient } from "@/shared/api";
import { toast } from "sonner";
import { useWorkspaceStore } from "@/features/workspace";
import { useAuthStore } from "@/features/auth";
import { createLogger } from "@/shared/logger";
import { issueKeys } from "@core/issues/queries";
import {
  onIssueCreated,
  onIssueUpdated,
  onIssueDeleted,
} from "@core/issues/ws-updaters";
import { onInboxNew, onInboxInvalidate, onInboxIssueStatusChanged } from "@core/inbox/ws-updaters";
import { inboxKeys } from "@core/inbox/queries";
import { workspaceKeys } from "@core/workspace/queries";
import type {
  MemberAddedPayload,
  WorkspaceDeletedPayload,
  MemberRemovedPayload,
  IssueUpdatedPayload,
  IssueCreatedPayload,
  IssueDeletedPayload,
  InboxNewPayload,
  CommentCreatedPayload,
  CommentUpdatedPayload,
  CommentDeletedPayload,
  ActivityCreatedPayload,
  ReactionAddedPayload,
  ReactionRemovedPayload,
  IssueReactionAddedPayload,
  IssueReactionRemovedPayload,
  SubscriberAddedPayload,
  SubscriberRemovedPayload,
} from "@/shared/types";

const logger = createLogger("realtime-sync");

/**
 * Centralized WS → store sync. Called once from WSProvider.
 *
 * Uses the "WS as invalidation signal + refetch" pattern:
 * - onAny handler extracts event prefix and calls the matching store refresh
 * - Debounce per-prefix prevents rapid-fire refetches (e.g. bulk issue updates)
 * - Precise handlers only for side effects (toast, navigation, self-check)
 *
 * Per-issue events (comments, activity, reactions, subscribers) are handled
 * both here (invalidation fallback) and by per-page useWSEvent hooks (granular
 * updates). Daemon events are handled by individual components only.
 */
export function useRealtimeSync(ws: WSClient | null) {
  const qc = useQueryClient();
  // Main sync: onAny → refreshMap with debounce
  useEffect(() => {
    if (!ws) return;

    const refreshMap: Record<string, () => void> = {
      inbox: () => {
        const wsId = useWorkspaceStore.getState().workspace?.id;
        if (wsId) onInboxInvalidate(qc, wsId);
      },
      agent: () => {
        const wsId = useWorkspaceStore.getState().workspace?.id;
        if (wsId) qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
      },
      member: () => {
        const wsId = useWorkspaceStore.getState().workspace?.id;
        if (wsId) qc.invalidateQueries({ queryKey: workspaceKeys.members(wsId) });
      },
      workspace: () => {
        qc.invalidateQueries({ queryKey: workspaceKeys.list() });
      },
      skill: () => {
        const wsId = useWorkspaceStore.getState().workspace?.id;
        if (wsId) qc.invalidateQueries({ queryKey: workspaceKeys.skills(wsId) });
      },
    };

    const timers = new Map<string, ReturnType<typeof setTimeout>>();
    const debouncedRefresh = (prefix: string, fn: () => void) => {
      const existing = timers.get(prefix);
      if (existing) clearTimeout(existing);
      timers.set(
        prefix,
        setTimeout(() => {
          timers.delete(prefix);
          fn();
        }, 100),
      );
    };

    // Event types handled by specific handlers below — skip generic refresh
    const specificEvents = new Set([
      "issue:updated", "issue:created", "issue:deleted", "inbox:new",
      "comment:created", "comment:updated", "comment:deleted",
      "activity:created",
      "reaction:added", "reaction:removed",
      "issue_reaction:added", "issue_reaction:removed",
      "subscriber:added", "subscriber:removed",
    ]);

    const unsubAny = ws.onAny((msg) => {
      if (specificEvents.has(msg.type)) return;
      const prefix = msg.type.split(":")[0] ?? "";
      const refresh = refreshMap[prefix];
      if (refresh) debouncedRefresh(prefix, refresh);
    });

    // --- Specific event handlers (granular cache updates) ---
    // No self-event filtering: actor_id identifies the USER, not the TAB.
    // Filtering by actor_id would block other tabs of the same user.
    // Instead, both mutations and WS handlers use dedup checks to be idempotent.

    const unsubIssueUpdated = ws.on("issue:updated", (p) => {
      const { issue } = p as IssueUpdatedPayload;
      if (!issue?.id) return;
      const wsId = useWorkspaceStore.getState().workspace?.id;
      if (wsId) {
        onIssueUpdated(qc, wsId, issue);
        if (issue.status) {
          onInboxIssueStatusChanged(qc, wsId, issue.id, issue.status);
        }
      }
    });

    const unsubIssueCreated = ws.on("issue:created", (p) => {
      const { issue } = p as IssueCreatedPayload;
      if (!issue) return;
      const wsId = useWorkspaceStore.getState().workspace?.id;
      if (wsId) onIssueCreated(qc, wsId, issue);
    });

    const unsubIssueDeleted = ws.on("issue:deleted", (p) => {
      const { issue_id } = p as IssueDeletedPayload;
      if (!issue_id) return;
      const wsId = useWorkspaceStore.getState().workspace?.id;
      if (wsId) onIssueDeleted(qc, wsId, issue_id);
    });

    const unsubInboxNew = ws.on("inbox:new", (p) => {
      const { item } = p as InboxNewPayload;
      if (!item) return;
      const wsId = useWorkspaceStore.getState().workspace?.id;
      if (wsId) onInboxNew(qc, wsId, item);
    });

    // --- Timeline event handlers (global fallback) ---
    // These events are also handled granularly by useIssueTimeline when
    // IssueDetail is mounted. This global handler ensures the timeline cache
    // is invalidated even when IssueDetail is unmounted, so stale data
    // isn't served on next mount (staleTime: Infinity relies on this).

    const invalidateTimeline = (issueId: string) => {
      qc.invalidateQueries({ queryKey: issueKeys.timeline(issueId) });
    };

    const unsubCommentCreated = ws.on("comment:created", (p) => {
      const { comment } = p as CommentCreatedPayload;
      if (comment?.issue_id) invalidateTimeline(comment.issue_id);
    });

    const unsubCommentUpdated = ws.on("comment:updated", (p) => {
      const { comment } = p as CommentUpdatedPayload;
      if (comment?.issue_id) invalidateTimeline(comment.issue_id);
    });

    const unsubCommentDeleted = ws.on("comment:deleted", (p) => {
      const { issue_id } = p as CommentDeletedPayload;
      if (issue_id) invalidateTimeline(issue_id);
    });

    const unsubActivityCreated = ws.on("activity:created", (p) => {
      const { issue_id } = p as ActivityCreatedPayload;
      if (issue_id) invalidateTimeline(issue_id);
    });

    const unsubReactionAdded = ws.on("reaction:added", (p) => {
      const { issue_id } = p as ReactionAddedPayload;
      if (issue_id) invalidateTimeline(issue_id);
    });

    const unsubReactionRemoved = ws.on("reaction:removed", (p) => {
      const { issue_id } = p as ReactionRemovedPayload;
      if (issue_id) invalidateTimeline(issue_id);
    });

    // --- Issue-level reactions & subscribers (global fallback) ---

    const unsubIssueReactionAdded = ws.on("issue_reaction:added", (p) => {
      const { issue_id } = p as IssueReactionAddedPayload;
      if (issue_id) qc.invalidateQueries({ queryKey: issueKeys.reactions(issue_id) });
    });

    const unsubIssueReactionRemoved = ws.on("issue_reaction:removed", (p) => {
      const { issue_id } = p as IssueReactionRemovedPayload;
      if (issue_id) qc.invalidateQueries({ queryKey: issueKeys.reactions(issue_id) });
    });

    const unsubSubscriberAdded = ws.on("subscriber:added", (p) => {
      const { issue_id } = p as SubscriberAddedPayload;
      if (issue_id) qc.invalidateQueries({ queryKey: issueKeys.subscribers(issue_id) });
    });

    const unsubSubscriberRemoved = ws.on("subscriber:removed", (p) => {
      const { issue_id } = p as SubscriberRemovedPayload;
      if (issue_id) qc.invalidateQueries({ queryKey: issueKeys.subscribers(issue_id) });
    });

    // --- Side-effect handlers (toast, navigation) ---

    const unsubWsDeleted = ws.on("workspace:deleted", (p) => {
      const { workspace_id } = p as WorkspaceDeletedPayload;
      const currentWs = useWorkspaceStore.getState().workspace;
      if (currentWs?.id === workspace_id) {
        logger.warn("current workspace deleted, switching");
        toast.info("This workspace was deleted");
        useWorkspaceStore.getState().refreshWorkspaces();
      }
    });

    const unsubMemberRemoved = ws.on("member:removed", (p) => {
      const { user_id } = p as MemberRemovedPayload;
      const myUserId = useAuthStore.getState().user?.id;
      if (user_id === myUserId) {
        logger.warn("removed from workspace, switching");
        toast.info("You were removed from this workspace");
        useWorkspaceStore.getState().refreshWorkspaces();
      }
    });

    const unsubMemberAdded = ws.on("member:added", (p) => {
      const { member, workspace_name } = p as MemberAddedPayload;
      const myUserId = useAuthStore.getState().user?.id;
      if (member.user_id === myUserId) {
        useWorkspaceStore.getState().refreshWorkspaces();
        toast.info(
          `You were invited to ${workspace_name ?? "a workspace"}`,
        );
      }
    });

    return () => {
      unsubAny();
      unsubIssueUpdated();
      unsubIssueCreated();
      unsubIssueDeleted();
      unsubInboxNew();
      unsubCommentCreated();
      unsubCommentUpdated();
      unsubCommentDeleted();
      unsubActivityCreated();
      unsubReactionAdded();
      unsubReactionRemoved();
      unsubIssueReactionAdded();
      unsubIssueReactionRemoved();
      unsubSubscriberAdded();
      unsubSubscriberRemoved();
      unsubWsDeleted();
      unsubMemberRemoved();
      unsubMemberAdded();
      timers.forEach(clearTimeout);
      timers.clear();
    };
  }, [ws, qc]);

  // Reconnect → refetch all data to recover missed events
  useEffect(() => {
    if (!ws) return;

    const unsub = ws.onReconnect(async () => {
      logger.info("reconnected, refetching all data");
      try {
        const wsId = useWorkspaceStore.getState().workspace?.id;
        if (wsId) {
          qc.invalidateQueries({ queryKey: issueKeys.all(wsId) });
          qc.invalidateQueries({ queryKey: inboxKeys.all(wsId) });
          qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
          qc.invalidateQueries({ queryKey: workspaceKeys.members(wsId) });
          qc.invalidateQueries({ queryKey: workspaceKeys.skills(wsId) });
        }
        qc.invalidateQueries({ queryKey: workspaceKeys.list() });
      } catch (e) {
        logger.error("reconnect refetch failed", e);
      }
    });

    return unsub;
  }, [ws, qc]);
}
