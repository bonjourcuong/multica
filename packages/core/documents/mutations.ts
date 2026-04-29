import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { documentKeys } from "./queries";
import { parentPath } from "./path-utils";

// PKM write mutations (MUL-18 endpoints). Each mutation invalidates the
// affected directory's tree query so the sidebar refreshes; file-level
// mutations also invalidate the file query so an open viewer pulls fresh
// content/mtime.

export function useUpdateDocumentFile(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ path, content }: { path: string; content: string }) =>
      api.putDocumentFile(wsId, path, content),
    onSuccess: (_data, { path }) => {
      qc.invalidateQueries({ queryKey: documentKeys.file(wsId, path) });
      qc.invalidateQueries({ queryKey: documentKeys.tree(wsId, parentPath(path)) });
    },
  });
}

export function useCreateDocumentFile(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ path, content }: { path: string; content: string }) =>
      api.createDocumentFile(wsId, path, content),
    onSuccess: (_data, { path }) => {
      qc.invalidateQueries({ queryKey: documentKeys.tree(wsId, parentPath(path)) });
    },
  });
}

export function useCreateDocumentFolder(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ path }: { path: string }) => api.createDocumentFolder(wsId, path),
    onSuccess: (_data, { path }) => {
      qc.invalidateQueries({ queryKey: documentKeys.tree(wsId, parentPath(path)) });
    },
  });
}

export function useDeleteDocumentFile(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ path }: { path: string }) => api.deleteDocumentFile(wsId, path),
    onSuccess: (_data, { path }) => {
      qc.invalidateQueries({ queryKey: documentKeys.tree(wsId, parentPath(path)) });
      qc.removeQueries({ queryKey: documentKeys.file(wsId, path) });
    },
  });
}

export function useDeleteDocumentFolder(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ path, force }: { path: string; force?: boolean }) =>
      api.deleteDocumentFolder(wsId, path, force ?? false),
    onSuccess: (_data, { path }) => {
      // Both the parent listing and the deleted folder's own subtree
      // need to drop their cached entries.
      qc.invalidateQueries({ queryKey: documentKeys.tree(wsId, parentPath(path)) });
      qc.removeQueries({ queryKey: documentKeys.all(wsId) });
    },
  });
}
