import { describe, it, expect, beforeEach } from "vitest";
import {
  useLanesStore,
  GLOBAL_LANE_ID,
  MAX_OPEN_WORKSPACE_LANES,
  _resetLanesStoreForTests,
} from "../lanes-store";

describe("lanes-store", () => {
  beforeEach(() => {
    _resetLanesStoreForTests();
  });

  it("starts with no open workspace lanes and Global active", () => {
    const state = useLanesStore.getState();
    expect(state.openLanes).toEqual([]);
    expect(state.activeLaneId).toBe(GLOBAL_LANE_ID);
  });

  it("openWorkspaceLane adds the workspace and activates it", () => {
    useLanesStore.getState().openWorkspaceLane("ws-a");
    expect(useLanesStore.getState().openLanes).toEqual(["ws-a"]);
    expect(useLanesStore.getState().activeLaneId).toBe("ws-a");
  });

  it("re-opening an already-open lane moves it to the front (LRU recency)", () => {
    useLanesStore.getState().openWorkspaceLane("ws-a");
    useLanesStore.getState().openWorkspaceLane("ws-b");
    useLanesStore.getState().openWorkspaceLane("ws-a");
    expect(useLanesStore.getState().openLanes).toEqual(["ws-a", "ws-b"]);
  });

  it("evicts the oldest lane when the cap is exceeded", () => {
    for (let i = 0; i < MAX_OPEN_WORKSPACE_LANES + 2; i++) {
      useLanesStore.getState().openWorkspaceLane(`ws-${i}`);
    }
    const lanes = useLanesStore.getState().openLanes;
    expect(lanes).toHaveLength(MAX_OPEN_WORKSPACE_LANES);
    // Most-recent-first: last opened is at index 0.
    expect(lanes[0]).toBe(`ws-${MAX_OPEN_WORKSPACE_LANES + 1}`);
    // Earliest two are evicted.
    expect(lanes).not.toContain("ws-0");
    expect(lanes).not.toContain("ws-1");
  });

  it("activateLane changes active without reordering the rail", () => {
    useLanesStore.getState().openWorkspaceLane("ws-a");
    useLanesStore.getState().openWorkspaceLane("ws-b");
    useLanesStore.getState().activateLane("ws-a");
    expect(useLanesStore.getState().openLanes).toEqual(["ws-b", "ws-a"]);
    expect(useLanesStore.getState().activeLaneId).toBe("ws-a");
  });

  it("activateLane(GLOBAL) returns to the orchestrator pane", () => {
    useLanesStore.getState().openWorkspaceLane("ws-a");
    useLanesStore.getState().activateLane(GLOBAL_LANE_ID);
    expect(useLanesStore.getState().activeLaneId).toBe(GLOBAL_LANE_ID);
    // Closing global isn't a thing — it stays implicit. The workspace lane
    // remains in the rail unchanged.
    expect(useLanesStore.getState().openLanes).toEqual(["ws-a"]);
  });

  it("closeWorkspaceLane removes the lane and falls back to Global if it was active", () => {
    useLanesStore.getState().openWorkspaceLane("ws-a");
    useLanesStore.getState().openWorkspaceLane("ws-b");
    useLanesStore.getState().activateLane("ws-a");
    useLanesStore.getState().closeWorkspaceLane("ws-a");

    expect(useLanesStore.getState().openLanes).toEqual(["ws-b"]);
    expect(useLanesStore.getState().activeLaneId).toBe(GLOBAL_LANE_ID);
  });

  it("closing a non-active lane preserves the active selection", () => {
    useLanesStore.getState().openWorkspaceLane("ws-a");
    useLanesStore.getState().openWorkspaceLane("ws-b");
    // ws-b is active.
    useLanesStore.getState().closeWorkspaceLane("ws-a");
    expect(useLanesStore.getState().activeLaneId).toBe("ws-b");
    expect(useLanesStore.getState().openLanes).toEqual(["ws-b"]);
  });
});
