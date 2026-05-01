import { describe, it, expect, beforeEach } from "vitest";
import { createChatStore, selectWorkspaceEntry } from "./store";
import type { StorageAdapter } from "../types";

function memoryStorage(): StorageAdapter & { dump(): Record<string, string> } {
  const map = new Map<string, string>();
  return {
    getItem: (k) => map.get(k) ?? null,
    setItem: (k, v) => {
      map.set(k, v);
    },
    removeItem: (k) => {
      map.delete(k);
    },
    dump: () => Object.fromEntries(map.entries()),
  };
}

describe("chat store — per-workspace shape", () => {
  let storage: ReturnType<typeof memoryStorage>;

  beforeEach(() => {
    storage = memoryStorage();
  });

  it("starts with no workspace entries when storage is empty", () => {
    const store = createChatStore({ storage });
    expect(store.getState().byWorkspace).toEqual({});
  });

  it("isolates state across workspaces — setting one does not touch the other", () => {
    const store = createChatStore({ storage });

    store.getState().setActiveSession("ws-a", "sess-a");
    store.getState().setActiveSession("ws-b", "sess-b");

    expect(selectWorkspaceEntry("ws-a")(store.getState()).activeSessionId).toBe(
      "sess-a",
    );
    expect(selectWorkspaceEntry("ws-b")(store.getState()).activeSessionId).toBe(
      "sess-b",
    );
  });

  it("returns a stable empty entry for an unknown workspace", () => {
    const store = createChatStore({ storage });

    const a = selectWorkspaceEntry("ws-unknown")(store.getState());
    const b = selectWorkspaceEntry("ws-unknown")(store.getState());
    expect(a).toBe(b);
    expect(a.activeSessionId).toBeNull();
    expect(a.selectedAgentId).toBeNull();
  });

  it("setActiveSession persists per-workspace and rehydrates on reload", () => {
    const store = createChatStore({ storage });
    store.getState().setActiveSession("ws-a", "sess-a");
    store.getState().setSelectedAgentId("ws-a", "agent-a");
    store.getState().setActiveSession("ws-b", "sess-b");

    // Simulate reload: a fresh store reading from the same storage.
    const reloaded = createChatStore({ storage });
    expect(selectWorkspaceEntry("ws-a")(reloaded.getState())).toEqual({
      activeSessionId: "sess-a",
      selectedAgentId: "agent-a",
    });
    expect(selectWorkspaceEntry("ws-b")(reloaded.getState())).toEqual({
      activeSessionId: "sess-b",
      selectedAgentId: null,
    });
  });

  it("setActiveSession(null) clears just that workspace's pointer", () => {
    const store = createChatStore({ storage });
    store.getState().setActiveSession("ws-a", "sess-a");
    store.getState().setActiveSession("ws-b", "sess-b");

    store.getState().setActiveSession("ws-a", null);

    expect(selectWorkspaceEntry("ws-a")(store.getState()).activeSessionId).toBeNull();
    expect(selectWorkspaceEntry("ws-b")(store.getState()).activeSessionId).toBe(
      "sess-b",
    );
  });

  it("changing the agent for one workspace doesn't blow away its session pointer", () => {
    const store = createChatStore({ storage });
    store.getState().setActiveSession("ws-a", "sess-a");
    store.getState().setSelectedAgentId("ws-a", "agent-a");
    store.getState().setSelectedAgentId("ws-a", "agent-b");

    expect(selectWorkspaceEntry("ws-a")(store.getState())).toEqual({
      activeSessionId: "sess-a",
      selectedAgentId: "agent-b",
    });
  });

  it("input drafts are global (keyed by sessionId/agent), not workspace-scoped", () => {
    const store = createChatStore({ storage });
    store.getState().setInputDraft("sess-a", "draft for A");
    store.getState().setInputDraft("sess-b", "draft for B");

    expect(store.getState().inputDrafts).toEqual({
      "sess-a": "draft for A",
      "sess-b": "draft for B",
    });

    const reloaded = createChatStore({ storage });
    expect(reloaded.getState().inputDrafts).toEqual({
      "sess-a": "draft for A",
      "sess-b": "draft for B",
    });
  });

  it("clearInputDraft removes the entry and prunes empties from storage", () => {
    const store = createChatStore({ storage });
    store.getState().setInputDraft("sess-a", "x");
    store.getState().setInputDraft("sess-b", "y");
    store.getState().clearInputDraft("sess-a");

    expect(store.getState().inputDrafts).toEqual({ "sess-b": "y" });
  });

  it("focusMode persists globally (no workspace suffix)", () => {
    const store = createChatStore({ storage });
    store.getState().setFocusMode(true);
    expect(storage.dump()["multica:chat:focusMode"]).toBe("true");

    const reloaded = createChatStore({ storage });
    expect(reloaded.getState().focusMode).toBe(true);

    reloaded.getState().setFocusMode(false);
    expect(storage.dump()["multica:chat:focusMode"]).toBeUndefined();
  });

  it("tolerates a corrupted byWorkspace blob by starting empty", () => {
    storage.setItem("multica:chat:byWorkspace", "{ not json");
    const store = createChatStore({ storage });
    expect(store.getState().byWorkspace).toEqual({});
    // And new writes still succeed.
    store.getState().setActiveSession("ws-a", "sess-a");
    expect(selectWorkspaceEntry("ws-a")(store.getState()).activeSessionId).toBe(
      "sess-a",
    );
  });
});
