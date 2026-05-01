import React from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type {
  SendGlobalChatMessageRequest,
  SendGlobalChatMessageResponse,
} from "@multica/core/api";
import type { Agent, GlobalChatMessage } from "@multica/core/types";
import { GlobalChatPane } from "../global-chat-pane";
import { PICKER_STORAGE_KEY } from "../agent-picker";

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (s: { user: { id: string } }) => unknown) =>
    selector({ user: { id: "u-1" } }),
}));

// Flatten the Base UI dropdown into inline elements so jsdom doesn't have to
// resolve portals or open animations — the menu items become regular buttons
// that respond to a single click. This matches the pattern used in
// `packages/views/modals/create-issue.test.tsx`.
vi.mock("@multica/ui/components/ui/dropdown-menu", () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuTrigger: ({
    children,
    ...props
  }: { children: React.ReactNode } & React.ButtonHTMLAttributes<HTMLButtonElement>) => (
    <button type="button" {...props}>
      {children}
    </button>
  ),
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => (
    <div role="menu">{children}</div>
  ),
  DropdownMenuGroup: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuLabel: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
  DropdownMenuItem: ({
    children,
    onClick,
  }: {
    children: React.ReactNode;
    onClick?: () => void;
  }) => (
    <button type="button" onClick={onClick}>
      {children}
    </button>
  ),
  DropdownMenuSeparator: () => null,
}));

const sendGlobalChatMessage =
  vi.fn<(payload: SendGlobalChatMessageRequest) => Promise<SendGlobalChatMessageResponse>>();
const listGlobalChatMessages = vi.fn<() => Promise<GlobalChatMessage[]>>();
const listGlobalChatAgents = vi.fn<() => Promise<Agent[]>>();
const getGlobalChatSession = vi.fn(() =>
  Promise.resolve({
    id: "ses-1",
    user_id: "u-1",
    agent_id: "a-1",
    created_at: "2026-04-28T00:00:00Z",
    archived_at: null,
  }),
);

vi.mock("@multica/core/api", () => ({
  api: {
    sendGlobalChatMessage: (payload: SendGlobalChatMessageRequest) =>
      sendGlobalChatMessage(payload),
    listGlobalChatMessages: () => listGlobalChatMessages(),
    listGlobalChatAgents: () => listGlobalChatAgents(),
    getGlobalChatSession: () => getGlobalChatSession(),
    bootstrapGlobalChatSession: () => getGlobalChatSession(),
  },
}));

const CLAUDE_ID = "11111111-1111-4111-8111-111111111111";
const TWIN_ID = "22222222-2222-4222-8222-222222222222";

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

function makeMessage(over: Partial<GlobalChatMessage> = {}): GlobalChatMessage {
  return {
    id: "msg-1",
    global_session_id: "ses-1",
    author_kind: "user",
    author_id: "u-1",
    body: "hello",
    metadata: {},
    dispatched_to: [],
    created_at: "2026-04-28T00:00:00Z",
    ...over,
  };
}

function makeSendResponse(
  message: GlobalChatMessage,
  over: Partial<SendGlobalChatMessageResponse> = {},
): SendGlobalChatMessageResponse {
  return { message, dispatch: [], mentions: [], ...over };
}

function renderPane(props: Parameters<typeof GlobalChatPane>[0] = {}) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <GlobalChatPane {...props} />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  sendGlobalChatMessage.mockReset();
  listGlobalChatMessages.mockReset();
  listGlobalChatMessages.mockResolvedValue([]);
  listGlobalChatAgents.mockReset();
  listGlobalChatAgents.mockResolvedValue([
    makeAgent(),
    makeAgent({ id: TWIN_ID, name: "Cuong Pho", owner_id: null }),
  ]);
  window.localStorage.clear();
});

