/**
 * Read-only filesystem types for the Documents tab — backed by the workspace's
 * configured `pkm_path` on the host filesystem.
 *
 * Shape mirrors the proposed FS read API in MUL-16:
 *   GET /workspaces/:id/documents/tree?path=<rel>
 *   GET /workspaces/:id/documents/file?path=<rel>
 *   GET /workspaces/:id/documents/image?path=<rel>
 *
 * All `path` values are POSIX-style and RELATIVE to the workspace `pkm_path`.
 * The empty string means the configured root.
 */

export type DocumentEntryType = "folder" | "file";

export interface DocumentEntry {
  /** Display name (basename), e.g. "README.md". */
  name: string;
  /** Path relative to the workspace `pkm_path`, e.g. "GROWTH/README.md". */
  path: string;
  type: DocumentEntryType;
  /** File size in bytes; absent for folders. */
  size?: number;
  /** Last-modified time (RFC3339). */
  mtime: string;
}

export interface DocumentTree {
  /** Path of the listed folder relative to `pkm_path`. */
  path: string;
  entries: DocumentEntry[];
}

export interface DocumentFile {
  /** Path relative to `pkm_path`. */
  path: string;
  /** Raw markdown content. */
  content: string;
  size: number;
  mtime: string;
}

/**
 * Server returns a structured "no pkm_path configured" error so the UI can
 * render an inviting empty state with a link to settings instead of a generic
 * error toast. Mapped from the `code` field of an ApiError 400/409 response.
 */
export const PKM_NOT_CONFIGURED_CODE = "pkm_not_configured";
