"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Send } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useAuthStore } from "@multica/core/auth";
import type { SendGlobalChatMessageResponse } from "@multica/core/api";
import type {
  Agent,
  GlobalChatMessage,
  GlobalChatMessageEventPayload,
  GlobalChatPendingTask,
  GlobalChatTaskUpdateEventPayload,
} from "@multica/core/types";
import { useWSEvent } from "@multica/core/realtime";
import {
  AgentDropdown,
  AgentAvatarSmall,
} from "../chat/components/agent-dropdown";
import {
  globalChatKeys,
  useGlobalChatAgents,
  useGlobalChatMessages,
  usePendingGlobalChatTask,
  useSendGlobalChatMessage,
} from "./use-global-chat";
import {
  readStoredAgentId,
  resolveDefaultAgentId,
  writeStoredAgentId,
} from "./agent-picker";

export interface GlobalChatPaneProps {
  /** Called when the user submits a draft, before the POST resolves. */
  onSubmit?: (body: string) => void;
  /** Called with the server response when the POST succeeds. */
  onResolved?: (resp: SendGlobalChatMessageResponse) => void;
  /** Called when the POST itself fails (transport error, 5xx, etc). */
  onErrored?: (err: Error) => void;
}

/**
 * Left column of `/global/chat`. A persistent dialogue with the user's
 * "global" orchestrator agent. Mentioning `@workspace[:agent]` in a message
 * triggers a backend dispatch into that workspace's mirror session, which
 * surfaces in the corresponding tile on the right.
 *
 * V3 (MUL-139): the interlocutor is no longer a fixed twin — a header
 * dropdown lets the user pick from the agents wired for global dispatch.
 * Each agent message in the log carries its author's avatar + name so the
 * user can tell who answered.
 */
