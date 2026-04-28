import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

// Workspace + paths context — same shape as documents-page.test.tsx so the
// two specs share a mental model.
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

// Per-test API mocks. We re-implement them in beforeEach because the page
// renders multiple components that share the cache, and stale promises can
// leak between tests.
const listDocumentTree = vi.fn();
const getDocumentFile = vi.fn();
const writeDocumentFile = vi.fn();
const createDocumentFile = vi.fn();
const deleteDocumentFile = vi.fn();
const createDocumentFolder = vi.fn();
const deleteDocumentFolder = vi.fn();

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
      writeDocumentFile: (...args: unknown[]) =>
        (writeDocumentFile as (...args: unknown[]) => unknown)(...args),
      createDocumentFile: (...args: unknown[]) =>
        (createDocumentFile as (...args: unknown[]) => unknown)(...args),
      deleteDocumentFile: (...args: unknown[]) =>
        (deleteDocumentFile as (...args: unknown[]) => unknown)(...args),
      createDocumentFolder: (...args: unknown[]) =>
        (createDocumentFolder as (...args: unknown[]) => unknown)(...args),
      deleteDocumentFolder: (...args: unknown[]) =>
        (deleteDocumentFolder as (...args: unknown[]) => unknown)(...args),
      documentImageUrl: (workspaceId: string, path: string) =>
        `http://api.test/api/workspaces/${workspaceId}/documents/image?path=${encodeURIComponent(path)}`,
    },
    setApiInstance: vi.fn(),
  };
});

vi.mock("@multica/ui/markdown", () => ({
  Markdown: ({ children }: { children: string }) => <pre data-testid="md">{children}</pre>,
}));

vi.mock("@multica/core/config", () => ({
  useConfigStore: <T,>(sel: (s: { cdnDomain: string }) => T) => sel({ cdnDomain: "" }),
}));

// sonner toasts hop through a portal; stub them out — we assert API calls,
// not toast text.
vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

// Base UI dropdown/dialog/alert-dialog/context-menu primitives all render
// through portals with their own mount lifecycles. Existing modal tests in
// this repo stub these out to keep menu items / dialog bodies in the same
// tree so user-event can interact with them. Same trick here.
vi.mock("@multica/ui/components/ui/dropdown-menu", () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuTrigger: ({ render }: { render: React.ReactElement }) => <>{render}</>,
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuItem: ({ children, onClick }: { children: React.ReactNode; onClick?: () => void }) => (
    <button type="button" onClick={onClick}>{children}</button>
  ),
  DropdownMenuSeparator: () => null,
}));

vi.mock("@multica/ui/components/ui/dialog", () => ({
  Dialog: ({ children, open }: { children: React.ReactNode; open?: boolean }) =>
    open ? <div role="dialog">{children}</div> : null,
  DialogContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DialogHeader: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DialogTitle: ({ children }: { children: React.ReactNode }) => <h2>{children}</h2>,
  DialogDescription: ({ children }: { children: React.ReactNode }) => <p>{children}</p>,
  DialogFooter: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}));

vi.mock("@multica/ui/components/ui/alert-dialog", () => ({
  AlertDialog: ({ children, open }: { children: React.ReactNode; open?: boolean }) =>
    open ? <div role="alertdialog">{children}</div> : null,
  AlertDialogContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  AlertDialogHeader: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  AlertDialogTitle: ({ children }: { children: React.ReactNode }) => <h2>{children}</h2>,
  AlertDialogDescription: ({ children }: { children: React.ReactNode }) => <p>{children}</p>,
  AlertDialogFooter: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  AlertDialogAction: ({ children, onClick, disabled }: {
    children: React.ReactNode; onClick?: () => void; disabled?: boolean;
  }) => (
    <button type="button" onClick={onClick} disabled={disabled}>{children}</button>
  ),
  AlertDialogCancel: ({ children }: { children: React.ReactNode }) => (
    <button type="button">{children}</button>
  ),
}));

vi.mock("@multica/ui/components/ui/context-menu", () => ({
  ContextMenu: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  ContextMenuTrigger: ({ render }: { render: React.ReactElement }) => <>{render}</>,
  ContextMenuContent: () => null,
  ContextMenuItem: () => null,
  ContextMenuSeparator: () => null,
}));

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
  writeDocumentFile.mockReset();
  createDocumentFile.mockReset();
  deleteDocumentFile.mockReset();
  createDocumentFolder.mockReset();
  deleteDocumentFolder.mockReset();
  replace.mockReset();
  for (const key of Array.from(searchParams.keys())) searchParams.delete(key);
});

