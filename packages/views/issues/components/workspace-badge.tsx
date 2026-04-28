"use client";

import { cn } from "@multica/ui/lib/utils";

/**
 * Compact chip used inside cross-workspace issue cards to identify the
 * owning workspace at a glance. Pairs the deterministic workspace color
 * (server-derived, see ADR 0001 §1.6) with the workspace's issue prefix —
 * the same prefix already shown in the issue identifier (e.g. `MUL-12`),
 * so the chip both colors and labels the card without redundant text.
 *
 * `name` is rendered into a `title` attribute for hover discoverability,
 * but never shown inline — keeping the chip narrow lets it sit on the
 * same row as the identifier without truncating long workspace names.
 */
export function WorkspaceBadge({
  color,
  prefix,
  name,
  className,
}: {
  /** Server-derived hex color (e.g. `#7c3aed`). */
  color: string;
  /** Issue prefix, e.g. `MUL`. */
  prefix: string;
  /** Workspace name. Used for the hover tooltip and a11y label. */
  name?: string;
  className?: string;
}) {
  const label = name ? `${name} (${prefix})` : prefix;
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-white",
        className,
      )}
      style={{ backgroundColor: color }}
      title={label}
      aria-label={label}
      data-testid="workspace-badge"
    >
      {prefix}
    </span>
  );
}