export function GlobalChatPane({
  onSubmit,
  onResolved,
  onErrored,
}: GlobalChatPaneProps = {}) {
  const messages = useGlobalChatMessages();
  const agents = useGlobalChatAgents();
  const pendingTask = usePendingGlobalChatTask();
  const user = useAuthStore((s) => s.user);
  const send = useSendGlobalChatMessage({ onSubmit, onResolved, onErrored });
  const qc = useQueryClient();
  const [draft, setDraft] = useState("");
  const [selectedAgentId, setSelectedAgentId] = useState<string | null>(null);
  const logEndRef = useRef<HTMLDivElement | null>(null);
  const persistTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // The "is thinking…" surface lives on the user-scope WS channel. On the
  // happy path the agent reply landing as `global_chat:message` is the
  // canonical "done" signal — we clear pending eagerly + invalidate so
  // the next read self-heals if the optimistic update raced the daemon.
  const handleGlobalMessage = useCallback(
    (payload: unknown) => {
      const evt = payload as GlobalChatMessageEventPayload;
      if (evt?.author_kind !== "agent") return;
      qc.setQueryData<GlobalChatPendingTask>(globalChatKeys.pendingTask(), {});
      qc.invalidateQueries({ queryKey: globalChatKeys.pendingTask() });
      qc.invalidateQueries({ queryKey: globalChatKeys.messages() });
    },
    [qc],
  );
  useWSEvent("global_chat:message", handleGlobalMessage);

  // Failure / cancel paths produce no `global_chat:message` (no agent
  // reply ever lands), so the indicator used to stick until the next
  // pendingTask poll refetched. The backend now emits a per-user lifecycle
  // event (MUL-192) — clear pending on terminal status so the UI
  // updates immediately.
  const handleGlobalTaskUpdate = useCallback(
    (payload: unknown) => {
      const evt = payload as GlobalChatTaskUpdateEventPayload;
      if (evt?.status !== "failed" && evt?.status !== "cancelled") return;
      qc.setQueryData<GlobalChatPendingTask>(globalChatKeys.pendingTask(), {});
      qc.invalidateQueries({ queryKey: globalChatKeys.pendingTask() });
    },
    [qc],
  );
  useWSEvent("global_chat:task_update", handleGlobalTaskUpdate);

  // Resolve the default selection once the agent list loads. Re-runs if the
  // list itself changes (refetch) so a removed agent no longer keeps the
  // picker pointing at a dead UUID.
  useEffect(() => {
    if (!agents.data) return;
    setSelectedAgentId((prev) => {
      if (prev && agents.data!.some((a) => a.id === prev && a.archived_at === null)) {
        return prev;
      }
      const stored = readStoredAgentId();
      return resolveDefaultAgentId(agents.data!, stored);
    });
  }, [agents.data]);

  // Debounced persist on selection change. Same value is harmless to write.
  useEffect(() => {
    if (!selectedAgentId) return;
    if (persistTimerRef.current) clearTimeout(persistTimerRef.current);
    persistTimerRef.current = setTimeout(() => {
      writeStoredAgentId(selectedAgentId);
    }, 250);
    return () => {
      if (persistTimerRef.current) clearTimeout(persistTimerRef.current);
    };
  }, [selectedAgentId]);

  const isPending = !!pendingTask.data?.task_id;

  // Autoscroll to the latest message — user expectation matches every other
  // chat surface in the app. jsdom (test env) lacks `scrollIntoView`, so we
  // feature-check before calling rather than registering a polyfill.
  useEffect(() => {
    const node = logEndRef.current;
    if (node && typeof node.scrollIntoView === "function") {
      node.scrollIntoView({ behavior: "smooth", block: "end" });
    }
  }, [messages.data?.length, isPending]);

  const agentList = useMemo(() => agents.data ?? [], [agents.data]);
  const agentById = useMemo(
    () => new Map(agentList.map((a) => [a.id, a])),
    [agentList],
  );
  const activeAgent = selectedAgentId ? agentById.get(selectedAgentId) ?? null : null;
  const noAgentAvailable = agents.isSuccess && agentList.length === 0;

  const submit = () => {
    const body = draft.trim();
    if (!body || send.isPending || noAgentAvailable) return;
    send.mutate({
      body,
      ...(selectedAgentId ? { agent_id: selectedAgentId } : {}),
    });
    setDraft("");
  };

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden rounded-md border bg-card text-card-foreground">
      <header className="flex items-center justify-between gap-2 border-b px-3 py-2">
        <div className="min-w-0">
          <h2 className="text-sm font-semibold">Global chat</h2>
          <p className="text-[11px] text-muted-foreground">
            Talk to your orchestrator. Mention <code>@workspace</code> to dispatch.
          </p>
        </div>
        <div data-testid="global-chat-agent-picker" className="shrink-0">
          {agents.isLoading ? (
            <span className="text-xs text-muted-foreground">Loading agents…</span>
          ) : noAgentAvailable ? (
            <span
              data-testid="global-chat-empty-agents"
              className="text-xs text-muted-foreground"
            >
              No global agent provisioned
            </span>
          ) : (
            <AgentDropdown
              agents={agentList}
              activeAgent={activeAgent}
              userId={user?.id}
              onSelect={(agent) => setSelectedAgentId(agent.id)}
            />
          )}
        </div>
      </header>
      <ol
        data-testid="global-chat-messages"
        className="flex-1 space-y-2 overflow-y-auto p-3 text-sm"
      >
        {messages.isLoading ? (
          <li className="text-xs text-muted-foreground">Loading…</li>
        ) : (messages.data && messages.data.length > 0) || isPending ? (
          <>
            {messages.data?.map((m) => (
              <MessageItem key={m.id} message={m} agentById={agentById} />
            ))}
            {isPending ? (
              <ThinkingIndicator
                agent={
                  (pendingTask.data?.agent_id
                    ? agentById.get(pendingTask.data.agent_id)
                    : null) ?? activeAgent
                }
              />
            ) : null}
          </>
        ) : (
          <li className="text-xs text-muted-foreground">
            No messages yet. Type something below to get started.
          </li>
        )}
        <div ref={logEndRef} />
      </ol>
      {send.isError ? (
        <div
          role="alert"
          className="border-t border-destructive/40 bg-destructive/10 px-3 py-1.5 text-xs text-destructive"
        >
          Could not send the message — try again in a moment.
        </div>
      ) : null}
      <div className="border-t p-2">
        <label htmlFor="global-chat-input" className="sr-only">
          Message
        </label>
        <div className="flex items-end gap-2">
          <textarea
            id="global-chat-input"
            data-testid="global-chat-input"
            className="min-h-[44px] flex-1 resize-none rounded-md border bg-background px-2 py-1.5 text-sm text-foreground placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            rows={2}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                submit();
              }
            }}
            placeholder="Talk to the global agent…"
            disabled={send.isPending || noAgentAvailable}
          />
          <button
            type="button"
            onClick={submit}
            aria-label="Send message"
            disabled={send.isPending || !draft.trim() || noAgentAvailable}
            className="inline-flex size-9 items-center justify-center rounded-md border bg-primary text-primary-foreground hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-50"
          >
            <Send className="size-4" aria-hidden="true" />
          </button>
        </div>
      </div>
    </div>
  );
}