describe("Documents editor (MUL-19)", () => {
  it("toggles into edit mode, saves, and PUTs the new content", async () => {
    // Pre-select a file via URL — keeps the test focused on the editor flow
    // and avoids the tree click → URL sync round-trip.
    searchParams.set("path", "README.md");

    listDocumentTree.mockResolvedValue({ path: "", entries: [] });
    getDocumentFile.mockResolvedValue({
      path: "README.md",
      content: "# Original",
      size: 10,
      mtime: "2026-04-01T00:00:00Z",
    });
    writeDocumentFile.mockResolvedValue({ path: "README.md", bytes: 11 });

    const user = userEvent.setup();
    renderPage();

    // The viewer hydrates from the file query, then we click Edit.
    await user.click(await screen.findByRole("button", { name: /edit/i }));
    const textarea = await screen.findByRole("textbox", { name: /markdown editor/i });
    expect(textarea).toHaveValue("# Original");

    // user.type() is brittle on controlled textareas (each keystroke
    // triggers a re-render and the cursor must follow). fireEvent.change()
    // simulates a single change event with the final value, which is what
    // we actually want to assert here.
    fireEvent.change(textarea, { target: { value: "# Edited" } });
    expect(textarea).toHaveValue("# Edited");

    await user.click(screen.getByRole("button", { name: /save/i }));

    await waitFor(() => {
      expect(writeDocumentFile).toHaveBeenCalledWith(
        "ws-1",
        "README.md",
        "# Edited",
      );
    });
  });

  it("disables Save when the draft matches the saved baseline", async () => {
    searchParams.set("path", "README.md");

    listDocumentTree.mockResolvedValue({ path: "", entries: [] });
    getDocumentFile.mockResolvedValue({
      path: "README.md",
      content: "# Hello",
      size: 7,
      mtime: "2026-04-01T00:00:00Z",
    });

    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: /edit/i }));
    const saveBtn = await screen.findByRole("button", { name: /save/i });
    expect(saveBtn).toBeDisabled();
  });

  it("creates a new file via the toolbar + dropdown", async () => {
    listDocumentTree.mockResolvedValue({ path: "", entries: [] });
    createDocumentFile.mockResolvedValue({ path: "new.md", bytes: 0 });

    const user = userEvent.setup();
    renderPage();

    // Wait for the tree to settle so the toolbar button is in the document.
    await screen.findByText(/empty folder/i);

    await user.click(screen.getByRole("button", { name: /add file or folder/i }));
    await user.click(await screen.findByText(/^new file$/i));

    const input = await screen.findByLabelText("Name");
    await user.type(input, "new");
    await user.click(screen.getByRole("button", { name: /^create$/i }));

    await waitFor(() => {
      // Auto-extension: "new" is normalized to "new.md" before posting.
      expect(createDocumentFile).toHaveBeenCalledWith("ws-1", "new.md", "");
    });
  });

  it("rejects empty filename without calling the API", async () => {
    listDocumentTree.mockResolvedValue({ path: "", entries: [] });

    const user = userEvent.setup();
    renderPage();

    await screen.findByText(/empty folder/i);

    await user.click(screen.getByRole("button", { name: /add file or folder/i }));
    await user.click(await screen.findByText(/^new file$/i));

    await user.click(screen.getByRole("button", { name: /^create$/i }));

    expect(await screen.findByText(/name is required/i)).toBeInTheDocument();
    expect(createDocumentFile).not.toHaveBeenCalled();
  });

  it("creates a new folder via the toolbar + dropdown", async () => {
    listDocumentTree.mockResolvedValue({ path: "", entries: [] });
    createDocumentFolder.mockResolvedValue({ path: "Drafts" });

    const user = userEvent.setup();
    renderPage();

    await screen.findByText(/empty folder/i);

    await user.click(screen.getByRole("button", { name: /add file or folder/i }));
    await user.click(await screen.findByText(/^new folder$/i));

    const input = await screen.findByLabelText("Name");
    await user.type(input, "Drafts");
    await user.click(screen.getByRole("button", { name: /^create$/i }));

    await waitFor(() => {
      expect(createDocumentFolder).toHaveBeenCalledWith("ws-1", "Drafts");
    });
  });
});
