/**
 * MUL-33 Task 16 — Global orchestrator chat: permissions / membership filter.
 *
 * Mirrors spec section 9 and audits ADR risk R5: a user MUST NOT be able to
 * dispatch into a workspace they're not a member of, regardless of how they
 * spell the mention.
 *
 * The default E2E user posts `@<stranger> hello` from `/global/chat`.
 * The backend must:
 *   - return a dispatch entry whose `error` mentions "Je n'ai pas accès" so
 *     the chat surfaces the rejection within 5s (chat-log redundancy);
 *   - NOT create any global_mirror chat_session in the stranger workspace
 *     for this user;
 *   - NOT insert any chat_message in any global_mirror session of the
 *     stranger workspace whose body carries our run marker.
 *
 * Tile-state note (MUL-99): the workspace list is membership-filtered, so
 * the stranger workspace has no tile in the user's grid. The
 * `not_authorized` tile state in the implementation covers the
 * defense-in-depth case where a workspace IS in the user's list but the
 * dispatch is rejected anyway (e.g. stale local cache); that path is
 * exercised by the unit tests in `packages/views/global-chat/__tests__`.
 *
 * This is the cross-workspace membership filter under test, not just a UI
 * politeness check — silent passes here would be a privilege-escalation bug.
 */

import { test, expect } from "@playwright/test";
import { loginAsDefault, createTestApi } from "./helpers";
import {
  STRANGER_USER_EMAIL,
  STRANGER_WORKSPACE_SLUG,
  countMirrorMessagesContaining,
  countMirrorSessions,
  ensureStrangerWorkspace,
  getUserIdByEmail,
  listGlobalMirrors,
} from "./global-chat-helpers";
import type { TestApiClient } from "./fixtures";

const DEFAULT_E2E_EMAIL = "e2e@multica.ai";

test.describe("Global chat — cross-workspace permissions", () => {
  let api: TestApiClient;
  let strangerWorkspaceId: string;
  let defaultUserId: string;

  test.beforeEach(async ({ page }) => {
    // Create the stranger user + workspace BEFORE the default user logs in,
    // so the dispatch target exists. The default user is intentionally NOT
    // added to this workspace.
    const stranger = await ensureStrangerWorkspace();
    strangerWorkspaceId = stranger.workspace_id;

    const uid = await getUserIdByEmail(DEFAULT_E2E_EMAIL);
    if (!uid) {
      throw new Error(`could not resolve user id for ${DEFAULT_E2E_EMAIL}`);
    }
    defaultUserId = uid;

    api = await createTestApi();
    await loginAsDefault(page);
  });

  test.afterEach(async () => {
    if (api) await api.cleanup();
  });

  test("rejects @workspace dispatch from a non-member with no DB side-effect", async ({
    page,
  }) => {
    // Snapshot the side-effect counters BEFORE the action so the post-action
    // assertion is robust against pre-existing state (e.g. older test runs).
    const sessionsBefore = await countMirrorSessions(
      strangerWorkspaceId,
      defaultUserId,
    );

    await page.goto("/global/chat");

    const input = page.getByTestId("global-chat-input");
    await expect(input).toBeVisible({ timeout: 10000 });

    const marker = `E2E-DENY-${Date.now()}`;
    const body = `@${STRANGER_WORKSPACE_SLUG} hello ${marker}`;

    const postPromise = page.waitForResponse(
      (resp) =>
        resp.url().includes("/api/global/chat/sessions/me/messages") &&
        resp.request().method() === "POST",
      { timeout: 5000 },
    );

    await input.fill(body);
    await page.getByRole("button", { name: "Send message" }).click();

    const post = await postPromise;
    expect(post.status()).toBe(201);
    const payload = (await post.json()) as {
      message: { body: string };
      dispatch: {
        workspace_slug: string;
        workspace_id?: string;
        mirror_session_id?: string;
        mirror_message_id?: string;
        error?: string;
      }[];
      mentions: { workspace_slug: string }[];
    };

    // The chat persists the user message regardless of dispatch outcome —
    // that's by design (audit trail + retry surface).
    expect(payload.message.body).toBe(body);
    expect(payload.mentions).toEqual([
      { workspace_slug: STRANGER_WORKSPACE_SLUG },
    ]);

    // The dispatch entry MUST carry a humanised "no access" rejection and
    // MUST NOT contain any of the IDs that a successful dispatch would
    // populate. Asserting the absence of mirror_session_id is the most
    // direct sign that no chat_session was created server-side.
    expect(payload.dispatch).toHaveLength(1);
    const target = payload.dispatch[0]!;
    expect(target.workspace_slug).toBe(STRANGER_WORKSPACE_SLUG);
    expect(target.error ?? "").toMatch(/Je n'ai pas accès/);
    expect(target.workspace_id).toBeFalsy();
    expect(target.mirror_session_id).toBeFalsy();
    expect(target.mirror_message_id).toBeFalsy();

    // The user message body itself shows up in the global chat log —
    // surfaces the rejected attempt to the user without leaking that the
    // workspace exists. Kept as the chat-log redundancy assertion called
    // out in the MUL-99 DoD.
    await expect(
      page.getByTestId("global-chat-messages").getByText(body),
    ).toBeVisible({ timeout: 5000 });

    // No tile renders for the stranger workspace — the workspace list is
    // membership-filtered, so non-members never see the slug as a tile.
    // This proves there's no UI leak even though the user typed the slug
    // in the composer.
    await expect(
      page.locator(
        `[data-testid="workspace-tile"][data-workspace-slug="${STRANGER_WORKSPACE_SLUG}"]`,
      ),
    ).toHaveCount(0);

    // ADR risk R5 audit — the membership filter must reject the call
    // BEFORE any write happens. Two independent DB checks:
    //   (a) zero global_mirror sessions for (stranger workspace, default user)
    //       compared to the pre-action snapshot, and
    //   (b) zero global_mirror messages anywhere in the stranger workspace
    //       carrying our run marker (catches the case where a write somehow
    //       lands on a pre-existing session).
    const sessionsAfter = await countMirrorSessions(
      strangerWorkspaceId,
      defaultUserId,
    );
    expect(sessionsAfter).toBe(sessionsBefore);

    const messagesWithMarker = await countMirrorMessagesContaining(
      strangerWorkspaceId,
      marker,
    );
    expect(messagesWithMarker).toBe(0);

    // /api/global/chat/mirrors must not surface the stranger workspace at
    // all for the default user — same membership filter as the dispatch
    // path, applied to the read endpoint that drives the tile grid.
    const token = api.getToken();
    if (!token) throw new Error("expected an auth token after login");
    const mirrors = await listGlobalMirrors(token);
    expect(
      mirrors.some((m) => m.workspace_id === strangerWorkspaceId),
      "stranger workspace must not appear in /api/global/chat/mirrors for non-member",
    ).toBe(false);

    // Stranger user accessor sanity — ensures the test isn't silently
    // reading the wrong account if email seeding regresses in the future.
    const strangerId = await getUserIdByEmail(STRANGER_USER_EMAIL);
    expect(strangerId).not.toBe(defaultUserId);
  });
});
