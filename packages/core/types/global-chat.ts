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
  workspace_id: string;
  mirror_session_id: string;
  mirror_message_id: string;
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
 * Lightweight descriptor returned alongside the workspace list for the global
 * chat tile grid. The mirror session is created lazily on first dispatch, so
 * it can be `null` for workspaces the user has never dispatched to.
 */
export interface GlobalMirrorSummary {
  workspace_id: string;
  workspace_slug: string;
  workspace_name: string;
  mirror_session_id: string | null;
  last_message_at: string | null;
}
