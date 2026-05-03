package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/auth"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestDaemonAuthFallbackAllowed pins the strict-mode whitelist (MUL-195).
// In default mode every daemon route accepts a user credential. In strict
// mode only /register, /heartbeat, and /workspaces/{id}/repos do.
func TestDaemonAuthFallbackAllowed(t *testing.T) {
	cases := []struct {
		path   string
		strict bool
		want   bool
	}{
		// Default mode — every path is allowed.
		{"/api/daemon/register", false, true},
		{"/api/daemon/heartbeat", false, true},
		{"/api/daemon/workspaces/abc/repos", false, true},
		{"/api/daemon/runtimes/abc/tasks/claim", false, true},
		{"/api/daemon/tasks/abc/messages", false, true},
		{"/api/daemon/tasks/abc/complete", false, true},

		// Strict mode — whitelist.
		{"/api/daemon/register", true, true},
		{"/api/daemon/heartbeat", true, true},
		{"/api/daemon/workspaces/abc/repos", true, true},
		{"/api/daemon/workspaces/abc-uuid-1234/repos", true, true},

		// Strict mode — gated.
		{"/api/daemon/deregister", true, false},
		{"/api/daemon/runtimes/abc/tasks/claim", true, false},
		{"/api/daemon/runtimes/abc/recover-orphans", true, false},
		{"/api/daemon/tasks/abc/messages", true, false},
		{"/api/daemon/tasks/abc/start", true, false},
		{"/api/daemon/tasks/abc/complete", true, false},
		{"/api/daemon/issues/abc/gc-check", true, false},

		// Strict mode — workspaces/<...>/repos must have exactly one path
		// segment between the prefix and suffix (no traversal).
		{"/api/daemon/workspaces//repos", true, false},
		{"/api/daemon/workspaces/abc/extra/repos", true, false},
		{"/api/daemon/workspaces/abc/repos/extra", true, false},

		// Defensive: paths outside /api/daemon are not the middleware's
		// concern, but the helper must not match them either.
		{"/api/workspaces/abc/repos", true, false},
	}
	for _, c := range cases {
		got := daemonAuthFallbackAllowed(c.path, c.strict)
		if got != c.want {
			t.Errorf("daemonAuthFallbackAllowed(%q, strict=%v) = %v, want %v", c.path, c.strict, got, c.want)
		}
	}
}

