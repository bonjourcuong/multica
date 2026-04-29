"use client";

import * as React from "react";
import { toast } from "sonner";
import { ApiError } from "@multica/core/api";
import {
  joinPath,
  useCreateDocumentFile,
  useCreateDocumentFolder,
} from "@multica/core/documents";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";

interface CreateEntryDialogProps {
  workspaceId: string;
  /** Folder under which the new entry is created. "" = workspace root. */
  parentPath: string;
  kind: "file" | "folder";
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Fired with the new entry's path after a successful create. */
  onCreated?: (path: string) => void;
}

/**
 * Single-input dialog for creating a new note or folder. The .md extension
 * is auto-appended for notes if the user omits it, so the input stays clean.
 *
 * Validation is deliberately minimal — the server is the source of truth on
 * what's a legal path. We only block the obviously-broken inputs (empty,
 * slash, leading/trailing dot) so we don't round-trip a 400 for those.
 */
export function CreateEntryDialog({
  workspaceId,
  parentPath,
  kind,
  open,
  onOpenChange,
  onCreated,
}: CreateEntryDialogProps) {
  const [name, setName] = React.useState("");
  const [error, setError] = React.useState<string | null>(null);
  const createFile = useCreateDocumentFile(workspaceId);
  const createFolder = useCreateDocumentFolder(workspaceId);
  const submitting = createFile.isPending || createFolder.isPending;

  // Reset state every time the dialog opens so a previous error or stale
  // value doesn't surface on the next open.
  React.useEffect(() => {
    if (open) {
      setName("");
      setError(null);
    }
  }, [open]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) {
      setError("Name is required");
      return;
    }
    if (trimmed.includes("/")) {
      setError("Name cannot contain '/'");
      return;
    }
    if (trimmed.startsWith(".") || trimmed.endsWith(".")) {
      setError("Name cannot start or end with '.'");
      return;
    }

    const filename =
      kind === "file" && !trimmed.toLowerCase().endsWith(".md")
        ? `${trimmed}.md`
        : trimmed;
    const path = joinPath(parentPath, filename);

    try {
      if (kind === "file") {
        await createFile.mutateAsync({ path, content: "" });
      } else {
        await createFolder.mutateAsync({ path });
      }
      toast.success(kind === "file" ? "Note created" : "Folder created");
      onCreated?.(path);
      onOpenChange(false);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to create");
    }
  };

  const title = kind === "file" ? "New note" : "New folder";
  const placeholder =
    kind === "file" ? "my-note (or my-note.md)" : "my-folder";
  const help =
    kind === "file"
      ? "Created as an empty markdown file. The .md extension is added automatically."
      : "Empty folder, ready for notes.";
  const inWhere = parentPath ? `in /${parentPath}` : "at workspace root";

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>Create {inWhere}.</DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="entry-name" className="text-xs text-muted-foreground">
              Name
            </Label>
            <Input
              id="entry-name"
              value={name}
              onChange={(e) => {
                setName(e.target.value);
                if (error) setError(null);
              }}
              placeholder={placeholder}
              autoFocus
              disabled={submitting}
              aria-invalid={error ? true : undefined}
            />
            {error ? (
              <p className="text-xs text-destructive" role="alert">
                {error}
              </p>
            ) : (
              <p className="text-xs text-muted-foreground">{help}</p>
            )}
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              onClick={() => onOpenChange(false)}
              disabled={submitting}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={submitting || !name.trim()}>
              {submitting ? "Creating..." : "Create"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
