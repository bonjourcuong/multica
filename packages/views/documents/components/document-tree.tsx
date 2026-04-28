"use client";

import * as React from "react";
import { useQuery } from "@tanstack/react-query";
import { documentTreeOptions } from "@multica/core/documents";
import type { DocumentEntry, DocumentTree } from "@multica/core/types";
import { ApiError } from "@multica/core/api";
import {
  ChevronRight,
  Folder,
  FolderOpen,
  FileText,
  AlertTriangle,
  FilePlus,
  FolderPlus,
  Trash2,
} from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  ContextMenu,
  ContextMenuTrigger,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
} from "@multica/ui/components/ui/context-menu";

export interface TreeActionHandlers {
  newFile: (parentPath: string) => void;
  newFolder: (parentPath: string) => void;
  del: (path: string, type: "file" | "folder") => void;
}

interface DocumentTreeProps {
  workspaceId: string;
  /** Currently selected file path (relative). Empty string = none. */
  selectedPath: string;
  /** Fired when the user clicks a `.md` file. */
  onSelectFile: (path: string) => void;
  /** Fired when a load error occurs at the root — lets the page show empty state for "pkm not configured". */
  onRootError?: (error: ApiError | Error) => void;
  /**
   * Right-click action handlers — called when the user picks a tree action
   * via the context menu. The page owns the dialog state for these (file/
   * folder creation, delete confirm) so the same dialogs back the toolbar
   * `+` button and the per-node menus.
   */
  onAction: TreeActionHandlers;
}

/**
 * Recursive folder tree rooted at the workspace `pkm_path`. Children are
 * fetched lazily — a folder's listing is only requested once the user expands
 * it. TanStack Query caches each path independently so re-expanding is
 * instant after the first fetch.
 *
 * Tree-action surface (MUL-19):
 * - The page exposes a `+` dropdown in the header that creates files /
 *   folders at the root.
 * - Right-click on any folder opens "New file here", "New folder here",
 *   "Delete folder".
 * - Right-click on any `.md` file opens "Delete file".
 *
 * The component itself only owns "which folders are expanded" UI state. All
 * other state (selection, errors, dialog state) is lifted to the parent.
 */
export function DocumentTree({
  workspaceId,
  selectedPath,
  onSelectFile,
  onRootError,
  onAction,
}: DocumentTreeProps) {
  const [expanded, setExpanded] = React.useState<Set<string>>(() => new Set([""]));

  const toggle = React.useCallback((path: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  }, []);

  return (
    <div role="tree" className="px-1 py-2 text-sm">
      <FolderNode
        workspaceId={workspaceId}
        path=""
        depth={0}
        expanded={expanded}
        onToggle={toggle}
        selectedPath={selectedPath}
        onSelectFile={onSelectFile}
        onRootError={onRootError}
        onAction={onAction}
      />
    </div>
  );
}

interface FolderNodeProps {
  workspaceId: string;
  path: string;
  depth: number;
  expanded: Set<string>;
  onToggle: (path: string) => void;
  selectedPath: string;
  onSelectFile: (path: string) => void;
  onRootError?: (error: ApiError | Error) => void;
  onAction: TreeActionHandlers;
}

function FolderNode({
  workspaceId,
  path,
  depth,
  expanded,
  onToggle,
  selectedPath,
  onSelectFile,
  onRootError,
  onAction,
}: FolderNodeProps) {
  const isOpen = expanded.has(path);
  const isRoot = depth === 0;

  // Always fetch the root so we can surface "pkm not configured" via the
  // empty state. Subfolders only fetch once expanded — keeps the tree
  // responsive on deep PKMs and avoids hammering the FS on mount.
  const enabled = isOpen || isRoot;
  const query = useQuery({
    ...documentTreeOptions(workspaceId, path),
    enabled,
  });

  React.useEffect(() => {
    if (isRoot && query.error && onRootError) {
      onRootError(query.error as ApiError | Error);
    }
  }, [isRoot, query.error, onRootError]);

  if (isRoot) {
    return (
      <FolderChildren
        workspaceId={workspaceId}
        depth={depth}
        expanded={expanded}
        onToggle={onToggle}
        selectedPath={selectedPath}
        onSelectFile={onSelectFile}
        onRootError={onRootError}
        query={query}
        onAction={onAction}
      />
    );
  }

  const name = path.slice(path.lastIndexOf("/") + 1);
  const Icon = isOpen ? FolderOpen : Folder;

  return (
    <div role="treeitem" aria-expanded={isOpen}>
      <ContextMenu>
        <ContextMenuTrigger
          render={
            <button
              type="button"
              className={cn(
                "group flex w-full items-center gap-1 rounded-md px-1.5 py-1 text-left transition-colors hover:bg-sidebar-accent/70",
              )}
              style={{ paddingLeft: `${depth * 12 + 6}px` }}
              onClick={() => onToggle(path)}
            >
              <ChevronRight
                className={cn(
                  "size-3 shrink-0 text-muted-foreground transition-transform",
                  isOpen && "rotate-90",
                )}
              />
              <Icon className="size-3.5 shrink-0 text-muted-foreground" />
              <span className="min-w-0 flex-1 truncate">{name}</span>
            </button>
          }
        />
        <ContextMenuContent>
          <ContextMenuItem onClick={() => onAction.newFile(path)}>
            <FilePlus className="size-4" />
            New file here
          </ContextMenuItem>
          <ContextMenuItem onClick={() => onAction.newFolder(path)}>
            <FolderPlus className="size-4" />
            New folder here
          </ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuItem
            variant="destructive"
            onClick={() => onAction.del(path, "folder")}
          >
            <Trash2 className="size-4" />
            Delete folder
          </ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>
      {isOpen && (
        <FolderChildren
          workspaceId={workspaceId}
          depth={depth}
          expanded={expanded}
          onToggle={onToggle}
          selectedPath={selectedPath}
          onSelectFile={onSelectFile}
          query={query}
          onAction={onAction}
        />
      )}
    </div>
  );
}

