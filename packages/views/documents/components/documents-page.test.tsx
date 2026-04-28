import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ApiError } from "@multica/core/api";

// `useWorkspaceId` lives at "@multica/core/hooks" — we hardcode a workspace
// id here because the page mounts outside any real route during tests.
vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/paths", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/paths")>(
    "@multica/core/paths",
  );
  return {
    ...actual,
    useCurrentWorkspace: () => ({ id: "ws-1", name: "Test WS", slug: "test" }),
    useWorkspacePaths: () => actual.paths.workspace("test"),
  };
});

const replace = vi.fn();
const searchParams = new URLSearchParams();
vi.mock("../../navigation", () => ({
  AppLink: ({ children, href, ...props }: { children: React.ReactNode; href: string }) => (
    <a href={href} {...props}>
      {children}
    </a>
  ),
  useNavigation: () => ({
    pathname: "/test/documents",
    searchParams,
    push: vi.fn(),
    replace,
  }),
  NavigationProvider: ({ children }: { children: React.ReactNode }) => children,
}));

// Track API calls so individual tests can swap implementations per case.
const listDocumentTree = vi.fn();
const getDocumentFile = vi.fn();
const documentImageUrl = vi.fn(
  (workspaceId: string, path: string) =>
    `http://api.test/api/workspaces/${workspaceId}/documents/image?path=${encodeURIComponent(path)}`,
);

vi.mock("@multica/core/api", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/api")>(
    "@multica/core/api",
  );
  return {
    ...actual,
    api: {
      listDocumentTree: (...args: unknown[]) =>
        (listDocumentTree as (...args: unknown[]) => unknown)(...args),
      getDocumentFile: (...args: unknown[]) =>
        (getDocumentFile as (...args: unknown[]) => unknown)(...args),
      documentImageUrl: (workspaceId: string, path: string) =>
        documentImageUrl(workspaceId, path),
    },
    setApiInstance: vi.fn(),
  };
});

// Markdown is rendered via @multica/ui — stub to a plain pre block so we can
// assert output without pulling rehype/remark into the test.
vi.mock("@multica/ui/markdown", () => ({
  Markdown: ({ children }: { children: string }) => <pre data-testid="md">{children}</pre>,
}));

// useConfigStore is consumed by the common Markdown wrapper for cdnDomain.
vi.mock("@multica/core/config", () => ({
  useConfigStore: <T,>(sel: (s: { cdnDomain: string }) => T) => sel({ cdnDomain: "" }),
}));

// useIsMobile uses matchMedia; the test setup stubs matchMedia, so the hook
// returns false (desktop layout). That's the variant we want to assert.
import { DocumentsPage } from "./documents-page";

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <DocumentsPage />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  listDocumentTree.mockReset();
  getDocumentFile.mockReset();
  replace.mockReset();
  // Reset URL state — searchParams is stable across tests by reference.
  for (const key of Array.from(searchParams.keys())) searchParams.delete(key);
});

describe("DocumentsPage", () => {
  it("renders the tree and lets the user open a markdown file", async () => {
    listDocumentTree.mockImplementation((_wsId: string, path: string) => {
      if (path === "") {
        return Promise.resolve({
          path: "",
          entries: [
            { name: "GROWTH", path: "GROWTH", type: "folder", mtime: "2026-04-01T00:00:00Z" },
            { name: "README.md", path: "README.md", type: "file", size: 10, mtime: "2026-04-01T00:00:00Z" },
          ],
        });
      }
      return Promise.resolve({ path, entries: [] });
    });
    getDocumentFile.mockResolvedValue({
      path: "README.md",
      content: "# Hello world",
      size: 13,
      mtime: "2026-04-01T00:00:00Z",
    });

    const user = userEvent.setup();
    renderPage();

    expect(await screen.findByText("README.md")).toBeInTheDocument();
    expect(screen.getByText("GROWTH")).toBeInTheDocument();

    await user.click(screen.getByText("README.md"));
    await waitFor(() => {
      expect(replace).toHaveBeenCalledWith("/test/documents?path=README.md");
    });
  });

  it("renders the configure-PKM empty state when the FS API reports pkm_not_configured", async () => {
    listDocumentTree.mockRejectedValue(
      new ApiError("PKM path is not configured for this workspace", 400, "Bad Request", "pkm_not_configured"),
    );

    renderPage();

    expect(await screen.findByText("No PKM folder configured")).toBeInTheDocument();
    const settingsLink = screen.getByText(/open settings/i).closest("a");
    expect(settingsLink).not.toBeNull();
    expect(settingsLink).toHaveAttribute("href", "/test/settings");
  });
});