// makeJWT signs a JWT for a non-DB middleware test. The middleware accepts
// any JWT signed with auth.JWTSecret() once it reaches the JWT branch.
func makeJWT(t *testing.T) string {
	t.Helper()
	claims := jwt.MapClaims{
		"sub":   "fake-user-id",
		"email": "test@multica.ai",
		"exp":   time.Now().Add(time.Hour).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := tok.SignedString(auth.JWTSecret())
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return s
}

// runJWTRequest exercises DaemonAuth on the given path with a JWT-bearing
// request. queries can be nil because the JWT branch never touches the DB.
func runJWTRequest(t *testing.T, path string) int {
	t.Helper()
	called := false
	handler := DaemonAuth(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("POST", path, nil)
	req.Header.Set("Authorization", "Bearer "+makeJWT(t))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code == http.StatusOK && !called {
		t.Fatalf("returned 200 but next handler was not called for %q", path)
	}
	return w.Code
}

// TestDaemonAuth_DefaultMode_JWTPassesAllRoutes confirms that without
// DAEMON_AUTH_STRICT the JWT fallback works on every daemon route — the
// existing behaviour every running daemon depends on today (MUL-195).
func TestDaemonAuth_DefaultMode_JWTPassesAllRoutes(t *testing.T) {
	t.Setenv("DAEMON_AUTH_STRICT", "")
	for _, p := range []string{
		"/api/daemon/heartbeat",
		"/api/daemon/runtimes/abc/tasks/claim",
		"/api/daemon/tasks/abc/messages",
		"/api/daemon/tasks/abc/complete",
	} {
		if got := runJWTRequest(t, p); got != http.StatusOK {
			t.Errorf("default mode: JWT on %q got %d, want 200", p, got)
		}
	}
}

// TestDaemonAuth_StrictMode_JWTBlockedOnLifecycle is the headline regression
// test for the new strict mode: a JWT (or PAT — same code path past the
// gate) must not be able to authenticate a daemon-lifecycle route.
func TestDaemonAuth_StrictMode_JWTBlockedOnLifecycle(t *testing.T) {
	t.Setenv("DAEMON_AUTH_STRICT", "true")
	for _, p := range []string{
		"/api/daemon/runtimes/abc/tasks/claim",
		"/api/daemon/runtimes/abc/recover-orphans",
		"/api/daemon/tasks/abc/messages",
		"/api/daemon/tasks/abc/complete",
		"/api/daemon/issues/abc/gc-check",
	} {
		if got := runJWTRequest(t, p); got != http.StatusUnauthorized {
			t.Errorf("strict mode: JWT on %q got %d, want 401", p, got)
		}
	}
}

// TestDaemonAuth_StrictMode_JWTAllowedOnWhitelist confirms the bootstrap
// routes still accept a user credential in strict mode, so a fresh daemon
// install can complete `multica daemon login` and exchange its user
// credential for an mdt_ token.
func TestDaemonAuth_StrictMode_JWTAllowedOnWhitelist(t *testing.T) {
	t.Setenv("DAEMON_AUTH_STRICT", "true")
	for _, p := range []string{
		"/api/daemon/register",
		"/api/daemon/heartbeat",
		"/api/daemon/workspaces/abc/repos",
	} {
		if got := runJWTRequest(t, p); got != http.StatusOK {
			t.Errorf("strict mode: JWT on whitelist %q got %d, want 200", p, got)
		}
	}
}

// TestDaemonAuth_StrictMode_PATGatedBeforeDB documents that the path gate
// runs before the PAT branch's DB lookup. Calling the middleware with nil
// queries on a non-whitelist route in strict mode must return 401 without
// panicking — proving the PAT fallback is gated identically to JWT.
func TestDaemonAuth_StrictMode_PATGatedBeforeDB(t *testing.T) {
	t.Setenv("DAEMON_AUTH_STRICT", "true")
	handler := DaemonAuth(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	}))
	req := httptest.NewRequest("POST", "/api/daemon/runtimes/abc/tasks/claim", nil)
	req.Header.Set("Authorization", "Bearer mul_unused_in_strict_mode_on_lifecycle")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", w.Code)
	}
}

// daemonAuthFixture seeds a workspace, user, PAT, and daemon token for the
// DB-backed integration tests. Caller is responsible for cleanup().
type daemonAuthFixture struct {
	pool        *pgxpool.Pool
	patToken    string
	mdtToken    string
	userID      string
	workspaceID string
	daemonID    string
	cleanup     func()
}