interface FolderChildrenProps extends Omit<FolderNodeProps, "depth" | "path"> {
  depth: number;
  query: ReturnType<typeof useQuery<DocumentTree>>;
}

function FolderChildren({
  workspaceId,
  depth,
  expanded,
  onToggle,
  selectedPath,
  onSelectFile,
  onRootError,
  query,
  onAction,
}: FolderChildrenProps) {
  if (query.isPending) {
    return (
      <div role="group" className="space-y-1 py-1" style={{ paddingLeft: `${(depth + 1) * 12 + 6}px` }}>
        <Skeleton className="h-4 w-32" />
        <Skeleton className="h-4 w-24" />
        <Skeleton className="h-4 w-28" />
      </div>
    );
  }

  if (query.error) {
    // Errors at the root bubble up via onRootError so the page can render an
    // appropriate empty state. For nested folders we render an inline notice
    // — the user is already in the tree and just needs to know this branch
    // is unreachable (e.g. permissions, removed since last fetch).
    if (depth === 0 && onRootError) return null;
    return (
      <div
        role="alert"
        className="flex items-center gap-1.5 px-1.5 py-1 text-xs text-muted-foreground"
        style={{ paddingLeft: `${(depth + 1) * 12 + 6}px` }}
      >
        <AlertTriangle className="size-3 shrink-0" />
        <span className="truncate">Could not load folder</span>
      </div>
    );
  }

  const entries = query.data?.entries ?? [];
  const sorted = sortEntries(entries);

  if (sorted.length === 0) {
    return (
      <div
        className="px-1.5 py-1 text-xs text-muted-foreground"
        style={{ paddingLeft: `${(depth + 1) * 12 + 6}px` }}
      >
        Empty folder
      </div>
    );
  }

  return (
    <div role="group">
      {sorted.map((entry) => {
        if (entry.type === "folder") {
          return (
            <FolderNode
              key={entry.path}
              workspaceId={workspaceId}
              path={entry.path}
              depth={depth + 1}
              expanded={expanded}
              onToggle={onToggle}
              selectedPath={selectedPath}
              onSelectFile={onSelectFile}
              onAction={onAction}
            />
          );
        }
        return (
          <FileNode
            key={entry.path}
            entry={entry}
            depth={depth + 1}
            isSelected={entry.path === selectedPath}
            onSelectFile={onSelectFile}
            onAction={onAction}
          />
        );
      })}
    </div>
  );
}

interface FileNodeProps {
  entry: DocumentEntry;
  depth: number;
  isSelected: boolean;
  onSelectFile: (path: string) => void;
  onAction: TreeActionHandlers;
}

function FileNode({
  entry,
  depth,
  isSelected,
  onSelectFile,
  onAction,
}: FileNodeProps) {
  // Only `.md` files are openable in the read-only viewer. Anything else
  // renders disabled so the user understands it's not browsable yet — image
  // files are referenced by markdown but not stand-alone targets.
  const isMarkdown = entry.name.toLowerCase().endsWith(".md");

  const node = (
    <button
      type="button"
      role="treeitem"
      aria-selected={isSelected}
      disabled={!isMarkdown}
      className={cn(
        "group flex w-full items-center gap-1 rounded-md px-1.5 py-1 text-left transition-colors",
        isMarkdown
          ? "hover:bg-sidebar-accent/70"
          : "cursor-default text-muted-foreground/60",
        isSelected && "bg-sidebar-accent text-sidebar-accent-foreground",
      )}
      style={{ paddingLeft: `${depth * 12 + 18}px` }}
      onClick={() => isMarkdown && onSelectFile(entry.path)}
    >
      <FileText className="size-3.5 shrink-0 text-muted-foreground" />
      <span className="min-w-0 flex-1 truncate">{entry.name}</span>
    </button>
  );

  // Only `.md` files participate in the right-click menu. Non-md files
  // (images referenced by markdown) are read-only here — deletion would
  // need a backend endpoint that strips the `.md`-only extension guard,
  // and that's out of scope for MUL-19.
  if (!isMarkdown) return node;

  return (
    <ContextMenu>
      <ContextMenuTrigger render={node} />
      <ContextMenuContent>
        <ContextMenuItem
          variant="destructive"
          onClick={() => onAction.del(entry.path, "file")}
        >
          <Trash2 className="size-4" />
          Delete file
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}

/** Folders before files; case-insensitive alphabetical within each group. */
function sortEntries(entries: DocumentEntry[]): DocumentEntry[] {
  return [...entries].sort((a, b) => {
    if (a.type !== b.type) return a.type === "folder" ? -1 : 1;
    return a.name.localeCompare(b.name, undefined, { sensitivity: "base" });
  });
}
