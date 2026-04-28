import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { CrossWorkspaceIssue } from "@multica/core/types";
import { NavigationProvider } from "../../navigation";
import type { NavigationAdapter } from "../../navigation/types";
import { GlobalKanban } from "./index";

const listCrossWorkspaceIssues = vi.fn<() => Promise<{
  issues: CrossWorkspaceIssue[];
  next_cursor: string | null;
  has_more: boolean;
  total_returned: number;
}>>();

vi.mock("@multica/core/api", () => ({
  api: {
    listCrossWorkspaceIssues: () => listCrossWorkspaceIssues(),
  },
}));

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

function renderKanban() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const navigation: NavigationAdapter = {
    pathname: "/global",
    searchParams: new URLSearchParams(),
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
  };
  return render(
    <QueryClientProvider client={qc}>
      <NavigationProvider value={navigation}>
        <GlobalKanban />
      </NavigationProvider>
    </QueryClientProvider>,
  );
}

describe("GlobalKanban", () => {
  beforeEach(() => {
    listCrossWorkspaceIssues.mockReset();
  });

  it("shows the skeleton while the cross-workspace query is pending", () => {
    listCrossWorkspaceIssues.mockImplementation(
      () => new Promise(() => {}),
    );
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

    // Column headers (5 visible columns per AC; blocked + cancelled are hidden).
    for (const label of ["Backlog", "Todo", "In Progress", "In Review", "Done"]) {
      expect(await screen.findByText(label)).toBeInTheDocument();
    }
    expect(screen.queryByText("Blocked")).not.toBeInTheDocument();

    // Each card renders its workspace badge — distinct per workspace.
    expect(await screen.findByLabelText("Acme (ACM)")).toBeInTheDocument();
    expect(await screen.findByLabelText("Beta (BET)")).toBeInTheDocument();

    // Cards link to the owning workspace's issue detail page.
    const acmeCard = screen.getByLabelText(/ACM-1 Backlog item \(Acme\)/);
    expect(acmeCard).toHaveAttribute("href", "/acme/issues/i1");
    const betaCard = screen.getByLabelText(/BET-7 Done item \(Beta\)/);
    expect(betaCard).toHaveAttribute("href", "/beta/issues/i2");
  });
});
