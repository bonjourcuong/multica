"use client";

import { workspaceColor } from "@multica/core/workspace/color";

/**
 * Minimal shape this tile needs to render. Wider than the workspace list
 * because it also carries the mirror-session pointer used to subscribe to
 * realtime updates (wired in the next commit).
 */
export interface WorkspaceTileSpec {
  workspace_id: string;
  workspace_slug: string;
  workspace_name: string;
  /** Set once the user has dispatched at least once into this workspace. */
  mirror_session_id: string | null;
  last_message_at: string | null;
}

export interface WorkspaceTileProps {
  workspace: WorkspaceTileSpec;
}

/**
 * Single workspace's mirror session, rendered as a card in the tile grid.
 * The realtime subscription is added in the follow-up commit; for now the
 * tile renders only the header so the grid layout (rows, cap, scroll) can
 * be exercised without a live websocket.
 */
export function WorkspaceTile({ workspace }: WorkspaceTileProps) {
  const initials = workspace.workspace_name.slice(0, 2).toUpperCase();
  const swatch = workspaceColor(workspace.workspace_id);

  return (
    <article
      data-testid="workspace-tile"
      className="flex min-h-0 flex-col overflow-hidden rounded-md border bg-card text-card-foreground"
      aria-label={`${workspace.workspace_name} mirror`}
    >
      <header className="flex items-center gap-2 border-b px-3 py-2">
        <span
          aria-hidden="true"
          className="grid size-6 shrink-0 place-items-center rounded-sm text-[10px] font-semibold text-white"
          style={{ backgroundColor: swatch }}
        >
          {initials}
        </span>
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-semibold leading-tight">
            {workspace.workspace_name}
          </div>
          <div className="truncate text-[11px] text-muted-foreground">
            @{workspace.workspace_slug}
          </div>
        </div>
      </header>
    </article>
  );
}
