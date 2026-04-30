import type { GlobalDispatchTarget } from "../types";

/**
 * UI state surfaced on each workspace tile while the user is composing or
 * after they have just dispatched a `@workspace` mention from the global
 * chat pane. Exhaustive on purpose so the tile component can render every
 * outcome the backend might return.
 */
export type TileDispatchState =
  | "idle"
  | "sending"
  | "delivered"
  | "not_authorized"
  | "error";

/**
 * The Go handler humanises `service.ErrNotWorkspaceMember` to a French
 * sentence prefixed with this marker. We pattern-match the prefix because
 * the protocol does not (yet) carry a typed error code — adding one would
 * be a backend change and is intentionally out of scope for MUL-99.
 *
 * If the humanizer ever changes, update this constant in lockstep with
 * `humanizeDispatchError` in server/internal/handler/global_chat.go.
 */
const NOT_AUTHORIZED_MARKER = "Je n'ai pas accès";

/**
 * Classifies one server-returned dispatch entry into a tile state.
 * `delivered` requires no error; the workspace_id may be empty in
 * theoretical edge cases but the absence of an error is the contract.
 */
export function classifyDispatchTarget(
  target: GlobalDispatchTarget,
): TileDispatchState {
  if (target.error && target.error.length > 0) {
    return target.error.startsWith(NOT_AUTHORIZED_MARKER)
      ? "not_authorized"
      : "error";
  }
  return "delivered";
}

/**
 * Mirrors `server/internal/mention/workspace.go::workspaceMentionRe`. The
 * frontend needs this to mark sending tiles immediately on submit, before
 * the server responds. Keep the two regexes in sync — a divergence shows
 * up as tiles that never enter `sending` even though the backend dispatched.
 *
 * Note: we use a global flag and the `/g` exec loop instead of `matchAll`
 * because TS lib targets older than ES2020 still need that fallback.
 */
const WORKSPACE_MENTION_RE =
  /(?:^|\s)@([a-z0-9][a-z0-9-]+)(?::([A-Za-z][A-Za-z0-9_-]*))?/g;

/**
 * Returns workspace slugs mentioned via `@slug[:agent]` in a chat body, in
 * the order they appear. Duplicates are preserved (the backend dedupes
 * when it dispatches; the frontend just needs the set of targets to mark).
 */
export function parseMentionSlugs(body: string): string[] {
  const out: string[] = [];
  WORKSPACE_MENTION_RE.lastIndex = 0;
  let match: RegExpExecArray | null;
  while ((match = WORKSPACE_MENTION_RE.exec(body)) !== null) {
    out.push(match[1]!);
  }
  return out;
}
