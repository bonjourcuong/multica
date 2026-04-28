"use client";

import * as React from "react";
import { useQuery } from "@tanstack/react-query";
import {
  documentFileOptions,
  parentPath,
  resolveRelative,
  breadcrumbs,
  useWriteDocumentFile,
} from "@multica/core/documents";
import { api, ApiError } from "@multica/core/api";
import { Markdown } from "../../common/markdown";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Button } from "@multica/ui/components/ui/button";
import { Textarea } from "@multica/ui/components/ui/textarea";
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@multica/ui/components/ui/breadcrumb";
import {
  AlertTriangle,
  FileText,
  Pencil,
  Save,
  X,
  Loader2,
} from "lucide-react";
import { toast } from "sonner";

interface DocumentViewerProps {
  workspaceId: string;
  /** Path of the file to render, relative to `pkm_path`. Empty string = no selection. */
  path: string;
  /**
   * Lifted dirty signal so the parent can wrap path-change navigation with an
   * unsaved-changes confirm. We don't manage the prompt here — only emit the
   * boolean.
   */
  onDirtyChange?: (dirty: boolean) => void;
}

type ViewerMode = "view" | "edit";

/**
 * Renders the markdown content of a single `.md` file with breadcrumb path,
 * sanitized markdown, and resolved inline images. Toggling into edit mode
 * swaps the rendered markdown for a textarea bound to the same content; the
 * Save button PUTs back to `/documents/file` and re-syncs the cache.
 *
 * The textarea is intentionally a plain monospace `<Textarea>` rather than
 * CodeMirror — keeps the editor dependency-free and the bundle small. We can
 * graduate to a richer editor in a follow-up if Cuong wants syntax highlights.
 *
 * Image resolution: relative refs in the markdown are resolved against the
 * file's parent folder, then rewritten to the documents image API endpoint
 * via `api.documentImageUrl()`. Absolute (`http(s)://`, `data:`) and
 * mention-protocol URLs pass through untouched. References that escape the
 * pkm root are left as-is — the server will refuse them, and we don't want
 * to silently rewrite them to a misleading URL.
 */
