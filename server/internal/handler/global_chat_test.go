package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/service"
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

// --- V3 (MUL-137) — agent picker handler tests ----------------------------

// globalAgentsFixture seeds a `Claude (terminator-9999)` runtime owned by
// the test user (no workspace_id binding for our purposes — agent_runtime
// requires workspace_id, so we reuse the test workspace), then bootstraps
// the user's global session which in turn provisions both the legacy
// twin and the V3 Claude Code global agent. Returns the IDs the calling
// test cares about.
type globalAgentsFixture struct {
	TwinAgentID       string
	ClaudeCodeAgentID string
	GlobalSessionID   string
}

func setupGlobalAgentsFixture(t *testing.T, pool *pgxpool.Pool, userID string) *globalAgentsFixture {
	t.Helper()
	ctx := context.Background()

	// Provision the runtime the bootstrap needs. agent_runtime requires
	// a workspace_id; we attach to the test workspace because runtime
	// lookups for global agents only key on (owner_id, name).
	if _, err := pool.Exec(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, owner_id, last_seen_at
		)
		VALUES ($1, NULL, $2, 'local', 'global_test', 'online',
			'Global test runtime', '{}'::jsonb, $3, now())
		ON CONFLICT DO NOTHING
	`, testWorkspaceID, service.ClaudeCodeGlobalRuntimeName, userID); err != nil {
		t.Fatalf("create claude runtime: %v", err)
	}

	// Bootstrapping the global session is the canonical path that
	// provisions both global agents.
	sess, err := testHandler.GlobalChat.EnsureSession(ctx,
		parseUUID(userID))
	if err != nil {
		t.Fatalf("ensure session: %v", err)
	}

	agents, err := testHandler.GlobalChat.ListGlobalAgents(ctx, parseUUID(userID))
	if err != nil {
		t.Fatalf("list global agents: %v", err)
	}
	f := &globalAgentsFixture{GlobalSessionID: uuidToString(sess.ID)}
	for _, a := range agents {
		switch a.Name {
		case service.ClaudeCodeGlobalAgentName:
			f.ClaudeCodeAgentID = uuidToString(a.ID)
		default:
			f.TwinAgentID = uuidToString(a.ID)
		}
	}

	t.Cleanup(func() {
		bg := context.Background()
		// Drop any tasks the test enqueued before the global session
		// (FK ON DELETE CASCADE on global_session_id handles its own,
		// but tasks pointing at workspace agents need explicit cleanup).
		pool.Exec(bg, `DELETE FROM agent_task_queue WHERE global_session_id = $1`, sess.ID)
		pool.Exec(bg, `DELETE FROM global_chat_session WHERE user_id = $1`, userID)
		pool.Exec(bg, `DELETE FROM agent WHERE scope = 'global' AND user_id = $1`, userID)
		pool.Exec(bg, `DELETE FROM agent_runtime WHERE owner_id = $1 AND name = $2`,
			userID, service.ClaudeCodeGlobalRuntimeName)
	})
	return f
}

func TestListGlobalChatAgents_Unauthenticated(t *testing.T) {
	if testHandler == nil {
		t.Skip("no test handler")
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/global/chat/agents", nil)
	testHandler.ListGlobalChatAgents(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestListGlobalChatAgents_ReturnsBothAgents(t *testing.T) {
	if testHandler == nil {
		t.Skip("no test handler")
	}
	f := setupGlobalAgentsFixture(t, testPool, testUserID)

	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/global/chat/agents", nil)
	testHandler.ListGlobalChatAgents(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var rows []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, w.Body.String())
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 agents (twin + claude code), got %d: %s", len(rows), w.Body.String())
	}
	seenTwin := false
	seenClaude := false
	for _, r := range rows {
		id, _ := r["id"].(string)
		switch id {
		case f.TwinAgentID:
			seenTwin = true
		case f.ClaudeCodeAgentID:
			seenClaude = true
			if got, _ := r["name"].(string); got != service.ClaudeCodeGlobalAgentName {
				t.Errorf("claude agent name = %q, want %q", got, service.ClaudeCodeGlobalAgentName)
			}
		}
	}
	if !seenTwin {
		t.Errorf("twin agent missing from response: %s", w.Body.String())
	}
	if !seenClaude {
		t.Errorf("claude code agent missing from response: %s", w.Body.String())
	}
}

func TestListGlobalChatAgents_IsolatesByUser(t *testing.T) {
	if testHandler == nil {
		t.Skip("no test handler")
	}
	setupGlobalAgentsFixture(t, testPool, testUserID)

	// A second user with no global agents must see an empty list, not
	// the test user's agents.
	ctx := context.Background()
	uniq := fmt.Sprintf("%d", time.Now().UnixNano())
	var strangerID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
	`, "Global Stranger "+uniq, "global-stranger-"+uniq+"@multica.ai").Scan(&strangerID); err != nil {
		t.Fatalf("create stranger user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, strangerID)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/global/chat/agents", nil)
	req.Header.Set("X-User-ID", strangerID)
	testHandler.ListGlobalChatAgents(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("stranger should see no global agents, got %d: %s", len(rows), w.Body.String())
	}
}

