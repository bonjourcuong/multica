package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// mirrorsFixture seeds three workspaces around the default test user:
//   - testWorkspaceID (owned by the user, no mirror).
//   - SecondWorkspaceID (also owned by the user, with a mirror session and
//     two messages — one user, one assistant — so we can assert ordering and
//     unread_count semantics).
//   - StrangerWorkspaceID (NOT a member; the user must never see it).
type mirrorsFixture struct {
	SecondWorkspaceID       string
	SecondWorkspaceSlug     string
	SecondMirrorSessionID   string
	SecondAgentID           string
	SecondLastMsg           time.Time
	StrangerUserID          string
	StrangerWorkspaceID     string
	StrangerWorkspaceSlug   string
	StrangerMirrorSessionID string
}

func setupMirrorsFixture(t *testing.T, pool *pgxpool.Pool, userID, ownerWorkspaceID string) *mirrorsFixture {
	t.Helper()
	ctx := context.Background()
	uniq := fmt.Sprintf("%d", time.Now().UnixNano())
	f := &mirrorsFixture{
		SecondWorkspaceSlug:   "mirrors-second-" + uniq,
		StrangerWorkspaceSlug: "mirrors-stranger-" + uniq,
	}

	// Workspace #2 owned by the test user. We seed a mirror session + two
	// messages so the endpoint has a non-trivial row to surface.
	if err := pool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, "Mirrors Second", f.SecondWorkspaceSlug, "second", "MS2").Scan(&f.SecondWorkspaceID); err != nil {
		t.Fatalf("create second workspace: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')
	`, f.SecondWorkspaceID, userID); err != nil {
		t.Fatalf("add owner to second workspace: %v", err)
	}

	// A workspace-scope agent to host the mirror session (matches what
	// GlobalDispatchService.resolveMirrorAgentID would pick).
	var secondRuntimeID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, last_seen_at
		)
		VALUES ($1, NULL, 'Second Runtime', 'cloud', 'second_test', 'online',
			'Second runtime', '{}'::jsonb, now())
		RETURNING id
	`, f.SecondWorkspaceID).Scan(&secondRuntimeID); err != nil {
		t.Fatalf("create second runtime: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, scope)
		VALUES ($1, 'Mirror Host', 'cloud', $2, 'workspace')
		RETURNING id
	`, f.SecondWorkspaceID, secondRuntimeID).Scan(&f.SecondAgentID); err != nil {
		t.Fatalf("create mirror host agent: %v", err)
	}

	// Mirror session itself. unread_since is set so we can assert
	// unread_count counts only assistant messages at/after that time.
	unreadSince := time.Now().UTC().Add(-90 * time.Second).Truncate(time.Microsecond)
	if err := pool.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, scope, unread_since)
		VALUES ($1, $2, $3, 'Cuong Global', 'global_mirror', $4)
		RETURNING id
	`, f.SecondWorkspaceID, f.SecondAgentID, userID, unreadSince).Scan(&f.SecondMirrorSessionID); err != nil {
		t.Fatalf("create mirror session: %v", err)
	}

	// Two mirror messages: one user (does NOT count as unread), one
	// assistant after unread_since (counts as 1 unread).
	if _, err := pool.Exec(ctx, `
		INSERT INTO chat_message (chat_session_id, role, content, created_at)
		VALUES ($1, 'user', 'hello mirror', $2)
	`, f.SecondMirrorSessionID, unreadSince.Add(-30*time.Second)); err != nil {
		t.Fatalf("insert user mirror message: %v", err)
	}
	f.SecondLastMsg = time.Now().UTC().Add(-15 * time.Second).Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx, `
		INSERT INTO chat_message (chat_session_id, role, content, created_at)
		VALUES ($1, 'assistant', 'mirror reply', $2)
	`, f.SecondMirrorSessionID, f.SecondLastMsg); err != nil {
		t.Fatalf("insert assistant mirror message: %v", err)
	}

	// Stranger user with their own workspace + their own mirror session.
	// The test user is NOT a member; the response must NEVER include this.
	if err := pool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
	`, "Mirrors Stranger "+uniq, "mirrors-stranger-"+uniq+"@multica.ai").Scan(&f.StrangerUserID); err != nil {
		t.Fatalf("create stranger user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, "Mirrors Stranger", f.StrangerWorkspaceSlug, "stranger", "MST").Scan(&f.StrangerWorkspaceID); err != nil {
		t.Fatalf("create stranger workspace: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')
	`, f.StrangerWorkspaceID, f.StrangerUserID); err != nil {
		t.Fatalf("add stranger member: %v", err)
	}

	var strangerRuntimeID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, last_seen_at
		)
		VALUES ($1, NULL, 'Stranger Runtime', 'cloud', 'stranger_test', 'online',
			'Stranger runtime', '{}'::jsonb, now())
		RETURNING id
	`, f.StrangerWorkspaceID).Scan(&strangerRuntimeID); err != nil {
		t.Fatalf("create stranger runtime: %v", err)
	}
	var strangerAgentID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, scope)
		VALUES ($1, 'Mirror Host', 'cloud', $2, 'workspace')
		RETURNING id
	`, f.StrangerWorkspaceID, strangerRuntimeID).Scan(&strangerAgentID); err != nil {
		t.Fatalf("create stranger agent: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, scope)
		VALUES ($1, $2, $3, 'Cuong Global', 'global_mirror')
		RETURNING id
	`, f.StrangerWorkspaceID, strangerAgentID, f.StrangerUserID).Scan(&f.StrangerMirrorSessionID); err != nil {
		t.Fatalf("create stranger mirror session: %v", err)
	}

	t.Cleanup(func() {
		bg := context.Background()
		// Workspace cascades drop chat_session and member rows.
		pool.Exec(bg, `DELETE FROM workspace WHERE slug IN ($1, $2)`, f.SecondWorkspaceSlug, f.StrangerWorkspaceSlug)
		pool.Exec(bg, `DELETE FROM "user" WHERE id = $1`, f.StrangerUserID)
		// Owner workspace stays — only its mirror sessions need cleaning if
		// any test wrote to them. setupMirrorsFixture itself never seeds the
		// owner workspace's mirror, but a sibling sub-test can.
		pool.Exec(bg, `DELETE FROM chat_session WHERE workspace_id = $1 AND scope = 'global_mirror'`, ownerWorkspaceID)
	})
	return f
}

func TestListGlobalMirrors_Unauthenticated(t *testing.T) {
	if testHandler == nil {
		t.Skip("no test handler")
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/global/chat/mirrors", nil)
	// Intentionally no X-User-ID header.
	testHandler.ListGlobalMirrors(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestListGlobalMirrors_OnlyMemberWorkspaces(t *testing.T) {
	if testHandler == nil {
		t.Skip("no test handler")
	}
	f := setupMirrorsFixture(t, testPool, testUserID, testWorkspaceID)

	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/global/chat/mirrors", nil)
	testHandler.ListGlobalMirrors(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var rows []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, w.Body.String())
	}

	// At minimum: owner workspace + second workspace. The stranger workspace
	// must NEVER appear; that is the load-bearing assertion of this test.
	seenSecond := false
	seenOwner := false
	for _, r := range rows {
		wsID, _ := r["workspace_id"].(string)
		switch wsID {
		case f.StrangerWorkspaceID:
			t.Fatalf("stranger workspace leaked into mirrors list: %+v", r)
		case f.SecondWorkspaceID:
			seenSecond = true
			if got, _ := r["workspace_slug"].(string); got != f.SecondWorkspaceSlug {
				t.Fatalf("second workspace_slug = %q, want %q", got, f.SecondWorkspaceSlug)
			}
			if got, _ := r["mirror_session_id"].(string); got != f.SecondMirrorSessionID {
				t.Fatalf("second mirror_session_id = %q, want %q", got, f.SecondMirrorSessionID)
			}
			if r["last_message_at"] == nil {
				t.Fatalf("second last_message_at unexpectedly null")
			}
			// One assistant message at/after unread_since → unread_count = 1.
			if got, _ := r["unread_count"].(float64); got != 1 {
				t.Fatalf("second unread_count = %v, want 1", got)
			}
		case testWorkspaceID:
			seenOwner = true
			// Owner workspace has no mirror seeded → null mirror_session_id,
			// null last_message_at, unread_count = 0.
			if r["mirror_session_id"] != nil {
				t.Fatalf("owner mirror_session_id should be null, got %v", r["mirror_session_id"])
			}
			if r["last_message_at"] != nil {
				t.Fatalf("owner last_message_at should be null, got %v", r["last_message_at"])
			}
			if got, _ := r["unread_count"].(float64); got != 0 {
				t.Fatalf("owner unread_count = %v, want 0", got)
			}
		}
	}
	if !seenSecond {
		t.Fatalf("second workspace missing from response: %s", w.Body.String())
	}
	if !seenOwner {
		t.Fatalf("owner workspace missing from response: %s", w.Body.String())
	}
}

func TestListGlobalMirrors_StrangerSeesNothingFromOwner(t *testing.T) {
	if testHandler == nil {
		t.Skip("no test handler")
	}
	f := setupMirrorsFixture(t, testPool, testUserID, testWorkspaceID)

	// Call as the stranger user — they only belong to their own workspace,
	// and must never see the owner's or second workspace's mirrors.
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/global/chat/mirrors", nil)
	req.Header.Set("X-User-ID", f.StrangerUserID)
	testHandler.ListGlobalMirrors(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var rows []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, w.Body.String())
	}
	for _, r := range rows {
		wsID, _ := r["workspace_id"].(string)
		switch wsID {
		case testWorkspaceID, f.SecondWorkspaceID:
			t.Fatalf("stranger leaked owner workspace %s into response: %+v", wsID, r)
		}
	}

	// The stranger must see exactly their own workspace.
	hasStranger := false
	for _, r := range rows {
		wsID, _ := r["workspace_id"].(string)
		if wsID == f.StrangerWorkspaceID {
			hasStranger = true
		}
	}
	if !hasStranger {
		t.Fatalf("stranger workspace missing from stranger response: %s", w.Body.String())
	}
}