function MessageItem({
  message,
  agentById,
}: {
  message: GlobalChatMessage;
  agentById: Map<string, Agent>;
}) {
  const isUser = message.author_kind === "user";
  if (isUser) {
    return (
      <li className="flex justify-end">
        <span className="inline-block max-w-[85%] whitespace-pre-wrap break-words rounded-md bg-primary px-3 py-1.5 text-sm text-primary-foreground">
          {message.body}
        </span>
      </li>
    );
  }

  const agent = agentById.get(message.author_id) ?? null;
  const displayName = agent
    ? agent.name
    : `Unknown agent (${message.author_id.slice(0, 8)}…)`;

  return (
    <li className="flex items-start justify-start gap-2">
      <AgentAvatarSmall agent={agent} />
      <div className="flex min-w-0 flex-1 flex-col items-start">
        <span className="px-1 text-[11px] font-medium text-muted-foreground">
          {displayName}
        </span>
        <span
          className={cn(
            "inline-block max-w-[85%] whitespace-pre-wrap break-words rounded-md bg-muted px-3 py-1.5 text-sm text-foreground",
          )}
        >
          {message.body}
        </span>
      </div>
    </li>
  );
}

/**
 * Renders the "<agent> is thinking…" row while a global chat task is in
 * flight. Mirrors the workspace chat pending bubble (chat-message-list) but
 * is local to the global pane so this surface stays self-contained — global
 * chat has no per-task message timeline yet (V1 only writes the final reply).
 */
function ThinkingIndicator({ agent }: { agent: Agent | null }) {
  const name = agent?.name ?? "Agent";
  return (
    <li
      role="status"
      aria-live="polite"
      data-testid="global-chat-pending"
      className="flex items-start justify-start gap-2"
    >
      <AgentAvatarSmall agent={agent} />
      <div className="flex min-w-0 flex-1 flex-col items-start">
        <span className="px-1 text-[11px] font-medium text-muted-foreground">
          {name}
        </span>
        <span className="inline-flex items-center gap-1 rounded-md bg-muted px-3 py-1.5 text-sm text-muted-foreground">
          <span className="sr-only">{name} is thinking</span>
          <span aria-hidden="true">{name} is thinking</span>
          <ThinkingDots />
        </span>
      </div>
    </li>
  );
}

function ThinkingDots() {
  return (
    <span aria-hidden="true" className="inline-flex items-end gap-0.5">
      <span className="size-1 rounded-full bg-current opacity-40 animate-pulse [animation-delay:-0.3s]" />
      <span className="size-1 rounded-full bg-current opacity-60 animate-pulse [animation-delay:-0.15s]" />
      <span className="size-1 rounded-full bg-current opacity-90 animate-pulse" />
    </span>
  );
}
