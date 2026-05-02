package service

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func validUUID() pgtype.UUID {
	return pgtype.UUID{Valid: true}
}

// TestTaskKindOf is a pure unit test (no DB) that locks every kind branch
// of the resolver classifier. Adding a new entity-type FK on
// agent_task_queue must extend this table — the typed enum is the only
// thing standing between the next pathway and a silent fallthrough of
// the MUL-182 / MUL-192 shape.
func TestTaskKindOf(t *testing.T) {
	cases := []struct {
		name string
		task db.AgentTaskQueue
		want TaskKind
	}{
		{
			name: "issue task",
			task: db.AgentTaskQueue{IssueID: validUUID()},
			want: TaskKindIssue,
		},
		{
			name: "chat task",
			task: db.AgentTaskQueue{ChatSessionID: validUUID()},
			want: TaskKindChat,
		},
		{
			name: "autopilot task",
			task: db.AgentTaskQueue{AutopilotRunID: validUUID()},
			want: TaskKindAutopilot,
		},
		{
			name: "global chat task",
			task: db.AgentTaskQueue{GlobalSessionID: validUUID()},
			want: TaskKindGlobal,
		},
		{
			name: "no FK set — orphan / future-pathway placeholder",
			task: db.AgentTaskQueue{},
			want: TaskKindUnknown,
		},
		{
			// Defensive: rows in the wild today carry exactly one FK.
			// If multiple are set, the resolver picks the most
			// workspace-scoped kind first — Issue beats Global.
			name: "issue + global both set: issue wins",
			task: db.AgentTaskQueue{
				IssueID:         validUUID(),
				GlobalSessionID: validUUID(),
			},
			want: TaskKindIssue,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TaskKindOf(tc.task); got != tc.want {
				t.Fatalf("TaskKindOf = %q, want %q", got, tc.want)
			}
		})
	}
}
