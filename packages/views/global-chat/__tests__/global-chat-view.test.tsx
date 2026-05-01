import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { SendGlobalChatMessageResponse } from "@multica/core/api";
import type {
  GlobalChatMessage,
  GlobalMirrorSummary,
} from "@multica/core/types";
import { GlobalChatView } from "../global-chat-view";

// Stub the workspace mirror hook — its WS subscription is not under test
// here and pulling it in would require a WSProvider for jsdom.
vi.mock("../use-workspace-mirror", () => ({
  useWorkspaceMirror: () => ({ messages: [] }),
}));

const sendGlobalChatMessage =
  vi.fn<(body: string) => Promise<SendGlobalChatMessageResponse>>();
const listGlobalChatMessages = vi.fn<() => Promise<GlobalChatMessage[]>>();
const listGlobalMirrors = vi.fn<() => Promise<GlobalMirrorSummary[]>>();

vi.mock("@multica/core/api", () => ({
  api: {
    sendGlobalChatMessage: (body: string) => sendGlobalChatMessage(body),
    listGlobalChatMessages: () => listGlobalChatMessages(),
    getGlobalChatSession: () =>
      Promise.resolve({
        id: "ses-1",
        user_id: "u-1",
        agent_id: "a-1",
        created_at: "2026-04-28T00:00:00Z",
        archived_at: null,
      }),
    bootstrapGlobalChatSession: () =>
      Promise.resolve({
        id: "ses-1",
        user_id: "u-1",
        agent_id: "a-1",
        created_at: "2026-04-28T00:00:00Z",
        archived_at: null,
      }),
    listGlobalMirrors: () => listGlobalMirrors(),
  },
}));

function makeMirror(
  over: Partial<GlobalMirrorSummary> = {},
): GlobalMirrorSummary {
  return {
    workspace_id: "ws-1",
    workspace_slug: "ws-1",
    workspace_name: "Workspace 1",
    mirror_session_id: null,
    last_message_at: null,
    unread_count: 0,
    ...over,
  };
}

function makeMessage(over: Partial<GlobalChatMessage> = {}): GlobalChatMessage {
  return {
    id: "msg-1",
    global_session_id: "ses-1",
    author_kind: "user",
    author_id: "u-1",
    body: "hi",
    metadata: {},
    dispatched_to: [],
    created_at: "2026-04-28T00:00:00Z",
    ...over,
  };
}

function renderView() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <GlobalChatView />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  sendGlobalChatMessage.mockReset();
  listGlobalChatMessages.mockReset();
  listGlobalChatMessages.mockResolvedValue([]);
  listGlobalMirrors.mockReset();
  listGlobalMirrors.mockResolvedValue([
    makeMirror({ workspace_id: "ws-1", workspace_slug: "ws-1", workspace_name: "Workspace 1" }),
    makeMirror({ workspace_id: "ws-2", workspace_slug: "ws-2", workspace_name: "Workspace 2" }),
  ]);
});

describe("GlobalChatView per-target tile state", () => {
  it("flips the matching tile to 'delivered' when the dispatch succeeds", async () => {
    sendGlobalChatMessage.mockResolvedValue({
      message: makeMessage({ body: "@ws-1 hi" }),
      dispatch: [
        {
          workspace_slug: "ws-1",
          workspace_id: "ws-1",
          mirror_session_id: "mirror-1",
          mirror_message_id: "mirror-msg-1",
        },
      ],
      mentions: [{ workspace_slug: "ws-1" }],
    });

    const { getByTestId, findAllByTestId, container } = renderView();

    const tiles = await findAllByTestId("workspace-tile");
    expect(tiles).toHaveLength(2);

    const input = getByTestId("global-chat-input") as HTMLTextAreaElement;
    fireEvent.change(input, { target: { value: "@ws-1 hi" } });
    fireEvent.keyDown(input, { key: "Enter" });

    await waitFor(() => {
      const ws1 = container.querySelector<HTMLElement>(
        '[data-testid="workspace-tile"][data-workspace-slug="ws-1"]',
      );
      expect(ws1?.dataset.dispatchState).toBe("delivered");
    });

    // The non-targeted tile stays idle.
    const ws2 = container.querySelector<HTMLElement>(
      '[data-testid="workspace-tile"][data-workspace-slug="ws-2"]',
    );
    expect(ws2?.dataset.dispatchState).toBe("idle");
  });

  it("flips the tile to 'not_authorized' when the dispatch is rejected by membership", async () => {
    sendGlobalChatMessage.mockResolvedValue({
      message: makeMessage({ body: "@ws-1 hi" }),
      dispatch: [
        {
          workspace_slug: "ws-1",
          workspace_id: "",
          mirror_session_id: "",
          mirror_message_id: "",
          error: "Je n'ai pas accès à `@ws-1`.",
        },
      ],
      mentions: [{ workspace_slug: "ws-1" }],
    });

    const { getByTestId, findAllByTestId, container } = renderView();
    await findAllByTestId("workspace-tile");

    const input = getByTestId("global-chat-input") as HTMLTextAreaElement;
    fireEvent.change(input, { target: { value: "@ws-1 hi" } });
    fireEvent.keyDown(input, { key: "Enter" });

    await waitFor(() => {
      const ws1 = container.querySelector<HTMLElement>(
        '[data-testid="workspace-tile"][data-workspace-slug="ws-1"]',
      );
      expect(ws1?.dataset.dispatchState).toBe("not_authorized");
    });
  });

  it("flips every sending tile to 'error' when the request fails outright", async () => {
    sendGlobalChatMessage.mockRejectedValue(new Error("network"));

    const { getByTestId, findAllByTestId, container } = renderView();
    await findAllByTestId("workspace-tile");

    const input = getByTestId("global-chat-input") as HTMLTextAreaElement;
    fireEvent.change(input, { target: { value: "@ws-1 @ws-2 hi" } });
    fireEvent.keyDown(input, { key: "Enter" });

    await waitFor(() => {
      const ws1 = container.querySelector<HTMLElement>(
        '[data-testid="workspace-tile"][data-workspace-slug="ws-1"]',
      );
      const ws2 = container.querySelector<HTMLElement>(
        '[data-testid="workspace-tile"][data-workspace-slug="ws-2"]',
      );
      expect(ws1?.dataset.dispatchState).toBe("error");
      expect(ws2?.dataset.dispatchState).toBe("error");
    });
  });
});
