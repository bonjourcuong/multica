package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const testResolverSlug = "middleware-resolver-test"

// openPool returns a connected pgxpool, or skips the test if the database is
// unreachable. Mirrors the handler package's fixture approach so tests don't
// require a DB in environments where one isn't available.
func openPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Skipf("skipping: could not connect to database: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("skipping: database not reachable: %v", err)
	}
	return pool
}

// setupResolverFixture inserts a workspace with a known slug and returns its
// UUID. The caller is responsible for calling the returned cleanup func.
func setupResolverFixture(t *testing.T, pool *pgxpool.Pool) (workspaceID string, cleanup func()) {
	t.Helper()
	ctx := context.Background()
	// Pre-cleanup in case a previous run didn't finish.
	_, _ = pool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, testResolverSlug)

	if err := pool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug, description, issue_prefix) VALUES ($1, $2, '', 'MRT') RETURNING id`,
		"Middleware Resolver Test", testResolverSlug,
	).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	return workspaceID, func() {
		_, _ = pool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, testResolverSlug)
	}
}

// TestResolveWorkspaceIDFromRequest pins down the priority order of the
// shared resolver. Every handler-level lookup of workspace identity — whether
// a route sits inside or outside the workspace middleware — must produce
// identical results, in the same priority, across all five supported
// mechanisms. Breaking any row here is a behavioral regression.
func TestResolveWorkspaceIDFromRequest(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	queries := db.New(pool)

	workspaceID, cleanup := setupResolverFixture(t, pool)
	defer cleanup()

	const (
		uuidA = "00000000-0000-0000-0000-000000000001"
		uuidB = "00000000-0000-0000-0000-000000000002"
	)

	cases := []struct {
		name      string
		setup     func(r *http.Request)
		want      string
		wantEmpty bool
	}{
		{
			name: "context UUID wins over everything else",
			setup: func(r *http.Request) {
				ctx := context.WithValue(r.Context(), ctxKeyWorkspaceID, uuidA)
				*r = *r.WithContext(ctx)
				r.Header.Set("X-Workspace-Slug", testResolverSlug)
				r.Header.Set("X-Workspace-ID", uuidB)
			},
			want: uuidA,
		},
		{
			name: "X-Workspace-Slug header resolves to UUID via DB lookup",
			setup: func(r *http.Request) {
				r.Header.Set("X-Workspace-Slug", testResolverSlug)
			},
			want: workspaceID,
		},
		{
			name: "X-Workspace-Slug wins over X-Workspace-ID (post-refactor priority)",
			setup: func(r *http.Request) {
				r.Header.Set("X-Workspace-Slug", testResolverSlug)
				r.Header.Set("X-Workspace-ID", uuidB)
			},
			want: workspaceID,
		},
		{
			name: "unknown X-Workspace-Slug falls through to UUID header",
			setup: func(r *http.Request) {
				r.Header.Set("X-Workspace-Slug", "does-not-exist")
				r.Header.Set("X-Workspace-ID", uuidB)
			},
			want: uuidB,
		},
		{
			name: "?workspace_slug query resolves to UUID via DB lookup",
			setup: func(r *http.Request) {
				q := r.URL.Query()
				q.Set("workspace_slug", testResolverSlug)
				r.URL.RawQuery = q.Encode()
			},
			want: workspaceID,
		},
		{
			name: "X-Workspace-ID header is returned when no slug provided",
			setup: func(r *http.Request) {
				r.Header.Set("X-Workspace-ID", uuidA)
			},
			want: uuidA,
		},
		{
			name: "?workspace_id query is the last-resort fallback",
			setup: func(r *http.Request) {
				q := r.URL.Query()
				q.Set("workspace_id", uuidA)
				r.URL.RawQuery = q.Encode()
			},
			want: uuidA,
		},
		{
			name:      "no identifier at all returns empty",
			setup:     func(r *http.Request) {},
			wantEmpty: true,
		},
		{
			name: "unknown slug with no UUID fallback returns empty",
			setup: func(r *http.Request) {
				r.Header.Set("X-Workspace-Slug", "does-not-exist")
			},
			wantEmpty: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/anything", nil)
			tc.setup(req)

			got := ResolveWorkspaceIDFromRequest(req, queries)

			if tc.wantEmpty {
				if got != "" {
					t.Fatalf("expected empty, got %q", got)
				}
				return
			}
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

// membershipFixture sets up two workspaces and one authenticated user that is
// a member of `MemberWorkspaceID` but explicitly NOT a member of
// `NonMemberWorkspaceID`. The non-member workspace is owned by a separate
// stranger user so that membership rows exist for it (catches a buggy
// implementation that returns "no rows" when the workspace has zero members).
type membershipFixture struct {
	UserID                string
	StrangerUserID        string
	MemberWorkspaceID     string
	MemberWorkspaceSlug   string
	NonMemberWorkspaceID  string
	NonMemberWorkspaceSlug string
}

func setupMembershipFixture(t *testing.T, pool *pgxpool.Pool) *membershipFixture {
	t.Helper()
	ctx := context.Background()
	uniq := fmt.Sprintf("%d", time.Now().UnixNano())
	f := &membershipFixture{
		MemberWorkspaceSlug:    "mw-spoof-member-" + uniq,
		NonMemberWorkspaceSlug: "mw-spoof-nonmember-" + uniq,
	}

	if err := pool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		"Spoof Member "+uniq, "spoof-member-"+uniq+"@multica.ai",
	).Scan(&f.UserID); err != nil {
		t.Fatalf("create member user: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		"Spoof Stranger "+uniq, "spoof-stranger-"+uniq+"@multica.ai",
	).Scan(&f.StrangerUserID); err != nil {
		t.Fatalf("create stranger user: %v", err)
	}

	if err := pool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug, description, issue_prefix) VALUES ($1, $2, '', $3) RETURNING id`,
		"Spoof Member WS", f.MemberWorkspaceSlug, "SMW",
	).Scan(&f.MemberWorkspaceID); err != nil {
		t.Fatalf("create member workspace: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`,
		f.MemberWorkspaceID, f.UserID,
	); err != nil {
		t.Fatalf("attach member to member workspace: %v", err)
	}

	if err := pool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug, description, issue_prefix) VALUES ($1, $2, '', $3) RETURNING id`,
		"Spoof NonMember WS", f.NonMemberWorkspaceSlug, "SNM",
	).Scan(&f.NonMemberWorkspaceID); err != nil {
		t.Fatalf("create non-member workspace: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`,
		f.NonMemberWorkspaceID, f.StrangerUserID,
	); err != nil {
		t.Fatalf("attach stranger to non-member workspace: %v", err)
	}

	t.Cleanup(func() {
		bg := context.Background()
		pool.Exec(bg, `DELETE FROM workspace WHERE slug IN ($1, $2)`, f.MemberWorkspaceSlug, f.NonMemberWorkspaceSlug)
		pool.Exec(bg, `DELETE FROM "user" WHERE id IN ($1, $2)`, f.UserID, f.StrangerUserID)
	})
	return f
}

