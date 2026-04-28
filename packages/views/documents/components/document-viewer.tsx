"use client";

import * as React from "react";
import { useQuery } from "@tanstack/react-query";
import {
  documentFileOptions,
  parentPath,
  resolveRelative,
  breadcrumbs,
} from "@multica/core/documents";
import { api } from "@multica/core/api";
import { Markdown } from "../../common/markdown";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@multica/ui/components/ui/breadcrumb";
import { AlertTriangle, FileText } from "lucide-react";

interface DocumentViewerProps {
  workspaceId: string;
  /** Path of the file to render, relative to `pkm_path`. Empty string = no selection. */
  path: string;
}

/**
 * Renders the markdown content of a single `.md` file with breadcrumb path,
 * sanitized markdown, and resolved inline images.
 *
 * Image resolution: relative refs in the markdown are resolved against the
 * file's parent folder, then rewritten to the documents image API endpoint
 * via `api.documentImageUrl()`. Absolute (`http(s)://`, `data:`) and
 * mention-protocol URLs pass through untouched. References that escape the
 * pkm root are left as-is — the server will refuse them, and we don't want
 * to silently rewrite them to a misleading URL.
 */
export function DocumentViewer({ workspaceId, path }: DocumentViewerProps) {
  const fileQuery = useQuery({
    ...documentFileOptions(workspaceId, path),
    enabled: !!path,
  });

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
      <div className="flex h-12 shrink-0 items-center border-b px-4">
        <DocumentBreadcrumb path={path} />
      </div>
      <div className="flex-1 min-h-0 overflow-y-auto">
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
      </div>
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
