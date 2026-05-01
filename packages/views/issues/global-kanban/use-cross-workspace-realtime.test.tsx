import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import type { Workspace, WSEventType } from "@multica/core/types";

const useExtraWorkspaceWSEventsMock = vi.fn();

vi.mock("@multica/core/realtime", () => ({
  useExtraWorkspaceWSEvents: (
    slugs: readonly string[],
    events: readonly WSEventType[],
    handler: () => void,
  ) => useExtraWorkspaceWSEventsMock(slugs, events, handler),
}));

import { useCrossWorkspaceIssueRealtime } from "./use-cross-workspace-realtime";
import { crossWorkspaceIssueKeys } from "@multica/core/issues/queries";

function makeWorkspace(over: Partial<Workspace> = {}): Workspace {
  return {
    id: "00000000-0000-0000-0000-0000000000aa",
    name: "Acme",
    slug: "acme",
    description: null,
    context: null,
    settings: {},
    repos: [],
    issue_prefix: "ACM",
    created_at: "2026-04-01T00:00:00Z",
    updated_at: "2026-04-01T00:00:00Z",
    ...over,
  };
}

function setup(workspaces: Workspace[]) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  qc.setQueryData(["workspaces", "list"], workspaces);
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  return { qc, wrapper };
}

describe("useCrossWorkspaceIssueRealtime", () => {
  beforeEach(() => {
    useExtraWorkspaceWSEventsMock.mockReset();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("subscribes to issue events for every workspace slug the user belongs to", () => {
    const workspaces = [
      makeWorkspace({ id: "ws-1", slug: "alpha" }),
      makeWorkspace({ id: "ws-2", slug: "beta" }),
      makeWorkspace({ id: "ws-3", slug: "gamma" }),
    ];
    const { wrapper } = setup(workspaces);

    renderHook(() => useCrossWorkspaceIssueRealtime(), { wrapper });

    expect(useExtraWorkspaceWSEventsMock).toHaveBeenCalledTimes(1);
    const [slugs, events] = useExtraWorkspaceWSEventsMock.mock.calls[0]!;
    expect(slugs).toEqual(["alpha", "beta", "gamma"]);
    expect(events).toEqual([
      "issue:created",
      "issue:updated",
      "issue:deleted",
      "issue_labels:changed",
    ]);
  });

  it("invalidates the cross-workspace cache once per throttle window even on bursty events", () => {
    const { qc, wrapper } = setup([makeWorkspace({ id: "ws-1", slug: "alpha" })]);
    const invalidateSpy = vi.spyOn(qc, "invalidateQueries");

    renderHook(() => useCrossWorkspaceIssueRealtime(), { wrapper });
    const handler = useExtraWorkspaceWSEventsMock.mock.calls[0]![2] as () => void;

    // Rapid burst: one invalidation should be scheduled, the rest dropped.
    for (let i = 0; i < 25; i += 1) handler();
    expect(invalidateSpy).not.toHaveBeenCalled();

    vi.advanceTimersByTime(250);
    expect(invalidateSpy).toHaveBeenCalledTimes(1);
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: crossWorkspaceIssueKeys.all(),
    });

    // After the window closes, the next event opens a fresh window.
    handler();
    expect(invalidateSpy).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(250);
    expect(invalidateSpy).toHaveBeenCalledTimes(2);
  });

  it("opens no subscriptions when the user is not in any workspace", () => {
    const { wrapper } = setup([]);
    renderHook(() => useCrossWorkspaceIssueRealtime(), { wrapper });

    const [slugs] = useExtraWorkspaceWSEventsMock.mock.calls[0]!;
    expect(slugs).toEqual([]);
  });
});