export function DocumentViewer({
  workspaceId,
  path,
  onDirtyChange,
}: DocumentViewerProps) {
  const fileQuery = useQuery({
    ...documentFileOptions(workspaceId, path),
    enabled: !!path,
  });

  const dir = React.useMemo(() => parentPath(path), [path]);

  const [mode, setMode] = React.useState<ViewerMode>("view");
  const [draft, setDraft] = React.useState<string>("");
  // The "saved" baseline against which dirty is computed. Updated on every
  // successful PUT and on every fresh server fetch (file change, refetch).
  const [baseline, setBaseline] = React.useState<string | null>(null);

  // Sync local draft+baseline when the server-side content changes (file
  // switch, after-save refetch). We only overwrite the draft when the user
  // is NOT actively editing — discarding their typing on a refetch would be
  // destructive. The save mutation explicitly resets via `setBaseline`.
  React.useEffect(() => {
    if (fileQuery.data === undefined) return;
    const next = fileQuery.data.content;
    setBaseline(next);
    if (mode === "view") {
      setDraft(next);
    }
  }, [fileQuery.data, mode]);

  // Drop edit state when the selected file changes — switching to a different
  // doc must not carry over draft text from the previous file. The parent's
  // unsaved-change guard fires before `path` changes, so by the time we get
  // here the user has already chosen to discard.
  React.useEffect(() => {
    setMode("view");
  }, [path]);

  const isDirty = mode === "edit" && baseline !== null && draft !== baseline;

  React.useEffect(() => {
    onDirtyChange?.(isDirty);
  }, [isDirty, onDirtyChange]);

  // beforeunload guards full-page navigation (refresh, tab close, hard URL
  // change). SPA route changes within the workspace are guarded by the
  // parent via `onDirtyChange` + window.confirm — those don't fire
  // beforeunload at all.
  React.useEffect(() => {
    if (!isDirty) return;
    const handler = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      // Legacy spec — modern browsers ignore the message but require this
      // assignment to actually surface the prompt.
      e.returnValue = "";
    };
    window.addEventListener("beforeunload", handler);
    return () => window.removeEventListener("beforeunload", handler);
  }, [isDirty]);

  const writeMutation = useWriteDocumentFile(workspaceId);

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

  const handleEnterEdit = React.useCallback(() => {
    if (fileQuery.data) {
      setDraft(fileQuery.data.content);
      setBaseline(fileQuery.data.content);
    }
    setMode("edit");
  }, [fileQuery.data]);

  const handleCancel = React.useCallback(() => {
    if (isDirty) {
      const ok = window.confirm(
        "Discard unsaved changes? Your edits will be lost.",
      );
      if (!ok) return;
    }
    if (fileQuery.data) setDraft(fileQuery.data.content);
    setMode("view");
  }, [isDirty, fileQuery.data]);

  const handleSave = React.useCallback(() => {
    if (writeMutation.isPending) return;
    writeMutation.mutate(
      { path, content: draft },
      {
        onSuccess: () => {
          // Anchor the new baseline on the bytes we just sent, so dirty flips
          // off immediately. The cache invalidation will re-fetch and confirm,
          // but the user shouldn't see a flash of "unsaved" while it's in
          // flight.
          setBaseline(draft);
          setMode("view");
          toast.success("Saved");
        },
        onError: (err) => {
          const msg =
            err instanceof ApiError ? err.message : "Failed to save document";
          toast.error(msg);
        },
      },
    );
  }, [draft, path, writeMutation]);

  if (!path) {
    return (
      <div className="flex h-full flex-col items-center justify-center text-muted-foreground">
        <FileText className="mb-3 size-10 text-muted-foreground/30" />
        <p className="text-sm">Select a markdown file to read</p>
      </div>
    );
  }

  const isLoading = fileQuery.isPending;
  const hasError = !!fileQuery.error;
  const content = fileQuery.data;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex h-12 shrink-0 items-center gap-2 border-b px-4">
        <DocumentBreadcrumb path={path} />
        <div className="flex shrink-0 items-center gap-1">
          {mode === "view" ? (
            <Button
              variant="outline"
              size="sm"
              onClick={handleEnterEdit}
              disabled={isLoading || hasError || !content}
            >
              <Pencil className="size-3.5" />
              Edit
            </Button>
          ) : (
            <>
              <Button
                variant="ghost"
                size="sm"
                onClick={handleCancel}
                disabled={writeMutation.isPending}
              >
                <X className="size-3.5" />
                Cancel
              </Button>
              <Button
                variant="default"
                size="sm"
                onClick={handleSave}
                disabled={!isDirty || writeMutation.isPending}
              >
                {writeMutation.isPending ? (
                  <Loader2 className="size-3.5 animate-spin" />
                ) : (
                  <Save className="size-3.5" />
                )}
                Save
              </Button>
            </>
          )}
          {mode === "edit" && (
            <span
              className="ml-1 hidden text-xs text-muted-foreground sm:inline"
              aria-live="polite"
            >
              {isDirty ? "Unsaved" : "Saved"}
            </span>
          )}
        </div>
      </div>
      <div className="flex-1 min-h-0 overflow-y-auto">
        <div className="mx-auto max-w-3xl px-6 py-6">
          {isLoading ? (
            <div className="space-y-3">
              <Skeleton className="h-7 w-2/3" />
              <Skeleton className="h-4 w-full" />
              <Skeleton className="h-4 w-full" />
              <Skeleton className="h-4 w-5/6" />
              <Skeleton className="h-4 w-3/4" />
            </div>
          ) : hasError ? (
            <div className="flex items-center gap-2 rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm text-destructive">
              <AlertTriangle className="size-4 shrink-0" />
              <span>Could not load this document.</span>
            </div>
          ) : mode === "edit" ? (
            <Textarea
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              className="min-h-[60vh] resize-none font-mono text-sm leading-relaxed"
              spellCheck={false}
              aria-label="Markdown editor"
              placeholder="Write markdown..."
            />
          ) : content ? (
            <Markdown mode="full" resolveImageSrc={resolveImageSrc}>
              {content.content}
            </Markdown>
          ) : null}
        </div>
      </div>
    </div>
  );
}

interface DocumentBreadcrumbProps {
  path: string;
}

function DocumentBreadcrumb({ path }: DocumentBreadcrumbProps) {
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
