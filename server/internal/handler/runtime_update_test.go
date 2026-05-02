package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// createTestRuntimeInWorkspace inserts a runtime row in the given workspace
// and registers cleanup. Each call returns a fresh runtime ID so callers do
// not collide on UpdateStore.Create's per-runtime "already in progress" guard.
func createTestRuntimeInWorkspace(t *testing.T, workspaceID, label string) string {
	t.Helper()

	daemonID := fmt.Sprintf("update-test-daemon-%s-%d", label, time.Now().UnixNano())
	name := fmt.Sprintf("Update Test Runtime %s %d", label, time.Now().UnixNano())

	var runtimeID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at
		)
		VALUES ($1, $2, $3, 'cloud', 'claude', 'online', $4, '{}'::jsonb, now())
		RETURNING id
	`, workspaceID, daemonID, name, "Update test runtime").Scan(&runtimeID); err != nil {
		t.Fatalf("create runtime: %v", err)
	}

	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})

	return runtimeID
}

// TestGetUpdate_CrossWorkspace_Returns404 verifies that GetUpdate validates
// the runtimeId URL param against the caller's workspace. A member of
// workspace A cannot poll an update that belongs to a runtime in workspace
// B, even if they happen to know (or guess) the unguessable v4 update ID.
//
// Symmetric with the access check ReportUpdateResult already enforces.
func TestGetUpdate_CrossWorkspace_Returns404(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	var foreignWorkspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, "Update IDOR Foreign", "update-idor-foreign", "Cross-tenant update IDOR test", "UIF").Scan(&foreignWorkspaceID); err != nil {
		t.Fatalf("setup: create foreign workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, foreignWorkspaceID)
	})

	foreignRuntimeID := createTestRuntimeInWorkspace(t, foreignWorkspaceID, "foreign")

	update, err := testHandler.UpdateStore.Create(foreignRuntimeID, "v0.1.0")
	if err != nil {
		t.Fatalf("setup: create update: %v", err)
	}
	t.Cleanup(func() { testHandler.UpdateStore.Complete(update.ID, "") })

	// testUserID belongs only to testWorkspaceID, so requireDaemonRuntimeAccess
	// must reject a probe against the foreign runtime — even when armed with
	// the real update ID.
	w := httptest.NewRecorder()
	req := withURLParams(
		newRequest(http.MethodGet, "/api/runtimes/"+foreignRuntimeID+"/update/"+update.ID, nil),
		"runtimeId", foreignRuntimeID,
		"updateId", update.ID,
	)
	testHandler.GetUpdate(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GetUpdate cross-workspace: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestGetUpdate_RuntimeMismatch_Returns404 covers the second leg of the
// access check: the runtimeId in the URL belongs to the caller's workspace,
// but the update was created for a different runtime. Without the
// `update.RuntimeID != runtimeID` check, the handler would leak the status
// of an unrelated update to anyone who can guess the update ID.
func TestGetUpdate_RuntimeMismatch_Returns404(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	// Two runtimes in the SAME workspace. Caller is a member; the access
	// check passes for either runtime, so only the RuntimeID match prevents
	// the leak.
	updateRuntimeID := createTestRuntimeInWorkspace(t, testWorkspaceID, "mismatch-update")
	otherRuntimeID := createTestRuntimeInWorkspace(t, testWorkspaceID, "mismatch-other")

	// Update belongs to updateRuntimeID, but the caller addresses it through
	// otherRuntimeID.
	update, err := testHandler.UpdateStore.Create(updateRuntimeID, "v0.1.0")
	if err != nil {
		t.Fatalf("setup: create update: %v", err)
	}
	t.Cleanup(func() { testHandler.UpdateStore.Complete(update.ID, "") })

	w := httptest.NewRecorder()
	req := withURLParams(
		newRequest(http.MethodGet, "/api/runtimes/"+otherRuntimeID+"/update/"+update.ID, nil),
		"runtimeId", otherRuntimeID,
		"updateId", update.ID,
	)
	testHandler.GetUpdate(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GetUpdate runtime mismatch: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestGetUpdate_OwnRuntime_Returns200 is the positive control: a member
// reading their own runtime's update still succeeds. Without this, a
// regression in the access check could turn every legitimate poll into a 404.
func TestGetUpdate_OwnRuntime_Returns200(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID := createTestRuntimeInWorkspace(t, testWorkspaceID, "own")

	update, err := testHandler.UpdateStore.Create(runtimeID, "v0.1.0")
	if err != nil {
		t.Fatalf("setup: create update: %v", err)
	}
	t.Cleanup(func() { testHandler.UpdateStore.Complete(update.ID, "") })

	w := httptest.NewRecorder()
	req := withURLParams(
		newRequest(http.MethodGet, "/api/runtimes/"+runtimeID+"/update/"+update.ID, nil),
		"runtimeId", runtimeID,
		"updateId", update.ID,
	)
	testHandler.GetUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetUpdate own runtime: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}
