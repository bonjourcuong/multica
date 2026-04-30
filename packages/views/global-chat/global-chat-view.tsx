"use client";

import { useMemo } from "react";
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

  return (
    <div className="flex flex-1 gap-3 p-3 min-h-0">
      <aside className="flex w-[360px] shrink-0 flex-col">
        <GlobalChatPane />
      </aside>
      <section className="flex min-w-0 flex-1 flex-col">
        <WorkspaceTilesGrid workspaces={specs} />
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
