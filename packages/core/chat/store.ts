import { create } from "zustand";
import type { StorageAdapter } from "../types";
import { createLogger } from "../logger";

const logger = createLogger("chat.store");

const STATE_KEY = "multica:chat:byWorkspace";
const DRAFTS_KEY = "multica:chat:drafts";
const FOCUS_MODE_KEY = "multica:chat:focusMode";
/** Placeholder sessionId for a chat that hasn't been created yet. */
export const DRAFT_NEW_SESSION = "__new__";

export interface ChatWorkspaceEntry {
  activeSessionId: string | null;
  selectedAgentId: string | null;
}

const EMPTY_ENTRY: ChatWorkspaceEntry = {
  activeSessionId: null,
  selectedAgentId: null,
};

/**
 * Kept as a public type because existing consumers (chat-message-list,
 * views/chat types) import it. Items themselves no longer live in the
 * store — they flow through the React Query cache keyed by task id.
 */
export interface ChatTimelineItem {
  seq: number;
  type: "tool_use" | "tool_result" | "thinking" | "text" | "error";
  tool?: string;
  content?: string;
  input?: Record<string, unknown>;
  output?: string;
}

/**
 * A derived "where I am" pointer — not stored, recomputed each render from
 * the current route + react-query cache. The type is exported because
 * consumers (buildAnchorMarkdown, chip props) share the same shape.
 */
export interface ContextAnchor {
  type: "issue" | "project";
  /** UUID for `issue`, UUID for `project`. */
  id: string;
  /** Human-readable label: issue identifier (MUL-1) or project title. */
  label: string;
  /** Optional secondary text — issue title for issue anchors. */
  subtitle?: string;
}

export interface ChatState {
  /**
   * Per-workspace chat pointer. Each workspace tracks its own active session
   * and selected agent so the global-chat V2 lanes can hold multiple
   * workspaces' chats live simultaneously without trampling each other.
   */
  byWorkspace: Record<string, ChatWorkspaceEntry>;
  /** Drafts per session: sessionId (or DRAFT_NEW_SESSION:agentId) → markdown. */
  inputDrafts: Record<string, string>;
  /**
   * When on, the chat tracks whatever issue/project/inbox-item the user is
   * looking at and prepends it to outgoing messages. Persisted globally so
   * the preference survives workspace switches and reloads.
   */
  focusMode: boolean;
  /**
   * Last location where a context anchor could be derived (issue/project/inbox).
   * Updated globally by useAnchorTracker; used as a fallback for the Chat page
   * which is its own route and therefore has no anchor of its own.
   * Not persisted — resets per session; focus mode itself persists.
   */
  lastAnchorLocation: { pathname: string; search: string } | null;
  setActiveSession: (wsId: string, sessionId: string | null) => void;
  setSelectedAgentId: (wsId: string, agentId: string) => void;
  setInputDraft: (sessionKey: string, draft: string) => void;
  clearInputDraft: (sessionKey: string) => void;
  setFocusMode: (on: boolean) => void;
  setLastAnchorLocation: (loc: { pathname: string; search: string } | null) => void;
}

export interface ChatStoreOptions {
  storage: StorageAdapter;
}

function readJson<T>(storage: StorageAdapter, key: string, fallback: T): T {
  const raw = storage.getItem(key);
  if (!raw) return fallback;
  try {
    const parsed = JSON.parse(raw);
    return parsed && typeof parsed === "object" ? (parsed as T) : fallback;
  } catch {
    return fallback;
  }
}

function writeJson(storage: StorageAdapter, key: string, value: unknown) {
  if (
    value &&
    typeof value === "object" &&
    Object.keys(value as Record<string, unknown>).length === 0
  ) {
    storage.removeItem(key);
    return;
  }
  storage.setItem(key, JSON.stringify(value));
}

function pruneDrafts(drafts: Record<string, string>): Record<string, string> {
  const pruned: Record<string, string> = {};
  for (const [k, v] of Object.entries(drafts)) {
    if (v) pruned[k] = v;
  }
  return pruned;
}

export function createChatStore(options: ChatStoreOptions) {
  const { storage } = options;

  const initialByWorkspace = readJson<Record<string, ChatWorkspaceEntry>>(
    storage,
    STATE_KEY,
    {},
  );
  const initialDrafts = readJson<Record<string, string>>(storage, DRAFTS_KEY, {});

  return create<ChatState>((set, get) => ({
    byWorkspace: initialByWorkspace,
    inputDrafts: initialDrafts,
    focusMode: storage.getItem(FOCUS_MODE_KEY) === "true",
    lastAnchorLocation: null,
    setLastAnchorLocation: (loc) => set({ lastAnchorLocation: loc }),
    setActiveSession: (wsId, sessionId) => {
      const prev = get().byWorkspace[wsId] ?? EMPTY_ENTRY;
      logger.info("setActiveSession", {
        wsId,
        from: prev.activeSessionId,
        to: sessionId,
      });
      const nextEntry: ChatWorkspaceEntry = {
        ...prev,
        activeSessionId: sessionId,
      };
      const next = { ...get().byWorkspace, [wsId]: nextEntry };
      writeJson(storage, STATE_KEY, next);
      set({ byWorkspace: next });
    },
    setSelectedAgentId: (wsId, agentId) => {
      const prev = get().byWorkspace[wsId] ?? EMPTY_ENTRY;
      logger.info("setSelectedAgentId", {
        wsId,
        from: prev.selectedAgentId,
        to: agentId,
      });
      const nextEntry: ChatWorkspaceEntry = {
        ...prev,
        selectedAgentId: agentId,
      };
      const next = { ...get().byWorkspace, [wsId]: nextEntry };
      writeJson(storage, STATE_KEY, next);
      set({ byWorkspace: next });
    },
    setInputDraft: (sessionKey, draft) => {
      // Debug level — onUpdate fires on every keystroke.
      logger.debug("setInputDraft", { sessionKey, length: draft.length });
      const next = pruneDrafts({ ...get().inputDrafts, [sessionKey]: draft });
      writeJson(storage, DRAFTS_KEY, next);
      set({ inputDrafts: next });
    },
    setFocusMode: (on) => {
      logger.info("setFocusMode", { to: on });
      if (on) storage.setItem(FOCUS_MODE_KEY, "true");
      else storage.removeItem(FOCUS_MODE_KEY);
      set({ focusMode: on });
    },
    clearInputDraft: (sessionKey) => {
      const current = get().inputDrafts;
      if (!(sessionKey in current)) {
        logger.debug("clearInputDraft skipped (no draft)", { sessionKey });
        return;
      }
      logger.info("clearInputDraft", { sessionKey });
      const next = { ...current };
      delete next[sessionKey];
      const pruned = pruneDrafts(next);
      writeJson(storage, DRAFTS_KEY, pruned);
      set({ inputDrafts: pruned });
    },
  }));
}

/**
 * Read-only selector helper: returns the workspace's chat entry, or an
 * empty default if the workspace has no recorded state yet. Use from
 * components via `useChatStore(selectWorkspaceEntry(wsId))`.
 *
 * Returning a stable reference for the empty case is important — Zustand
 * will rerender on identity changes, and we don't want a fresh object on
 * every keystroke just because this workspace has never been touched.
 */
export function selectWorkspaceEntry(wsId: string) {
  return (s: ChatState): ChatWorkspaceEntry =>
    s.byWorkspace[wsId] ?? EMPTY_ENTRY;
}
