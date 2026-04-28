import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { documentKeys } from "./queries";
import { parentPath } from "./path-utils";
import type { DocumentWriteResult } from "../types";

// All mutations invalidate the affected folder listing AND the file cache so
// the tree and viewer see the change without a manual refetch. Folder paths
// are computed from the target path via `parentPath` — the same helper the
// viewer uses for breadcrumb math, so the keys stay consistent.

interface FilePathArgs {
  path: string;
}

interface FileWriteArgs extends FilePathArgs {
  content: string;
}

export function useWriteDocumentFile(workspaceId: string) {
  const qc = useQueryClient();
  return useMutation<DocumentWriteResult, Error, FileWriteArgs>({
    mutationFn: ({ path, content }) =>
      api.writeDocumentFile(workspaceId, path, content),
    onSuccess: (_data, { path }) => {
      qc.invalidateQueries({ queryKey: documentKeys.file(workspaceId, path) });
      qc.invalidateQueries({
        queryKey: documentKeys.tree(workspaceId, parentPath(path)),
      });
    },
  });
}

export function useCreateDocumentFile(workspaceId: string) {
  const qc = useQueryClient();
  return useMutation<DocumentWriteResult, Error, FileWriteArgs>({
    mutationFn: ({ path, content }) =>
      api.createDocumentFile(workspaceId, path, content),
    onSuccess: (_data, { path }) => {
      qc.invalidateQueries({
        queryKey: documentKeys.tree(workspaceId, parentPath(path)),
      });
    },
  });
}

export function useDeleteDocumentFile(workspaceId: string) {
  const qc = useQueryClient();
  return useMutation<void, Error, FilePathArgs>({
    mutationFn: ({ path }) => api.deleteDocumentFile(workspaceId, path),
    onSuccess: (_data, { path }) => {
      qc.invalidateQueries({
        queryKey: documentKeys.tree(workspaceId, parentPath(path)),
      });
      qc.removeQueries({ queryKey: documentKeys.file(workspaceId, path) });
    },
  });
}

export function useCreateDocumentFolder(workspaceId: string) {
  const qc = useQueryClient();
  return useMutation<{ path: string }, Error, FilePathArgs>({
    mutationFn: ({ path }) => api.createDocumentFolder(workspaceId, path),
    onSuccess: (_data, { path }) => {
      qc.invalidateQueries({
        queryKey: documentKeys.tree(workspaceId, parentPath(path)),
      });
    },
  });
}

interface FolderDeleteArgs extends FilePathArgs {
  /** Recursive delete — server requires the X-Confirm-Force-Delete header. */
  force?: boolean;
}

export function useDeleteDocumentFolder(workspaceId: string) {
  const qc = useQueryClient();
  return useMutation<void, Error, FolderDeleteArgs>({
    mutationFn: ({ path, force }) =>
      api.deleteDocumentFolder(workspaceId, path, { force }),
    onSuccess: (_data, { path }) => {
      qc.invalidateQueries({
        queryKey: documentKeys.tree(workspaceId, parentPath(path)),
      });
      qc.removeQueries({
        queryKey: documentKeys.tree(workspaceId, path),
      });
    },
  });
}
