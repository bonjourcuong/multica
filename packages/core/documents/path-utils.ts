/**
 * Pure helpers for manipulating POSIX-style paths relative to the workspace
 * `pkm_path`. Used by the tree (parent/child math) and the viewer (resolving
 * relative image references against the file's parent folder).
 *
 * All paths are forward-slash, never start with "/", and use empty string for
 * the root. We don't accept absolute or backslash paths — those are an FS API
 * concern and rejected server-side.
 */

/** Drop the trailing basename. "a/b/c.md" → "a/b". Returns "" for top-level. */
export function parentPath(path: string): string {
  const normalized = path.replace(/^\/+|\/+$/g, "");
  const idx = normalized.lastIndexOf("/");
  return idx === -1 ? "" : normalized.slice(0, idx);
}

/** Last segment of a path. "a/b/c.md" → "c.md". */
export function basename(path: string): string {
  const normalized = path.replace(/\/+$/g, "");
  const idx = normalized.lastIndexOf("/");
  return idx === -1 ? normalized : normalized.slice(idx + 1);
}

/** Join segments POSIX-style, ignoring empty parts. */
export function joinPath(...parts: string[]): string {
  return parts
    .filter((p) => p && p !== ".")
    .join("/")
    .replace(/\/+/g, "/");
}

/**
 * Resolve a relative reference (e.g. an image href in a `.md`) against the
 * file's parent folder. Resolution mirrors browser/Node semantics: ".." pops
 * a segment, "." is a no-op. Returns the canonicalized path relative to the
 * workspace `pkm_path`, or null if the reference escapes the root.
 *
 * Absolute hrefs (`/foo`, `http(s)://...`, `data:`, `mailto:`) are not the
 * caller's job — they're filtered out before this is invoked.
 */
export function resolveRelative(fromDir: string, ref: string): string | null {
  if (!ref) return null;
  const baseSegments = fromDir
    .split("/")
    .filter((s) => s && s !== ".");
  const refSegments = ref.split("/").filter((s) => s !== "");
  for (const seg of refSegments) {
    if (seg === ".") continue;
    if (seg === "..") {
      if (baseSegments.length === 0) {
        // ".." would escape the root.
        return null;
      }
      baseSegments.pop();
      continue;
    }
    baseSegments.push(seg);
  }
  return baseSegments.join("/");
}

/**
 * Build breadcrumb segments for a path. Empty string yields `[{ name: "", path: "" }]`,
 * "a/b/c.md" yields root + "a" + "a/b" + "a/b/c.md".
 */
export interface BreadcrumbSegment {
  name: string;
  path: string;
}

export function breadcrumbs(path: string, rootLabel = ""): BreadcrumbSegment[] {
  const normalized = path.replace(/^\/+|\/+$/g, "");
  const segments: BreadcrumbSegment[] = [{ name: rootLabel, path: "" }];
  if (!normalized) return segments;
  const parts = normalized.split("/");
  let cumulative = "";
  for (const part of parts) {
    cumulative = cumulative ? `${cumulative}/${part}` : part;
    segments.push({ name: part, path: cumulative });
  }
  return segments;
}
