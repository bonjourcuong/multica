"use client";

import { useMemo, useState } from "react";
import { Plus } from "lucide-react";
import type { TileDispatchState } from "@multica/core/global-chat";
import { WorkspaceTile, type WorkspaceTileSpec } from "./workspace-tile";

/**
 * Cap the number of workspace tiles rendered (and therefore subscribed to)
 * by default. The remainder is reachable via a "show more" affordance. See
 * ADR D7 — a user with 20+ workspaces would otherwise force the realtime
 * hub to fan out 20+ channels per page load.
 */
const DEFAULT_TILE_CAP = 12;

export interface WorkspaceTilesGridProps {
  workspaces: WorkspaceTileSpec[];
  /**
   * Per-target dispatch state keyed by workspace id. Tiles whose id is
   * absent from this map render as `idle`. Optional for callers that
   * don't drive dispatch state (e.g. unit tests).
   */
  tileStates?: Record<string, TileDispatchState>;
  /**
   * Click handler forwarded to each tile. When set, tiles render as buttons
   * and route the click to the V2 lane rail (DoD bullet 4).
   */
  onOpenLane?: (workspaceId: string) => void;
  /** Override for tests; production always uses {@link DEFAULT_TILE_CAP}. */
  cap?: number;
}

/**
 * The right-hand pane of `/global/chat`. A single horizontal scroll
 * container holds a 2-row CSS grid of workspace tiles, one per workspace
 * the user is a member of.
 *
 * Sort assumption: parent has already ordered `workspaces` by recent
 * activity. The grid does not re-sort.
 */
export function WorkspaceTilesGrid({
  workspaces,
  tileStates,
  onOpenLane,
  cap = DEFAULT_TILE_CAP,
}: WorkspaceTilesGridProps) {
  const [expanded, setExpanded] = useState(false);

  const visible = useMemo(() => {
    if (expanded) return workspaces;
    return workspaces.slice(0, cap);
  }, [workspaces, cap, expanded]);

  const hidden = workspaces.length - visible.length;

  if (workspaces.length === 0) {
    return (
      <div
        data-testid="tiles-empty"
        className="flex flex-1 items-center justify-center rounded-md border border-dashed bg-card/40 p-6 text-sm text-muted-foreground"
      >
        No workspaces yet — create one to start dispatching from the global chat.
      </div>
    );
  }

  return (
    <div className="flex min-w-0 flex-1 flex-col gap-2">
      <div
        data-testid="tiles-scroll"
        className="flex-1 overflow-x-auto overflow-y-hidden rounded-md"
      >
        <div
          data-testid="tiles-grid"
          className="grid h-full gap-2"
          style={{
            gridTemplateRows: "1fr 1fr",
            gridAutoFlow: "column",
            gridAutoColumns: "260px",
          }}
        >
          {visible.map((spec) => (
            <WorkspaceTile
              key={spec.workspace_id}
              workspace={spec}
              dispatchState={tileStates?.[spec.workspace_id]}
              onOpenLane={onOpenLane}
            />
          ))}
        </div>
      </div>
      {hidden > 0 ? (
        <button
          type="button"
          data-testid="show-more-tiles"
          onClick={() => setExpanded(true)}
          className="self-start inline-flex items-center gap-1 rounded-md border bg-background px-2 py-1 text-xs text-muted-foreground hover:bg-accent hover:text-accent-foreground"
        >
          <Plus className="size-3" aria-hidden="true" />
          Show {hidden} more
        </button>
      ) : null}
    </div>
  );
}
