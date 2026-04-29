package service

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type fakeCrossWS struct {
	rows         []db.ListCrossWorkspaceIssuesRow
	lastArg      db.ListCrossWorkspaceIssuesParams
	timesCalled  int
}

func (f *fakeCrossWS) ListCrossWorkspaceIssues(ctx context.Context, arg db.ListCrossWorkspaceIssuesParams) ([]db.ListCrossWorkspaceIssuesRow, error) {
	f.lastArg = arg
	f.timesCalled++
	return f.rows, nil
}

func TestListOpenIssuesAcrossWorkspaces_PassesUserAndOpenOnly(t *testing.T) {
	ctx := context.Background()
	user := mustNewUUID()
	f := &fakeCrossWS{}

	svc := NewCrossWorkspaceQueryService(f)
	_, err := svc.ListOpenIssuesAcrossWorkspaces(ctx, user, ListIssuesFilters{Limit: 25})
	if err != nil {
		t.Fatal(err)
	}
	if f.timesCalled != 1 {
		t.Fatalf("expected 1 call, got %d", f.timesCalled)
	}
	if f.lastArg.UserID != user {
		t.Errorf("UserID not propagated")
	}
	if !f.lastArg.OpenOnly.Valid || !f.lastArg.OpenOnly.Bool {
		t.Errorf("expected OpenOnly=true")
	}
	if f.lastArg.Limit != 25 {
		t.Errorf("expected Limit=25, got %d", f.lastArg.Limit)
	}
	if len(f.lastArg.Statuses) != 0 {
		t.Errorf("OpenOnly path must not pass Statuses, got %v", f.lastArg.Statuses)
	}
}

func TestListIssuesAcrossWorkspaces_AcceptsExplicitStatuses(t *testing.T) {
	ctx := context.Background()
	user := mustNewUUID()
	f := &fakeCrossWS{}

	svc := NewCrossWorkspaceQueryService(f)
	_, err := svc.ListIssuesAcrossWorkspaces(ctx, user, ListIssuesFilters{
		Statuses: []string{"todo", "in_progress"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.lastArg.OpenOnly.Valid {
		t.Errorf("non-OpenOnly path must leave OpenOnly null, got %v", f.lastArg.OpenOnly)
	}
	if len(f.lastArg.Statuses) != 2 {
		t.Errorf("expected Statuses passed through, got %v", f.lastArg.Statuses)
	}
}

func TestListIssuesAcrossWorkspaces_ClampsLimit(t *testing.T) {
	ctx := context.Background()
	user := mustNewUUID()
	cases := map[string]struct {
		in       int32
		expected int32
	}{
		"zero falls back to default": {in: 0, expected: crossWSDefaultLimit},
		"negative falls back":        {in: -10, expected: crossWSDefaultLimit},
		"over max clamps":            {in: 1000, expected: crossWSMaxLimit},
		"in range passes through":    {in: 75, expected: 75},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fakeCrossWS{}
			svc := NewCrossWorkspaceQueryService(f)
			if _, err := svc.ListIssuesAcrossWorkspaces(ctx, user, ListIssuesFilters{Limit: c.in}); err != nil {
				t.Fatal(err)
			}
			if f.lastArg.Limit != c.expected {
				t.Errorf("Limit: in=%d expected=%d got=%d", c.in, c.expected, f.lastArg.Limit)
			}
		})
	}
}

func TestListIssuesAcrossWorkspaces_RejectsZeroUserID(t *testing.T) {
	ctx := context.Background()
	svc := NewCrossWorkspaceQueryService(&fakeCrossWS{})
	if _, err := svc.ListIssuesAcrossWorkspaces(ctx, pgtype.UUID{}, ListIssuesFilters{}); err == nil {
		t.Fatal("expected error when user_id is zero")
	}
}

func TestListOpenIssuesAcrossWorkspaces_ShapesResultRows(t *testing.T) {
	ctx := context.Background()
	user := mustNewUUID()
	wsID := mustNewUUID()
	issueID := mustNewUUID()
	f := &fakeCrossWS{rows: []db.ListCrossWorkspaceIssuesRow{
		{
			ID:                   issueID,
			Number:               42,
			WorkspaceID:          wsID,
			WorkspaceSlug:        "fuchsia-b2b",
			WorkspaceName:        "Fuchsia B2B",
			WorkspaceIssuePrefix: "FUB",
			Title:                "Open one",
			Status:               "todo",
			Priority:             "high",
		},
	}}

	svc := NewCrossWorkspaceQueryService(f)
	rows, err := svc.ListOpenIssuesAcrossWorkspaces(ctx, user, ListIssuesFilters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.Identifier != "FUB-42" {
		t.Errorf("expected Identifier 'FUB-42', got %q", r.Identifier)
	}
	if r.WorkspaceSlug != "fuchsia-b2b" {
		t.Errorf("WorkspaceSlug mismatch: %q", r.WorkspaceSlug)
	}
	if r.Status != "todo" || r.Priority != "high" {
		t.Errorf("status/priority not propagated")
	}
}
