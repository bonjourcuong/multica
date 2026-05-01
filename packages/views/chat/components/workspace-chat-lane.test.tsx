import { describe, it, expect, vi, beforeEach } from "vitest";
import { render } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  pickDefaultLaneAgent,
  WorkspaceChatLane,
} from "./workspace-chat-lane";
import { createChatStore, registerChatStore } from "@multica/core/chat";
import type { Agent } from "@multica/core/types";

// Stub realtime + chat input editor — both pull in browser-only APIs
// (WebSocket, contenteditable) we don't need for a smoke test.
vi.mock("@multica/core/realtime", () => ({
  useWSEvent: () => undefined,
}));

vi.mock("../../editor", () => ({
  ContentEditor: () => null,
}));

// Minimal API stub. The lane's queries fire on mount — we resolve them with
// empty data so render doesn't throw.
const listAgents = vi.fn(
  async (_filter?: unknown, _opts?: { workspaceSlug?: string }) =>
    [] as unknown[],
);
const listMembers = vi.fn(async (_wsId?: string) => [] as unknown[]);
const listChatSessions = vi.fn(
  async (_filter?: unknown, _opts?: { workspaceSlug?: string }) =>
    [] as unknown[],
);

vi.mock("@multica/core/api", () => ({
  api: {
    listAgents: (filter?: unknown, opts?: { workspaceSlug?: string }) =>
      listAgents(filter, opts),
    listMembers: (wsId?: string) => listMembers(wsId),
    listChatSessions: (filter?: unknown, opts?: { workspaceSlug?: string }) =>
      listChatSessions(filter, opts),
    listChatMessages: async () => [],
    getPendingChatTask: async () => ({}),
    sendChatMessage: async () => ({ task_id: "t-1", status: "queued" }),
    cancelTaskById: async () => undefined,
    findOrCreateChatSession: async () => ({
      id: "s-1",
      agent_id: "a-1",
      created_at: "2026-05-01T00:00:00Z",
      title: "",
      status: "active",
      has_unread: false,
    }),
    markChatSessionRead: async () => undefined,
  },
}));

// `useCurrentWorkspace` reads from a Context the lane doesn't set up here;
// returning null lets the mutation hooks fall through to opts.wsId, which
// is exactly the global-chat lane code path under test.
vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => null,
  useWorkspaceSlug: () => null,
  useWorkspacePaths: () => ({
    issueDetail: (id: string) => `/issues/${id}`,
    projectDetail: (id: string) => `/projects/${id}`,
  }),
}));

// useWorkspaceId throws by default outside a workspace route — the page
// variant of the lane uses ContextAnchorButton/Card which call this hook.
// Returning the lane's workspaceId mirrors what `[workspaceSlug]/layout.tsx`
// would resolve in a real /chat render.
vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-a",
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (s: { user: { id: string } }) => unknown) =>
    selector({ user: { id: "u-1" } }),
}));

beforeEach(() => {
  // Each test gets a fresh chat store so per-workspace state doesn't bleed.
  const storage = {
    getItem: () => null,
    setItem: () => {},
    removeItem: () => {},
  };
  registerChatStore(createChatStore({ storage }));
  listAgents.mockClear();
  listMembers.mockClear();
  listChatSessions.mockClear();
});

function renderLane(props: { variant: "page" | "compact" }) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <WorkspaceChatLane
        workspaceId="ws-a"
        workspaceSlug="ws-a-slug"
        variant={props.variant}
      />
    </QueryClientProvider>,
  );
}

describe("WorkspaceChatLane (compact / global-chat lane variant)", () => {
  // The page variant pulls in ContextAnchorButton/Card which require a real
  // NavigationProvider; that path is the unchanged /chat render and stays
  // covered by the existing /chat E2E surface. This spec focuses on the
  // new compact path used by /global/chat lanes.

  it("mounts without throwing", () => {
    expect(() => renderLane({ variant: "compact" })).not.toThrow();
  });

  it("forwards the workspace slug to the agent list query", async () => {
    renderLane({ variant: "compact" });
    // Flush microtasks so the queries actually fire.
    await Promise.resolve();
    expect(listAgents).toHaveBeenCalled();
    const lastCall = listAgents.mock.calls[listAgents.mock.calls.length - 1];
    // Second arg is the per-call request options carrying workspaceSlug.
    expect(lastCall?.[1]).toMatchObject({ workspaceSlug: "ws-a-slug" });
  });

  it("forwards the workspace slug to the chat sessions query", async () => {
    renderLane({ variant: "compact" });
    await Promise.resolve();
    expect(listChatSessions).toHaveBeenCalled();
    const lastCall =
      listChatSessions.mock.calls[listChatSessions.mock.calls.length - 1];
    expect(lastCall?.[1]).toMatchObject({ workspaceSlug: "ws-a-slug" });
  });
});

describe("pickDefaultLaneAgent (ADR D5)", () => {
  function agent(over: Partial<Agent>): Agent {
    return {
      id: "a-1",
      workspace_id: "ws-1",
      owner_id: "u-1",
      name: "Some Agent",
      description: null,
      avatar_url: null,
      visibility: "public",
      runtime_id: null,
      runtime_kind: null,
      model_id: null,
      created_at: "2026-04-28T00:00:00Z",
      updated_at: "2026-04-28T00:00:00Z",
      archived_at: null,
      ...over,
    } as Agent;
  }

  it("returns null when no agents are available", () => {
    expect(pickDefaultLaneAgent([])).toBeNull();
  });

  it("prefers an agent whose name matches the Pepper family regex", () => {
    const others = [agent({ id: "a-1", name: "Tony Backend" })];
    const pepper = agent({ id: "a-2", name: "Pepper [WS]" });
    expect(pickDefaultLaneAgent([...others, pepper])?.id).toBe("a-2");
  });

  it("matches Pepper EX as the secondary form", () => {
    const pepper = agent({ id: "a-3", name: "Pepper EX [MF]" });
    const others = [agent({ id: "a-1", name: "Tony Backend" })];
    expect(pickDefaultLaneAgent([pepper, ...others])?.id).toBe("a-3");
  });

  it("falls back to the first agent when no Pepper is present", () => {
    const a = agent({ id: "a-1", name: "Tony Backend" });
    const b = agent({ id: "a-2", name: "Bruce QA" });
    expect(pickDefaultLaneAgent([a, b])?.id).toBe("a-1");
  });

  it("does not match an unrelated 'Pepperoni'-style false positive", () => {
    // Regex is /^Pepper(\s|\[|EX|$)/i — `Pepperoni` should not qualify
    // because it neither ends after `Pepper` nor follows it with whitespace,
    // a bracket, or `EX`.
    const a = agent({ id: "a-1", name: "Pepperoni" });
    const b = agent({ id: "a-2", name: "Pepper [Sales]" });
    expect(pickDefaultLaneAgent([a, b])?.id).toBe("a-2");
  });
});