func setupDaemonAuthFixture(t *testing.T) *daemonAuthFixture {
	t.Helper()
	pool := openPool(t)
	ctx := context.Background()
	const slug = "daemon-auth-strict-test"
	const email = "daemon-auth-strict-test@multica.ai"

	// Pre-cleanup in case a previous run aborted.
	_, _ = pool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, slug)
	_, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, email)

	var userID, workspaceID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		"Daemon Auth Strict Test", email,
	).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug, description, issue_prefix) VALUES ($1, $2, '', 'DAS') RETURNING id`,
		"Daemon Auth Strict Test", slug,
	).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}

	patToken, err := auth.GeneratePATToken()
	if err != nil {
		t.Fatalf("generate pat: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO personal_access_token (user_id, name, token_hash, token_prefix) VALUES ($1, $2, $3, $4)`,
		userID, "test-pat", auth.HashToken(patToken), patToken[:8],
	); err != nil {
		t.Fatalf("insert pat: %v", err)
	}

	mdtToken, err := auth.GenerateDaemonToken()
	if err != nil {
		t.Fatalf("generate mdt: %v", err)
	}
	const daemonID = "daemon-auth-strict-test-daemon"
	if _, err := pool.Exec(ctx,
		`INSERT INTO daemon_token (token_hash, workspace_id, user_id, daemon_id, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		auth.HashToken(mdtToken), workspaceID, userID, daemonID, time.Now().Add(time.Hour),
	); err != nil {
		t.Fatalf("insert daemon_token: %v", err)
	}

	return &daemonAuthFixture{
		pool:        pool,
		patToken:    patToken,
		mdtToken:    mdtToken,
		userID:      userID,
		workspaceID: workspaceID,
		daemonID:    daemonID,
		cleanup: func() {
			_, _ = pool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, slug)
			_, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, email)
			pool.Close()
		},
	}
}

func runWithToken(t *testing.T, queries *db.Queries, path, token string) int {
	t.Helper()
	called := false
	handler := DaemonAuth(queries)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("POST", path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code == http.StatusOK && !called {
		t.Fatalf("returned 200 but next handler was not called for %q", path)
	}
	return w.Code
}

// TestDaemonAuth_DBBacked_StrictAndDefault is the integration test Maria
// asked for in MUL-195. It uses a real Postgres pool to exercise the PAT
// and mdt_ branches end-to-end across both modes.
func TestDaemonAuth_DBBacked_StrictAndDefault(t *testing.T) {
	fx := setupDaemonAuthFixture(t)
	defer fx.cleanup()
	queries := db.New(fx.pool)

	const lifecyclePath = "/api/daemon/runtimes/abc/tasks/claim"
	const heartbeatPath = "/api/daemon/heartbeat"

	// --- Default mode (DAEMON_AUTH_STRICT unset) — legacy behaviour preserved. ---
	t.Run("default_PAT_lifecycle_passes", func(t *testing.T) {
		t.Setenv("DAEMON_AUTH_STRICT", "")
		if got := runWithToken(t, queries, lifecyclePath, fx.patToken); got != http.StatusOK {
			t.Errorf("default mode: PAT on %q got %d, want 200", lifecyclePath, got)
		}
	})
	t.Run("default_mdt_lifecycle_passes", func(t *testing.T) {
		t.Setenv("DAEMON_AUTH_STRICT", "")
		if got := runWithToken(t, queries, lifecyclePath, fx.mdtToken); got != http.StatusOK {
			t.Errorf("default mode: mdt_ on %q got %d, want 200", lifecyclePath, got)
		}
	})

	// --- Strict mode — PAT blocked on lifecycle, mdt_ still works, whitelist still accepts PAT. ---
	t.Run("strict_PAT_lifecycle_401", func(t *testing.T) {
		t.Setenv("DAEMON_AUTH_STRICT", "true")
		if got := runWithToken(t, queries, lifecyclePath, fx.patToken); got != http.StatusUnauthorized {
			t.Errorf("strict mode: PAT on %q got %d, want 401", lifecyclePath, got)
		}
	})
	t.Run("strict_PAT_heartbeat_passes", func(t *testing.T) {
		t.Setenv("DAEMON_AUTH_STRICT", "true")
		if got := runWithToken(t, queries, heartbeatPath, fx.patToken); got != http.StatusOK {
			t.Errorf("strict mode: PAT on %q got %d, want 200", heartbeatPath, got)
		}
	})
	t.Run("strict_mdt_lifecycle_passes", func(t *testing.T) {
		t.Setenv("DAEMON_AUTH_STRICT", "true")
		if got := runWithToken(t, queries, lifecyclePath, fx.mdtToken); got != http.StatusOK {
			t.Errorf("strict mode: mdt_ on %q got %d, want 200", lifecyclePath, got)
		}
	})
}
