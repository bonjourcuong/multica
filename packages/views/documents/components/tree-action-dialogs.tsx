"use client";

import * as React from "react";
import { ApiError } from "@multica/core/api";
import {
  useCreateDocumentFile,
  useCreateDocumentFolder,
  useDeleteDocumentFile,
  useDeleteDocumentFolder,
  joinPath,
} from "@multica/core/documents";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
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
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";

// Validation: a single path segment, no slashes, no leading dots, no
// reserved characters. The server enforces stronger rules (rejects `..`,
// NUL bytes, backslashes, absolute paths) — this client check is for fast
// feedback, not security.
const SEGMENT_RE = /^[^\\/\0]+$/;

function validateSegment(name: string, opts: { extension?: string }): string | null {
  const trimmed = name.trim();
  if (!trimmed) return "Name is required";
  if (!SEGMENT_RE.test(trimmed)) return "Name cannot contain / or \\";
  if (trimmed === "." || trimmed === "..") return "Reserved name";
  if (opts.extension && !trimmed.toLowerCase().endsWith(opts.extension)) {
    return `Name must end with ${opts.extension}`;
  }
  return null;
}

interface NewFileDialogProps {
  workspaceId: string;
  /** Folder the new file is being created in (relative). Empty = root. */
  parentPath: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Fired with the newly-created file path on success. */
  onCreated?: (path: string) => void;
}

/**
 * Prompts for a `.md` filename and POSTs to create the file under
 * `parentPath`. The dialog forces the `.md` extension if the user omits it
 * — slight ergonomics, the server would reject other extensions anyway.
 */
