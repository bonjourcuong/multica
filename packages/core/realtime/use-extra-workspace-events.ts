"use client";

import { useEffect, useMemo, useRef } from "react";
import type { WSEventType } from "../types";
import { useWS } from "./provider";

type EventHandler = (payload: unknown, actorId?: string) => void;

/**
 * Open one WS connection per workspace slug and forward the given event
 * types from any of them to a single handler.
 *
 * The main `WSProvider` connection is bound to the URL-driven workspace
 * slug only; events from other workspaces never reach it. Cross-workspace
 * views (e.g. `/global` Kanban) use this primitive to subscribe to a fixed
 * set of issue events across every workspace the user is a member of.
 *
 * Connections open on mount, tear down on unmount or when the slug set
 * changes (e.g. user joins or leaves a workspace mid-session). Slug
 * ordering is irrelevant — connections are keyed on the sorted set.
 *
 * Caller's `handler` is read through a ref, so passing an inline arrow
 * function never re-opens connections. `events` is similarly identity-stable.
 */
export function useExtraWorkspaceWSEvents(
  slugs: readonly string[],
  events: readonly WSEventType[],
  handler: EventHandler,
) {
  const { createWorkspaceConnection } = useWS();

  const handlerRef = useRef(handler);
  handlerRef.current = handler;

  const slugsKey = useMemo(() => [...slugs].sort().join("|"), [slugs]);
  const eventsKey = useMemo(() => [...events].sort().join("|"), [events]);

  useEffect(() => {
    if (slugsKey === "" || eventsKey === "") return;

    const sortedSlugs = slugsKey.split("|");
    const sortedEvents = eventsKey.split("|") as WSEventType[];

    const teardowns: Array<() => void> = [];
    for (const slug of sortedSlugs) {
      const client = createWorkspaceConnection(slug);
      if (!client) continue;

      const dispatch: EventHandler = (payload, actorId) => {
        handlerRef.current(payload, actorId);
      };
      const unsubs = sortedEvents.map((evt) => client.on(evt, dispatch));

      client.connect();

      teardowns.push(() => {
        for (const u of unsubs) u();
        client.disconnect();
      });
    }

    return () => {
      for (const t of teardowns) t();
    };
  }, [slugsKey, eventsKey, createWorkspaceConnection]);
}
