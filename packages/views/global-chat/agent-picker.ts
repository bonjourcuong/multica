import type { Agent } from "@multica/core/types";

/** Versioned localStorage key for the global-chat agent picker selection. */
export const PICKER_STORAGE_KEY = "multica.global-chat.selected-agent.v1";

const UUID_RE =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const CLAUDE_CODE_NAME = /^Claude Code\b/i;

function isAvailable(agent: Agent): boolean {
  return agent.archived_at === null;
}

/**
 * Default-selection resolver for the global-chat agent picker. Resolved on
 * mount and again whenever the agent list refetches; the result becomes the
 * `selectedAgentId` state in `GlobalChatPane`.
 *
 * Priority (per V3 ADR D1):
 *   1. `storedId` from localStorage if it's a UUID and matches an agent in the list.
 *   2. First agent whose name matches `/^Claude Code\b/i`.
 *   3. First non-archived agent.
 *   4. `null` — caller renders the empty state.
 *
 * Archived agents are never auto-selected, even if their UUID is the stored
 * value (defensive: archive state can flip server-side between sessions).
 */
export function resolveDefaultAgentId(
  agents: Agent[],
  storedId: string | null,
): string | null {
  if (storedId && UUID_RE.test(storedId)) {
    const stored = agents.find((a) => a.id === storedId);
    if (stored && isAvailable(stored)) return stored.id;
  }
  const claude = agents.find(
    (a) => isAvailable(a) && CLAUDE_CODE_NAME.test(a.name),
  );
  if (claude) return claude.id;
  const firstAvailable = agents.find(isAvailable);
  if (firstAvailable) return firstAvailable.id;
  return null;
}

/**
 * Reads and validates the persisted picker selection. Returns null if the
 * blob is missing, malformed, or storage is unavailable (private mode,
 * SSR). Parse failures fall through to the resolver's default branch.
 */
export function readStoredAgentId(storage?: Storage | null): string | null {
  const s = storage === undefined ? safeStorage() : storage;
  if (!s) return null;
  try {
    const raw = s.getItem(PICKER_STORAGE_KEY);
    if (!raw) return null;
    if (UUID_RE.test(raw)) return raw;
    const parsed = JSON.parse(raw);
    if (typeof parsed === "string" && UUID_RE.test(parsed)) return parsed;
    return null;
  } catch {
    return null;
  }
}

export function writeStoredAgentId(
  agentId: string,
  storage?: Storage | null,
): void {
  const s = storage === undefined ? safeStorage() : storage;
  if (!s) return;
  try {
    s.setItem(PICKER_STORAGE_KEY, agentId);
  } catch {
    // Quota / private mode — best-effort, do not throw.
  }
}

function safeStorage(): Storage | null {
  try {
    if (typeof window === "undefined") return null;
    return window.localStorage;
  } catch {
    return null;
  }
}
