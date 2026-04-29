"use client";

import * as React from "react";
import { useQuery } from "@tanstack/react-query";
import {
  documentFileOptions,
  parentPath,
  resolveRelative,
  breadcrumbs,
  useUpdateDocumentFile,
} from "@multica/core/documents";
import { ApiError, api } from "@multica/core/api";
import { toast } from "sonner";
import { Markdown } from "../../common/markdown";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Button } from "@multica/ui/components/ui/button";
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@multica/ui/components/ui/breadcrumb";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { AlertTriangle, FileText, Pencil, Save, X } from "lucide-react";

interface DocumentViewerProps {
  workspaceId: string;
  /** Path of the file to render, relative to `pkm_path`. Empty string = no selection. */
  path: string;
}

/**
 * Renders or edits the markdown content of a single `.md` file. View mode
 * sanitizes and rewrites images; edit mode swaps the rendered output for a
 * monospaced textarea bound to the same content.
 *
 * Conflict mitigation: the file query is invalidated on window focus so a
 * user returning to the tab after editing the file in an external editor
 * sees the fresh version before they overwrite it. There is no real lock —
 * concurrent writes still race, but the failure window shrinks.
 */
export function DocumentViewer({ workspaceId, path }: DocumentViewerProps) {
  const fileQuery = useQuery({
    ...documentFileOptions(workspaceId, path),
    enabled: !!path,
    refetchOnWindowFocus: true,
  });
  const updateFile = useUpdateDocumentFile(workspaceId);

  const [mode, setMode] = React.useState<"view" | "edit">("view");
  const [draft, setDraft] = React.useState("");
  const [discardOpen, setDiscardOpen] = React.useState(false);
  const dirty = mode === "edit" && draft !== (fileQuery.data?.content ?? "");

  // Reset edit state when switching files. Without this, opening a new file
  // while still in edit mode would show the previous file's draft.
  React.useEffect(() => {
    setMode("view");
    setDraft("");
  }, [path]);

  const enterEditMode = React.useCallback(() => {
    if (!fileQuery.data) return;
    setDraft(fileQuery.data.content);
    setMode("edit");
  }, [fileQuery.data]);

  const exitEditMode = React.useCallback(() => {
    if (dirty) {
      setDiscardOpen(true);
      return;
    }
    setMode("view");
    setDraft("");
  }, [dirty]);

  const save = React.useCallback(async () => {
    if (!path || !dirty) return;
    try {
      await updateFile.mutateAsync({ path, content: draft });
      toast.success("Saved");
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : "Failed to save";
      toast.error(msg);
    }
  }, [path, dirty, draft, updateFile]);

  // Cmd/Ctrl+S saves without exiting edit mode. Listener attaches only in
  // edit mode to avoid intercepting save in unrelated views.
  React.useEffect(() => {
    if (mode !== "edit") return;
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "s") {
        e.preventDefault();
        void save();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [mode, save]);

  const dir = React.useMemo(() => parentPath(path), [path]);

  const resolveImageSrc = React.useCallback(
    (src: string): string | undefined => {
      if (!src) return undefined;
      if (/^[a-z][a-z0-9+.-]*:/i.test(src)) return undefined; // http:, https:, data:, mention:, etc.
      if (src.startsWith("//")) return undefined; // protocol-relative
      if (src.startsWith("/")) return undefined; // absolute path — out of scope for read-only viewer
      const resolved = resolveRelative(dir, src);
      if (resolved === null) return undefined;
      return api.documentImageUrl(workspaceId, resolved);
    },
    [workspaceId, dir],
  );

  if (!path) {
    return (
      <div className="flex h-full flex-col items-center justify-center text-muted-foreground">
        <FileText className="mb-3 size-10 text-muted-foreground/30" />
        <p className="text-sm">Select a markdown file to read</p>
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex h-12 shrink-0 items-center gap-2 border-b px-4">
        <DocumentBreadcrumb path={path} />
        <div className="flex items-center gap-1">
          {mode === "view" ? (
            <Button
              variant="ghost"
              size="sm"
              onClick={enterEditMode}
              disabled={!fileQuery.data}
              aria-label="Edit document"
            >
              <Pencil className="size-4" />
              Edit
            </Button>
          ) : (
            <>
              {dirty && (
                <span className="text-xs text-muted-foreground" aria-live="polite">
                  Unsaved
                </span>
              )}
              <Button
                variant="default"
                size="sm"
                onClick={save}
                disabled={!dirty || updateFile.isPending}
              >
                <Save className="size-4" />
                {updateFile.isPending ? "Saving..." : "Save"}
              </Button>
              <Button
                variant="ghost"
                size="sm"
                onClick={exitEditMode}
                aria-label="Exit edit mode"
              >
                <X className="size-4" />
              </Button>
            </>
          )}
        </div>
      </div>
      <div className="flex-1 min-h-0 overflow-y-auto">
        {mode === "edit" ? (
          <textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            className="h-full w-full resize-none border-0 bg-background p-6 font-mono text-sm leading-relaxed focus:outline-none"
            spellCheck={false}
            autoFocus
            aria-label="Document content"
          />
        ) : (
          <div className="mx-auto max-w-3xl px-6 py-6">
            {fileQuery.isPending ? (
              <div className="space-y-3">
                <Skeleton className="h-7 w-2/3" />
                <Skeleton className="h-4 w-full" />
                <Skeleton className="h-4 w-full" />
                <Skeleton className="h-4 w-5/6" />
                <Skeleton className="h-4 w-3/4" />
              </div>
            ) : fileQuery.error ? (
              <div className="flex items-center gap-2 rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm text-destructive">
                <AlertTriangle className="size-4 shrink-0" />
                <span>Could not load this document.</span>
              </div>
            ) : fileQuery.data ? (
              <Markdown mode="full" resolveImageSrc={resolveImageSrc}>
                {fileQuery.data.content}
              </Markdown>
            ) : null}
          </div>
        )}
      </div>

      <AlertDialog open={discardOpen} onOpenChange={setDiscardOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Discard unsaved changes?</AlertDialogTitle>
            <AlertDialogDescription>
              Your edits to this document will be lost.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Keep editing</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                setDiscardOpen(false);
                setMode("view");
                setDraft("");
              }}
            >
              Discard
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function DocumentBreadcrumb({ path }: { path: string }) {
  const segments = breadcrumbs(path, "Documents");
  const last = segments[segments.length - 1];
  if (!last) return null;
  return (
    <Breadcrumb className="min-w-0 flex-1 overflow-hidden">
      <BreadcrumbList className="flex-nowrap">
        {segments.slice(0, -1).map((seg, i) => (
          <React.Fragment key={`${seg.path}-${i}`}>
            <BreadcrumbItem className="min-w-0 truncate">
              <span className="truncate text-muted-foreground">{seg.name || "Documents"}</span>
            </BreadcrumbItem>
            <BreadcrumbSeparator />
          </React.Fragment>
        ))}
        <BreadcrumbItem className="min-w-0 truncate">
          <BreadcrumbPage className="truncate text-foreground">{last.name}</BreadcrumbPage>
        </BreadcrumbItem>
      </BreadcrumbList>
    </Breadcrumb>
  );
}
