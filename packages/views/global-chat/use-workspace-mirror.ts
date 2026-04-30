"use client";

import { useEffect, useState, useCallback } from "react";
import { useWSEvent } from "@multica/core/realtime";
import type { ChatMessageEventPayload } from "@multica/core/types";

export interface MirrorMessage {
  id: string;
  body: string;
  author_kind: "user" | "agent";
  created_at: string;
}

/**
 * Subscribes to a single workspace's "global mirror" chat session and keeps a
 * running tail of its messages. Used by `<WorkspaceTile>` to render live
 * activity in the global-chat tile grid.
 *
 * Mounting/unmounting follows the tile: capped tiles are not subscribed.
 * That's the implementation half of ADR D7 (don't fan out the WS hub for
 * users with 20+ workspaces).
 *
 * The realtime connection itself is workspace-scoped (see WSProvider), so on
 * the `/global/chat` route — where there is no current workspace — `useWSEvent`
 * is effectively a no-op and the hook returns the empty list. That is the
 * interim shape until the global WS multiplexing in MUL-31 lands; the
 * component contract (subscribe + render messages, drop on unmount) does not
 * change once it does.
 */
export function useWorkspaceMirror(mirrorSessionId: string | null | undefined) {
  const [messages, setMessages] = useState<MirrorMessage[]>([]);

  // Reset the tail when the underlying session changes — otherwise stale
  // messages from a previous session would briefly flash in the new tile.
  useEffect(() => {
    setMessages([]);
  }, [mirrorSessionId]);

  const handler = useCallback(
    (payload: unknown) => {
      if (!mirrorSessionId) return;
      const evt = payload as ChatMessageEventPayload;
      if (evt.chat_session_id !== mirrorSessionId) return;
      const next: MirrorMessage = {
        id: evt.message_id,
        body: evt.content,
        author_kind: evt.role === "user" ? "user" : "agent",
        created_at: evt.created_at,
      };
      setMessages((prev) =>
        prev.some((m) => m.id === next.id) ? prev : [...prev, next],
      );
    },
    [mirrorSessionId],
  );

  useWSEvent("chat:message", handler);

  return { messages };
}
