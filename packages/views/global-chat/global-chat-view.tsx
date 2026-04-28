"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { workspaceListOptions } from "@multica/core/workspace/queries";
import type { Workspace } from "@multica/core/types";
import { GlobalChatPane } from "./global-chat-pane";
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
 * Visual identity is 100% from the existing Multica design system —
 * tokens come from `packages/ui` and the shared Tailwind theme.
 *
 * Mirror session IDs come from the upcoming `/api/global/chat/mirrors`
 * endpoint (MUL-31). Until that lands, tiles fall back to `mirror_session_id
 * = null` and render the empty-state placeholder; see `WorkspaceTile`.
 */
export function GlobalChatView() {
  const { data: workspaces } = useQuery(workspaceListOptions());

  const specs = useMemo<WorkspaceTileSpec[]>(
    () => (workspaces ?? []).map(toTileSpec),
    [workspaces],
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

function toTileSpec(workspace: Workspace): WorkspaceTileSpec {
  return {
    workspace_id: workspace.id,
    workspace_slug: workspace.slug,
    workspace_name: workspace.name,
    mirror_session_id: null,
    last_message_at: null,
  };
}
