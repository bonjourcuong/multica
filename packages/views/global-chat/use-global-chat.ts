"use client";

import { useMutation, useQuery, useQueryClient, queryOptions } from "@tanstack/react-query";
import { api, type SendGlobalChatMessageResponse } from "@multica/core/api";
import type { GlobalChatMessage, GlobalChatSession } from "@multica/core/types";

/**
 * TanStack Query keys for the global-chat slice. Centralised so cache writes
 * and invalidations can't drift out of sync between hooks.
 */
export const globalChatKeys = {
  all: ["global-chat"] as const,
  session: () => [...globalChatKeys.all, "session", "me"] as const,
  messages: () => [...globalChatKeys.all, "messages", "me"] as const,
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

export function useGlobalChatSession() {
  return useQuery(globalChatSessionOptions());
}

export function useGlobalChatMessages() {
  return useQuery(globalChatMessagesOptions());
}

/**
 * Sends a user message to the global chat. The mutation invalidates the
 * messages cache on success so the next read refetches the persisted log.
 * Realtime fan-out lands in MUL-31 — until then this works as a polling
 * surface backed by manual invalidations.
 */
export function useSendGlobalChatMessage() {
  const qc = useQueryClient();
  return useMutation<SendGlobalChatMessageResponse, Error, string>({
    mutationFn: (body) => api.sendGlobalChatMessage(body),
    onSuccess: (resp) => {
      qc.setQueryData<GlobalChatMessage[]>(
        globalChatKeys.messages(),
        (prev) => (prev ? [...prev, resp.message] : [resp.message]),
      );
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
