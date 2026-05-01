package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// withWorkspaceContext mirrors what RequireWorkspaceMember does in production:
// stamps the workspace ID + member onto the request context so handlers that
// read ctxWorkspaceID see a value.
func withWorkspaceContext(req *http.Request, workspaceID, userID string) *http.Request {
	member := db.Member{
		UserID:      parseUUID(userID),
		WorkspaceID: parseUUID(workspaceID),
		Role:        "owner",
	}
	ctx := middleware.SetMemberContext(req.Context(), workspaceID, member)
	return req.WithContext(ctx)
}

func TestFindOrCreateChatSession_CreatesWhenMissing(t *testing.T) {
	if testHandler == nil {
		t.Skip("no test handler")
	}
	agentID := createHandlerTestAgent(t, "FOC create agent", []byte("[]"))

	w := httptest.NewRecorder()
	req := withWorkspaceContext(
		newRequest("POST", "/api/chat/sessions/find-or-create", map[string]any{"agent_id": agentID}),
		testWorkspaceID, testUserID,
	)
	testHandler.FindOrCreateChatSession(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var got ChatSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, w.Body.String())
	}
	if got.ID == "" || got.AgentID != agentID || got.CreatorID != testUserID {
		t.Fatalf("unexpected session payload: %+v", got)
	}
	if got.Status != "active" {
		t.Fatalf("expected status active, got %q", got.Status)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id = $1`, got.ID)
	})
}

func TestFindOrCreateChatSession_ReturnsExisting(t *testing.T) {
	if testHandler == nil {
		t.Skip("no test handler")
	}
	agentID := createHandlerTestAgent(t, "FOC hit agent", []byte("[]"))

	// Seed an existing active workspace-scope session for (workspace, user, agent).
	var seededID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, scope)
		VALUES ($1, $2, $3, 'pre-existing', 'workspace')
		RETURNING id
	`, testWorkspaceID, agentID, testUserID).Scan(&seededID); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id = $1`, seededID)
	})

	w := httptest.NewRecorder()
	req := withWorkspaceContext(
		newRequest("POST", "/api/chat/sessions/find-or-create", map[string]any{
			"agent_id": agentID,
			"title":    "ignored on hit",
		}),
		testWorkspaceID, testUserID,
	)
	testHandler.FindOrCreateChatSession(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got ChatSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, w.Body.String())
	}
	if got.ID != seededID {
		t.Fatalf("expected existing session %q, got %q", seededID, got.ID)
	}
	if got.Title == "ignored on hit" {
		t.Fatalf("title should not be overwritten on hit, got %q", got.Title)
	}
}

func TestFindOrCreateChatSession_PrefersMostRecent(t *testing.T) {
	if testHandler == nil {
		t.Skip("no test handler")
	}
	agentID := createHandlerTestAgent(t, "FOC recent agent", []byte("[]"))

	// Two active sessions; expect the most recently updated to win.
	var olderID, newerID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, scope, updated_at)
		VALUES ($1, $2, $3, 'older', 'workspace', $4)
		RETURNING id
	`, testWorkspaceID, agentID, testUserID, time.Now().Add(-2*time.Hour)).Scan(&olderID); err != nil {
		t.Fatalf("seed older: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, scope, updated_at)
		VALUES ($1, $2, $3, 'newer', 'workspace', now())
		RETURNING id
	`, testWorkspaceID, agentID, testUserID).Scan(&newerID); err != nil {
		t.Fatalf("seed newer: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id IN ($1, $2)`, olderID, newerID)
	})

	w := httptest.NewRecorder()
	req := withWorkspaceContext(
		newRequest("POST", "/api/chat/sessions/find-or-create", map[string]any{"agent_id": agentID}),
		testWorkspaceID, testUserID,
	)
	testHandler.FindOrCreateChatSession(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got ChatSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, w.Body.String())
	}
	if got.ID != newerID {
		t.Fatalf("expected newer session %q, got %q", newerID, got.ID)
	}
}

