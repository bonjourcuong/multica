/**
 * Cross-workspace orchestrator chat ("Global Chat") types.
 *
 * The user has a single global session bound to their account. Messages in the
 * global session can dispatch to per-workspace mirror sessions; the dispatch
 * targets are recorded on each global message for audit and live-tile updates.
 */
export interface GlobalChatSession {
  id: string;
  user_id: string;
  agent_id: string;
  created_at: string;
  archived_at: string | null;
}

export type GlobalChatAuthorKind = "user" | "agent";

export interface GlobalDispatchTarget {
  workspace_slug: string;
  /** Empty when the dispatch was rejected before resolution. */
  workspace_id: string;
  /** Empty when the dispatch was rejected. */
  mirror_session_id: string;
  /** Empty when the dispatch was rejected. */
  mirror_message_id: string;
  /** Humanised rejection message. Absent on success. */
  error?: string;
}

export interface GlobalChatMessage {
  id: string;
  global_session_id: string;
  author_kind: GlobalChatAuthorKind;
  author_id: string;
  body: string;
  metadata: Record<string, unknown>;
  /**
   * Workspaces this message was dispatched into. Empty for plain user/agent
   * messages that didn't trigger any dispatch.
   */
  dispatched_to: GlobalDispatchTarget[];
  created_at: string;
}

/**
 * Response from GET /api/global/chat/sessions/me/pending-task. All fields
 * are absent when the user's global session has no in-flight task — the
 * server returns an empty object on cold start (no session bootstrapped
 * yet) so the frontend doesn't have to special-case it.
 *
 * `agent_id` lets the FE attribute the "is thinking…" indicator to the
 * specific agent answering this turn, even if the user switches the
 * picker mid-flight. Optional because pre-V3 task rows may pre-date the
 * field; the indicator falls back to the active picker agent when absent.
 */
export interface GlobalChatPendingTask {
  task_id?: string;
  status?: string;
  agent_id?: string;
}

/**
 * Lightweight descriptor returned alongside the workspace list for the global
 * chat tile grid. The mirror session is created lazily on first dispatch, so
 * it can be `null` for workspaces the user has never dispatched to.
 *
 * `unread_count` is the number of assistant-authored mirror messages the
 * user has not yet acknowledged in this workspace; 0 when the session is
 * read or has never received an assistant reply. Used by the tile to render
 * an unread badge.
 */
export interface GlobalMirrorSummary {
  workspace_id: string;
  workspace_slug: string;
  workspace_name: string;
  mirror_session_id: string | null;
  last_message_at: string | null;
  unread_count: number;
}
