import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

// All workspace-scoped queries are keyed on `wsId` — switching workspaces
// changes the cache key automatically (per CLAUDE.md state rules).
export const documentKeys = {
  all: (wsId: string) => ["documents", wsId] as const,
  tree: (wsId: string, path: string) =>
    [...documentKeys.all(wsId), "tree", path] as const,
  file: (wsId: string, path: string) =>
    [...documentKeys.all(wsId), "file", path] as const,
};

/**
 * Lazy folder listing. The tree component fetches the root on mount and one
 * level deeper each time the user expands a folder — the per-path cache key
 * means each subfolder is fetched and cached independently.
 */
export function documentTreeOptions(wsId: string, path: string) {
  return queryOptions({
    queryKey: documentKeys.tree(wsId, path),
    queryFn: () => api.listDocumentTree(wsId, path),
  });
}

export function documentFileOptions(wsId: string, path: string) {
  return queryOptions({
    queryKey: documentKeys.file(wsId, path),
    queryFn: () => api.getDocumentFile(wsId, path),
  });
}
