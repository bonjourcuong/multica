package service

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// globalSessionRow is a minimal pgx.Row that scans a `global_chat_session`
// row in the column order produced by sqlc (`getGlobalChatSession`):
// id, user_id, agent_id, title, created_at, archived_at.
type globalSessionRow struct {
	id      pgtype.UUID
	userID  pgtype.UUID
	agentID pgtype.UUID
}

func (r *globalSessionRow) Scan(dest ...any) error {
	values := []any{
		r.id, r.userID, r.agentID,
		"global chat",
		pgtype.Timestamptz{},
		pgtype.Timestamptz{},
	}
	for i, d := range dest {
		if i >= len(values) {
			break
		}
		switch ptr := d.(type) {
		case *pgtype.UUID:
			*ptr = values[i].(pgtype.UUID)
		case *string:
			*ptr = values[i].(string)
		case *pgtype.Timestamptz:
			*ptr = values[i].(pgtype.Timestamptz)
		}
	}
	return nil
}

// globalChatDBTX is a minimal DBTX that recognises only the
// GetGlobalChatSession SQL pattern (every other QueryRow returns ErrNoRows
// so unrelated queries surface as test failures rather than silent zero
// values).
type globalChatDBTX struct {
	session globalSessionRow
}

func (m *globalChatDBTX) Exec(_ context.Context, _ string, _ ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag(""), nil
}

func (m *globalChatDBTX) Query(_ context.Context, _ string, _ ...interface{}) (pgx.Rows, error) {
	return nil, pgx.ErrNoRows
}

func (m *globalChatDBTX) QueryRow(_ context.Context, sql string, _ ...interface{}) pgx.Row {
	if strings.Contains(sql, "FROM global_chat_session") {
		return &m.session
	}
	return &mockRow{err: pgx.ErrNoRows}
}

func newGlobalSessionUUID(b byte) pgtype.UUID {
	var u pgtype.UUID
	u.Valid = true
	u.Bytes[0] = b
	return u
}

// TestBroadcastTaskEvent_GlobalTaskFailed asserts that when a global-chat
// task is funnelled through broadcastTaskEvent with EventTaskFailed (the
// path a foreign daemon failure walks via FailTask / HandleFailedTasks),
// the helper publishes a `global_chat:task_update` event keyed on the
// originating user's UserID — not a workspace-keyed event that would be
// silently dropped because ResolveTaskWorkspaceID returns "" for tasks
// whose only linkage column is GlobalSessionID. Regression for MUL-192,
// audit Finding #2 from MUL-190.
func TestBroadcastTaskEvent_GlobalTaskFailed(t *testing.T) {
	taskID := newGlobalSessionUUID(0xA1)
	agentID := newGlobalSessionUUID(0xA2)
	sessionID := newGlobalSessionUUID(0xA3)
	userID := newGlobalSessionUUID(0xA4)

	mock := &globalChatDBTX{
		session: globalSessionRow{id: sessionID, userID: userID, agentID: agentID},
	}
	bus := events.New()

	var (
		mu       sync.Mutex
		captured []events.Event
	)
	bus.SubscribeAll(func(e events.Event) {
		mu.Lock()
		defer mu.Unlock()
		captured = append(captured, e)
	})

	svc := &TaskService{
		Queries: db.New(mock),
		Bus:     bus,
	}

	task := db.AgentTaskQueue{
		ID:              taskID,
		AgentID:         agentID,
		Status:          "failed",
		GlobalSessionID: sessionID,
		Error:           pgtype.Text{String: "agent crashed", Valid: true},
	}
	svc.broadcastTaskEvent(context.Background(), protocol.EventTaskFailed, task)

	mu.Lock()
	defer mu.Unlock()
	if len(captured) != 1 {
		t.Fatalf("expected exactly 1 event, got %d", len(captured))
	}
	e := captured[0]
	if e.Type != protocol.EventGlobalChatTaskUpdate {
		t.Errorf("event type = %q, want %q (workspace-keyed task:failed would have been silently dropped)", e.Type, protocol.EventGlobalChatTaskUpdate)
	}
	if e.WorkspaceID != "" {
		t.Errorf("event WorkspaceID = %q, want empty (global tasks must publish on per-user channel)", e.WorkspaceID)
	}
	if e.UserID != uuidValue(userID) {
		t.Errorf("event UserID = %q, want %q (originating user)", e.UserID, uuidValue(userID))
	}
	if e.TaskID != uuidValue(taskID) {
		t.Errorf("event TaskID = %q, want %q", e.TaskID, uuidValue(taskID))
	}
	payload, ok := e.Payload.(protocol.GlobalChatTaskUpdatePayload)
	if !ok {
		t.Fatalf("payload type = %T, want GlobalChatTaskUpdatePayload", e.Payload)
	}
	if payload.Status != "failed" {
		t.Errorf("payload.Status = %q, want %q", payload.Status, "failed")
	}
	if payload.GlobalSessionID != uuidValue(sessionID) {
		t.Errorf("payload.GlobalSessionID = %q, want %q", payload.GlobalSessionID, uuidValue(sessionID))
	}
	if payload.AgentID != uuidValue(agentID) {
		t.Errorf("payload.AgentID = %q, want %q", payload.AgentID, uuidValue(agentID))
	}
	if payload.Error != "agent crashed" {
		t.Errorf("payload.Error = %q, want %q (failure reason should reach the user)", payload.Error, "agent crashed")
	}
}

