"use client";

import type { KeyboardEvent } from "react";
import type { TileDispatchState } from "@multica/core/global-chat";
import { workspaceColor } from "@multica/core/workspace/color";
import { cn } from "@multica/ui/lib/utils";
import { useWorkspaceMirror } from "./use-workspace-mirror";

/**
 * Minimal shape this tile needs to render. Wider than the workspace list
 * because it also carries the mirror-session pointer used to subscribe to
 * realtime updates (see `use-workspace-mirror`).
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
  /** Per-target dispatch state for the most recent global-chat submission. */
  dispatchState?: TileDispatchState;
  /**
   * Optional click handler — clicking the tile opens (or activates) that
   * workspace's V2 lane in the global-chat rail. Tile is rendered as a
   * static card when omitted (test/preview surfaces).
   */
  onOpenLane?: (workspaceId: string) => void;
}

/**
 * Single workspace's mirror session, rendered as a card in the tile grid.
 * Subscribes to live updates while mounted; the parent grid is responsible
 * for not mounting more tiles than the realtime hub can comfortably fan
 * out to (see ADR D7 — the cap lives in the grid, not here).
 */
export function WorkspaceTile({
  workspace,
  dispatchState = "idle",
  onOpenLane,
}: WorkspaceTileProps) {
  const { messages } = useWorkspaceMirror(workspace.mirror_session_id);
  const initials = workspace.workspace_name.slice(0, 2).toUpperCase();
  const swatch = workspaceColor(workspace.workspace_id);

  // The tile becomes a button when an onOpenLane handler is wired (V2 lanes
  // entry point — DoD bullet 4); otherwise it stays a plain article so the
  // V1 surface and unit tests don't grow accidental click semantics.
  const interactive = !!onOpenLane;

  const interactiveProps = interactive
    ? {
        role: "button" as const,
        tabIndex: 0,
        onClick: () => onOpenLane?.(workspace.workspace_id),
        onKeyDown: (e: KeyboardEvent<HTMLElement>) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            onOpenLane?.(workspace.workspace_id);
          }
        },
      }
    : {};

  return (
    <article
      data-testid="workspace-tile"
      data-workspace-slug={workspace.workspace_slug}
      data-dispatch-state={dispatchState}
      className={cn(
        "flex min-h-0 flex-col overflow-hidden rounded-md border bg-card text-card-foreground",
        interactive &&
          "cursor-pointer transition-colors hover:border-brand/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
      )}
      aria-label={`${workspace.workspace_name} mirror`}
      {...interactiveProps}
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
        <DispatchStateBadge state={dispatchState} />
      </header>
      <ol className="flex-1 space-y-1 overflow-y-auto p-2 text-xs">
        {messages.length === 0 ? (
          <li
            className="text-muted-foreground"
            data-testid="tile-empty"
          >
            No activity yet
          </li>
        ) : (
          messages.map((m) => (
            <li
              key={m.id}
              className={cn(
                "rounded-sm px-2 py-1",
                m.author_kind === "user"
                  ? "bg-muted text-foreground"
                  : "text-foreground/85",
              )}
            >
              {m.body}
            </li>
          ))
        )}
      </ol>
    </article>
  );
}

interface DispatchStateBadgeProps {
  state: TileDispatchState;
}

const BADGE_LABELS: Record<TileDispatchState, string> = {
  idle: "",
  sending: "Sending…",
  delivered: "Delivered",
  not_authorized: "No access",
  error: "Failed",
};

/**
 * Status badge in the tile header. Idle is intentionally invisible — we
 * do not want to crowd the chrome of every workspace tile when no dispatch
 * is in flight. ADR D6: tokens only, no new visual identity.
 */
function DispatchStateBadge({ state }: DispatchStateBadgeProps) {
  if (state === "idle") return null;

  const label = BADGE_LABELS[state];

  return (
    <span
      data-testid="tile-dispatch-state"
      data-state={state}
      role="status"
      aria-label={`Dispatch ${label}`}
      className={cn(
        "inline-flex shrink-0 items-center rounded-full border px-2 py-0.5 text-[10px] font-medium leading-none",
        state === "sending" &&
          "border-border bg-muted text-muted-foreground animate-pulse",
        state === "delivered" && "border-success/40 bg-success/10 text-success",
        state === "not_authorized" &&
          "border-warning/40 bg-warning/10 text-warning",
        state === "error" &&
          "border-destructive/40 bg-destructive/10 text-destructive",
      )}
    >
      {label}
    </span>
  );
}
