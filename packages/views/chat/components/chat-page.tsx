"use client";

import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspaceSlug } from "@multica/core/paths";
import { WorkspaceChatLane } from "./workspace-chat-lane";
import { useRouteAnchorCandidate } from "./context-anchor";

/**
 * `/chat` route entry. Thin wrapper around `WorkspaceChatLane` bound to the
 * current ambient workspace — same UI, same behavior as before the V2
 * lanes refactor (MUL-125). The wrapper resolves the focus-mode anchor
 * candidate that depends on the `NavigationProvider`, which only exists on
 * workspace-scoped routes; lanes mounted from `/global/chat` simply omit
 * this prop and focus mode no-ops there.
 */
export function ChatPage() {
  const wsId = useWorkspaceId();
  const slug = useWorkspaceSlug() ?? undefined;
  const { candidate } = useRouteAnchorCandidate(wsId);
  return (
    <WorkspaceChatLane
      workspaceId={wsId}
      workspaceSlug={slug}
      anchorCandidate={candidate}
    />
  );
}
