package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/auth"
)

// MUL-201 / ADR 2026-05-03 — six table-driven tests for the daemon-token
// mint handler. Each test isolates the (workspace_id, daemon_id) namespace
// it touches and cleans up via t.Cleanup so they can run in parallel and in
// any order without colliding through the shared testWorkspaceID.

const daemonTokenTestDaemonPrefix = "daemon-token-test-"

// dtTestDaemonID returns a deterministic, test-scoped daemon_id so the row
// it produces does not collide with any other test's pre-seed or the
// fixture's NULL-daemon_id runtime row. Using the test name guarantees
// uniqueness without per-test bookkeeping.
func dtTestDaemonID(t *testing.T) string {
	t.Helper()
	return daemonTokenTestDaemonPrefix + t.Name()
}

// dtCleanupRows wipes any daemon_token rows that this test inserted (via the
// handler) and any agent_runtime rows the test pre-seeded for its scoped
// daemon_id. Registered with t.Cleanup so a panic mid-test still removes the
// rows.
func dtCleanupRows(t *testing.T, daemonID string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := testPool.Exec(ctx, `DELETE FROM daemon_token WHERE daemon_id = $1`, daemonID); err != nil {
			t.Logf("daemon_token cleanup failed for %s: %v", daemonID, err)
		}
		if _, err := testPool.Exec(ctx, `DELETE FROM agent_runtime WHERE daemon_id = $1`, daemonID); err != nil {
			t.Logf("agent_runtime cleanup failed for %s: %v", daemonID, err)
		}
	})
}

