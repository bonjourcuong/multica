import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "../api/client";
import {
  crossWorkspaceIssueKeys,
  crossWorkspaceIssueListOptions,
} from "./queries";

afterEach(() => {
  vi.unstubAllGlobals();
});

const okJson = (body: unknown) =>
  new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });

describe("listCrossWorkspaceIssues (HTTP contract)", () => {
  it("hits /api/issues/cross-workspace with no query string when no filters are passed", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      okJson({ issues: [], next_cursor: null, has_more: false, total_returned: 0 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    await client.listCrossWorkspaceIssues();

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "https://api.example.test/api/issues/cross-workspace",
    );
  });

  it("comma-joins array filters and forwards scalars verbatim", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      okJson({ issues: [], next_cursor: null, has_more: false, total_returned: 0 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    await client.listCrossWorkspaceIssues({
      status: ["todo", "in_progress"],
      priority: ["high"],
      assignee_ids: ["a-1", "a-2"],
      workspace_ids: ["ws-1"],
      limit: 50,
      open_only: true,
    });

    const url = fetchMock.mock.calls[0]?.[0] as string;
    const search = new URL(url).searchParams;
    expect(search.get("status")).toBe("todo,in_progress");
    expect(search.get("priority")).toBe("high");
    expect(search.get("assignee_ids")).toBe("a-1,a-2");
    expect(search.get("workspace_ids")).toBe("ws-1");
    expect(search.get("limit")).toBe("50");
    expect(search.get("open_only")).toBe("true");
  });

  it("omits empty arrays and falsy flags from the URL", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      okJson({ issues: [], next_cursor: null, has_more: false, total_returned: 0 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    await client.listCrossWorkspaceIssues({
      status: [],
      priority: [],
      assignee_ids: [],
      workspace_ids: [],
      open_only: false,
    });

    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "https://api.example.test/api/issues/cross-workspace",
    );
  });
});

describe("crossWorkspaceIssueKeys (filter normalization)", () => {
  it("treats undefined and empty filters as the same cache entry", () => {
    const a = crossWorkspaceIssueListOptions({}).queryKey;
    const b = crossWorkspaceIssueListOptions({
      workspace_ids: [],
      status: [],
    }).queryKey;
    expect(a).toEqual(b);
  });

  it("sorts array filters so caller-side ordering does not split the cache", () => {
    const a = crossWorkspaceIssueListOptions({
      workspace_ids: ["ws-2", "ws-1"],
    }).queryKey;
    const b = crossWorkspaceIssueListOptions({
      workspace_ids: ["ws-1", "ws-2"],
    }).queryKey;
    expect(a).toEqual(b);
  });

  it("keys differ when the filter values actually differ", () => {
    const a = crossWorkspaceIssueListOptions({ status: ["todo"] }).queryKey;
    const b = crossWorkspaceIssueListOptions({ status: ["done"] }).queryKey;
    expect(a).not.toEqual(b);
  });

  it("`crossWorkspaceIssueKeys.all()` is a prefix of every list key (so a single invalidation covers all filter variants)", () => {
    const all = crossWorkspaceIssueKeys.all();
    const filtered = crossWorkspaceIssueListOptions({
      workspace_ids: ["ws-1"],
    }).queryKey;
    expect(filtered.slice(0, all.length)).toEqual(all);
  });
});
