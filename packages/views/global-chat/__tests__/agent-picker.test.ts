import { describe, it, expect, beforeEach } from "vitest";
import type { Agent } from "@multica/core/types";
import {
  PICKER_STORAGE_KEY,
  readStoredAgentId,
  resolveDefaultAgentId,
  writeStoredAgentId,
} from "../agent-picker";

const CLAUDE_ID = "11111111-1111-4111-8111-111111111111";
const TWIN_ID = "22222222-2222-4222-8222-222222222222";
const ARCHIVED_ID = "33333333-3333-4333-8333-333333333333";

function makeAgent(over: Partial<Agent> = {}): Agent {
  return {
    id: CLAUDE_ID,
    workspace_id: "ws-global",
    runtime_id: "runtime-1",
    name: "Claude Code (terminator-9999)",
    description: "",
    instructions: "",
    avatar_url: null,
    runtime_mode: "cloud",
    runtime_config: {},
    custom_env: {},
    custom_args: [],
    custom_env_redacted: false,
    visibility: "private",
    status: "active",
    max_concurrent_tasks: 1,
    model: "claude-opus-4-7",
    owner_id: "u-1",
    skills: [],
    created_at: "2026-04-28T00:00:00Z",
    updated_at: "2026-04-28T00:00:00Z",
    archived_at: null,
    archived_by: null,
    ...over,
  } as Agent;
}

describe("resolveDefaultAgentId", () => {
  const agents: Agent[] = [
    makeAgent({ id: TWIN_ID, name: "Cuong Pho" }),
    makeAgent({ id: CLAUDE_ID, name: "Claude Code (terminator-9999)" }),
    makeAgent({
      id: ARCHIVED_ID,
      name: "Old Agent",
      archived_at: "2026-04-01T00:00:00Z",
    }),
  ];

  it("returns the stored UUID when it points at an available agent", () => {
    expect(resolveDefaultAgentId(agents, TWIN_ID)).toBe(TWIN_ID);
  });

  it("ignores a stored UUID that points at an archived agent and falls through to Claude Code", () => {
    expect(resolveDefaultAgentId(agents, ARCHIVED_ID)).toBe(CLAUDE_ID);
  });

  it("falls through to Claude Code by name when storage is empty", () => {
    expect(resolveDefaultAgentId(agents, null)).toBe(CLAUDE_ID);
  });

  it("falls through to Claude Code by name when the stored value is malformed", () => {
    expect(resolveDefaultAgentId(agents, "not-a-uuid")).toBe(CLAUDE_ID);
  });

  it("returns the first non-archived agent when no Claude Code is present", () => {
    const noClaude: Agent[] = [
      makeAgent({ id: TWIN_ID, name: "Cuong Pho" }),
      makeAgent({
        id: ARCHIVED_ID,
        name: "Old Agent",
        archived_at: "2026-04-01T00:00:00Z",
      }),
    ];
    expect(resolveDefaultAgentId(noClaude, null)).toBe(TWIN_ID);
  });

  it("returns null when every agent is archived (empty-state branch)", () => {
    const allArchived: Agent[] = [
      makeAgent({
        id: CLAUDE_ID,
        name: "Claude Code",
        archived_at: "2026-04-01T00:00:00Z",
      }),
    ];
    expect(resolveDefaultAgentId(allArchived, null)).toBeNull();
  });

  it("returns null on empty input", () => {
    expect(resolveDefaultAgentId([], null)).toBeNull();
  });

  it("matches Claude Code case-insensitively and tolerates the runtime suffix", () => {
    const variant: Agent[] = [makeAgent({ name: "claude code (foo)" })];
    expect(resolveDefaultAgentId(variant, null)).toBe(CLAUDE_ID);
  });
});

describe("readStoredAgentId / writeStoredAgentId", () => {
  function makeStorage(initial: Record<string, string> = {}): Storage {
    const data = new Map(Object.entries(initial));
    return {
      get length() {
        return data.size;
      },
      clear: () => data.clear(),
      getItem: (k) => data.get(k) ?? null,
      key: (i) => Array.from(data.keys())[i] ?? null,
      removeItem: (k) => {
        data.delete(k);
      },
      setItem: (k, v) => {
        data.set(k, v);
      },
    };
  }

  beforeEach(() => {
    try {
      window.localStorage.clear();
    } catch {
      /* jsdom may throw if storage is shimmed */
    }
  });

  it("round-trips a UUID through storage", () => {
    const s = makeStorage();
    writeStoredAgentId(CLAUDE_ID, s);
    expect(readStoredAgentId(s)).toBe(CLAUDE_ID);
  });

  it("returns null for missing keys", () => {
    expect(readStoredAgentId(makeStorage())).toBeNull();
  });

  it("returns null for non-UUID raw values", () => {
    const s = makeStorage({ [PICKER_STORAGE_KEY]: "garbage" });
    expect(readStoredAgentId(s)).toBeNull();
  });

  it("accepts a JSON-encoded UUID for forward compat", () => {
    const s = makeStorage({ [PICKER_STORAGE_KEY]: JSON.stringify(TWIN_ID) });
    expect(readStoredAgentId(s)).toBe(TWIN_ID);
  });

  it("returns null when storage is unavailable", () => {
    expect(readStoredAgentId(null)).toBeNull();
    // Write is a no-op rather than throwing.
    expect(() => writeStoredAgentId(CLAUDE_ID, null)).not.toThrow();
  });
});