// dtCreateExtraUser inserts a second user that is also a member of the
// shared testWorkspaceID. Returned user_id is used as the "other party" in
// cross-user ownership tests. Cleaned up via t.Cleanup.
func dtCreateExtraUser(t *testing.T, label string) string {
	t.Helper()
	ctx := context.Background()
	email := fmt.Sprintf("daemon-token-test-%s-%d@multica.ai", label, time.Now().UnixNano())

	var userID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		"DT Test "+label, email,
	).Scan(&userID); err != nil {
		t.Fatalf("create extra user: %v", err)
	}
	if _, err := testPool.Exec(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')`,
		testWorkspaceID, userID,
	); err != nil {
		t.Fatalf("add extra user as workspace member: %v", err)
	}
	t.Cleanup(func() {
		// member rows cascade on user delete via FK.
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	return userID
}

func dtPostMint(t *testing.T, userID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequest("POST", "/api/daemon-tokens", body)
	if userID != "" {
		req.Header.Set("X-User-ID", userID)
	}
	w := httptest.NewRecorder()
	testHandler.CreateDaemonToken(w, req)
	return w
}

func dtCountRows(t *testing.T, daemonID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM daemon_token WHERE workspace_id = $1 AND daemon_id = $2`,
		testWorkspaceID, daemonID,
	).Scan(&n); err != nil {
		t.Fatalf("count daemon_token rows: %v", err)
	}
	return n
}

func dtFetchSingleRow(t *testing.T, daemonID string) (tokenHash, userID string) {
	t.Helper()
	if err := testPool.QueryRow(context.Background(),
		`SELECT token_hash, user_id FROM daemon_token
		 WHERE workspace_id = $1 AND daemon_id = $2`,
		testWorkspaceID, daemonID,
	).Scan(&tokenHash, &userID); err != nil {
		t.Fatalf("fetch daemon_token row: %v", err)
	}
	return tokenHash, userID
}

// 1. Happy path — mint returns mdt_…, row exists with correct fields,
//    expires_at ≈ now + 90d.
func TestCreateDaemonToken_HappyPath(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	daemonID := dtTestDaemonID(t)
	dtCleanupRows(t, daemonID)

	w := dtPostMint(t, "", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    daemonID,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp CreateDaemonTokenResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.HasPrefix(resp.Token, "mdt_") {
		t.Errorf("expected mdt_ prefix on token, got %q", resp.Token)
	}
	if resp.WorkspaceID != testWorkspaceID || resp.DaemonID != daemonID {
		t.Errorf("response identifiers wrong: ws=%s daemon=%s", resp.WorkspaceID, resp.DaemonID)
	}

	expiresAt, err := time.Parse(time.RFC3339, resp.ExpiresAt)
	if err != nil {
		t.Fatalf("parse expires_at: %v", err)
	}
	want := time.Now().Add(90 * 24 * time.Hour)
	delta := expiresAt.Sub(want)
	if delta < -2*time.Minute || delta > 2*time.Minute {
		t.Errorf("expires_at %s not within 2min of want %s (delta %s)", expiresAt, want, delta)
	}

	if dtCountRows(t, daemonID) != 1 {
		t.Errorf("expected exactly 1 row after mint, got %d", dtCountRows(t, daemonID))
	}
	gotHash, gotUser := dtFetchSingleRow(t, daemonID)
	if gotHash != auth.HashToken(resp.Token) {
		t.Errorf("stored hash != HashToken(returned token)")
	}
	if gotUser != testUserID {
		t.Errorf("row user_id = %s, want caller %s", gotUser, testUserID)
	}
}

// 2. Cross-workspace rejection — caller is not a member of the requested
//    workspace_id. Use a UUID for a workspace that does not include the test
//    user as member.
func TestCreateDaemonToken_NonMemberWorkspace(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	daemonID := dtTestDaemonID(t)
	dtCleanupRows(t, daemonID)

	// Create a sibling workspace that the test user is NOT a member of.
	ctx := context.Background()
	var otherWorkspaceID string
	slug := fmt.Sprintf("dt-nonmember-%d", time.Now().UnixNano())
	if err := testPool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug, description, issue_prefix)
		 VALUES ('DT Nonmember', $1, '', 'DTN') RETURNING id`, slug,
	).Scan(&otherWorkspaceID); err != nil {
		t.Fatalf("create sibling workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, otherWorkspaceID)
	})

	w := dtPostMint(t, "", map[string]any{
		"workspace_id": otherWorkspaceID,
		"daemon_id":    daemonID,
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-member workspace, got %d: %s", w.Code, w.Body.String())
	}
	if n := dtCountRows(t, daemonID); n != 0 {
		t.Errorf("expected no rows after rejected mint, got %d", n)
	}
}

// 3. Rotation replaces existing — second call by the same caller deletes
//    the prior row and inserts a fresh one. Count stays at 1; hash differs.
func TestCreateDaemonToken_RotationReplacesExisting(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	daemonID := dtTestDaemonID(t)
	dtCleanupRows(t, daemonID)

	w1 := dtPostMint(t, "", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    daemonID,
	})
	if w1.Code != http.StatusCreated {
		t.Fatalf("first mint failed: %d: %s", w1.Code, w1.Body.String())
	}
	var resp1 CreateDaemonTokenResponse
	json.NewDecoder(w1.Body).Decode(&resp1)
	hash1, _ := dtFetchSingleRow(t, daemonID)

	w2 := dtPostMint(t, "", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    daemonID,
	})
	if w2.Code != http.StatusCreated {
		t.Fatalf("rotation mint failed: %d: %s", w2.Code, w2.Body.String())
	}
	var resp2 CreateDaemonTokenResponse
	json.NewDecoder(w2.Body).Decode(&resp2)
	hash2, _ := dtFetchSingleRow(t, daemonID)

	if n := dtCountRows(t, daemonID); n != 1 {
		t.Errorf("after rotation expected exactly 1 row, got %d", n)
	}
	if hash1 == hash2 {
		t.Errorf("expected new token hash after rotation, got identical")
	}
	if resp1.Token == resp2.Token {
		t.Errorf("expected new raw token after rotation, got identical")
	}
}

