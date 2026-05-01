import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { CrossWorkspaceIssue, Workspace } from "@multica/core/types";
import { NavigationProvider } from "../../navigation";
import type { NavigationAdapter } from "../../navigation/types";
import { GlobalKanban, parseStatusParam, parseWorkspaceIdsParam } from "./index";

const listCrossWorkspaceIssues = vi.fn<
  (params?: { workspace_ids?: string[]; status?: string[] }) => Promise<{
    issues: CrossWorkspaceIssue[];
    next_cursor: string | null;
    has_more: boolean;
    total_returned: number;
  }>
>();
const listWorkspaces = vi.fn<() => Promise<Workspace[]>>();

vi.mock("@multica/core/api", () => ({
  api: {
    listCrossWorkspaceIssues: (params?: {
      workspace_ids?: string[];
      status?: string[];
    }) => listCrossWorkspaceIssues(params),
    listWorkspaces: () => listWorkspaces(),
  },
}));

// Stub the WS provider — no real WebSocket plumbing in this DOM-only test.
// The cross-workspace realtime hook called by GlobalKanban does no work when
// it can't open extra connections, so a no-op factory is enough.
vi.mock("@multica/core/realtime", () => ({
  useExtraWorkspaceWSEvents: () => {},
}));

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

function makeIssue(over: Partial<CrossWorkspaceIssue> = {}): CrossWorkspaceIssue {
  return {
    id: "00000000-0000-0000-0000-000000000001",
    workspace_id: "00000000-0000-0000-0000-0000000000aa",
    number: 1,
    identifier: "ACM-1",
    title: "First issue",
    description: null,
    status: "todo",
    priority: "medium",
    assignee_type: null,
    assignee_id: null,
    creator_type: "member",
    creator_id: "00000000-0000-0000-0000-0000000000bb",
    parent_issue_id: null,
    project_id: null,
    position: 0,
    due_date: null,
    created_at: "2026-04-28T00:00:00Z",
    updated_at: "2026-04-28T00:00:00Z",
    workspace: {
      id: "00000000-0000-0000-0000-0000000000aa",
      name: "Acme",
      slug: "acme",
      issue_prefix: "ACM",
      color: "#7c3aed",
    },
    ...over,
  };
}

function renderKanban({
  searchParams = new URLSearchParams(),
  workspaces = [makeWorkspace()],
}: {
  searchParams?: URLSearchParams;
  workspaces?: Workspace[];
} = {}) {
  listWorkspaces.mockResolvedValue(workspaces);
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  // Pre-seed so workspaceListOptions resolves synchronously and the filter
  // bar paints on the first render — keeps assertions deterministic.
  qc.setQueryData(["workspaces", "list"], workspaces);
  const replace = vi.fn();
  const navigation: NavigationAdapter = {
    pathname: "/global",
    searchParams,
    push: vi.fn(),
    replace,
    back: vi.fn(),
  };
  const utils = render(
    <QueryClientProvider client={qc}>
      <NavigationProvider value={navigation}>
        <GlobalKanban />
      </NavigationProvider>
    </QueryClientProvider>,
  );
  return { ...utils, replace };
}

describe("parseWorkspaceIdsParam", () => {
  it("returns [] for null / empty / whitespace input", () => {
    expect(parseWorkspaceIdsParam(null)).toEqual([]);
    expect(parseWorkspaceIdsParam(undefined)).toEqual([]);
    expect(parseWorkspaceIdsParam("")).toEqual([]);
    expect(parseWorkspaceIdsParam(",,, ,")).toEqual([]);
  });

  it("splits comma-separated values and trims surrounding whitespace", () => {
    expect(parseWorkspaceIdsParam("ws-1, ws-2 ,ws-3")).toEqual([
      "ws-1",
      "ws-2",
      "ws-3",
    ]);
  });
});

describe("parseStatusParam", () => {
  it("returns [] for null / empty / whitespace input", () => {
    expect(parseStatusParam(null)).toEqual([]);
    expect(parseStatusParam(undefined)).toEqual([]);
    expect(parseStatusParam("")).toEqual([]);
    expect(parseStatusParam(",,, ,")).toEqual([]);
  });

  it("keeps known board statuses and drops anything else", () => {
    expect(parseStatusParam("in_progress, todo")).toEqual([
      "in_progress",
      "todo",
    ]);
    // `cancelled` and `blocked` are not part of the global board (ADR 0001),
    // so a hand-typed URL must not leak them through.
    expect(parseStatusParam("cancelled,in_progress,bogus,blocked")).toEqual([
      "in_progress",
    ]);
  });
});

