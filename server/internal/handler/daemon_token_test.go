package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
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
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-member workspace, got %d: %s", w.Code, w.Body.String())
	}
	// Body must be the same opaque "forbidden" the ownership-rejection branches
	// emit (ADR D9 §"daemon_id ownership validation"). Locks the invariant
	// that a caller cannot distinguish "workspace doesn't exist" from
	// "you're not a member" from "daemon belongs to someone else".
	assertOpaqueForbidden(t, w)
	if n := dtCountRows(t, daemonID); n != 0 {
		t.Errorf("expected no rows after rejected mint, got %d", n)
	}
}

// assertOpaqueForbidden checks that a 403 response carries the canonical
// {"error":"forbidden"} body the daemon-token handler emits for membership
// and ownership rejections alike. Used by tests 2, 4, and 5 so any future
// drift in one branch's body shape (e.g. adding a leak-y "reason" field)
// fails the opaqueness invariant immediately.
func assertOpaqueForbidden(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode forbidden body: %v (raw: %s)", err, w.Body.String())
	}
	if body["error"] != "forbidden" || len(body) != 1 {
		t.Errorf("expected body {\"error\":\"forbidden\"} only, got %v", body)
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
	assertOpaqueForbidden(t, w)
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
	assertOpaqueForbidden(t, w)

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

// 6. Torn rotation — exercise the rollback path that the ADR D9
//    §"Transaction-wrapped rotation" wrap is supposed to protect.
//
//    The cancel-before-call pattern from the prior revision short-circuited
//    the whole rotation (the very first pgx op observed the cancellation),
//    so the wrap was never put under load. Bruce flagged this on PR #57.
//
//    This rewrite injects the failure inside the transaction, after Delete
//    commits but before Create commits, via a pgx.Tx shim that lets the
//    sqlc-generated `q.db.Exec(...)` for Delete pass through to the real
//    transaction and forces the next `q.db.QueryRow(...)` (Create) to fail.
//    The deferred Rollback then has real work to do — undoing the Delete —
//    which is exactly the invariant the wrap exists for.
//
//    Drives the request through CreateDaemonToken so the four assertions
//    Maria's spec calls out can all be made:
//      (a) count(*) FROM daemon_token WHERE (workspace, daemon) is exactly 1
//      (b) surviving row's token_hash and user_id match pre-rotation values
//      (c) handler returns 500
//      (d) no orphaned rows (covered by (a) + (b))
func TestCreateDaemonToken_TornRotationRollsBack(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	daemonID := dtTestDaemonID(t)
	dtCleanupRows(t, daemonID)

	// Pre-seed the existing token (the "live" row that rotation must not
	// destroy if it can't replace it). user_id matches the caller so the
	// step-3 ownership validation passes and rotation actually starts.
	const preHash = "pre-rotate-hash-"
	preHashFull := preHash + daemonID
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO daemon_token
		    (token_hash, workspace_id, daemon_id, user_id, expires_at)
		VALUES ($1, $2, $3, $4, now() + interval '90 days')
	`, preHashFull, testWorkspaceID, daemonID, testUserID); err != nil {
		t.Fatalf("seed pre-rotation row: %v", err)
	}

	// Build a handler clone whose TxStarter wraps testPool with a shim that
	// forces the first QueryRow inside any rotation tx to return an error.
	// Everything else (validation queries against h.Queries on the pool, the
	// real Delete inside the tx) runs against the unmodified pool.
	tornStarter := &tornRotationTxStarter{inner: testPool}
	hClone := *testHandler
	hClone.TxStarter = tornStarter

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/daemon-tokens", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    daemonID,
	})
	hClone.CreateDaemonToken(w, req)

	// (c) handler returns 500
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 from torn rotation, got %d: %s", w.Code, w.Body.String())
	}
	// Confirm the shim actually fired — guards against a future refactor that
	// reorders the rotation pair (Delete before Create) silently making this
	// test pass without ever exercising the rollback.
	if !tornStarter.deleteRan() {
		t.Fatal("shim never observed the Delete inside the tx — torn-rotation canary did not exercise the rollback")
	}
	if !tornStarter.createBlocked() {
		t.Fatal("shim never blocked the Create inside the tx — torn-rotation canary did not exercise the rollback")
	}

	// (a) + (d) exactly one row remains, no orphans
	if n := dtCountRows(t, daemonID); n != 1 {
		t.Errorf("expected 1 row after torn rotation (original preserved), got %d", n)
	}
	// (b) surviving row matches the pre-rotation values
	gotHash, gotUser := dtFetchSingleRow(t, daemonID)
	if gotHash != preHashFull {
		t.Errorf("original token_hash mutated: got %q, want %q", gotHash, preHashFull)
	}
	if gotUser != testUserID {
		t.Errorf("original user_id mutated: got %s, want %s", gotUser, testUserID)
	}
}

// errSimulatedTornRotation is the synthetic failure the torn-rotation shim
// injects between Delete and Create. Picked as a sentinel so a future test
// reading handler logs can grep for it unambiguously.
var errSimulatedTornRotation = errors.New("simulated create failure after delete")

// tornRotationTxStarter wraps a pgxpool so transactions it opens get the
// torn-rotation shim layered on. Used only by TestCreateDaemonToken_
// TornRotationRollsBack. The shim is per-tx so concurrent tests are
// unaffected.
type tornRotationTxStarter struct {
	inner *pgxpool.Pool
	// observed is the most recently created shim tx. Stored so the test can
	// assert the Delete-then-Create path actually ran. We only ever expect
	// one tx per test invocation (rotateDaemonToken opens exactly one).
	observed *tornRotationTx
}

func (s *tornRotationTxStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.inner.Begin(ctx)
	if err != nil {
		return nil, err
	}
	shim := &tornRotationTx{Tx: tx}
	s.observed = shim
	return shim, nil
}

func (s *tornRotationTxStarter) deleteRan() bool {
	return s.observed != nil && s.observed.deleteSeen
}

func (s *tornRotationTxStarter) createBlocked() bool {
	return s.observed != nil && s.observed.createBlocked
}

// tornRotationTx implements pgx.Tx by embedding the real tx and overriding
// the two methods sqlc uses for Delete/Create. Exec is the Delete path
// (`-- name: DeleteDaemonTokensByWorkspaceAndDaemon :exec`), QueryRow is the
// Create path (`-- name: CreateDaemonToken :one`). We let Delete commit
// inside the tx, then synthesise a failure on the next QueryRow so Create
// never runs — the deferred Rollback in rotateDaemonToken then has real
// state to undo.
type tornRotationTx struct {
	pgx.Tx
	deleteSeen    bool
	createBlocked bool
}

func (t *tornRotationTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tag, err := t.Tx.Exec(ctx, sql, args...)
	if err == nil && strings.Contains(sql, "DeleteDaemonTokensByWorkspaceAndDaemon") {
		t.deleteSeen = true
	}
	return tag, err
}

func (t *tornRotationTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if t.deleteSeen && strings.Contains(sql, "CreateDaemonToken") {
		t.createBlocked = true
		return errRow{err: errSimulatedTornRotation}
	}
	return t.Tx.QueryRow(ctx, sql, args...)
}

// errRow is a pgx.Row that returns a fixed error from Scan. Used by the
// torn-rotation shim to make the Create branch fail without round-tripping
// to Postgres.
type errRow struct{ err error }

func (e errRow) Scan(dest ...any) error { return e.err }
