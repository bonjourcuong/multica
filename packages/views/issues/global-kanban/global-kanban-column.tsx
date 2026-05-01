"use client";

import type { CrossWorkspaceIssue, IssueStatus } from "@multica/core/types";
import { STATUS_CONFIG } from "@multica/core/issues/config";
import { StatusIcon } from "../components/status-icon";
import { CrossWorkspaceCard } from "./cross-workspace-card";

/**
 * One column of the global Kanban. Static layout (no drag-drop) — drag
 * across workspaces is intentionally v2 (see ADR 0001). Renders a header
 * with status badge + count, then a scrollable list of `CrossWorkspaceCard`s.
 *
 * Visual parity with `<BoardColumn />` is by design: same width, same
 * column tint, same header shape. Sharing the component would have
 * dragged in dnd-kit primitives and the workspace-scoped editable card,
 * so this is a deliberate render-only sibling.
 */
export function GlobalKanbanColumn({
  status,
  issues,
}: {
  status: IssueStatus;
  issues: CrossWorkspaceIssue[];
}) {
  const cfg = STATUS_CONFIG[status];

  return (
    <div
      className={`flex w-[280px] shrink-0 flex-col rounded-xl ${cfg.columnBg} p-2`}
      data-testid={`global-kanban-column-${status}`}
    >
      <div className="mb-2 flex items-center justify-between px-1.5">
        <div className="flex items-center gap-2">
          <span
            className={`inline-flex items-center gap-1.5 rounded px-2 py-0.5 text-xs font-semibold ${cfg.badgeBg} ${cfg.badgeText}`}
          >
            <StatusIcon status={status} className="h-3 w-3" inheritColor />
            {cfg.label}
          </span>
          <span className="text-xs text-muted-foreground">{issues.length}</span>
        </div>
      </div>
      <div className="min-h-[200px] flex-1 space-y-2 overflow-y-auto rounded-lg p-1">
        {issues.length === 0 ? (
          <p className="py-8 text-center text-xs text-muted-foreground">
            No issues
          </p>
        ) : (
          issues.map((issue) => (
            <CrossWorkspaceCard key={issue.id} issue={issue} />
          ))
        )}
      </div>
    </div>
  );
}
