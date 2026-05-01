"use client";

import { useCallback, useMemo, useState } from "react";
import {
  classifyDispatchTarget,
  parseMentionSlugs,
  type TileDispatchState,
} from "@multica/core/global-chat";
import type { SendGlobalChatMessageResponse } from "@multica/core/api";
import type { GlobalMirrorSummary } from "@multica/core/types";
import { WorkspaceChatLane } from "@multica/views/chat";
import { GlobalChatPane } from "./global-chat-pane";
import { useGlobalMirrors } from "./use-global-chat";
import { WorkspaceTilesGrid } from "./workspace-tiles-grid";
import type { WorkspaceTileSpec } from "./workspace-tile";
import {
  GLOBAL_LANE_ID,
  MAX_OPEN_WORKSPACE_LANES,
  useLanesPersistence,
  useLanesStore,
} from "./lanes-store";
import { LaneRail, buildLaneEntries } from "./lane-rail";

/**
 * Top-level layout for `/global/chat`.
 *
 * Three columns:
 *  - Left rail (~220px): list of open lanes — always-on Global at the top,
 *    one entry per workspace lane the user has opened. Click activates;
 *    hover-× closes a workspace lane (without deleting the underlying chat
 *    session).
 *  - Main pane (flex): the active lane's content. Either the V1
 *    `GlobalChatPane` (orchestrator chat with @workspace dispatch) or a
 *    `WorkspaceChatLane` for a specific workspace (normal chat thread,
 *    backed by that workspace's chat session).
 *  - Right grid (~V1-shaped): workspace tiles, one per workspace the user
 *    is a member of. Tiles still mirror their workspace's "global mirror"
 *    session in real time. Clicking a tile opens or activates that
 *    workspace's lane in the rail (DoD bullet 4).
 *
 * All open workspace lanes stay mounted in the background so unread badges
 * accumulate and realtime subscriptions stay live. The lane store caps
 * background lanes at {@link MAX_OPEN_WORKSPACE_LANES} (LRU) — see ADR D8.
 */
export function GlobalChatView() {
  useLanesPersistence();
  const { data: mirrors } = useGlobalMirrors();

  const openLanes = useLanesStore((s) => s.openLanes);
  const activeLaneId = useLanesStore((s) => s.activeLaneId);
  const openWorkspaceLane = useLanesStore((s) => s.openWorkspaceLane);
  const activateLane = useLanesStore((s) => s.activateLane);
  const closeWorkspaceLane = useLanesStore((s) => s.closeWorkspaceLane);

  const specs = useMemo<WorkspaceTileSpec[]>(
    () => (mirrors ?? []).map(toTileSpec),
    [mirrors],
  );

  const mirrorById = useMemo(() => {
    const m = new Map<string, GlobalMirrorSummary>();
    for (const x of mirrors ?? []) m.set(x.workspace_id, x);
    return m;
  }, [mirrors]);

  const railEntries = useMemo(
    () => buildLaneEntries(openLanes, mirrors),
    [openLanes, mirrors],
  );

  const [tileStates, setTileStates] = useState<
    Record<string, TileDispatchState>
  >({});

  // Slug → workspace_id map sourced from the mirror summaries. Lets dispatch
  // responses (which carry the slug plus a possibly-empty workspace_id when
  // the dispatch was rejected) resolve to a tile id without a second fetch.
  const slugToId = useMemo(() => {
    const map = new Map<string, string>();
    for (const m of mirrors ?? []) map.set(m.workspace_slug, m.workspace_id);
    return map;
  }, [mirrors]);

  const onSubmit = useCallback(
    (body: string) => {
      const slugs = parseMentionSlugs(body);
      if (slugs.length === 0) return;
      setTileStates((prev) => {
        const next = { ...prev };
        for (const slug of slugs) {
          const id = slugToId.get(slug);
          if (id) next[id] = "sending";
        }
        return next;
      });
    },
    [slugToId],
  );

  const onResolved = useCallback(
    (resp: SendGlobalChatMessageResponse) => {
      setTileStates((prev) => {
        const next = { ...prev };
        for (const target of resp.dispatch) {
          const id =
            target.workspace_id || slugToId.get(target.workspace_slug);
          if (!id) continue;
          next[id] = classifyDispatchTarget(target);
        }
        return next;
      });
    },
    [slugToId],
  );

  // Transport-level failure (network, 5xx). Every tile that was marked
  // `sending` for this submission flips to `error` so the user does not
  // see a perpetual spinner.
  const onErrored = useCallback(() => {
    setTileStates((prev) => {
      const next: Record<string, TileDispatchState> = {};
      for (const [id, state] of Object.entries(prev)) {
        next[id] = state === "sending" ? "error" : state;
      }
      return next;
    });
  }, []);

  return (
    <div className="flex flex-1 gap-3 p-3 min-h-0">
      <LaneRail
        entries={railEntries}
        activeLaneId={activeLaneId}
        onActivate={activateLane}
        onCloseWorkspace={closeWorkspaceLane}
      />

      <section
        data-testid="lane-main"
        className="flex min-w-0 flex-1 flex-col"
      >
        {/*
          Background-mounted lanes (ADR D8): every open workspace lane stays
          mounted so realtime subs and React Query caches stay warm; only the
          active one is visible. The Global pane is always-on too.
        */}
        <div
          data-testid="lane-global"
          className={
            activeLaneId === GLOBAL_LANE_ID
              ? "flex h-full min-h-0 flex-col"
              : "hidden"
          }
        >
          <GlobalChatPane
            onSubmit={onSubmit}
            onResolved={onResolved}
            onErrored={onErrored}
          />
        </div>

        {openLanes.map((wsId) => {
          const m = mirrorById.get(wsId);
          const visible = activeLaneId === wsId;
          // Slug is required to drive the workspace-slug header for every
          // chat API call this lane fires (we are outside the workspace's
          // URL segment, so the api client has no ambient slug to read).
          // If the mirror hasn't loaded yet — or membership was revoked
          // since the lane was last persisted — we skip mounting rather
          // than fire requests against the wrong / null workspace.
          if (!m?.workspace_slug) return null;
          return (
            <div
              key={wsId}
              data-testid="lane-workspace"
              data-workspace-id={wsId}
              className={
                visible
                  ? "flex h-full min-h-0 flex-1 flex-col"
                  : "hidden"
              }
            >
              {visible && (
                <header className="flex items-center gap-2 border-b px-3 py-2">
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-sm font-semibold leading-tight">
                      {m.workspace_name}
                    </div>
                    <div className="truncate text-[11px] text-muted-foreground">
                      @{m.workspace_slug}
                    </div>
                  </div>
                </header>
              )}
              <WorkspaceChatLane
                workspaceId={wsId}
                workspaceSlug={m.workspace_slug}
                variant="compact"
              />
            </div>
          );
        })}
      </section>

      <aside className="flex w-[360px] shrink-0 flex-col">
        <WorkspaceTilesGrid
          workspaces={specs}
          tileStates={tileStates}
          onOpenLane={openWorkspaceLane}
        />
      </aside>
    </div>
  );
}

function toTileSpec(mirror: GlobalMirrorSummary): WorkspaceTileSpec {
  return {
    workspace_id: mirror.workspace_id,
    workspace_slug: mirror.workspace_slug,
    workspace_name: mirror.workspace_name,
    mirror_session_id: mirror.mirror_session_id,
    last_message_at: mirror.last_message_at,
  };
}

export { MAX_OPEN_WORKSPACE_LANES };
