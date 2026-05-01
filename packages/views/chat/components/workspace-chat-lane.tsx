"use client";

import React, { useCallback, useEffect, useMemo, useRef } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { History, Plus, Bot, ChevronDown, Check } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { Avatar, AvatarFallback, AvatarImage } from "@multica/ui/components/ui/avatar";
import { Button } from "@multica/ui/components/ui/button";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { Popover, PopoverContent, PopoverTrigger } from "@multica/ui/components/ui/popover";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { useAuthStore } from "@multica/core/auth";
import { agentListOptions, memberListOptions } from "@multica/core/workspace/queries";
import { canAssignAgent } from "@multica/views/issues/components";
import { api } from "@multica/core/api";
import {
  chatSessionsOptions,
  allChatSessionsOptions,
  chatMessagesOptions,
  pendingChatTaskOptions,
  chatKeys,
} from "@multica/core/chat/queries";
import {
  useFindOrCreateChatSession,
  useMarkChatSessionRead,
} from "@multica/core/chat/mutations";
import { useChatStore, selectWorkspaceEntry } from "@multica/core/chat";
import { PageHeader } from "../../layout/page-header";
import { ChatMessageList, ChatMessageSkeleton } from "./chat-message-list";
import { ChatInput } from "./chat-input";
import {
  ContextAnchorButton,
  ContextAnchorCard,
  buildAnchorMarkdown,
} from "./context-anchor";
import { createLogger } from "@multica/core/logger";
import type { Agent, ChatMessage, ChatSession } from "@multica/core/types";
import type { ContextAnchor } from "@multica/core/chat";

const uiLogger = createLogger("chat.ui");
const apiLogger = createLogger("chat.api");

export interface WorkspaceChatLaneProps {
  /** Workspace this lane is bound to. The lane resolves agents, sessions, and messages from this id. */
  workspaceId: string;
  /**
   * Optional slug override forwarded as `X-Workspace-Slug` to every chat
   * API call. Required when the lane runs outside the workspace's URL
   * segment (global-chat V2 lanes); leave undefined inside a workspace
   * route — the api client picks the slug from workspace-storage.
   */
  workspaceSlug?: string;
  /**
   * Lane chrome variant.
   *
   * - `page` (default): renders the workspace chat page header (title, history
   *   popover, new-chat button). Used at `/chat`.
   * - `compact`: drops the page header. Used inside `/global/chat` lanes,
   *   where the rail/header is owned by the surrounding shell.
   */
  variant?: "page" | "compact";
  /**
   * Optional context anchor used when focus mode is on. Provided by the
   * `/chat` page wrapper from `useRouteAnchorCandidate`; omitted on
   * global-chat lanes (focus mode doesn't apply there — the lane sits on
   * a route with no anchorable entity of its own). When omitted, focus
   * mode silently no-ops as if no candidate were available.
   */
  anchorCandidate?: ContextAnchor | null;
}

/**
 * Per-workspace chat thread. Owns its own (session, agent) selection via
 * `useChatStore.byWorkspace[workspaceId]` and routes every API call through
 * the per-call `wsSlug` override Tony shipped in MUL-124, so this same
 * subtree works equally well at `/chat` (ambient workspace) or as one of
 * the global-chat V2 lanes (workspace different from the URL slug).
 *
 * Mounted lanes keep their realtime + query state warm — see ADR D8 — so
 * the parent rail can hold up to 12 lanes simultaneously and accumulate
 * unread counts without losing thread context on switch.
 */
