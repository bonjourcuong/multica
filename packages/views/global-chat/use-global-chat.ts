"use client";

import { useMutation, useQuery, useQueryClient, queryOptions } from "@tanstack/react-query";
import { api, type SendGlobalChatMessageResponse } from "@multica/core/api";
import type {
  GlobalChatMessage,
  GlobalChatSession,
  GlobalMirrorSummary,
} from "@multica/core/types";

/**
 * TanStack Query keys for the global-chat slice. Centralised so cache writes
 * and invalidations can't drift out of sync between hooks.
 */
export const globalChatKeys = {
  all: ["global-chat"] as const,
  session: () => [...globalChatKeys.all, "session", "me"] as const,
  messages: () => [...globalChatKeys.all, "messages", "me"] as const,
  mirrors: () => [...globalChatKeys.all, "mirrors", "me"] as const,
};

export function globalChatSessionOptions() {
  return queryOptions({
    queryKey: globalChatKeys.session(),
    queryFn: () => api.getGlobalChatSession(),
    staleTime: Infinity,
  });
}

export function globalChatMessagesOptions() {
  return queryOptions({
    queryKey: globalChatKeys.messages(),
    queryFn: () => api.listGlobalChatMessages(),
    staleTime: Infinity,
  });
}

/**
 * Per-workspace mirror summaries used to populate the tile grid on
 * `/global/chat`. One entry per workspace the user is a member of, with the
 * mirror session pointer (nullable until first dispatch), the last activity
 * timestamp, and an unread count. Backed by `GET /api/global/chat/mirrors`.
 */
export function globalMirrorsOptions() {
  return queryOptions<GlobalMirrorSummary[]>({
    queryKey: globalChatKeys.mirrors(),
    queryFn: () => api.listGlobalMirrors(),
    staleTime: 30_000,
  });
}

export function useGlobalChatSession() {
  return useQuery(globalChatSessionOptions());
}

export function useGlobalChatMessages() {
  return useQuery(globalChatMessagesOptions());
}

export function useGlobalMirrors() {
  return useQuery(globalMirrorsOptions());
}

export interface UseSendGlobalChatMessageOptions {
  /** Fires before the network request — used to flip tiles to `sending`. */
  onSubmit?: (body: string) => void;
  /** Fires on successful POST with the dispatch entries the server returned. */
  onResolved?: (resp: SendGlobalChatMessageResponse) => void;
  /** Fires when the POST itself fails (network/5xx) — distinct from a per-target reject. */
  onErrored?: (err: Error) => void;
}

/**
 * Sends a user message to the global chat. The mutation invalidates the
 * messages cache on success so the next read refetches the persisted log.
 * Realtime fan-out lands in MUL-31 — until then this works as a polling
 * surface backed by manual invalidations.
 *
 * Optional lifecycle callbacks let the caller drive per-target tile state
 * without the hook owning that concern itself.
 */
export function useSendGlobalChatMessage(
  options?: UseSendGlobalChatMessageOptions,
) {
  const qc = useQueryClient();
  return useMutation<SendGlobalChatMessageResponse, Error, string>({
    mutationFn: (body) => api.sendGlobalChatMessage(body),
    onMutate: (body) => {
      options?.onSubmit?.(body);
    },
    onSuccess: (resp) => {
      qc.setQueryData<GlobalChatMessage[]>(
        globalChatKeys.messages(),
        (prev) => (prev ? [...prev, resp.message] : [resp.message]),
      );
      options?.onResolved?.(resp);
    },
    onError: (err) => {
      options?.onErrored?.(err);
    },
    // Defense-in-depth: even if the optimistic write above goes stale (e.g.
    // server contract drifts again), invalidating on settle self-heals on the
    // next read instead of leaving the cache wrong indefinitely.
    onSettled: () =>
      qc.invalidateQueries({ queryKey: globalChatKeys.messages() }),
  });
}

export function useBootstrapGlobalChatSession() {
  const qc = useQueryClient();
  return useMutation<GlobalChatSession, Error, void>({
    mutationFn: () => api.bootstrapGlobalChatSession(),
    onSuccess: (session) => {
      qc.setQueryData(globalChatKeys.session(), session);
    },
  });
}
