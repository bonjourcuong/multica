"use client";

import { GlobalChatView } from "@multica/views/global-chat";

/**
 * Cross-workspace orchestrator chat (ADR 2026-04-28 / MUL-30).
 *
 * Sits next to `/global` (cross-workspace Kanban). The page itself is a
 * thin shell — the layout (chat pane + workspace tiles grid) lives in
 * `@multica/views/global-chat` so it can be unit-tested without Next's
 * routing machinery.
 */
export default function GlobalChatPage() {
  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <GlobalChatView />
    </div>
  );
}
