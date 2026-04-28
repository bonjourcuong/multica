"use client";

import { useQuery } from "@tanstack/react-query";
import { workspaceListOptions } from "@multica/core/workspace/queries";
import { workspaceColor } from "@multica/core/workspace/color";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";

/**
 * Inline workspace picker shown above the global Kanban. One chip per
 * workspace the user belongs to; clicking toggles inclusion in the
 * `workspace_ids` filter.
 *
 * Why a chip row instead of a multi-select dropdown:
 * - The expected workspace count for self-host users is ~3-10. A chip
 *   row is faster (one click per toggle, no opening/closing menus) and
 *   makes the active set visible at a glance, which is the whole point
 *   of the cross-workspace view.
 * - It keeps the v1 surface dependency-free — no new shadcn primitive,
 *   no command palette wiring.
 *
 * Empty selection means "all workspaces" — the AC's "clearing it shows
 * everything" branch is the default state, no separate "All" tile.
 */
export function GlobalKanbanFilters({
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
    // With 0-1 workspaces the filter is a no-op; hiding the row keeps
    // the empty/single-workspace UX clean.
    return null;
  }

  const toggle = (id: string) => {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    onChange(Array.from(next));
  };

  return (
    <div className="flex shrink-0 items-center gap-2 border-b border-border px-4 py-2 overflow-x-auto">
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
