"use client";

import { useCallback, useMemo, useState } from "react";
import {
  classifyDispatchTarget,
  parseMentionSlugs,
  type TileDispatchState,
} from "@multica/core/global-chat";
import type { SendGlobalChatMessageResponse } from "@multica/core/api";
import type { GlobalMirrorSummary } from "@multica/core/types";
import { GlobalChatPane } from "./global-chat-pane";
import { useGlobalMirrors } from "./use-global-chat";
import { WorkspaceTilesGrid } from "./workspace-tiles-grid";
import type { WorkspaceTileSpec } from "./workspace-tile";

/**
 * Top-level layout for `/global/chat`.
 *
 * Two columns side by side:
 *  - Left, fixed width (~360px): persistent chat with the user's global
 *    orchestrator agent. Sending a message can mention `@workspace[:agent]`,
 *    which triggers a backend dispatch into that workspace's mirror session.
 *  - Right, flex: a single horizontal scroll container holding a 2-row grid
 *    of workspace tiles, one per workspace the user is a member of. Each
 *    tile mirrors that workspace's "global" chat session in real time.
 *
 * Tile data comes from `GET /api/global/chat/mirrors`: one summary per
 * workspace the caller is a member of, ordered by recent mirror activity
 * (workspaces with no dispatch yet sink to the tail, so freshly joined
 * workspaces don't push active ones off-screen).
 */
export function GlobalChatView() {
  const { data: mirrors } = useGlobalMirrors();

  const specs = useMemo<WorkspaceTileSpec[]>(
    () => (mirrors ?? []).map(toTileSpec),
    [mirrors],
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
      <aside className="flex w-[360px] shrink-0 flex-col">
        <GlobalChatPane
          onSubmit={onSubmit}
          onResolved={onResolved}
          onErrored={onErrored}
        />
      </aside>
      <section className="flex min-w-0 flex-1 flex-col">
        <WorkspaceTilesGrid workspaces={specs} tileStates={tileStates} />
      </section>
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