describe("GlobalChatPane", () => {
  it("submits the draft on Enter, clears the input, and calls the API with selected agent", async () => {
    sendGlobalChatMessage.mockResolvedValue(
      makeSendResponse(makeMessage({ body: "hello" })),
    );

    const { getByTestId, findAllByText } = renderPane();
    // Wait for the picker to resolve to its default (Claude Code).
    await findAllByText("Claude Code (terminator-9999)");

    const input = getByTestId("global-chat-input") as HTMLTextAreaElement;
    fireEvent.change(input, { target: { value: "hello" } });
    fireEvent.keyDown(input, { key: "Enter" });

    await waitFor(() =>
      expect(sendGlobalChatMessage).toHaveBeenCalledWith({
        body: "hello",
        agent_id: CLAUDE_ID,
      }),
    );
    expect(input.value).toBe("");
  });

  it("does not submit when the draft is empty or whitespace-only", async () => {
    const { getByTestId, findAllByText } = renderPane();
    await findAllByText("Claude Code (terminator-9999)");
    const input = getByTestId("global-chat-input") as HTMLTextAreaElement;

    fireEvent.change(input, { target: { value: "   " } });
    fireEvent.keyDown(input, { key: "Enter" });

    expect(sendGlobalChatMessage).not.toHaveBeenCalled();
  });

  it("inserts a newline on Shift+Enter rather than submitting", async () => {
    const { getByTestId, findAllByText } = renderPane();
    await findAllByText("Claude Code (terminator-9999)");
    const input = getByTestId("global-chat-input") as HTMLTextAreaElement;

    fireEvent.change(input, { target: { value: "first" } });
    fireEvent.keyDown(input, { key: "Enter", shiftKey: true });

    expect(sendGlobalChatMessage).not.toHaveBeenCalled();
    expect(input.value).toBe("first");
  });

  it("renders existing messages from the query cache", async () => {
    listGlobalChatMessages.mockResolvedValue([
      makeMessage({ id: "m-1", body: "ping", author_kind: "user" }),
      makeMessage({
        id: "m-2",
        body: "pong",
        author_kind: "agent",
        author_id: CLAUDE_ID,
      }),
    ]);

    const { findByText } = renderPane();
    expect(await findByText("ping")).toBeInTheDocument();
    expect(await findByText("pong")).toBeInTheDocument();
  });

  it("invokes lifecycle callbacks around the send mutation", async () => {
    const dispatch = [
      {
        workspace_slug: "ws1",
        workspace_id: "ws-uuid-1",
        mirror_session_id: "ses-1",
        mirror_message_id: "msg-1",
      },
    ];
    sendGlobalChatMessage.mockResolvedValue(
      makeSendResponse(makeMessage({ body: "@ws1 hi" }), { dispatch }),
    );

    const onSubmit = vi.fn();
    const onResolved = vi.fn();

    const { getByTestId, findAllByText } = renderPane({ onSubmit, onResolved });
    await findAllByText("Claude Code (terminator-9999)");
    const input = getByTestId("global-chat-input") as HTMLTextAreaElement;

    fireEvent.change(input, { target: { value: "@ws1 hi" } });
    fireEvent.keyDown(input, { key: "Enter" });

    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith("@ws1 hi"));
    await waitFor(() =>
      expect(onResolved).toHaveBeenCalledWith(
        expect.objectContaining({ dispatch }),
      ),
    );
  });

  it("invokes onErrored when the send mutation rejects", async () => {
    sendGlobalChatMessage.mockRejectedValue(new Error("boom"));

    const onErrored = vi.fn();
    const { getByTestId, findAllByText } = renderPane({ onErrored });
    await findAllByText("Claude Code (terminator-9999)");
    const input = getByTestId("global-chat-input") as HTMLTextAreaElement;

    fireEvent.change(input, { target: { value: "hi" } });
    fireEvent.keyDown(input, { key: "Enter" });

    await waitFor(() =>
      expect(onErrored).toHaveBeenCalledWith(expect.any(Error)),
    );
  });
});