func TestFindOrCreateChatSession_IgnoresArchived(t *testing.T) {
	if testHandler == nil {
		t.Skip("no test handler")
	}
	agentID := createHandlerTestAgent(t, "FOC archived agent", []byte("[]"))

	// Existing session is archived → must miss and create a new one (201).
	var archivedID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, scope, status)
		VALUES ($1, $2, $3, 'archived', 'workspace', 'archived')
		RETURNING id
	`, testWorkspaceID, agentID, testUserID).Scan(&archivedID); err != nil {
		t.Fatalf("seed archived: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id = $1`, archivedID)
	})

	w := httptest.NewRecorder()
	req := withWorkspaceContext(
		newRequest("POST", "/api/chat/sessions/find-or-create", map[string]any{"agent_id": agentID}),
		testWorkspaceID, testUserID,
	)
	testHandler.FindOrCreateChatSession(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var got ChatSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, w.Body.String())
	}
	if got.ID == archivedID {
		t.Fatalf("returned archived session %q instead of creating a new one", archivedID)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id = $1`, got.ID)
	})
}

func TestFindOrCreateChatSession_NeverReturnsGlobalMirror(t *testing.T) {
	if testHandler == nil {
		t.Skip("no test handler")
	}
	agentID := createHandlerTestAgent(t, "FOC mirror agent", []byte("[]"))

	// A 'global_mirror' session for the same triple must be invisible to the
	// find-or-create lane lookup, even when active.
	var mirrorID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, scope)
		VALUES ($1, $2, $3, 'mirror', 'global_mirror')
		RETURNING id
	`, testWorkspaceID, agentID, testUserID).Scan(&mirrorID); err != nil {
		t.Fatalf("seed mirror: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id = $1`, mirrorID)
	})

	w := httptest.NewRecorder()
	req := withWorkspaceContext(
		newRequest("POST", "/api/chat/sessions/find-or-create", map[string]any{"agent_id": agentID}),
		testWorkspaceID, testUserID,
	)
	testHandler.FindOrCreateChatSession(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 (mirror must be ignored), got %d: %s", w.Code, w.Body.String())
	}
	var got ChatSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, w.Body.String())
	}
	if got.ID == mirrorID {
		t.Fatalf("global_mirror session leaked into lane response: %q", got.ID)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id = $1`, got.ID)
	})
}

func TestFindOrCreateChatSession_RejectsArchivedAgent(t *testing.T) {
	if testHandler == nil {
		t.Skip("no test handler")
	}
	agentID := createHandlerTestAgent(t, "FOC archived-agent agent", []byte("[]"))
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent SET archived_at = now() WHERE id = $1`, agentID); err != nil {
		t.Fatalf("archive agent: %v", err)
	}

	w := httptest.NewRecorder()
	req := withWorkspaceContext(
		newRequest("POST", "/api/chat/sessions/find-or-create", map[string]any{"agent_id": agentID}),
		testWorkspaceID, testUserID,
	)
	testHandler.FindOrCreateChatSession(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for archived agent, got %d: %s", w.Code, w.Body.String())
	}
}

func TestFindOrCreateChatSession_AgentNotInWorkspace(t *testing.T) {
	if testHandler == nil {
		t.Skip("no test handler")
	}
	// Bogus agent id (well-formed UUID, not in the workspace).
	w := httptest.NewRecorder()
	req := withWorkspaceContext(
		newRequest("POST", "/api/chat/sessions/find-or-create", map[string]any{
			"agent_id": "00000000-0000-0000-0000-000000000000",
		}),
		testWorkspaceID, testUserID,
	)
	testHandler.FindOrCreateChatSession(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown agent, got %d: %s", w.Code, w.Body.String())
	}
}

func TestFindOrCreateChatSession_RequiresAgentID(t *testing.T) {
	if testHandler == nil {
		t.Skip("no test handler")
	}
	w := httptest.NewRecorder()
	req := withWorkspaceContext(
		newRequest("POST", "/api/chat/sessions/find-or-create", map[string]any{}),
		testWorkspaceID, testUserID,
	)
	testHandler.FindOrCreateChatSession(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestFindOrCreateChatSession_Unauthenticated(t *testing.T) {
	if testHandler == nil {
		t.Skip("no test handler")
	}
	body, _ := json.Marshal(map[string]any{"agent_id": "00000000-0000-0000-0000-000000000000"})
	req := httptest.NewRequest("POST", "/api/chat/sessions/find-or-create", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// Intentionally no X-User-ID header.
	req = withWorkspaceContext(req, testWorkspaceID, testUserID)
	w := httptest.NewRecorder()
	testHandler.FindOrCreateChatSession(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}
