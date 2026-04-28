package service

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// CrossWorkspaceQueries is the slice of *db.Queries the cross-workspace
// query service depends on. The exact query reused here is the same one
// powering the global Kanban (handler/issue_cross_workspace.go), so the
// auth semantics — membership-filtered via SQL JOIN — are identical.
type CrossWorkspaceQueries interface {
	ListCrossWorkspaceIssues(ctx context.Context, arg db.ListCrossWorkspaceIssuesParams) ([]db.ListCrossWorkspaceIssuesRow, error)
}

// CrossWorkspaceIssue is the shape returned to the caller (typically the
// `cross_ws_query` agent tool, which JSON-encodes the slice). Field names
// mirror the global Kanban response so frontends and prompts can reuse them.
type CrossWorkspaceIssue struct {
	ID            string `json:"id"`
	Number        int32  `json:"number"`
	Identifier    string `json:"identifier"`
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceSlug string `json:"workspace_slug"`
	WorkspaceName string `json:"workspace_name"`
	Title         string `json:"title"`
	Status        string `json:"status"`
	Priority      string `json:"priority"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// ListIssuesFilters narrows the cross-workspace listing.
//
// Limit is clamped to [1, 200]; zero/negative falls back to the default.
type ListIssuesFilters struct {
	OpenOnly bool
	Statuses []string
	Limit    int32
}

const (
	crossWSDefaultLimit int32 = 50
	crossWSMaxLimit     int32 = 200
)

// CrossWorkspaceQueryService is the read surface used by the Cuong Pho
// global agent to inspect issues across every workspace the bound user is a
// member of. Membership is enforced via the underlying SQL JOIN — never
// post-filtered in Go — so a coding bug here cannot leak rows from
// non-member workspaces.
type CrossWorkspaceQueryService struct {
	q CrossWorkspaceQueries
}

// NewCrossWorkspaceQueryService wires the service to a query implementation.
func NewCrossWorkspaceQueryService(q CrossWorkspaceQueries) *CrossWorkspaceQueryService {
	return &CrossWorkspaceQueryService{q: q}
}

// ListOpenIssuesAcrossWorkspaces returns open issues (status not in 'done',
// 'cancelled') across all workspaces the user is a member of.
//
// Convenience over ListIssuesAcrossWorkspaces with OpenOnly=true.
func (s *CrossWorkspaceQueryService) ListOpenIssuesAcrossWorkspaces(
	ctx context.Context,
	userID pgtype.UUID,
	f ListIssuesFilters,
) ([]CrossWorkspaceIssue, error) {
	f.OpenOnly = true
	f.Statuses = nil
	return s.ListIssuesAcrossWorkspaces(ctx, userID, f)
}

// ListIssuesAcrossWorkspaces is the general read; statuses + open_only are
// mutually exclusive (open_only wins, matching the SQL contract). Returned
// rows are sorted (created_at DESC, id DESC).
func (s *CrossWorkspaceQueryService) ListIssuesAcrossWorkspaces(
	ctx context.Context,
	userID pgtype.UUID,
	f ListIssuesFilters,
) ([]CrossWorkspaceIssue, error) {
	if !userID.Valid {
		return nil, fmt.Errorf("user_id is required")
	}
	limit := clampLimit(f.Limit)

	var openOnly pgtype.Bool
	if f.OpenOnly {
		openOnly = pgtype.Bool{Bool: true, Valid: true}
	}

	rows, err := s.q.ListCrossWorkspaceIssues(ctx, db.ListCrossWorkspaceIssuesParams{
		UserID:   userID,
		Limit:    limit,
		OpenOnly: openOnly,
		Statuses: f.Statuses,
	})
	if err != nil {
		return nil, fmt.Errorf("list cross-workspace issues: %w", err)
	}

	out := make([]CrossWorkspaceIssue, len(rows))
	for i, r := range rows {
		out[i] = CrossWorkspaceIssue{
			ID:            uuidValue(r.ID),
			Number:        r.Number,
			Identifier:    fmt.Sprintf("%s-%d", r.WorkspaceIssuePrefix, r.Number),
			WorkspaceID:   uuidValue(r.WorkspaceID),
			WorkspaceSlug: r.WorkspaceSlug,
			WorkspaceName: r.WorkspaceName,
			Title:         r.Title,
			Status:        r.Status,
			Priority:      r.Priority,
			CreatedAt:     timestamptzString(r.CreatedAt),
			UpdatedAt:     timestamptzString(r.UpdatedAt),
		}
	}
	return out, nil
}

func clampLimit(n int32) int32 {
	if n <= 0 {
		return crossWSDefaultLimit
	}
	if n > crossWSMaxLimit {
		return crossWSMaxLimit
	}
	return n
}

func timestamptzString(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.Format("2006-01-02T15:04:05Z07:00")
}