export function WorkspaceChatLane({
  workspaceId,
  workspaceSlug,
  variant = "page",
  anchorCandidate,
}: WorkspaceChatLaneProps) {
  const wsId = workspaceId;
  const entry = useChatStore(selectWorkspaceEntry(wsId));
  const activeSessionId = entry.activeSessionId;
  const selectedAgentId = entry.selectedAgentId;
  const setActiveSession = useChatStore((s) => s.setActiveSession);
  const setSelectedAgentId = useChatStore((s) => s.setSelectedAgentId);

  const user = useAuthStore((s) => s.user);
  const { data: agents = [] } = useQuery(agentListOptions(wsId, workspaceSlug));
  // Members are workspace-scoped and loaded per ambient route today; this
  // lane is read-only against the member list, so passing the wsSlug header
  // override would only matter if we ever called member-write APIs from a
  // lane outside its workspace. Today we don't.
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: sessions = [], isSuccess: sessionsLoaded } = useQuery(
    chatSessionsOptions(wsId, workspaceSlug),
  );
  const { data: allSessions = [] } = useQuery(
    allChatSessionsOptions(wsId, workspaceSlug),
  );
  const { data: rawMessages, isLoading: messagesLoading } = useQuery(
    chatMessagesOptions(activeSessionId ?? "", workspaceSlug),
  );
  const messages = activeSessionId ? rawMessages ?? [] : [];
  const showSkeleton = !!activeSessionId && messagesLoading;

  const { data: pendingTask } = useQuery(
    pendingChatTaskOptions(activeSessionId ?? "", workspaceSlug),
  );
  const pendingTaskId = pendingTask?.task_id ?? null;

  const currentSession = activeSessionId
    ? allSessions.find((s) => s.id === activeSessionId)
    : null;
  const isSessionArchived = currentSession?.status === "archived";

  const qc = useQueryClient();
  const findOrCreate = useFindOrCreateChatSession({
    wsId,
    wsSlug: workspaceSlug,
  });
  const markRead = useMarkChatSessionRead({ wsId, wsSlug: workspaceSlug });

  const currentMember = members.find((m) => m.user_id === user?.id);
  const memberRole = currentMember?.role;
  const availableAgents = useMemo(
    () => agents.filter((a) => !a.archived_at && canAssignAgent(a, user?.id, memberRole)),
    [agents, user?.id, memberRole],
  );
  const activeAgent =
    availableAgents.find((a) => a.id === selectedAgentId) ??
    pickDefaultLaneAgent(availableAgents) ??
    null;

  // Surface the resolved default back into the store on first availability,
  // so reopening this lane (or a sibling that reads `selectedAgentId`)
  // gets the same answer without re-running the picker. We only write when
  // the store entry is empty — manual selections must not be overwritten.
  useEffect(() => {
    if (selectedAgentId) return;
    if (!activeAgent) return;
    setSelectedAgentId(wsId, activeAgent.id);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- run when default first resolves
  }, [activeAgent?.id, selectedAgentId, wsId]);

  // Resume the lane's last session once sessions resolve. Refs reset when the
  // workspace id changes — a remounted lane (rail switch) needs to restore
  // for its workspace, not skip because a sibling already restored.
  const didRestoreRef = useRef<string | null>(null);
  useEffect(() => {
    if (didRestoreRef.current === wsId) return;
    if (!sessionsLoaded) return;
    didRestoreRef.current = wsId;
    if (activeSessionId) return;
    const latest = sessions.find((s) => s.status === "active");
    if (latest) setActiveSession(wsId, latest.id);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- run once per workspace when sessions load
  }, [wsId, sessionsLoaded, sessions]);

  // Auto mark-as-read whenever the viewer is on a session with unread.
  const currentHasUnread =
    sessions.find((s) => s.id === activeSessionId)?.has_unread ?? false;
  useEffect(() => {
    if (!activeSessionId || !currentHasUnread) return;
    markRead.mutate(activeSessionId);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- markRead ref stable
  }, [activeSessionId, currentHasUnread]);

  const handleSend = useCallback(
    async (content: string) => {
      if (!activeAgent) {
        apiLogger.warn("sendChatMessage skipped: no active agent");
        return;
      }
      const focusOn = useChatStore.getState().focusMode;
      const finalContent = focusOn && anchorCandidate
        ? `${buildAnchorMarkdown(anchorCandidate)}\n\n${content}`
        : content;

      let sessionId = activeSessionId;
      const isNewSession = !sessionId;

      apiLogger.info("sendChatMessage.start", {
        wsId,
        sessionId,
        isNewSession,
        agentId: activeAgent.id,
        contentLength: finalContent.length,
      });

      if (!sessionId) {
        // Lane bootstrap: prefer find-or-create so reopening a workspace lane
        // resumes the same (workspace, user, agent) thread instead of forking
        // a new session each time. Inside `/chat` this collapses to the same
        // single-session-per-agent shape the page already has, so there's no
        // visible difference at that route.
        const session = await findOrCreate.mutateAsync({
          agent_id: activeAgent.id,
          title: finalContent.slice(0, 50),
        });
        sessionId = session.id;
        setActiveSession(wsId, sessionId);
      }

      const optimistic: ChatMessage = {
        id: `optimistic-${Date.now()}`,
        chat_session_id: sessionId,
        role: "user",
        content: finalContent,
        task_id: null,
        created_at: new Date().toISOString(),
      };
      qc.setQueryData<ChatMessage[]>(
        chatKeys.messages(sessionId),
        (old) => (old ? [...old, optimistic] : [optimistic]),
      );

      const reqOpts = workspaceSlug ? { workspaceSlug } : undefined;
      const result = await api.sendChatMessage(sessionId, finalContent, reqOpts);
      qc.setQueryData(chatKeys.pendingTask(sessionId), {
        task_id: result.task_id,
        status: "queued",
      });
      qc.invalidateQueries({ queryKey: chatKeys.messages(sessionId) });
    },
    [
      activeSessionId,
      activeAgent,
      anchorCandidate,
      findOrCreate,
      setActiveSession,
      qc,
      wsId,
      workspaceSlug,
    ],
  );

  const handleStop = useCallback(async () => {
    if (!pendingTaskId) return;
    try {
      const reqOpts = workspaceSlug ? { workspaceSlug } : undefined;
      await api.cancelTaskById(pendingTaskId, reqOpts);
    } catch (err) {
      apiLogger.warn("cancelTask.error", { taskId: pendingTaskId, err });
    }
    if (activeSessionId) {
      qc.setQueryData(chatKeys.pendingTask(activeSessionId), {});
      qc.invalidateQueries({ queryKey: chatKeys.messages(activeSessionId) });
    }
  }, [pendingTaskId, activeSessionId, qc, workspaceSlug]);

  const handleSelectAgent = useCallback(
    (agent: Agent) => {
      if (activeAgent && agent.id === activeAgent.id) return;
      uiLogger.info("selectAgent", { wsId, from: selectedAgentId, to: agent.id });
      setSelectedAgentId(wsId, agent.id);
      setActiveSession(wsId, null);
    },
    [activeAgent, selectedAgentId, setSelectedAgentId, setActiveSession, wsId],
  );

  const handleNewChat = useCallback(() => {
    setActiveSession(wsId, null);
  }, [setActiveSession, wsId]);

  const handleSelectSession = useCallback(
    (session: ChatSession) => {
      if (activeAgent && session.agent_id !== activeAgent.id) {
        setSelectedAgentId(wsId, session.agent_id);
      }
      setActiveSession(wsId, session.id);
    },
    [activeAgent, setSelectedAgentId, setActiveSession, wsId],
  );

  const hasMessages = messages.length > 0 || !!pendingTaskId;
  const activeTitle = currentSession?.title?.trim() || "New chat";

  // ContextAnchorCard / Button reach into useChatStore.lastAnchorLocation
  // and useWorkspaceId() — when the lane runs outside its workspace's
  // route (global-chat lanes), the ambient anchor doesn't apply, so the
  // card and button are disabled. The components themselves no-op when
  // there's no candidate; we omit them via variant=compact to avoid the
  // useWorkspaceId() throw.
  const isPageVariant = variant === "page";

  return (
    <div className="flex h-full min-h-0 flex-1 flex-col">
      {isPageVariant && (
        <PageHeader className="gap-2">
          <span className="text-sm font-medium truncate">{activeTitle}</span>
          <div className="ml-auto flex items-center gap-1">
            <HistoryPopover
              sessions={allSessions}
              agents={agents}
              activeSessionId={activeSessionId}
              onSelectSession={handleSelectSession}
            />
            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    className="text-muted-foreground"
                    onClick={handleNewChat}
                    aria-label="New chat"
                  />
                }
              >
                <Plus />
              </TooltipTrigger>
              <TooltipContent side="bottom">New chat</TooltipContent>
            </Tooltip>
          </div>
        </PageHeader>
      )}

      <div className="relative flex min-h-0 flex-1 flex-col">
        {showSkeleton ? (
          <div className="mx-auto flex w-full max-w-3xl flex-1 flex-col">
            <ChatMessageSkeleton />
          </div>
        ) : hasMessages ? (
          <div className="mx-auto flex w-full max-w-3xl flex-1 flex-col min-h-0">
            <ChatMessageList
              messages={messages}
              pendingTaskId={pendingTaskId}
              isWaiting={!!pendingTaskId}
            />
          </div>
        ) : (
          <EmptyState agentName={activeAgent?.name} onPickPrompt={handleSend} />
        )}

        <div className="mx-auto w-full max-w-3xl pb-4">
          <ChatInput
            onSend={handleSend}
            onStop={handleStop}
            isRunning={!!pendingTaskId}
            disabled={isSessionArchived}
            agentName={activeAgent?.name}
            activeSessionId={activeSessionId}
            selectedAgentId={selectedAgentId}
            topSlot={isPageVariant ? <ContextAnchorCard /> : null}
            leftAdornment={
              <AgentDropdown
                agents={availableAgents}
                activeAgent={activeAgent}
                userId={user?.id}
                onSelect={handleSelectAgent}
              />
            }
            rightAdornment={isPageVariant ? <ContextAnchorButton /> : null}
          />
        </div>
      </div>
    </div>
  );
}

