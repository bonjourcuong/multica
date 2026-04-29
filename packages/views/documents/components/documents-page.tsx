"use client";

import * as React from "react";
import { useDefaultLayout } from "react-resizable-panels";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { ApiError } from "@multica/core/api";
import { PKM_NOT_CONFIGURED_CODE } from "@multica/core/types";
import { useNavigation, AppLink } from "../../navigation";
import { PageHeader } from "../../layout/page-header";
import { DocumentTree } from "./document-tree";
import { DocumentViewer } from "./document-viewer";
import { CreateEntryDialog } from "./create-entry-dialog";
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
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { Button } from "@multica/ui/components/ui/button";
import {
  Settings,
  FolderTree,
  ArrowLeft,
  Plus,
  FilePlus,
  FolderPlus,
} from "lucide-react";

/**
 * Documents tab — read/write browser for the workspace's PKM folder.
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
 */
export function DocumentsPage() {
  const wsId = useWorkspaceId();
  const wsPaths = useWorkspacePaths();
  const { searchParams, replace } = useNavigation();
  const selectedPath = searchParams.get("path") ?? "";
  const isMobile = useIsMobile();

  const setSelectedPath = React.useCallback(
    (path: string) => {
      const documentsPath = wsPaths.documents();
      const url = path
        ? `${documentsPath}?path=${encodeURIComponent(path)}`
        : documentsPath;
      replace(url);
    },
    [replace, wsPaths],
  );

  const [pkmConfigured, setPkmConfigured] = React.useState(true);
  const handleRootError = React.useCallback((err: ApiError | Error) => {
    if (err instanceof ApiError && err.code === PKM_NOT_CONFIGURED_CODE) {
      setPkmConfigured(false);
    }
  }, []);

  // Single shared dialog for both "new note" and "new folder". The page
  // owns it (rather than the tree) because the header "+" button needs to
  // create at root, and per-folder triggers in the tree dispatch through
  // the same opener via callback prop.
  const [createDialog, setCreateDialog] = React.useState<{
    parentPath: string;
    kind: "file" | "folder";
  } | null>(null);
  const openCreate = React.useCallback(
    (parentPath: string, kind: "file" | "folder") => {
      setCreateDialog({ parentPath, kind });
    },
    [],
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
        <h1 className="flex-1 text-sm font-semibold">Documents</h1>
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <Button variant="ghost" size="sm" aria-label="Create new entry" />
            }
          >
            <Plus className="size-4" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onClick={() => openCreate("", "file")}>
              <FilePlus className="size-4" />
              New note
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => openCreate("", "folder")}>
              <FolderPlus className="size-4" />
              New folder
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </PageHeader>
      <div className="flex-1 min-h-0 overflow-y-auto">
        <DocumentTree
          workspaceId={wsId}
          selectedPath={selectedPath}
          onSelectFile={setSelectedPath}
          onRootError={handleRootError}
          onCreateRequest={openCreate}
          onAfterDelete={(path) => {
            // If the deleted entry is (or contains) the selection, drop it
            // so the viewer doesn't sit on a 404.
            if (selectedPath === path || selectedPath.startsWith(path + "/")) {
              setSelectedPath("");
            }
          }}
        />
      </div>
    </div>
  );

  const viewerPanel = <DocumentViewer workspaceId={wsId} path={selectedPath} />;

  const dialog = createDialog ? (
    <CreateEntryDialog
      workspaceId={wsId}
      parentPath={createDialog.parentPath}
      kind={createDialog.kind}
      open
      onOpenChange={(open) => !open && setCreateDialog(null)}
      onCreated={(path) => {
        if (createDialog.kind === "file") setSelectedPath(path);
      }}
    />
  ) : null;

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
          {dialog}
        </div>
      );
    }
    return (
      <div className="flex flex-1 flex-col min-h-0">
        {treePanel}
        {dialog}
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
      {dialog}
    </>
  );
}