export function NewFileDialog({
  workspaceId,
  parentPath,
  open,
  onOpenChange,
  onCreated,
}: NewFileDialogProps) {
  const [name, setName] = React.useState("");
  const [error, setError] = React.useState<string | null>(null);
  const create = useCreateDocumentFile(workspaceId);

  // Reset local form state whenever the dialog closes. We intentionally do
  // NOT depend on `create` here — the mutation result object's identity
  // changes every render, which would refire this effect every render and
  // loop forever. Mutation-internal error state isn't surfaced in the UI
  // anyway (local `error` is the source of truth), so no `create.reset()`
  // call is needed.
  React.useEffect(() => {
    if (!open) {
      setName("");
      setError(null);
    }
  }, [open]);

  const submit = React.useCallback(() => {
    let candidate = name.trim();
    if (candidate && !candidate.toLowerCase().endsWith(".md")) {
      candidate += ".md";
    }
    const validationError = validateSegment(candidate, { extension: ".md" });
    if (validationError) {
      setError(validationError);
      return;
    }
    const fullPath = joinPath(parentPath, candidate);
    create.mutate(
      { path: fullPath, content: "" },
      {
        onSuccess: () => {
          toast.success(`Created ${candidate}`);
          onCreated?.(fullPath);
          onOpenChange(false);
        },
        onError: (err) => {
          if (err instanceof ApiError && err.status === 409) {
            setError("A file with this name already exists");
            return;
          }
          setError(err instanceof Error ? err.message : "Could not create file");
        },
      },
    );
  }, [name, parentPath, create, onCreated, onOpenChange]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New markdown file</DialogTitle>
          <DialogDescription>
            {parentPath ? `Inside ${parentPath}` : "In the root folder"}
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-2">
          <Label htmlFor="new-file-name">Name</Label>
          <Input
            id="new-file-name"
            autoFocus
            value={name}
            onChange={(e) => {
              setName(e.target.value);
              if (error) setError(null);
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                submit();
              }
            }}
            placeholder="example.md"
            aria-invalid={!!error}
            disabled={create.isPending}
          />
          {error && <p className="text-xs text-destructive">{error}</p>}
        </div>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={create.isPending}
          >
            Cancel
          </Button>
          <Button onClick={submit} disabled={create.isPending}>
            {create.isPending && <Loader2 className="size-4 animate-spin" />}
            Create
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

interface NewFolderDialogProps {
  workspaceId: string;
  parentPath: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function NewFolderDialog({
  workspaceId,
  parentPath,
  open,
  onOpenChange,
}: NewFolderDialogProps) {
  const [name, setName] = React.useState("");
  const [error, setError] = React.useState<string | null>(null);
  const create = useCreateDocumentFolder(workspaceId);

  // See NewFileDialog for why `create` is intentionally absent from deps.
  React.useEffect(() => {
    if (!open) {
      setName("");
      setError(null);
    }
  }, [open]);

  const submit = React.useCallback(() => {
    const candidate = name.trim();
    const validationError = validateSegment(candidate, {});
    if (validationError) {
      setError(validationError);
      return;
    }
    const fullPath = joinPath(parentPath, candidate);
    create.mutate(
      { path: fullPath },
      {
        onSuccess: () => {
          toast.success(`Created folder ${candidate}`);
          onOpenChange(false);
        },
        onError: (err) => {
          if (err instanceof ApiError && err.status === 409) {
            setError("A folder with this name already exists");
            return;
          }
          setError(err instanceof Error ? err.message : "Could not create folder");
        },
      },
    );
  }, [name, parentPath, create, onOpenChange]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New folder</DialogTitle>
          <DialogDescription>
            {parentPath ? `Inside ${parentPath}` : "In the root folder"}
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-2">
          <Label htmlFor="new-folder-name">Name</Label>
          <Input
            id="new-folder-name"
            autoFocus
            value={name}
            onChange={(e) => {
              setName(e.target.value);
              if (error) setError(null);
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                submit();
              }
            }}
            placeholder="folder-name"
            aria-invalid={!!error}
            disabled={create.isPending}
          />
          {error && <p className="text-xs text-destructive">{error}</p>}
        </div>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={create.isPending}
          >
            Cancel
          </Button>
          <Button onClick={submit} disabled={create.isPending}>
            {create.isPending && <Loader2 className="size-4 animate-spin" />}
            Create
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

interface DeleteConfirmDialogProps {
  workspaceId: string;
  /** Target path (file or folder) — empty when no target is queued. */
  target: { path: string; type: "file" | "folder" } | null;
  onOpenChange: (open: boolean) => void;
  /** Fired after a successful delete with the deleted path. */
  onDeleted?: (path: string) => void;
}

/**
 * Two-step folder delete: first attempt is non-recursive; if the server
 * returns 409 (not empty), we reveal a "Delete with all contents" action
 * that retries with `?force=true` + the X-Confirm-Force-Delete header.
 *
 * File delete is single-step. Both flows share the same AlertDialog so the
 * tree only needs one piece of state to manage them.
 */
export function DeleteConfirmDialog({
  workspaceId,
  target,
  onOpenChange,
  onDeleted,
}: DeleteConfirmDialogProps) {
  const open = !!target;
  const isFolder = target?.type === "folder";

  const deleteFile = useDeleteDocumentFile(workspaceId);
  const deleteFolder = useDeleteDocumentFolder(workspaceId);
  const [showForce, setShowForce] = React.useState(false);

  // Mutation result objects re-render with new identity each tick — keeping
  // them in deps would loop the reset call forever. Local UI state is enough.
  React.useEffect(() => {
    if (!open) {
      setShowForce(false);
    }
  }, [open]);

  const isPending = deleteFile.isPending || deleteFolder.isPending;

  const runFolderDelete = React.useCallback(
    (force: boolean) => {
      if (!target) return;
      deleteFolder.mutate(
        { path: target.path, force },
        {
          onSuccess: () => {
            toast.success(`Deleted ${target.path}`);
            onDeleted?.(target.path);
            onOpenChange(false);
          },
          onError: (err) => {
            if (err instanceof ApiError && err.status === 409) {
              setShowForce(true);
              return;
            }
            toast.error(err instanceof Error ? err.message : "Delete failed");
          },
        },
      );
    },
    [target, deleteFolder, onDeleted, onOpenChange],
  );

  const handleConfirm = React.useCallback(() => {
    if (!target) return;
    if (target.type === "folder") {
      runFolderDelete(false);
      return;
    }
    deleteFile.mutate(
      { path: target.path },
      {
        onSuccess: () => {
          toast.success(`Deleted ${target.path}`);
          onDeleted?.(target.path);
          onOpenChange(false);
        },
        onError: (err) => {
          toast.error(err instanceof Error ? err.message : "Delete failed");
        },
      },
    );
  }, [target, deleteFile, runFolderDelete, onDeleted, onOpenChange]);

  return (
    <AlertDialog
      open={open}
      onOpenChange={(v) => {
        if (!v) onOpenChange(false);
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {isFolder ? "Delete folder" : "Delete file"}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {showForce ? (
              <>
                <span className="block">
                  <strong className="font-medium text-foreground">
                    {target?.path}
                  </strong>{" "}
                  is not empty.
                </span>
                <span className="mt-1 block">
                  Permanently delete the folder and everything it contains?
                  This cannot be undone.
                </span>
              </>
            ) : (
              <>
                Permanently delete{" "}
                <strong className="font-medium text-foreground">
                  {target?.path}
                </strong>
                ? This cannot be undone.
              </>
            )}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={isPending}>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            onClick={() => {
              if (showForce) runFolderDelete(true);
              else handleConfirm();
            }}
            disabled={isPending}
          >
            {isPending && <Loader2 className="size-4 animate-spin" />}
            {showForce ? "Delete with contents" : "Delete"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
