import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { SendGlobalChatMessageResponse } from "@multica/core/api";
import type { GlobalChatMessage } from "@multica/core/types";
import { GlobalChatPane } from "../global-chat-pane";

const sendGlobalChatMessage = vi.fn<(body: string) => Promise<SendGlobalChatMessageResponse>>();
const listGlobalChatMessages = vi.fn<() => Promise<GlobalChatMessage[]>>();
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
    sendGlobalChatMessage: (body: string) => sendGlobalChatMessage(body),
    listGlobalChatMessages: () => listGlobalChatMessages(),
    getGlobalChatSession: () => getGlobalChatSession(),
    bootstrapGlobalChatSession: () => getGlobalChatSession(),
  },
}));

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
): SendGlobalChatMessageResponse {
  return { message, dispatch: [], mentions: [] };
}

function renderPane() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <GlobalChatPane />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  sendGlobalChatMessage.mockReset();
  listGlobalChatMessages.mockReset();
  listGlobalChatMessages.mockResolvedValue([]);
});

describe("GlobalChatPane", () => {
  it("submits the draft on Enter, clears the input, and calls the API", async () => {
    sendGlobalChatMessage.mockResolvedValue(
      makeSendResponse(makeMessage({ body: "hello" })),
    );

    const { getByTestId } = renderPane();
    const input = getByTestId("global-chat-input") as HTMLTextAreaElement;

    fireEvent.change(input, { target: { value: "hello" } });
    fireEvent.keyDown(input, { key: "Enter" });

    await waitFor(() =>
      expect(sendGlobalChatMessage).toHaveBeenCalledWith("hello"),
    );
    expect(input.value).toBe("");
  });

  it("does not submit when the draft is empty or whitespace-only", () => {
    const { getByTestId } = renderPane();
    const input = getByTestId("global-chat-input") as HTMLTextAreaElement;

    fireEvent.change(input, { target: { value: "   " } });
    fireEvent.keyDown(input, { key: "Enter" });

    expect(sendGlobalChatMessage).not.toHaveBeenCalled();
  });

  it("inserts a newline on Shift+Enter rather than submitting", () => {
    const { getByTestId } = renderPane();
    const input = getByTestId("global-chat-input") as HTMLTextAreaElement;

    fireEvent.change(input, { target: { value: "first" } });
    fireEvent.keyDown(input, { key: "Enter", shiftKey: true });

    expect(sendGlobalChatMessage).not.toHaveBeenCalled();
    // Input retains its value because Shift+Enter is not intercepted.
    expect(input.value).toBe("first");
  });

  it("renders existing messages from the query cache", async () => {
    listGlobalChatMessages.mockResolvedValue([
      makeMessage({ id: "m-1", body: "ping", author_kind: "user" }),
      makeMessage({ id: "m-2", body: "pong", author_kind: "agent" }),
    ]);

    const { findByText } = renderPane();
    expect(await findByText("ping")).toBeInTheDocument();
    expect(await findByText("pong")).toBeInTheDocument();
  });
});
