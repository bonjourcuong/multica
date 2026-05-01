import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useCurrentWorkspace } from "../paths";
import { chatKeys } from "./queries";
import { createLogger } from "../logger";
import type { ChatSession, SendChatMessageResponse } from "../types";

const logger = createLogger("chat.mut");

// Per-call workspace override — see queries.ts for the rationale. Pass `wsSlug`
// (and the matching `wsId` for cache invalidation) when the consuming UI runs
// outside the target workspace's URL segment, e.g. global-chat V2 lanes.
//
// Inside a workspace route, omit both — `useCurrentWorkspace()` resolves the
// ambient wsId from the URL-driven Context and the api client picks the slug
// from workspace-storage as today.

export function useCreateChatSession(opts?: { wsId?: string; wsSlug?: string }) {
  const qc = useQueryClient();
  // Prefer the explicit `opts.wsId` — `useWorkspaceId()` throws when there
  // is no ambient workspace, which would break this hook on `/global/chat`
  // V2 lanes. Fall back to the ambient workspace only when no explicit id
  // was given (the in-workspace `/chat` call site).
  const ambient = useCurrentWorkspace();
  const wsId = opts?.wsId ?? ambient?.id;
  if (!wsId) {
    throw new Error(
      "useCreateChatSession: no workspace id — pass opts.wsId or render inside a workspace route",
    );
  }
  const reqOpts = opts?.wsSlug ? { workspaceSlug: opts.wsSlug } : undefined;

  return useMutation({
    mutationFn: (data: { agent_id: string; title?: string }) => {
      logger.info("createChatSession.start", { agent_id: data.agent_id, titleLength: data.title?.length ?? 0 });
      return api.createChatSession(data, reqOpts);
    },
    onSuccess: (session) => {
      logger.info("createChatSession.success", { sessionId: session.id, agentId: session.agent_id });
    },
    onError: (err) => {
      logger.error("createChatSession.error", err);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: chatKeys.sessions(wsId) });
      qc.invalidateQueries({ queryKey: chatKeys.allSessions(wsId) });
    },
  });
}

/**
 * Idempotent lane bootstrap — wraps `POST /api/chat/sessions/find-or-create`.
 * Returns the existing active workspace-scope session for `(workspace, me,
 * agent)` if any (HTTP 200); creates one and returns it otherwise (HTTP 201).
 * Use this from the global-chat V2 lanes so reopening a lane lands on the
 * same thread instead of forking a new one each time. Pass `wsSlug` to target
 * a workspace other than the URL-ambient one.
 */
export function useFindOrCreateChatSession(opts?: { wsId?: string; wsSlug?: string }) {
  const qc = useQueryClient();
  // See useCreateChatSession — explicit opts win, ambient is only a fallback.
  const ambient = useCurrentWorkspace();
  const wsId = opts?.wsId ?? ambient?.id;
  if (!wsId) {
    throw new Error(
      "useFindOrCreateChatSession: no workspace id — pass opts.wsId or render inside a workspace route",
    );
  }
  const reqOpts = opts?.wsSlug ? { workspaceSlug: opts.wsSlug } : undefined;

  return useMutation({
    mutationFn: (data: { agent_id: string; title?: string }) => {
      logger.info("findOrCreateChatSession.start", { agent_id: data.agent_id });
      return api.findOrCreateChatSession(data, reqOpts);
    },
    onSuccess: (session) => {
      logger.info("findOrCreateChatSession.success", { sessionId: session.id, agentId: session.agent_id });
    },
    onError: (err) => {
      logger.error("findOrCreateChatSession.error", err);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: chatKeys.sessions(wsId) });
      qc.invalidateQueries({ queryKey: chatKeys.allSessions(wsId) });
    },
  });
}

/**
 * Sends a user message into an existing chat session. Thin wrapper over
 * `api.sendChatMessage`; exists so callers running outside the session's
 * workspace (global-chat V2 lanes) can pass `wsSlug` per-call. The optimistic
 * insert + pending-task seed remains the caller's responsibility (the
 * existing chat-page does it inline; this hook does not duplicate that
 * logic to avoid shifting behaviour for in-workspace callers).
 */
export function useSendChatMessage(opts?: { wsSlug?: string }) {
  const reqOpts = opts?.wsSlug ? { workspaceSlug: opts.wsSlug } : undefined;

  return useMutation<SendChatMessageResponse, unknown, { sessionId: string; content: string }>({
    mutationFn: ({ sessionId, content }) => {
      logger.info("sendChatMessage.start", { sessionId, contentLength: content.length });
      return api.sendChatMessage(sessionId, content, reqOpts);
    },
    onError: (err, vars) => {
      logger.error("sendChatMessage.error", { sessionId: vars.sessionId, err });
    },
  });
}

