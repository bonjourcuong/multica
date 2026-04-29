"use client";

import * as React from "react";
import { toast } from "sonner";
import { ApiError } from "@multica/core/api";
import {
  useDeleteDocumentFile,
  useDeleteDocumentFolder,
} from "@multica/core/documents";
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

interface DeleteEntryDialogProps {
  workspaceId: string;
  /** Path being deleted, relative to pkm_path. */
  path: string;
  type: "file" | "folder";
  /** Display name (basename) shown in the prompt. */
  name: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Fired after a successful delete; viewer can then drop selection. */
  onDeleted?: () => void;
}

/**
 * Two-stage confirm for deletes. Files delete in one step. Folders try a
 * non-recursive delete first; if the server replies 409 (not empty), the
 * dialog re-renders with a stronger warning and a "Delete with all
 * contents" button that retries with `force=true` and the
 * X-Confirm-Force-Delete header (handled by the API client).
 */
export function DeleteEntryDialog({
  workspaceId,
  path,
  type,
  name,
  open,
  onOpenChange,
  onDeleted,
}: DeleteEntryDialogProps) {
  const [needsForce, setNeedsForce] = React.useState(false);
  const deleteFile = useDeleteDocumentFile(workspaceId);
  const deleteFolder = useDeleteDocumentFolder(workspaceId);
  const submitting = deleteFile.isPending || deleteFolder.isPending;

  React.useEffect(() => {
    if (open) setNeedsForce(false);
  }, [open]);

  const runDelete = async (force: boolean) => {
    try {
      if (type === "file") {
        await deleteFile.mutateAsync({ path });
      } else {
        await deleteFolder.mutateAsync({ path, force });
      }
      toast.success(type === "file" ? "Note deleted" : "Folder deleted");
      onDeleted?.();
      onOpenChange(false);
    } catch (err) {
      // 409 on a folder delete means "not empty" — show the force step
      // instead of a toast so the user can choose to recurse.
      if (
        type === "folder" &&
        !force &&
        err instanceof ApiError &&
        err.status === 409
      ) {
        setNeedsForce(true);
        return;
      }
      toast.error(err instanceof ApiError ? err.message : "Failed to delete");
    }
  };

  const isFolderForce = type === "folder" && needsForce;

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {isFolderForce ? "Folder is not empty" : `Delete ${type}?`}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {isFolderForce ? (
              <>
                <strong>{name}</strong> contains other files or folders.
                Deleting it will permanently remove all of its contents.
              </>
            ) : (
              <>
                Permanently delete <strong>{name}</strong>? This cannot be
                undone.
              </>
            )}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={submitting}>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            onClick={(e) => {
              e.preventDefault();
              void runDelete(isFolderForce);
            }}
            disabled={submitting}
          >
            {submitting
              ? "Deleting..."
              : isFolderForce
                ? "Delete with all contents"
                : "Delete"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