// TestRequireWorkspaceMember_NonMemberSpoof locks down the existence-hiding
// 404 contract for the slug-based middleware. An authenticated user pointing
// X-Workspace-Slug at a workspace they're not a member of must get a 404
// indistinguishable from "no such workspace" — never a 403 — and the wrapped
// handler must never run. Every workspace-scoped endpoint depends on this.
func TestRequireWorkspaceMember_NonMemberSpoof(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	queries := db.New(pool)
	f := setupMembershipFixture(t, pool)

	cases := []struct {
		name  string
		setup func(r *http.Request)
	}{
		{
			name: "X-Workspace-Slug spoof",
			setup: func(r *http.Request) {
				r.Header.Set("X-User-ID", f.UserID)
				r.Header.Set("X-Workspace-Slug", f.NonMemberWorkspaceSlug)
			},
		},
		{
			name: "X-Workspace-ID spoof",
			setup: func(r *http.Request) {
				r.Header.Set("X-User-ID", f.UserID)
				r.Header.Set("X-Workspace-ID", f.NonMemberWorkspaceID)
			},
		},
		{
			name: "?workspace_slug query spoof",
			setup: func(r *http.Request) {
				r.Header.Set("X-User-ID", f.UserID)
				q := r.URL.Query()
				q.Set("workspace_slug", f.NonMemberWorkspaceSlug)
				r.URL.RawQuery = q.Encode()
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handlerCalled := false
			sentinel := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handlerCalled = true
				w.WriteHeader(http.StatusOK)
			})
			mw := RequireWorkspaceMember(queries)(sentinel)

			req := httptest.NewRequest("GET", "/api/anything", nil)
			tc.setup(req)
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
			}
			if got := rec.Body.String(); got != `{"error":"workspace not found"}` {
				t.Fatalf("expected existence-hiding body, got %q", got)
			}
			if handlerCalled {
				t.Fatalf("wrapped handler must NOT be invoked when membership check fails")
			}
		})
	}
}

// TestRequireWorkspaceMemberFromURL_NonMemberSpoof is the same contract for
// the chi-URL-param variant. Both factories funnel through buildMiddleware,
// so they share the existence-hiding 404 path; this test pins it down for
// the URL-param entry point too.
func TestRequireWorkspaceMemberFromURL_NonMemberSpoof(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	queries := db.New(pool)
	f := setupMembershipFixture(t, pool)

	handlerCalled := false
	sentinel := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})
	mw := RequireWorkspaceMemberFromURL(queries, "id")(sentinel)

	req := httptest.NewRequest("GET", "/api/workspaces/"+f.NonMemberWorkspaceID, nil)
	req.Header.Set("X-User-ID", f.UserID)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", f.NonMemberWorkspaceID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != `{"error":"workspace not found"}` {
		t.Fatalf("expected existence-hiding body, got %q", got)
	}
	if handlerCalled {
		t.Fatalf("wrapped handler must NOT be invoked when membership check fails")
	}
}

// TestRequireWorkspaceMember_MemberPasses is the positive control: the same
// user, same middleware, against a workspace they ARE a member of, must
// reach the wrapped handler. Without this, a bug that turns every request
// into a 404 would still pass the spoof test above.
func TestRequireWorkspaceMember_MemberPasses(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	queries := db.New(pool)
	f := setupMembershipFixture(t, pool)

	handlerCalled := false
	sentinel := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})
	mw := RequireWorkspaceMember(queries)(sentinel)

	req := httptest.NewRequest("GET", "/api/anything", nil)
	req.Header.Set("X-User-ID", f.UserID)
	req.Header.Set("X-Workspace-Slug", f.MemberWorkspaceSlug)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !handlerCalled {
		t.Fatalf("wrapped handler must run for a member of the resolved workspace")
	}
}
