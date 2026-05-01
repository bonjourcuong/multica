"use client";

import { useMemo } from "react";
import { X, Globe } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { workspaceColor } from "@multica/core/workspace/color";
import type { GlobalMirrorSummary } from "@multica/core/types";
import { GLOBAL_LANE_ID, type LaneId } from "./lanes-store";

export interface LaneRailEntry {
  /** Lane id — either GLOBAL_LANE_ID or a workspace UUID. */
  id: LaneId;
  /** Display label shown in the rail. */
  label: string;
  /** Optional slug — used to render the @slug subtitle on workspace lanes. */
  slug?: string;
  /** Optional unread count from the workspace's mirror summary. */
  unreadCount?: number;
  /** Optional ISO timestamp of last activity in the workspace's mirror. */
  lastActivityAt?: string | null;
}

interface LaneRailProps {
  /** Ordered rail entries — first entry is rendered at the top. */
  entries: LaneRailEntry[];
  /** Active lane id. The matching entry highlights. */
  activeLaneId: LaneId;
  /** Activates an existing lane in place — no rail reorder. */
  onActivate: (id: LaneId) => void;
  /** Closes a workspace lane (Global is not closeable). */
  onCloseWorkspace: (wsId: string) => void;
}

/**
 * Left rail of `/global/chat`. Lists the always-on Global lane plus any
 * open workspace lanes in most-recent activation order. Click activates,
 * the small × on hover removes the lane from the rail (the underlying
 * chat session is preserved — see DoD `Closing a lane`).
 */
export function LaneRail({
  entries,
  activeLaneId,
  onActivate,
  onCloseWorkspace,
}: LaneRailProps) {
  return (
    <nav
      data-testid="lane-rail"
      aria-label="Open chat lanes"
      className="flex w-[220px] shrink-0 flex-col gap-1 border-r bg-card/40 p-2"
    >
      <div className="px-2 py-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
        Lanes
      </div>
      {entries.map((entry) => (
        <LaneRailItem
          key={entry.id}
          entry={entry}
          active={entry.id === activeLaneId}
          onActivate={() => onActivate(entry.id)}
          onClose={
            entry.id === GLOBAL_LANE_ID
              ? undefined
              : () => onCloseWorkspace(entry.id)
          }
        />
      ))}
    </nav>
  );
}

function LaneRailItem({
  entry,
  active,
  onActivate,
  onClose,
}: {
  entry: LaneRailEntry;
  active: boolean;
  onActivate: () => void;
  onClose?: () => void;
}) {
  const isGlobal = entry.id === GLOBAL_LANE_ID;
  const swatch = useMemo(
    () => (isGlobal ? null : workspaceColor(entry.id)),
    [entry.id, isGlobal],
  );
  const initials = entry.label.slice(0, 2).toUpperCase();
  const hasUnread = (entry.unreadCount ?? 0) > 0;

  return (
    <div
      data-testid="lane-rail-item"
      data-lane-id={entry.id}
      data-active={active ? "true" : "false"}
      className={cn(
        "group relative flex items-center gap-2 rounded-md px-2 py-1.5 transition-colors",
        active ? "bg-accent text-accent-foreground" : "hover:bg-accent/60",
      )}
    >
      <button
        type="button"
        onClick={onActivate}
        className="flex min-w-0 flex-1 items-center gap-2 text-left outline-none"
        aria-current={active ? "page" : undefined}
        aria-label={`Activate ${entry.label} lane`}
      >
        {isGlobal ? (
          <span
            aria-hidden="true"
            className="grid size-6 shrink-0 place-items-center rounded-sm bg-primary text-primary-foreground"
          >
            <Globe className="size-3.5" />
          </span>
        ) : (
          <span
            aria-hidden="true"
            className="grid size-6 shrink-0 place-items-center rounded-sm text-[10px] font-semibold text-white"
            style={swatch ? { backgroundColor: swatch } : undefined}
          >
            {initials}
          </span>
        )}
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm font-medium leading-tight">
            {entry.label}
          </span>
          {entry.slug && (
            <span className="block truncate text-[10px] text-muted-foreground">
              @{entry.slug}
            </span>
          )}
        </span>
        {hasUnread && (
          <span
            data-testid="lane-rail-unread"
            className="ml-1 inline-flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-brand px-1 text-[10px] font-medium text-white"
          >
            {(entry.unreadCount ?? 0) > 9 ? "9+" : entry.unreadCount}
          </span>
        )}
      </button>
      {onClose && (
        <button
          type="button"
          onClick={onClose}
          aria-label={`Close ${entry.label} lane`}
          className="opacity-0 transition-opacity group-hover:opacity-100 focus-visible:opacity-100 rounded-sm p-1 text-muted-foreground hover:bg-background hover:text-foreground"
        >
          <X className="size-3" />
        </button>
      )}
    </div>
  );
}

/**
 * Build rail entries from the open lanes order plus the cached mirror
 * summaries. Mirror summaries are the source of truth for workspace name,
 * slug, and unread count — the lane store only carries the workspace IDs.
 *
 * Workspaces in the rail without a corresponding mirror summary (e.g. user
 * just lost membership) are still listed but with placeholder text — the
 * user can still close the orphaned entry to clean up the rail.
 */
export function buildLaneEntries(
  openLanes: string[],
  mirrors: GlobalMirrorSummary[] | undefined,
): LaneRailEntry[] {
  const byId = new Map<string, GlobalMirrorSummary>();
  for (const m of mirrors ?? []) byId.set(m.workspace_id, m);
  const global: LaneRailEntry = {
    id: GLOBAL_LANE_ID,
    label: "Global",
  };
  const workspaceEntries: LaneRailEntry[] = openLanes.map((wsId) => {
    const m = byId.get(wsId);
    return {
      id: wsId,
      label: m?.workspace_name ?? "Unknown workspace",
      slug: m?.workspace_slug,
      unreadCount: m?.unread_count,
      lastActivityAt: m?.last_message_at ?? null,
    };
  });
  return [global, ...workspaceEntries];
}