const PEPPER_AGENT_NAME = /^Pepper(\s|\[|EX|$)/i;

/**
 * Pick a sensible default agent for a freshly opened lane (ADR D5):
 *
 *   1. First non-archived agent whose name matches Pepper [WS] / Pepper EX.
 *   2. Else the first non-archived available agent.
 *   3. Else null — caller renders the empty state.
 *
 * `agents` is expected to already be filtered through `canAssignAgent` and
 * exclude archived rows; this helper just runs the priority match.
 */
export function pickDefaultLaneAgent(agents: Agent[]): Agent | null {
  if (agents.length === 0) return null;
  const pepper = agents.find((a) => PEPPER_AGENT_NAME.test(a.name));
  if (pepper) return pepper;
  return agents[0] ?? null;
}

/**
 * Popover-based history list. Per product direction, session history lives
 * inside the Chat tab — not in the global sidebar — so that Multica doesn't
 * read as "just another chat app." The trigger is a History icon in the
 * page header.
 */
function HistoryPopover({
  sessions,
  agents,
  activeSessionId,
  onSelectSession,
}: {
  sessions: ChatSession[];
  agents: Agent[];
  activeSessionId: string | null;
  onSelectSession: (session: ChatSession) => void;
}) {
  const [open, setOpen] = React.useState(false);
  const agentById = useMemo(() => new Map(agents.map((a) => [a.id, a])), [agents]);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <Tooltip>
        <TooltipTrigger
          render={
            <PopoverTrigger
              render={
                <Button
                  variant="ghost"
                  size="icon-sm"
                  className="text-muted-foreground"
                  aria-label="History"
                />
              }
            />
          }
        >
          <History />
        </TooltipTrigger>
        <TooltipContent side="bottom">History</TooltipContent>
      </Tooltip>
      <PopoverContent align="end" sideOffset={6} className="w-80 p-0">
        <div className="px-3 py-2 border-b">
          <span className="text-xs font-medium text-muted-foreground">History</span>
        </div>
        <div className="max-h-96 overflow-y-auto">
          {sessions.length === 0 ? (
            <div className="px-3 py-6 text-center text-xs text-muted-foreground">
              No previous chats
            </div>
          ) : (
            sessions.map((session) => {
              const isCurrent = session.id === activeSessionId;
              const agent = agentById.get(session.agent_id) ?? null;
              return (
                <button
                  key={session.id}
                  type="button"
                  onClick={() => {
                    onSelectSession(session);
                    setOpen(false);
                  }}
                  className={cn(
                    "flex w-full items-start gap-2 px-3 py-2 text-left transition-colors hover:bg-accent/60",
                    isCurrent && "bg-accent/40",
                  )}
                >
                  <AgentAvatarSmall agent={agent} />
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-sm">
                      {session.title?.trim() || "New chat"}
                    </div>
                    <div className="truncate text-xs text-muted-foreground">
                      {agent?.name ?? "Unknown agent"}
                    </div>
                  </div>
                  {session.has_unread && (
                    <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-brand" />
                  )}
                  {isCurrent && (
                    <Check className="mt-1 size-3.5 shrink-0 text-muted-foreground" />
                  )}
                </button>
              );
            })
          )}
        </div>
      </PopoverContent>
    </Popover>
  );
}