// 4. Cross-user via pre-seeded agent_runtime row — Bob (member of the same
//    workspace) cannot mint a token for Alice's daemon_id once Alice has
//    registered a runtime with that daemon_id and her owner_id.
//
//    Step 2 of the validation algorithm (ADR D9).
func TestCreateDaemonToken_CrossUser_AgentRuntimeOwned(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	daemonID := dtTestDaemonID(t)
	dtCleanupRows(t, daemonID)

	// Alice = the existing testUserID. Bob = a new workspace member.
	bob := dtCreateExtraUser(t, "bob-runtime")

	// Pre-seed Alice's agent_runtime row carrying the daemon_id.
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO agent_runtime
		    (workspace_id, daemon_id, name, runtime_mode, provider, status,
		     device_info, metadata, owner_id, last_seen_at)
		VALUES ($1, $2, 'Alice Runtime', 'local', 'claude', 'online',
		        'alice-host', '{}'::jsonb, $3, now())
	`, testWorkspaceID, daemonID, testUserID); err != nil {
		t.Fatalf("seed agent_runtime: %v", err)
	}

	w := dtPostMint(t, bob, map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    daemonID,
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 cross-user via agent_runtime, got %d: %s", w.Code, w.Body.String())
	}
	if n := dtCountRows(t, daemonID); n != 0 {
		t.Errorf("expected no daemon_token row after rejected mint, got %d", n)
	}
}

// 5. Cross-user via pre-seeded daemon_token row — same Bob-vs-Alice scenario,
//    but Alice ran daemon login first (daemon_token row exists) and never
//    started her daemon (no agent_runtime row yet).
//
//    Step 3 of the validation algorithm (ADR D9).
func TestCreateDaemonToken_CrossUser_DaemonTokenOwned(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	daemonID := dtTestDaemonID(t)
	dtCleanupRows(t, daemonID)

	bob := dtCreateExtraUser(t, "bob-token")

	// Pre-seed Alice's daemon_token row directly (simulates a prior mint).
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO daemon_token
		    (token_hash, workspace_id, daemon_id, user_id, expires_at)
		VALUES ($1, $2, $3, $4, now() + interval '90 days')
	`, "alice-seed-hash-"+daemonID, testWorkspaceID, daemonID, testUserID); err != nil {
		t.Fatalf("seed daemon_token: %v", err)
	}

	w := dtPostMint(t, bob, map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    daemonID,
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 cross-user via daemon_token, got %d: %s", w.Code, w.Body.String())
	}

	// Alice's row must still be intact — Bob's rejected mint must not have
	// touched it.
	gotHash, gotUser := dtFetchSingleRow(t, daemonID)
	if gotHash != "alice-seed-hash-"+daemonID {
		t.Errorf("Alice's row hash mutated: %q", gotHash)
	}
	if gotUser != testUserID {
		t.Errorf("Alice's row user_id mutated: got %s, want %s", gotUser, testUserID)
	}
	if n := dtCountRows(t, daemonID); n != 1 {
		t.Errorf("expected Alice's single row preserved, got count %d", n)
	}
}

// 6. Torn rotation — call rotateDaemonToken with an already-cancelled
//    context. Whichever DB op trips the cancellation first, the deferred
//    Rollback must restore the prior state. Verifies the rollback invariant
//    of the transaction wrap (ADR D9 §"Transaction-wrapped rotation").
func TestCreateDaemonToken_TornRotationRollsBack(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	daemonID := dtTestDaemonID(t)
	dtCleanupRows(t, daemonID)

	// Pre-seed the existing token (the "live" row that rotation must not
	// destroy if it can't replace it).
	const preHash = "pre-rotate-hash-"
	preHashFull := preHash + daemonID
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO daemon_token
		    (token_hash, workspace_id, daemon_id, user_id, expires_at)
		VALUES ($1, $2, $3, $4, now() + interval '90 days')
	`, preHashFull, testWorkspaceID, daemonID, testUserID); err != nil {
		t.Fatalf("seed pre-rotation row: %v", err)
	}

	// Cancel the context up-front so the next pgx call observes the
	// cancellation and the deferred Rollback restores state.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rawToken, err := auth.GenerateDaemonToken()
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	_, err = testHandler.rotateDaemonToken(
		ctx,
		parseUUID(testWorkspaceID),
		daemonID,
		parseUUID(testUserID),
		rawToken,
		time.Now().Add(90*24*time.Hour),
	)
	if err == nil {
		t.Fatalf("expected error from torn rotation, got nil")
	}

	// Original row survives intact.
	if n := dtCountRows(t, daemonID); n != 1 {
		t.Errorf("expected 1 row after torn rotation (original preserved), got %d", n)
	}
	gotHash, gotUser := dtFetchSingleRow(t, daemonID)
	if gotHash != preHashFull {
		t.Errorf("original token_hash mutated: got %q, want %q", gotHash, preHashFull)
	}
	if gotUser != testUserID {
		t.Errorf("original user_id mutated: got %s, want %s", gotUser, testUserID)
	}
}
