"use client";

import * as React from "react";
import { useDefaultLayout } from "react-resizable-panels";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { ApiError } from "@multica/core/api";
import { PKM_NOT_CONFIGURED_CODE } from "@multica/core/types";
import { useNavigation, AppLink } from "../../navigation";
import { PageHeader } from "../../layout/page-header";
import { DocumentTree, type TreeActionHandlers } from "./document-tree";
import { DocumentViewer } from "./document-viewer";
import {
  NewFileDialog,
  NewFolderDialog,
  DeleteConfirmDialog,
} from "./tree-action-dialogs";
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@multica/ui/components/ui/empty";
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@multica/ui/components/ui/resizable";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { Button } from "@multica/ui/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
} from "@multica/ui/components/ui/dropdown-menu";
import {
  Settings,
  FolderTree,
  ArrowLeft,
  Plus,
  FilePlus,
  FolderPlus,
} from "lucide-react";

type DialogState =
  | { kind: "new-file"; parentPath: string }
  | { kind: "new-folder"; parentPath: string }
  | { kind: "delete"; path: string; type: "file" | "folder" }
  | null;

/**
 * Documents tab — read-only browser for the workspace's PKM folder.
 *
 * Layout: tree on the left, viewer on the right. Selection lives in the URL
 * (`?path=<rel>`) so deep-linking and back/forward work, and so refreshing
 * the page keeps the user where they were. The tree component owns expansion
 * state internally — that's pure UI presentation, not worth persisting.
 *
 * Empty state: when the workspace has no `pkm_path` configured, the FS API
 * responds with a structured `{ code: "pkm_not_configured" }` error. We
 * detect that on the root tree query and render an Empty card linking to
 * settings instead of the regular tree/viewer split.
 *
 * Editor (MUL-19): the page owns the dialog state for tree mutations so the
 * `+` toolbar button and the per-node right-click menus share one set of
 * dialogs. The viewer's dirty bit is also lifted here — that lets us guard
 * tree-driven file switches with a "discard unsaved changes?" confirm
 * before the URL mutates.
 */