// TestBroadcastTaskEvent_GlobalTaskCancelled covers the cancel path
// (CancelTask, CancelTasksForIssue, RerunIssue) — same primitive as the
// failure path but routed via EventTaskCancelled. Cancel is the second of
// the two terminal states the global-chat pane needs to clear "agent is
// thinking…" on (MUL-159).
func TestBroadcastTaskEvent_GlobalTaskCancelled(t *testing.T) {
	taskID := newGlobalSessionUUID(0xB1)
	agentID := newGlobalSessionUUID(0xB2)
	sessionID := newGlobalSessionUUID(0xB3)
	userID := newGlobalSessionUUID(0xB4)

	mock := &globalChatDBTX{
		session: globalSessionRow{id: sessionID, userID: userID, agentID: agentID},
	}
	bus := events.New()

	var (
		mu       sync.Mutex
		captured []events.Event
	)
	bus.SubscribeAll(func(e events.Event) {
		mu.Lock()
		defer mu.Unlock()
		captured = append(captured, e)
	})

	svc := &TaskService{Queries: db.New(mock), Bus: bus}

	task := db.AgentTaskQueue{
		ID:              taskID,
		AgentID:         agentID,
		Status:          "cancelled",
		GlobalSessionID: sessionID,
	}
	svc.broadcastTaskEvent(context.Background(), protocol.EventTaskCancelled, task)

	mu.Lock()
	defer mu.Unlock()
	if len(captured) != 1 {
		t.Fatalf("expected 1 event, got %d", len(captured))
	}
	payload, ok := captured[0].Payload.(protocol.GlobalChatTaskUpdatePayload)
	if !ok {
		t.Fatalf("payload type = %T, want GlobalChatTaskUpdatePayload", captured[0].Payload)
	}
	if payload.Status != "cancelled" {
		t.Errorf("payload.Status = %q, want cancelled", payload.Status)
	}
}

// TestBroadcastTaskDispatch_GlobalTask covers the claim path
// (ClaimTask → broadcastTaskDispatch). For workspace tasks dispatch ships
// the inline `task:dispatch` event with the runtime payload; for global
// tasks we instead emit the lifecycle ping so observers without
// message-level state can react.
func TestBroadcastTaskDispatch_GlobalTask(t *testing.T) {
	taskID := newGlobalSessionUUID(0xC1)
	agentID := newGlobalSessionUUID(0xC2)
	sessionID := newGlobalSessionUUID(0xC3)
	userID := newGlobalSessionUUID(0xC4)

	mock := &globalChatDBTX{
		session: globalSessionRow{id: sessionID, userID: userID, agentID: agentID},
	}
	bus := events.New()

	var (
		mu       sync.Mutex
		captured []events.Event
	)
	bus.SubscribeAll(func(e events.Event) {
		mu.Lock()
		defer mu.Unlock()
		captured = append(captured, e)
	})

	svc := &TaskService{Queries: db.New(mock), Bus: bus}

	task := db.AgentTaskQueue{
		ID:              taskID,
		AgentID:         agentID,
		Status:          "dispatched",
		GlobalSessionID: sessionID,
	}
	svc.broadcastTaskDispatch(context.Background(), task)

	mu.Lock()
	defer mu.Unlock()
	if len(captured) != 1 {
		t.Fatalf("expected 1 event, got %d", len(captured))
	}
	if captured[0].Type != protocol.EventGlobalChatTaskUpdate {
		t.Errorf("event type = %q, want %q", captured[0].Type, protocol.EventGlobalChatTaskUpdate)
	}
	payload := captured[0].Payload.(protocol.GlobalChatTaskUpdatePayload)
	if payload.Status != "dispatched" {
		t.Errorf("payload.Status = %q, want dispatched", payload.Status)
	}
}