describe("GlobalKanban", () => {
  beforeEach(() => {
    listCrossWorkspaceIssues.mockReset();
    listWorkspaces.mockReset();
  });

  it("shows the skeleton while the cross-workspace query is pending", () => {
    listCrossWorkspaceIssues.mockImplementation(() => new Promise(() => {}));
    renderKanban();
    expect(
      screen.getByRole("status", { name: /loading cross-workspace kanban/i }),
    ).toBeInTheDocument();
  });

  it("shows the empty state when the API returns zero issues", async () => {
    listCrossWorkspaceIssues.mockResolvedValueOnce({
      issues: [],
      next_cursor: null,
      has_more: false,
      total_returned: 0,
    });
    renderKanban();
    expect(
      await screen.findByText(/no issues across your workspaces yet/i),
    ).toBeInTheDocument();
  });

  it("renders each card with its workspace badge and the columns the AC requires", async () => {
    listCrossWorkspaceIssues.mockResolvedValueOnce({
      issues: [
        makeIssue({ id: "i1", identifier: "ACM-1", title: "Backlog item", status: "backlog" }),
        makeIssue({
          id: "i2",
          identifier: "BET-7",
          title: "Done item",
          status: "done",
          workspace: {
            id: "ws-2",
            name: "Beta",
            slug: "beta",
            issue_prefix: "BET",
            color: "#22c55e",
          },
        }),
      ],
      next_cursor: null,
      has_more: false,
      total_returned: 2,
    });
    renderKanban();

    // Wait for cards to land before scoping queries to the column headers.
    await screen.findByLabelText("Acme (ACM)");
    for (const [status, label] of [
      ["backlog", "Backlog"],
      ["todo", "Todo"],
      ["in_progress", "In Progress"],
      ["in_review", "In Review"],
      ["done", "Done"],
    ] as const) {
      const column = screen.getByTestId(`global-kanban-column-${status}`);
      expect(within(column).getByText(label)).toBeInTheDocument();
    }
    expect(
      screen.queryByTestId("global-kanban-column-blocked"),
    ).not.toBeInTheDocument();

    expect(await screen.findByLabelText("Acme (ACM)")).toBeInTheDocument();
    expect(await screen.findByLabelText("Beta (BET)")).toBeInTheDocument();

    const acmeCard = screen.getByLabelText(/ACM-1 Backlog item \(Acme\)/);
    expect(acmeCard).toHaveAttribute("href", "/acme/issues/i1");
    const betaCard = screen.getByLabelText(/BET-7 Done item \(Beta\)/);
    expect(betaCard).toHaveAttribute("href", "/beta/issues/i2");
  });

  it("forwards the URL `workspace_ids` filter to the cross-workspace API call", async () => {
    listCrossWorkspaceIssues.mockResolvedValueOnce({
      issues: [],
      next_cursor: null,
      has_more: false,
      total_returned: 0,
    });
    renderKanban({
      searchParams: new URLSearchParams("workspace_ids=ws-1,ws-2"),
      workspaces: [
        makeWorkspace({ id: "ws-1", name: "Acme", slug: "acme", issue_prefix: "ACM" }),
        makeWorkspace({ id: "ws-2", name: "Beta", slug: "beta", issue_prefix: "BET" }),
      ],
    });
    expect(
      await screen.findByText(/no issues match the current filter/i),
    ).toBeInTheDocument();
    expect(listCrossWorkspaceIssues).toHaveBeenCalledWith(
      expect.objectContaining({ workspace_ids: ["ws-1", "ws-2"] }),
    );
  });

  it("toggling a workspace chip pushes the new set into the URL via NavigationAdapter.replace", async () => {
    listCrossWorkspaceIssues.mockResolvedValue({
      issues: [],
      next_cursor: null,
      has_more: false,
      total_returned: 0,
    });
    const { replace } = renderKanban({
      workspaces: [
        makeWorkspace({ id: "ws-1", name: "Acme", slug: "acme", issue_prefix: "ACM" }),
        makeWorkspace({ id: "ws-2", name: "Beta", slug: "beta", issue_prefix: "BET" }),
      ],
    });

    fireEvent.click(screen.getByTestId("workspace-filter-chip-acme"));
    expect(replace).toHaveBeenCalledWith("/global?workspace_ids=ws-1");
  });

  it("hides the workspace filter row when the user only belongs to one workspace", () => {
    listCrossWorkspaceIssues.mockImplementation(() => new Promise(() => {}));
    renderKanban({
      workspaces: [makeWorkspace({ id: "ws-only", name: "Solo", slug: "solo" })],
    });
    expect(
      screen.queryByRole("group", { name: /workspace filter/i }),
    ).toBeNull();
    // Status filter is always available, even for single-workspace users.
    expect(
      screen.getByRole("group", { name: /status filter/i }),
    ).toBeInTheDocument();
  });

  it("forwards the URL `status` filter to the cross-workspace API call", async () => {
    listCrossWorkspaceIssues.mockResolvedValueOnce({
      issues: [],
      next_cursor: null,
      has_more: false,
      total_returned: 0,
    });
    renderKanban({
      searchParams: new URLSearchParams("status=in_progress,todo"),
    });
    expect(
      await screen.findByText(/no issues match the current filter/i),
    ).toBeInTheDocument();
    expect(listCrossWorkspaceIssues).toHaveBeenCalledWith(
      expect.objectContaining({ status: ["in_progress", "todo"] }),
    );
  });

  it("toggling a status chip pushes `status` into the URL via NavigationAdapter.replace", async () => {
    listCrossWorkspaceIssues.mockResolvedValue({
      issues: [],
      next_cursor: null,
      has_more: false,
      total_returned: 0,
    });
    const { replace } = renderKanban();

    fireEvent.click(screen.getByTestId("status-filter-chip-in_progress"));
    expect(replace).toHaveBeenCalledWith("/global?status=in_progress");
  });
});
