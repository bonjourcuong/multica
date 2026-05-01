"use client";

import { useQuery } from "@tanstack/react-query";
import { workspaceListOptions } from "@multica/core/workspace/queries";
import { workspaceColor } from "@multica/core/workspace/color";
import { STATUS_CONFIG } from "@multica/core/issues/config";
import type { IssueStatus } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { StatusIcon } from "../components/status-icon";

/**
 * Filter bar shown above the global Kanban. Two chip rows:
 *
 * - **Status** — always rendered, one chip per status surfaced on the
 *   global board (`backlog`, `todo`, `in_progress`, `in_review`, `done`).
 *   Empty selection means "all statuses". Toggling chips mirrors to the
 *   `?status=...` URL search param so refresh and back/forward restore
 *   the same view.
 * - **Workspaces** — rendered only when the user belongs to more than one
 *   workspace. With 0-1 workspaces the filter is a no-op and we hide it
 *   to keep the bar uncluttered. Empty selection means "all workspaces".
 *
 * Both rows use the same chip pattern: button with `role="checkbox"` +
 * `aria-checked`, `transition-colors` accent on toggle. We avoid a
 * dropdown / multi-select primitive on purpose — chip rows make the
 * active filter set visible at a glance, which is the whole point of
 * the cross-workspace view.
 */
export function GlobalKanbanFilters({
  selectedWorkspaceIds,
  onWorkspaceChange,
  boardStatuses,
  selectedStatuses,
  onStatusChange,
}: {
  selectedWorkspaceIds: string[];
  onWorkspaceChange: (next: string[]) => void;
  boardStatuses: IssueStatus[];
  selectedStatuses: IssueStatus[];
  onStatusChange: (next: IssueStatus[]) => void;
}) {
  return (
    <div className="flex shrink-0 flex-col border-b border-border">
      <StatusChips
        boardStatuses={boardStatuses}
        selectedStatuses={selectedStatuses}
        onChange={onStatusChange}
      />
      <WorkspaceChips
        selectedWorkspaceIds={selectedWorkspaceIds}
        onChange={onWorkspaceChange}
      />
    </div>
  );
}

function StatusChips({
  boardStatuses,
  selectedStatuses,
  onChange,
}: {
  boardStatuses: IssueStatus[];
  selectedStatuses: IssueStatus[];
  onChange: (next: IssueStatus[]) => void;
}) {
  const selected = new Set(selectedStatuses);
  const hasFilter = selected.size > 0;

  const toggle = (status: IssueStatus) => {
    const next = new Set(selected);
    if (next.has(status)) next.delete(status);
    else next.add(status);
    onChange(boardStatuses.filter((s) => next.has(s)));
  };

  return (
    <div className="flex items-center gap-2 px-4 py-2 overflow-x-auto">
      <span className="text-xs font-medium text-muted-foreground shrink-0">
        Status:
      </span>
      <div
        role="group"
        aria-label="Status filter"
        className="flex items-center gap-1.5"
      >
        {boardStatuses.map((status) => {
          const active = selected.has(status);
          return (
            <button
              key={status}
              type="button"
              role="checkbox"
              aria-checked={active}
              aria-label={STATUS_CONFIG[status].label}
              onClick={() => toggle(status)}
              className={cn(
                "inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs transition-colors",
                "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                active
                  ? "border-foreground/30 bg-accent text-foreground"
                  : "border-border bg-transparent text-muted-foreground hover:bg-accent/50 hover:text-foreground",
              )}
              data-testid={`status-filter-chip-${status}`}
            >
              <StatusIcon status={status} className="h-3 w-3" />
              <span>{STATUS_CONFIG[status].label}</span>
            </button>
          );
        })}
      </div>
      {hasFilter && (
        <Button
          variant="ghost"
          size="sm"
          onClick={() => onChange([])}
          className="ml-1 h-6 px-2 text-xs text-muted-foreground"
          data-testid="status-filter-clear"
        >
          Clear
        </Button>
      )}
    </div>
  );
}

function WorkspaceChips({
  selectedWorkspaceIds,
  onChange,
}: {
  selectedWorkspaceIds: string[];
  onChange: (next: string[]) => void;
}) {
  const { data: workspaces, isPending } = useQuery(workspaceListOptions());
  const selected = new Set(selectedWorkspaceIds);
  const hasFilter = selected.size > 0;

  if (isPending || !workspaces || workspaces.length <= 1) {
    return null;
  }

  const toggle = (id: string) => {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    onChange(Array.from(next));
  };

  return (
    <div className="flex items-center gap-2 border-t border-border px-4 py-2 overflow-x-auto">
      <span className="text-xs font-medium text-muted-foreground shrink-0">
        Workspaces:
      </span>
      <div
        role="group"
        aria-label="Workspace filter"
        className="flex items-center gap-1.5"
      >
        {workspaces.map((ws) => {
          const active = selected.has(ws.id);
          const color = workspaceColor(ws.id);
          return (
            <button
              key={ws.id}
              type="button"
              role="checkbox"
              aria-checked={active}
              aria-label={ws.name}
              onClick={() => toggle(ws.id)}
              className={cn(
                "inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs transition-colors",
                "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                active
                  ? "border-foreground/30 bg-accent text-foreground"
                  : "border-border bg-transparent text-muted-foreground hover:bg-accent/50 hover:text-foreground",
              )}
              data-testid={`workspace-filter-chip-${ws.slug}`}
            >
              <span
                aria-hidden="true"
                className="size-2.5 shrink-0 rounded-sm"
                style={{ backgroundColor: color }}
              />
              <span className="max-w-[140px] truncate">{ws.name}</span>
            </button>
          );
        })}
      </div>
      {hasFilter && (
        <Button
          variant="ghost"
          size="sm"
          onClick={() => onChange([])}
          className="ml-1 h-6 px-2 text-xs text-muted-foreground"
        >
          Clear
        </Button>
      )}
    </div>
  );
}