describe("GlobalChatPane — V3 agent picker", () => {
  it("preselects Claude Code by default and renders its name in the trigger", async () => {
    const { findAllByText, getByTestId } = renderPane();
    const matches = await findAllByText("Claude Code (terminator-9999)");
    expect(matches.length).toBeGreaterThan(0);
    expect(getByTestId("global-chat-agent-picker")).toBeInTheDocument();
  });

  it("hydrates the picker from localStorage when a known UUID is stored", async () => {
    window.localStorage.setItem(PICKER_STORAGE_KEY, TWIN_ID);
    const { findByTestId } = renderPane();
    const trigger = await findByTestId("agent-dropdown-trigger");
    expect(trigger.textContent).toContain("Cuong Pho");
  });

  it("falls back to Claude Code when localStorage holds garbage", async () => {
    window.localStorage.setItem(PICKER_STORAGE_KEY, "not-a-uuid");
    const { findByTestId } = renderPane();
    const trigger = await findByTestId("agent-dropdown-trigger");
    expect(trigger.textContent).toContain("Claude Code (terminator-9999)");
  });

  it("persists the selection to localStorage after the debounce", async () => {
    const { findAllByText } = renderPane();
    await findAllByText("Claude Code (terminator-9999)");
    // Open the menu and pick the twin agent.
    const triggers = await findAllByText("Claude Code (terminator-9999)");
    fireEvent.click(triggers[0]!);
    const twinItem = (await findAllByText("Cuong Pho"))[0]!;
    fireEvent.click(twinItem);
    await waitFor(
      () => {
        expect(window.localStorage.getItem(PICKER_STORAGE_KEY)).toBe(TWIN_ID);
      },
      { timeout: 1000 },
    );
  });

  it("sends the new agent_id after picker switch", async () => {
    sendGlobalChatMessage.mockResolvedValue(
      makeSendResponse(makeMessage({ body: "hi" })),
    );
    const { findAllByText, getByTestId } = renderPane();
    const triggers = await findAllByText("Claude Code (terminator-9999)");
    fireEvent.click(triggers[0]!);
    const twin = (await findAllByText("Cuong Pho"))[0]!;
    fireEvent.click(twin);

    const input = getByTestId("global-chat-input") as HTMLTextAreaElement;
    fireEvent.change(input, { target: { value: "hi" } });
    fireEvent.keyDown(input, { key: "Enter" });

    await waitFor(() =>
      expect(sendGlobalChatMessage).toHaveBeenCalledWith({
        body: "hi",
        agent_id: TWIN_ID,
      }),
    );
  });

  it("renders an empty-state and disables the input when no agents are provisioned", async () => {
    listGlobalChatAgents.mockResolvedValue([]);
    const { findByTestId, getByTestId } = renderPane();
    expect(await findByTestId("global-chat-empty-agents")).toBeInTheDocument();
    const input = getByTestId("global-chat-input") as HTMLTextAreaElement;
    expect(input.disabled).toBe(true);
  });
});

describe("GlobalChatPane — author attribution", () => {
  it("renders agent messages with the agent's name and avatar", async () => {
    listGlobalChatMessages.mockResolvedValue([
      makeMessage({
        id: "m-1",
        body: "hi from claude",
        author_kind: "agent",
        author_id: CLAUDE_ID,
      }),
    ]);
    const { findByText } = renderPane();
    // The picker trigger AND the message attribution both render the name —
    // querying `findAllByText` is sufficient to prove the message side has it.
    const matches = await findByText("hi from claude");
    expect(matches).toBeInTheDocument();
    // Author name appears inside the message log (not just the picker).
    const log = (await findByText("hi from claude")).closest("li");
    expect(log?.textContent).toContain("Claude Code (terminator-9999)");
  });

  it("falls back to 'Unknown agent' for messages whose author is no longer in the list", async () => {
    const lostId = "99999999-9999-4999-8999-999999999999";
    listGlobalChatMessages.mockResolvedValue([
      makeMessage({
        id: "m-1",
        body: "ghost",
        author_kind: "agent",
        author_id: lostId,
      }),
    ]);
    const { findByText } = renderPane();
    const ghost = await findByText("ghost");
    const log = ghost.closest("li");
    expect(log?.textContent).toMatch(/Unknown agent/);
    expect(log?.textContent).toContain(lostId.slice(0, 8));
  });

  it("does not attach an avatar to user messages", async () => {
    listGlobalChatMessages.mockResolvedValue([
      makeMessage({ id: "m-1", body: "from me", author_kind: "user" }),
    ]);
    const { findByText } = renderPane();
    const item = (await findByText("from me")).closest("li");
    expect(item).not.toBeNull();
    // The user branch renders a single span and no avatar root.
    expect(item!.querySelector('[data-slot="avatar"]')).toBeNull();
  });
});