function AgentDropdown({
  agents,
  activeAgent,
  userId,
  onSelect,
}: {
  agents: Agent[];
  activeAgent: Agent | null;
  userId: string | undefined;
  onSelect: (agent: Agent) => void;
}) {
  const { mine, others } = useMemo(() => {
    const mine: Agent[] = [];
    const others: Agent[] = [];
    for (const a of agents) {
      if (a.owner_id === userId) mine.push(a);
      else others.push(a);
    }
    return { mine, others };
  }, [agents, userId]);

  if (!activeAgent) {
    return <span className="text-xs text-muted-foreground">No agents</span>;
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger className="flex items-center gap-1.5 rounded-md px-1.5 py-1 -ml-1 cursor-pointer outline-none transition-colors hover:bg-accent aria-expanded:bg-accent">
        <AgentAvatarSmall agent={activeAgent} />
        <span className="text-xs font-medium max-w-28 truncate">{activeAgent.name}</span>
        <ChevronDown className="size-3 text-muted-foreground shrink-0" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" side="top" className="max-h-80 w-auto max-w-64">
        {mine.length > 0 && (
          <DropdownMenuGroup>
            <DropdownMenuLabel>My agents</DropdownMenuLabel>
            {mine.map((agent) => (
              <AgentMenuItem
                key={agent.id}
                agent={agent}
                isCurrent={agent.id === activeAgent.id}
                onSelect={onSelect}
              />
            ))}
          </DropdownMenuGroup>
        )}
        {mine.length > 0 && others.length > 0 && <DropdownMenuSeparator />}
        {others.length > 0 && (
          <DropdownMenuGroup>
            <DropdownMenuLabel>Others</DropdownMenuLabel>
            {others.map((agent) => (
              <AgentMenuItem
                key={agent.id}
                agent={agent}
                isCurrent={agent.id === activeAgent.id}
                onSelect={onSelect}
              />
            ))}
          </DropdownMenuGroup>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function AgentMenuItem({
  agent,
  isCurrent,
  onSelect,
}: {
  agent: Agent;
  isCurrent: boolean;
  onSelect: (agent: Agent) => void;
}) {
  return (
    <DropdownMenuItem
      onClick={() => onSelect(agent)}
      className="flex min-w-0 items-center gap-2"
    >
      <AgentAvatarSmall agent={agent} />
      <span className="truncate flex-1">{agent.name}</span>
      {isCurrent && <Check className="size-3.5 text-muted-foreground shrink-0" />}
    </DropdownMenuItem>
  );
}

function AgentAvatarSmall({ agent }: { agent: Agent | null }) {
  return (
    <Avatar className="size-6 shrink-0">
      {agent?.avatar_url && <AvatarImage src={agent.avatar_url} />}
      <AvatarFallback className="bg-purple-100 text-purple-700">
        <Bot className="size-3.5" />
      </AvatarFallback>
    </Avatar>
  );
}

const STARTER_PROMPTS: { icon: string; text: string }[] = [
  { icon: "📋", text: "List my open tasks by priority" },
  { icon: "📝", text: "Summarize what I did today" },
  { icon: "💡", text: "Plan what to work on next" },
];

function EmptyState({
  agentName,
  onPickPrompt,
}: {
  agentName?: string;
  onPickPrompt: (text: string) => void;
}) {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-6 px-6 py-12">
      <div className="text-center space-y-1">
        <h3 className="text-xl font-semibold">
          {agentName ? `Hi, I'm ${agentName}` : "Welcome to Multica"}
        </h3>
        <p className="text-sm text-muted-foreground">How can I help?</p>
      </div>
      <div className="w-full max-w-md space-y-2">
        {STARTER_PROMPTS.map((prompt) => (
          <button
            key={prompt.text}
            type="button"
            onClick={() => onPickPrompt(prompt.text)}
            className="w-full rounded-lg border border-border bg-card px-3 py-2 text-left text-sm text-foreground transition-colors hover:bg-accent hover:border-brand/40"
          >
            <span className="mr-2">{prompt.icon}</span>
            {prompt.text}
          </button>
        ))}
      </div>
    </div>
  );
}
