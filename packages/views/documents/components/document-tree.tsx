"use client";

import * as React from "react";
import { useQuery } from "@tanstack/react-query";
import { documentTreeOptions } from "@multica/core/documents";
import type { DocumentEntry, DocumentTree } from "@multica/core/types";
import { ApiError } from "@multica/core/api";
import { ChevronRight, Folder, FolderOpen, FileText, AlertTriangle } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { Skeleton } from "@multica/ui/components/ui/skeleton";

interface DocumentTreeProps {
  workspaceId: string;
  /** Currently selected file path (relative). Empty string = none. */
  selectedPath: string;
  /** Fired when the user clicks a `.md` file. */
  onSelectFile: (path: string) => void;
  /** Fired when a load error occurs at the root — lets the page show empty state for "pkm not configured". */
  onRootError?: (error: ApiError | Error) => void;
}

/**
 * Recursive folder tree rooted at the workspace `pkm_path`. Children are
 * fetched lazily — a folder's listing is only requested once the user expands
 * it. TanStack Query caches each path independently so re-expanding is
 * instant after the first fetch.
 *
 * The component itself only owns "which folders are expanded" UI state. All
 * other state (selection, errors) is lifted to the parent so the viewer can
 * react to selection changes without round-tripping through the tree.
 */
export function DocumentTree({
  workspaceId,
  selectedPath,
  onSelectFile,
  onRootError,
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
      />
    );
  }

  const name = path.slice(path.lastIndexOf("/") + 1);
  const Icon = isOpen ? FolderOpen : Folder;

  return (
    <div role="treeitem" aria-expanded={isOpen}>
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
      {isOpen && (
        <FolderChildren
          workspaceId={workspaceId}
          depth={depth}
          expanded={expanded}
          onToggle={onToggle}
          selectedPath={selectedPath}
          onSelectFile={onSelectFile}
          query={query}
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
}

function FileNode({ entry, depth, isSelected, onSelectFile }: FileNodeProps) {
  // Only `.md` files are openable in the read-only viewer. Anything else
  // renders disabled so the user understands it's not browsable yet — image
  // files are referenced by markdown but not stand-alone targets.
  const isMarkdown = entry.name.toLowerCase().endsWith(".md");

  return (
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
}

/** Folders before files; case-insensitive alphabetical within each group. */
function sortEntries(entries: DocumentEntry[]): DocumentEntry[] {
  return [...entries].sort((a, b) => {
    if (a.type !== b.type) return a.type === "folder" ? -1 : 1;
    return a.name.localeCompare(b.name, undefined, { sensitivity: "base" });
  });
}