export function DocumentsPage() {
  const wsId = useWorkspaceId();
  const wsPaths = useWorkspacePaths();
  const { searchParams, replace } = useNavigation();
  const selectedPath = searchParams.get("path") ?? "";
  const isMobile = useIsMobile();

  // Lifted from the viewer so we can intercept tree clicks (and any other
  // path-changing action) with a discard-changes confirm before the URL
  // mutates. `beforeunload` covers full-page nav inside the viewer itself.
  const [editorDirty, setEditorDirty] = React.useState(false);
  const editorDirtyRef = React.useRef(false);
  React.useEffect(() => {
    editorDirtyRef.current = editorDirty;
  }, [editorDirty]);

  const setSelectedPath = React.useCallback(
    (path: string) => {
      if (editorDirtyRef.current && path !== selectedPath) {
        const ok = window.confirm(
          "Discard unsaved changes? Your edits will be lost.",
        );
        if (!ok) return;
        // The viewer flips its own dirty bit on path change via useEffect,
        // but we sync the lifted copy eagerly so any subsequent guard call
        // in the same tick sees the fresh value.
        setEditorDirty(false);
        editorDirtyRef.current = false;
      }
      const documentsPath = wsPaths.documents();
      const url = path
        ? `${documentsPath}?path=${encodeURIComponent(path)}`
        : documentsPath;
      replace(url);
    },
    [replace, wsPaths, selectedPath],
  );

  const [pkmConfigured, setPkmConfigured] = React.useState(true);
  const handleRootError = React.useCallback((err: ApiError | Error) => {
    if (err instanceof ApiError && err.code === PKM_NOT_CONFIGURED_CODE) {
      setPkmConfigured(false);
    }
  }, []);

  // Tree-action dialogs. Hoisted to the page so the toolbar `+` button and
  // the per-node context menus open the same modals.
  const [dialog, setDialog] = React.useState<DialogState>(null);

  const treeActions = React.useMemo<TreeActionHandlers>(
    () => ({
      newFile: (parentPath) => setDialog({ kind: "new-file", parentPath }),
      newFolder: (parentPath) => setDialog({ kind: "new-folder", parentPath }),
      del: (path, type) => setDialog({ kind: "delete", path, type }),
    }),
    [],
  );

  const handleFileCreated = React.useCallback(
    (path: string) => {
      // Mirror the user's intent — open the file they just created. We let
      // setSelectedPath handle the dirty-guard, but creating from a "+" menu
      // happens in view mode so the guard will be a no-op.
      setSelectedPath(path);
    },
    [setSelectedPath],
  );

  const handleDeleted = React.useCallback(
    (path: string) => {
      // If the open file (or its ancestor) was deleted, drop the selection.
      // Folder delete needs a prefix match — checking against `path/` ensures
      // "foo/bar.md" doesn't match a "foo-2" folder.
      if (path === selectedPath || selectedPath.startsWith(`${path}/`)) {
        setSelectedPath("");
      }
    },
    [selectedPath, setSelectedPath],
  );

  const { defaultLayout, onLayoutChanged } = useDefaultLayout({
    id: "multica_documents_layout",
  });

  if (!pkmConfigured) {
    return (
      <div className="flex flex-1 flex-col min-h-0">
        <PageHeader>
          <h1 className="text-sm font-semibold">Documents</h1>
        </PageHeader>
        <div className="flex flex-1 items-center justify-center p-6">
          <Empty className="max-w-md border bg-card/30">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <FolderTree />
              </EmptyMedia>
              <EmptyTitle>No PKM folder configured</EmptyTitle>
              <EmptyDescription>
                Set the workspace PKM path in settings to browse your markdown notes here.
              </EmptyDescription>
            </EmptyHeader>
            <EmptyContent>
              <Button nativeButton={false} render={<AppLink href={wsPaths.settings()} />}>
                <Settings className="size-4" />
                Open settings
              </Button>
            </EmptyContent>
          </Empty>
        </div>
      </div>
    );
  }

  const treePanel = (
    <div className="flex h-full flex-col border-r">
      <PageHeader className="px-3">
        <h1 className="text-sm font-semibold">Documents</h1>
        <div className="ml-auto">
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label="Add file or folder"
                >
                  <Plus className="size-3.5" />
                </Button>
              }
            />
            <DropdownMenuContent align="end" className="w-44">
              <DropdownMenuItem onClick={() => treeActions.newFile("")}>
                <FilePlus className="size-4" />
                New file
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => treeActions.newFolder("")}>
                <FolderPlus className="size-4" />
                New folder
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </PageHeader>
      <div className="flex-1 min-h-0 overflow-y-auto">
        <DocumentTree
          workspaceId={wsId}
          selectedPath={selectedPath}
          onSelectFile={setSelectedPath}
          onRootError={handleRootError}
          onAction={treeActions}
        />
      </div>
    </div>
  );

  const viewerPanel = (
    <DocumentViewer
      workspaceId={wsId}
      path={selectedPath}
      onDirtyChange={setEditorDirty}
    />
  );

  const dialogs = (
    <>
      <NewFileDialog
        workspaceId={wsId}
        parentPath={dialog?.kind === "new-file" ? dialog.parentPath : ""}
        open={dialog?.kind === "new-file"}
        onOpenChange={(v) => {
          if (!v) setDialog(null);
        }}
        onCreated={handleFileCreated}
      />
      <NewFolderDialog
        workspaceId={wsId}
        parentPath={dialog?.kind === "new-folder" ? dialog.parentPath : ""}
        open={dialog?.kind === "new-folder"}
        onOpenChange={(v) => {
          if (!v) setDialog(null);
        }}
      />
      <DeleteConfirmDialog
        workspaceId={wsId}
        target={
          dialog?.kind === "delete"
            ? { path: dialog.path, type: dialog.type }
            : null
        }
        onOpenChange={(v) => {
          if (!v) setDialog(null);
        }}
        onDeleted={handleDeleted}
      />
    </>
  );

  if (isMobile) {
    if (selectedPath) {
      return (
        <div className="flex flex-1 flex-col min-h-0">
          <div className="flex h-12 shrink-0 items-center border-b px-2">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setSelectedPath("")}
              className="gap-1.5 text-muted-foreground"
            >
              <ArrowLeft className="size-4" />
              Documents
            </Button>
          </div>
          <div className="flex-1 min-h-0">{viewerPanel}</div>
          {dialogs}
        </div>
      );
    }
    return (
      <div className="flex flex-1 flex-col min-h-0">
        {treePanel}
        {dialogs}
      </div>
    );
  }

  return (
    <>
      <ResizablePanelGroup
        orientation="horizontal"
        className="flex-1 min-h-0"
        defaultLayout={defaultLayout}
        onLayoutChanged={onLayoutChanged}
      >
        <ResizablePanel
          id="tree"
          defaultSize={280}
          minSize={220}
          maxSize={480}
          groupResizeBehavior="preserve-pixel-size"
        >
          {treePanel}
        </ResizablePanel>
        <ResizableHandle />
        <ResizablePanel id="viewer" minSize="40%">
          {viewerPanel}
        </ResizablePanel>
      </ResizablePanelGroup>
      {dialogs}
    </>
  );
}
