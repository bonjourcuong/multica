"use client";

import { GlobalKanban } from "@multica/views/issues/global-kanban";

/**
 * Cross-workspace Kanban view (ADR 0001 / MUL-6). Aggregates issues from
 * every workspace the current user belongs to into a single five-column
 * Kanban board. The route lives outside `/[workspaceSlug]` because the
 * view has no current workspace — `WorkspaceRail` (mounted in the parent
 * layout) is the only switcher visible here.
 */
export default function GlobalPage() {
  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <GlobalKanban />
    </div>
  );
}
