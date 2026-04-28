import type { Label } from "./label";

export type IssueStatus =
  | "backlog"
  | "todo"
  | "in_progress"
  | "in_review"
  | "done"
  | "blocked"
  | "cancelled";

export type IssuePriority = "urgent" | "high" | "medium" | "low" | "none";

export type IssueAssigneeType = "member" | "agent";

export interface IssueReaction {
  id: string;
  issue_id: string;
  actor_type: string;
  actor_id: string;
  emoji: string;
  created_at: string;
}

export interface Issue {
  id: string;
  workspace_id: string;
  number: number;
  identifier: string;
  title: string;
  description: string | null;
  status: IssueStatus;
  priority: IssuePriority;
  assignee_type: IssueAssigneeType | null;
  assignee_id: string | null;
  creator_type: IssueAssigneeType;
  creator_id: string;
  parent_issue_id: string | null;
  project_id: string | null;
  position: number;
  due_date: string | null;
  reactions?: IssueReaction[];
  labels?: Label[];
  created_at: string;
  updated_at: string;
}

/**
 * Inline workspace summary attached to each issue returned by
 * `GET /api/issues/cross-workspace`. Carries everything the cross-workspace
 * card needs to render a per-workspace badge + link without a second lookup.
 */
export interface CrossWorkspaceIssueWorkspace {
  id: string;
  name: string;
  slug: string;
  issue_prefix: string;
  /** Server-derived from `WorkspaceColor()` (see ADR 0001 §1.6). */
  color: string;
}

/** An issue plus the workspace summary that owns it. */
export interface CrossWorkspaceIssue extends Issue {
  workspace: CrossWorkspaceIssueWorkspace;
}
