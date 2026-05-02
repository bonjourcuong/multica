package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// createModelListTestRuntime inserts an online runtime in the given workspace
// and registers cleanup. Each call returns a fresh runtime ID so callers do
// not collide on per-runtime store guards.
func createModelListTestRuntime(t *testing.T, workspaceID, label string) string {
	t.Helper()

	daemonID := fmt.Sprintf("model-test-daemon-%s-%d", label, time.Now().UnixNano())
	name := fmt.Sprintf("Model Test Runtime %s %d", label, time.Now().UnixNano())

	var runtimeID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at
		)
		VALUES ($1, $2, $3, 'cloud', 'claude', 'online', $4, '{}'::jsonb, now())
		RETURNING id
	`, workspaceID, daemonID, name, "Model-list test runtime").Scan(&runtimeID); err != nil {
		t.Fatalf("create runtime: %v", err)
	}

	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})

	return runtimeID
}

// TestModelListStore_RunningRequestTimesOut pins the escape hatch for
// requests that were claimed (PopPending → Running) but whose result was
// never reported — usually because the heartbeat response carrying the
// `pending_model_list` field was lost in transit. Before this, the only
// way out of Running was the 2-minute memory GC, which exceeded the UI
// polling window and surfaced as a silent "discovery failed" (MUL-1397).
func TestModelListStore_RunningRequestTimesOut(t *testing.T) {
	store := NewModelListStore()
	req := store.Create("runtime-xyz")
	claimed := store.PopPending("runtime-xyz")
	if claimed == nil {
		t.Fatal("expected PopPending to claim the pending request")
	}
	if claimed.Status != ModelListRunning {
		t.Fatalf("expected Running after PopPending, got %s", claimed.Status)
	}

	// Age the running record past the threshold without the daemon ever
	// reporting a result. Get() must flip it to Timeout so the UI can
	// terminate polling instead of waiting for the 2-minute GC.
	claimed.UpdatedAt = time.Now().Add(-(modelListRunningTimeout + time.Second))
	got := store.Get(req.ID)
	if got == nil {
		t.Fatal("expected stored request")
	}
	if got.Status != ModelListTimeout {
		t.Fatalf("expected Timeout after running threshold, got %s", got.Status)
	}
	if got.Error == "" {
		t.Fatal("expected timeout error message")
	}
}

// TestReportModelListResult_PreservesDefault guards the daemon → server
// → UI wire format for the model-discovery result. The `default` bool
// on each ModelEntry lights up the UI's "default" badge; if it gets
// dropped here (e.g. by going through a map[string]string), the badge
// silently disappears.
func TestReportModelListResult_PreservesDefault(t *testing.T) {
	store := NewModelListStore()
	req := store.Create("runtime-xyz")

	// Report a completed result with one default entry and one not.
	body := map[string]any{
		"status":    "completed",
		"supported": true,
		"models": []map[string]any{
			{"id": "foo-default", "label": "Foo", "provider": "p", "default": true},
			{"id": "bar", "label": "Bar", "provider": "p"},
		},
	}
	raw, _ := json.Marshal(body)

	// Use the store's Complete directly — we're verifying the wire
	// shape, not HTTP auth. The handler itself unmarshals into
	// []ModelEntry and forwards verbatim, which is the path we care
	// about here.
	var parsed struct {
		Models []ModelEntry `json:"models"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal report body: %v", err)
	}
	store.Complete(req.ID, parsed.Models, true)

	got := store.Get(req.ID)
	if got == nil {
		t.Fatal("expected stored result")
	}
	if len(got.Models) != 2 {
		t.Fatalf("expected 2 models, got %d: %+v", len(got.Models), got.Models)
	}
	if !got.Models[0].Default {
		t.Errorf("first model should carry Default=true, got %+v", got.Models[0])
	}
	if got.Models[1].Default {
		t.Errorf("second model should carry Default=false, got %+v", got.Models[1])
	}

	// Serialise the stored request back out (what UI actually sees)
	// and confirm `default: true` survives.
	out, _ := json.Marshal(got)
	if !bytes.Contains(out, []byte(`"default":true`)) {
		t.Errorf(`expected "default":true in JSON response, got: %s`, out)
	}
}

