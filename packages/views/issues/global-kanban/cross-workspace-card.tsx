"use client";

import { memo } from "react";
import { paths } from "@multica/core/paths";
import type { CrossWorkspaceIssue } from "@multica/core/types";
import { PRIORITY_CONFIG } from "@multica/core/issues/config";
import { AppLink } from "../../navigation";
import { PriorityIcon } from "../components/priority-icon";
import { WorkspaceBadge } from "../components/workspace-badge";

/**
 * Static, non-editable issue card used inside `<GlobalKanban />`. Renders
 * the workspace badge + identifier + title + priority chip. Wraps in
 * `<AppLink>` to the owning workspace's issue detail page so a click moves
 * the user out of `/global` and into the issue's home workspace.
 *
 * Why a parallel component instead of reusing `<BoardCardContent />`:
 * - `BoardCardContent` (and its editable pickers) calls `useWorkspaceId()`,
 *   `useUpdateIssue()`, `useViewStore()` — all workspace-scoped, all throw
 *   outside a workspace route.
 * - The cross-workspace view has no current workspace, so those hooks
 *   would crash. Refactoring them into optional sub-trees is out of scope
 *   for v1; the visual surface here is minimal enough to live standalone.
 *
 * v1 deliberately omits assignee avatar, project chip, due date, labels,
 * sub-issue progress — surfacing them across workspaces requires either
 * server-side denormalization or N parallel per-workspace lookups, both
 * tracked as v2 follow-ups in the PR description.
 */
export const CrossWorkspaceCard = memo(function CrossWorkspaceCard({
  issue,
}: {
  issue: CrossWorkspaceIssue;
}) {
  const priorityCfg = PRIORITY_CONFIG[issue.priority];
  const href = paths.workspace(issue.workspace.slug).issueDetail(issue.id);
  const showPriority = issue.priority !== "none";

  return (
    <AppLink
      href={href}
      aria-label={`${issue.identifier} ${issue.title} (${issue.workspace.name})`}
      className="group block focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring rounded-lg"
      data-testid="global-kanban-card"
    >
      <div className="rounded-lg border-[0.5px] border-border bg-card py-3 px-2.5 shadow-[0_3px_6px_-2px_rgba(0,0,0,0.02),0_1px_1px_0_rgba(0,0,0,0.04)] transition-colors group-hover:border-accent group-hover:bg-accent">
        <div className="flex items-center gap-1.5">
          <WorkspaceBadge
            color={issue.workspace.color}
            prefix={issue.workspace.issue_prefix}
            name={issue.workspace.name}
          />
          <p className="text-xs text-muted-foreground">{issue.identifier}</p>
        </div>
        <p className="mt-1 text-sm font-medium leading-snug line-clamp-2">
          {issue.title}
        </p>
        {showPriority && (
          <div className="mt-3 flex items-center gap-2">
            <span
              className={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-xs font-medium ${priorityCfg.badgeBg} ${priorityCfg.badgeText}`}
            >
              <PriorityIcon
                priority={issue.priority}
                className="h-3 w-3"
                inheritColor
              />
              {priorityCfg.label}
            </span>
          </div>
        )}
      </div>
    </AppLink>
  );
});
