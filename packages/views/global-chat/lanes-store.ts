"use client";

import { useEffect, useRef } from "react";
import { create } from "zustand";

/**
 * Pseudo-id for the always-on Global lane (V1 `GlobalChatPane`). Reserved
 * so it can never collide with a workspace UUID.
 */
export const GLOBAL_LANE_ID = "global";

/** Versioned localStorage key — bump if the persisted shape ever changes. */
export const LANES_STORAGE_KEY = "multica.global-chat.v2.lanes";

const PERSISTED_VERSION = 1;

/**
 * Cap on background-mounted workspace lanes (ADR D8 / DoD bullet 9). The
 * Global lane is always-on and does not count against the cap.
 */
export const MAX_OPEN_WORKSPACE_LANES = 12;

export type LaneId = string; // either GLOBAL_LANE_ID or a workspace UUID

/** Persisted blob shape — version 1. */
interface PersistedV1 {
  version: 1;
  openLanes: string[];
  activeLaneId: LaneId;
}

interface LanesState {
  /**
   * Workspace IDs in rail order (most-recent-first by open/activate). The
   * Global lane is implicit at the top of the rail and is not tracked here.
   */
  openLanes: string[];
  /** Currently active lane — either GLOBAL_LANE_ID or a workspace id. */
  activeLaneId: LaneId;
  /** Internal hydration flag — true once the localStorage blob has been read. */
  hydrated: boolean;

  /**
   * Open or activate a lane for the given workspace. If the lane is already
   * open, it is moved to the front of the rail (most-recent activation
   * wins). If the rail would exceed the cap, the oldest lane (last entry)
   * is evicted via LRU. Activating sets `activeLaneId` as a side effect.
   */
  openWorkspaceLane: (wsId: string) => void;

  /** Activate an already-open lane without reordering the rail. */
  activateLane: (laneId: LaneId) => void;

  /**
   * Remove a workspace lane from the rail. The underlying chat session
   * is NOT touched — only the rail entry. If the closed lane was active,
   * activation falls back to GLOBAL.
   */
  closeWorkspaceLane: (wsId: string) => void;

  /** Replace the entire state — used by the persistence hook on hydrate. */
  hydrate: (next: { openLanes: string[]; activeLaneId: LaneId }) => void;
}

export const useLanesStore = create<LanesState>((set, get) => ({
  openLanes: [],
  activeLaneId: GLOBAL_LANE_ID,
  hydrated: false,

  hydrate: ({ openLanes, activeLaneId }) =>
    set({ openLanes, activeLaneId, hydrated: true }),

  openWorkspaceLane: (wsId) => {
    const { openLanes } = get();
    const without = openLanes.filter((id) => id !== wsId);
    const next = [wsId, ...without].slice(0, MAX_OPEN_WORKSPACE_LANES);
    set({ openLanes: next, activeLaneId: wsId });
  },

  activateLane: (laneId) => set({ activeLaneId: laneId }),

  closeWorkspaceLane: (wsId) => {
    const { openLanes, activeLaneId } = get();
    const next = openLanes.filter((id) => id !== wsId);
    const nextActive: LaneId =
      activeLaneId === wsId ? GLOBAL_LANE_ID : activeLaneId;
    set({ openLanes: next, activeLaneId: nextActive });
  },
}));

interface PersistOptions {
  storage?: Storage | null;
  /** Debounce window for writes — keystroke-cheap UI changes write often. */
  debounceMs?: number;
}

function safeStorage(): Storage | null {
  try {
    if (typeof window === "undefined") return null;
    return window.localStorage;
  } catch {
    return null;
  }
}

function readPersisted(storage: Storage): PersistedV1 | null {
  try {
    const raw = storage.getItem(LANES_STORAGE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    if (
      !parsed ||
      typeof parsed !== "object" ||
      parsed.version !== PERSISTED_VERSION
    ) {
      return null;
    }
    const openLanes = Array.isArray(parsed.openLanes)
      ? parsed.openLanes.filter((id: unknown): id is string => typeof id === "string")
      : [];
    const activeLaneId =
      typeof parsed.activeLaneId === "string" ? parsed.activeLaneId : GLOBAL_LANE_ID;
    return {
      version: PERSISTED_VERSION,
      openLanes: openLanes.slice(0, MAX_OPEN_WORKSPACE_LANES),
      activeLaneId,
    };
  } catch {
    return null;
  }
}

function writePersisted(
  storage: Storage,
  next: { openLanes: string[]; activeLaneId: LaneId },
) {
  const blob: PersistedV1 = {
    version: PERSISTED_VERSION,
    openLanes: next.openLanes,
    activeLaneId: next.activeLaneId,
  };
  try {
    storage.setItem(LANES_STORAGE_KEY, JSON.stringify(blob));
  } catch {
    // Quota / private mode — best-effort, do not throw.
  }
}

/**
 * Hydrates the lanes store from localStorage on first mount, then debounces
 * writes back. Must be called once near the root of `<GlobalChatView>` so
 * lane state survives reload (DoD bullet 8). Tolerates missing/parse-broken
 * blobs by resetting to defaults — one open lane = "global", no workspaces.
 */
export function useLanesPersistence(options: PersistOptions = {}) {
  const storage = options.storage === undefined ? safeStorage() : options.storage;
  const debounceMs = options.debounceMs ?? 100;
  const hydrated = useLanesStore((s) => s.hydrated);
  const writeRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const didHydrateRef = useRef(false);

  // Hydrate once.
  useEffect(() => {
    if (didHydrateRef.current) return;
    didHydrateRef.current = true;
    if (!storage) {
      useLanesStore.getState().hydrate({
        openLanes: [],
        activeLaneId: GLOBAL_LANE_ID,
      });
      return;
    }
    const parsed = readPersisted(storage);
    useLanesStore.getState().hydrate({
      openLanes: parsed?.openLanes ?? [],
      activeLaneId: parsed?.activeLaneId ?? GLOBAL_LANE_ID,
    });
  }, [storage]);

  // Debounced persist on subsequent state changes.
  useEffect(() => {
    if (!storage || !hydrated) return;
    const unsub = useLanesStore.subscribe((state, prev) => {
      if (
        state.openLanes === prev.openLanes &&
        state.activeLaneId === prev.activeLaneId
      ) {
        return;
      }
      if (writeRef.current) clearTimeout(writeRef.current);
      writeRef.current = setTimeout(() => {
        writePersisted(storage, {
          openLanes: state.openLanes,
          activeLaneId: state.activeLaneId,
        });
      }, debounceMs);
    });
    return () => {
      unsub();
      if (writeRef.current) clearTimeout(writeRef.current);
    };
  }, [storage, hydrated, debounceMs]);
}

/** Test helper — resets the in-memory store. Storage is the caller's job. */
export function _resetLanesStoreForTests() {
  useLanesStore.setState({
    openLanes: [],
    activeLaneId: GLOBAL_LANE_ID,
    hydrated: false,
  });
}