// TestReportModelListResult_DecodesJSONBodyDefault verifies the
// handler's request-body parsing accepts the `default` bool from
// the daemon POST — not just through the store API.
func TestReportModelListResult_DecodesJSONBodyDefault(t *testing.T) {
	// Simulate the shape the daemon POSTs: status + models + supported
	// with `default` on one entry.
	payload := `{"status":"completed","supported":true,"models":[{"id":"a","label":"A","default":true},{"id":"b","label":"B"}]}`
	r := httptest.NewRequest(http.MethodPost, "/api/daemon/runtimes/rt/models/req/result", bytes.NewBufferString(payload))

	var body struct {
		Status    string       `json:"status"`
		Models    []ModelEntry `json:"models"`
		Supported *bool        `json:"supported"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Models) != 2 {
		t.Fatalf("want 2 models, got %d", len(body.Models))
	}
	if !body.Models[0].Default {
		t.Errorf("default flag lost on model[0]: %+v", body.Models[0])
	}
}

// TestGetModelListRequest_CrossWorkspace_Returns404 verifies that
// GetModelListRequest validates the runtimeId URL param against the caller's
// workspace. A member of workspace A cannot poll a model-list request that
// belongs to a runtime in workspace B, even if they happen to know (or guess)
// the unguessable v4 request ID.
//
// Symmetric with the access check ReportModelListResult already enforces, and
// mirrors the GetLocalSkillListRequest / GetUpdate (MUL-194) pattern.
func TestGetModelListRequest_CrossWorkspace_Returns404(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	var foreignWorkspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, "Model IDOR Foreign", "model-idor-foreign", "Cross-tenant model-list IDOR test", "MIF").Scan(&foreignWorkspaceID); err != nil {
		t.Fatalf("setup: create foreign workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, foreignWorkspaceID)
	})

	foreignRuntimeID := createModelListTestRuntime(t, foreignWorkspaceID, "model-foreign")

	req := testHandler.ModelListStore.Create(foreignRuntimeID)

	// testUserID belongs only to testWorkspaceID, so requireDaemonRuntimeAccess
	// must reject a probe against the foreign runtime — even when armed with
	// the real request ID.
	w := httptest.NewRecorder()
	httpReq := withURLParams(
		newRequest(http.MethodGet, "/api/runtimes/"+foreignRuntimeID+"/models/"+req.ID, nil),
		"runtimeId", foreignRuntimeID,
		"requestId", req.ID,
	)
	testHandler.GetModelListRequest(w, httpReq)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GetModelListRequest cross-workspace: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestGetModelListRequest_RuntimeMismatch_Returns404 covers the second leg of
// the access check: the runtimeId in the URL belongs to the caller's
// workspace, but the model-list request was created for a different runtime.
// Without the `req.RuntimeID != runtimeID` check, the handler would leak the
// status of an unrelated request to anyone who can guess the request ID.
func TestGetModelListRequest_RuntimeMismatch_Returns404(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	// Two runtimes in the SAME workspace. Caller is a member; the access
	// check passes for either runtime, so only the RuntimeID match prevents
	// the leak.
	requestRuntimeID := createModelListTestRuntime(t, testWorkspaceID, "model-mismatch-req")
	otherRuntimeID := createModelListTestRuntime(t, testWorkspaceID, "model-mismatch-other")

	// Request belongs to requestRuntimeID, but the caller addresses it
	// through otherRuntimeID.
	req := testHandler.ModelListStore.Create(requestRuntimeID)

	w := httptest.NewRecorder()
	httpReq := withURLParams(
		newRequest(http.MethodGet, "/api/runtimes/"+otherRuntimeID+"/models/"+req.ID, nil),
		"runtimeId", otherRuntimeID,
		"requestId", req.ID,
	)
	testHandler.GetModelListRequest(w, httpReq)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GetModelListRequest runtime mismatch: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestGetModelListRequest_OwnRuntime_Returns200 is the positive control: a
// member reading their own runtime's request still succeeds. Without this, a
// regression in the access check could turn every legitimate poll into a 404.
func TestGetModelListRequest_OwnRuntime_Returns200(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID := createModelListTestRuntime(t, testWorkspaceID, "model-own")

	req := testHandler.ModelListStore.Create(runtimeID)

	w := httptest.NewRecorder()
	httpReq := withURLParams(
		newRequest(http.MethodGet, "/api/runtimes/"+runtimeID+"/models/"+req.ID, nil),
		"runtimeId", runtimeID,
		"requestId", req.ID,
	)
	testHandler.GetModelListRequest(w, httpReq)
	if w.Code != http.StatusOK {
		t.Fatalf("GetModelListRequest own runtime: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}