func TestPostGlobalMessage_NoAgentIdFallsThroughToV1(t *testing.T) {
	if testHandler == nil {
		t.Skip("no test handler")
	}
	setupGlobalAgentsFixture(t, testPool, testUserID)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/global/chat/sessions/me/messages", map[string]any{
		"body": "hello no agent",
	})
	testHandler.PostGlobalMessage(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v, ok := resp["task_id"].(string); ok && v != "" {
		t.Fatalf("expected no task_id in V1 fallback, got %q", v)
	}
	// No row should be enqueued.
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_task_queue WHERE global_session_id IS NOT NULL`,
	).Scan(&n); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 global chat tasks enqueued, got %d", n)
	}
}

func TestPostGlobalMessage_WithAgentIdEnqueuesTask(t *testing.T) {
	if testHandler == nil {
		t.Skip("no test handler")
	}
	f := setupGlobalAgentsFixture(t, testPool, testUserID)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/global/chat/sessions/me/messages", map[string]any{
		"body":     "hello claude",
		"agent_id": f.ClaudeCodeAgentID,
	})
	testHandler.PostGlobalMessage(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	taskID, _ := resp["task_id"].(string)
	if taskID == "" {
		t.Fatalf("expected non-empty task_id, got %s", w.Body.String())
	}
	if got, _ := resp["agent_id"].(string); got != f.ClaudeCodeAgentID {
		t.Fatalf("agent_id mismatch: got %q want %q", got, f.ClaudeCodeAgentID)
	}

	// Verify the task row points at the right session + agent.
	var globalSessionID, agentID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT global_session_id::text, agent_id::text FROM agent_task_queue WHERE id = $1`,
		taskID,
	).Scan(&globalSessionID, &agentID); err != nil {
		t.Fatalf("load task row: %v", err)
	}
	if globalSessionID != f.GlobalSessionID {
		t.Errorf("global_session_id = %s, want %s", globalSessionID, f.GlobalSessionID)
	}
	if agentID != f.ClaudeCodeAgentID {
		t.Errorf("agent_id = %s, want %s", agentID, f.ClaudeCodeAgentID)
	}
}

func TestPostGlobalMessage_RejectsCrossUserAgent(t *testing.T) {
	if testHandler == nil {
		t.Skip("no test handler")
	}
	f := setupGlobalAgentsFixture(t, testPool, testUserID)

	// Stranger tries to use the test user's global agent.
	ctx := context.Background()
	uniq := fmt.Sprintf("%d", time.Now().UnixNano())
	var strangerID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
	`, "Cross Stranger "+uniq, "cross-stranger-"+uniq+"@multica.ai").Scan(&strangerID); err != nil {
		t.Fatalf("create stranger: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, strangerID)
	})

	w := httptest.NewRecorder()
	body := bytesBuffer(map[string]any{
		"body":     "hi",
		"agent_id": f.ClaudeCodeAgentID,
	})
	req := httptest.NewRequest("POST", "/api/global/chat/sessions/me/messages", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", strangerID)
	testHandler.PostGlobalMessage(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 (collapsed cross-user), got %d: %s", w.Code, w.Body.String())
	}
}

func TestPostGlobalMessage_RejectsArchivedAgent(t *testing.T) {
	if testHandler == nil {
		t.Skip("no test handler")
	}
	f := setupGlobalAgentsFixture(t, testPool, testUserID)

	// Archive the agent.
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent SET archived_at = now() WHERE id = $1`, f.ClaudeCodeAgentID,
	); err != nil {
		t.Fatalf("archive agent: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/global/chat/sessions/me/messages", map[string]any{
		"body":     "hi",
		"agent_id": f.ClaudeCodeAgentID,
	})
	testHandler.PostGlobalMessage(w, req)
	// Service-level lookup `GetGlobalAgentForUser` filters out archived
	// agents so the user gets a 404 here too — equivalent to "no longer
	// available". (If a future pass wants 422 instead, swap the filter
	// in service.ListGlobalAgents to include archived rows and rely on
	// the explicit ArchivedAt check in the handler.)
	if w.Code != http.StatusNotFound && w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 404 or 422 for archived agent, got %d: %s", w.Code, w.Body.String())
	}
}

// --- agent reply (MUL-158) ------------------------------------------------