/**
 * Clears the session's unread state server-side. Optimistically flips
 * has_unread to false in the cached lists so the FAB badge drops
 * immediately. The server broadcasts chat:session_read so other devices
 * also sync.
 */
export function useMarkChatSessionRead(opts?: { wsId?: string; wsSlug?: string }) {
  const qc = useQueryClient();
  // See useCreateChatSession — explicit opts win, ambient is only a fallback,
  // and we tolerate "no ambient workspace" so global-chat lanes can call this.
  const ambient = useCurrentWorkspace();
  const wsId = opts?.wsId ?? ambient?.id;
  if (!wsId) {
    throw new Error(
      "useMarkChatSessionRead: no workspace id — pass opts.wsId or render inside a workspace route",
    );
  }
  const reqOpts = opts?.wsSlug ? { workspaceSlug: opts.wsSlug } : undefined;

  return useMutation({
    mutationFn: (sessionId: string) => {
      logger.info("markChatSessionRead.start", { sessionId });
      return api.markChatSessionRead(sessionId, reqOpts);
    },
    onMutate: async (sessionId) => {
      await qc.cancelQueries({ queryKey: chatKeys.sessions(wsId) });
      await qc.cancelQueries({ queryKey: chatKeys.allSessions(wsId) });

      const prevSessions = qc.getQueryData<ChatSession[]>(chatKeys.sessions(wsId));
      const prevAll = qc.getQueryData<ChatSession[]>(chatKeys.allSessions(wsId));

      const clear = (old?: ChatSession[]) =>
        old?.map((s) => (s.id === sessionId ? { ...s, has_unread: false } : s));
      qc.setQueryData<ChatSession[]>(chatKeys.sessions(wsId), clear);
      qc.setQueryData<ChatSession[]>(chatKeys.allSessions(wsId), clear);

      return { prevSessions, prevAll };
    },
    onError: (err, sessionId, ctx) => {
      logger.error("markChatSessionRead.error.rollback", { sessionId, err });
      if (ctx?.prevSessions) qc.setQueryData(chatKeys.sessions(wsId), ctx.prevSessions);
      if (ctx?.prevAll) qc.setQueryData(chatKeys.allSessions(wsId), ctx.prevAll);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: chatKeys.sessions(wsId) });
      qc.invalidateQueries({ queryKey: chatKeys.allSessions(wsId) });
    },
  });
}

export function useArchiveChatSession(opts?: { wsId?: string; wsSlug?: string }) {
  const qc = useQueryClient();
  const ambient = useCurrentWorkspace();
  const wsId = opts?.wsId ?? ambient?.id;
  if (!wsId) {
    throw new Error(
      "useArchiveChatSession: no workspace id — pass opts.wsId or render inside a workspace route",
    );
  }
  const reqOpts = opts?.wsSlug ? { workspaceSlug: opts.wsSlug } : undefined;

  return useMutation({
    mutationFn: (sessionId: string) => {
      logger.info("archiveChatSession.start", { sessionId });
      return api.archiveChatSession(sessionId, reqOpts);
    },
    onMutate: async (sessionId) => {
      await qc.cancelQueries({ queryKey: chatKeys.sessions(wsId) });
      await qc.cancelQueries({ queryKey: chatKeys.allSessions(wsId) });

      const prevSessions = qc.getQueryData<ChatSession[]>(chatKeys.sessions(wsId));
      const prevAll = qc.getQueryData<ChatSession[]>(chatKeys.allSessions(wsId));

      // Optimistic: remove from active, mark as archived in allSessions
      qc.setQueryData<ChatSession[]>(chatKeys.sessions(wsId), (old) =>
        old ? old.filter((s) => s.id !== sessionId) : old,
      );
      qc.setQueryData<ChatSession[]>(chatKeys.allSessions(wsId), (old) =>
        old?.map((s) =>
          s.id === sessionId ? { ...s, status: "archived" as const } : s,
        ),
      );

      logger.debug("archiveChatSession.optimistic", { sessionId });
      return { prevSessions, prevAll };
    },
    onError: (err, sessionId, ctx) => {
      logger.error("archiveChatSession.error.rollback", { sessionId, err });
      if (ctx?.prevSessions) qc.setQueryData(chatKeys.sessions(wsId), ctx.prevSessions);
      if (ctx?.prevAll) qc.setQueryData(chatKeys.allSessions(wsId), ctx.prevAll);
    },
    onSettled: (_data, _err, sessionId) => {
      logger.debug("archiveChatSession.settled", { sessionId });
      qc.invalidateQueries({ queryKey: chatKeys.sessions(wsId) });
      qc.invalidateQueries({ queryKey: chatKeys.allSessions(wsId) });
    },
  });
}