func TestPostGlobalAgentReply_PersistsAsAgent(t *testing.T) {
	if testHandler == nil {
		t.Skip("no test handler")
	}
	f := setupGlobalAgentsFixture(t, testPool, testUserID)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/global/chat/sessions/me/messages/agent-reply", map[string]any{
		"content":  "interim update from the agent",
		"agent_id": f.ClaudeCodeAgentID,
	})
	testHandler.PostGlobalAgentReply(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, w.Body.String())
	}
	if got, _ := resp["author_kind"].(string); got != "agent" {
		t.Errorf("author_kind = %q, want agent", got)
	}
	if got, _ := resp["author_id"].(string); got != f.ClaudeCodeAgentID {
		t.Errorf("author_id = %q, want %q", got, f.ClaudeCodeAgentID)
	}
	if got, _ := resp["body"].(string); got != "interim update from the agent" {
		t.Errorf("body mismatch: %q", got)
	}

	// Verify persistence: the message lands on the user's global session.
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM global_chat_message
		 WHERE global_session_id = $1 AND author_kind = 'agent' AND author_id = $2`,
		f.GlobalSessionID, f.ClaudeCodeAgentID,
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 agent message persisted, got %d", n)
	}
}

func TestPostGlobalAgentReply_AgentIDFromHeader(t *testing.T) {
	if testHandler == nil {
		t.Skip("no test handler")
	}
	f := setupGlobalAgentsFixture(t, testPool, testUserID)

	w := httptest.NewRecorder()
	body := bytesBuffer(map[string]any{"content": "from header"})
	req := httptest.NewRequest("POST", "/api/global/chat/sessions/me/messages/agent-reply", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Agent-ID", f.ClaudeCodeAgentID)
	testHandler.PostGlobalAgentReply(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if got, _ := resp["author_id"].(string); got != f.ClaudeCodeAgentID {
		t.Errorf("author_id = %q, want %q", got, f.ClaudeCodeAgentID)
	}
}

func TestPostGlobalAgentReply_MissingAgentID(t *testing.T) {
	if testHandler == nil {
		t.Skip("no test handler")
	}
	setupGlobalAgentsFixture(t, testPool, testUserID)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/global/chat/sessions/me/messages/agent-reply", map[string]any{
		"content": "no agent id",
	})
	testHandler.PostGlobalAgentReply(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing agent_id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPostGlobalAgentReply_RejectsCrossUserAgent(t *testing.T) {
	if testHandler == nil {
		t.Skip("no test handler")
	}
	f := setupGlobalAgentsFixture(t, testPool, testUserID)

	// Stranger tries to post a reply attributed to the test user's global agent.
	ctx := context.Background()
	uniq := fmt.Sprintf("%d", time.Now().UnixNano())
	var strangerID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
	`, "Reply Stranger "+uniq, "reply-stranger-"+uniq+"@multica.ai").Scan(&strangerID); err != nil {
		t.Fatalf("create stranger: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, strangerID)
	})

	w := httptest.NewRecorder()
	body := bytesBuffer(map[string]any{
		"content":  "spoofed",
		"agent_id": f.ClaudeCodeAgentID,
	})
	req := httptest.NewRequest("POST", "/api/global/chat/sessions/me/messages/agent-reply", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", strangerID)
	testHandler.PostGlobalAgentReply(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-user agent, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPostGlobalAgentReply_EmptyContent(t *testing.T) {
	if testHandler == nil {
		t.Skip("no test handler")
	}
	f := setupGlobalAgentsFixture(t, testPool, testUserID)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/global/chat/sessions/me/messages/agent-reply", map[string]any{
		"content":  "",
		"agent_id": f.ClaudeCodeAgentID,
	})
	testHandler.PostGlobalAgentReply(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty content, got %d: %s", w.Code, w.Body.String())
	}
}

func TestListGlobalChatAgents_ParseUUIDOK(t *testing.T) {
	// Smoke: parseUUID must accept the seeded agent IDs (catches a
	// regression where the helper would silently mark them invalid).
	if testHandler == nil {
		t.Skip("no test handler")
	}
	f := setupGlobalAgentsFixture(t, testPool, testUserID)
	if u := parseUUID(f.TwinAgentID); !u.Valid {
		t.Errorf("twin id %q failed to parse", f.TwinAgentID)
	}
	if u := parseUUID(f.ClaudeCodeAgentID); !u.Valid {
		t.Errorf("claude id %q failed to parse", f.ClaudeCodeAgentID)
	}
}

// bytesBuffer returns a JSON-encoded request body wrapped in an
// io.Reader. Local helper so the cross-user test can build a request
// outside of newRequest (which forces X-User-ID = testUserID).
func bytesBuffer(v any) io.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
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
