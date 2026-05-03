package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// MUL-201 / ADR 2026-05-03 — daemon-token mint endpoint.
//
// Issues an mdt_-prefixed daemon token for a (workspace_id, daemon_id) pair,
// bound to the calling user. Rotation-by-replace: a second call for the same
// pair deletes the existing row and inserts a new one inside a single
// transaction (ADR D9 §"Transaction-wrapped rotation"), so a torn rotation
// never invalidates the live daemon's token.
//
// Validation algorithm (ADR D9 §"daemon_id ownership validation"):
//  1. Workspace membership (handled by requireWorkspaceMember).
//  2. agent_runtime.owner_id check — reject if any runtime row for
//     (workspace_id, daemon_id) is already owned by another user.
//  3. daemon_token.user_id check — reject if a previous mint for the same
//     (workspace_id, daemon_id) was bound to another user. Covers the case
//     where daemon login was run before daemon start (no agent_runtime row
//     exists yet).
//  4. Trust on first use — if neither signal exists, accept and bind to the
//     caller. The next mint is then gated by step 3 against this row.

const (
	defaultDaemonTokenExpiryDays = 90
	maxDaemonTokenExpiryDays     = 3650 // 10 years; refuses obvious fat-finger inputs
)

type CreateDaemonTokenRequest struct {
	WorkspaceID   string `json:"workspace_id"`
	DaemonID      string `json:"daemon_id"`
	ExpiresInDays *int   `json:"expires_in_days,omitempty"`
}

type CreateDaemonTokenResponse struct {
	Token       string `json:"token"`
	WorkspaceID string `json:"workspace_id"`
	DaemonID    string `json:"daemon_id"`
	ExpiresAt   string `json:"expires_at"`
}

func (h *Handler) CreateDaemonToken(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var req CreateDaemonTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.WorkspaceID == "" || req.DaemonID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id and daemon_id are required")
		return
	}
	expiresInDays := defaultDaemonTokenExpiryDays
	if req.ExpiresInDays != nil {
		if *req.ExpiresInDays <= 0 || *req.ExpiresInDays > maxDaemonTokenExpiryDays {
			writeError(w, http.StatusBadRequest, "expires_in_days must be between 1 and 3650")
			return
		}
		expiresInDays = *req.ExpiresInDays
	}

	// Step 1 — workspace membership. requireWorkspaceMember writes a 404 on
	// miss, which matches the existing pattern in requireDaemonWorkspaceAccess
	// (anti-enumeration: don't leak whether the workspace exists).
	if _, ok := h.requireWorkspaceMember(w, r, req.WorkspaceID, "not found"); !ok {
		return
	}

	callerUUID := parseUUID(userID)
	workspaceUUID := parseUUID(req.WorkspaceID)

	// Step 2 — agent_runtime.owner_id ownership check. Any non-null owner_id
	// for this (workspace_id, daemon_id) must equal the caller. Body is the
	// opaque "forbidden" so we don't leak which signal failed.
	owners, err := h.Queries.ListAgentRuntimeOwnersByWorkspaceAndDaemon(r.Context(),
		db.ListAgentRuntimeOwnersByWorkspaceAndDaemonParams{
			WorkspaceID: workspaceUUID,
			DaemonID:    pgtype.Text{String: req.DaemonID, Valid: true},
		})
	if err != nil {
		slog.Error("daemon_token: list runtime owners failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to validate ownership")
		return
	}
	for _, ownerID := range owners {
		if !uuidsEqual(ownerID, callerUUID) {
			slog.Warn("daemon_token: cross-user agent_runtime ownership rejected",
				"workspace_id", req.WorkspaceID, "daemon_id", req.DaemonID, "caller", userID)
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
	}

	// Step 3 — daemon_token.user_id ownership check. Same opaque body.
	existing, err := h.Queries.ListDaemonTokensByWorkspaceAndDaemon(r.Context(),
		db.ListDaemonTokensByWorkspaceAndDaemonParams{
			WorkspaceID: workspaceUUID,
			DaemonID:    req.DaemonID,
		})
	if err != nil {
		slog.Error("daemon_token: list existing tokens failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to validate ownership")
		return
	}
	for _, row := range existing {
		if !uuidsEqual(row.UserID, callerUUID) {
			slog.Warn("daemon_token: cross-user daemon_token ownership rejected",
				"workspace_id", req.WorkspaceID, "daemon_id", req.DaemonID, "caller", userID)
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
	}

	// Step 4 — mint. Generate the raw token outside the transaction so a tx
	// retry would not reuse the same secret across attempts.
	rawToken, err := auth.GenerateDaemonToken()
	if err != nil {
		slog.Error("daemon_token: generate token failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	expiresAt := time.Now().Add(time.Duration(expiresInDays) * 24 * time.Hour)

	row, err := h.rotateDaemonToken(r.Context(), workspaceUUID, req.DaemonID, callerUUID, rawToken, expiresAt)
	if err != nil {
		slog.Error("daemon_token: rotate failed", "error", err,
			"workspace_id", req.WorkspaceID, "daemon_id", req.DaemonID)
		writeError(w, http.StatusInternalServerError, "failed to mint token")
		return
	}

	writeJSON(w, http.StatusCreated, CreateDaemonTokenResponse{
		Token:       rawToken,
		WorkspaceID: uuidToString(row.WorkspaceID),
		DaemonID:    row.DaemonID,
		ExpiresAt:   row.ExpiresAt.Time.UTC().Format(time.RFC3339),
	})
}

// rotateDaemonToken wraps the delete + create pair in a single transaction
// (ADR 2026-05-03 D9 §"Transaction-wrapped rotation"). Rotation runs against
// a daemon that is currently online and serving traffic — a torn rotation
// would silently invalidate the live token and brick the daemon at next
// heartbeat. The transaction guarantees the existing row only disappears if
// the new row commits successfully; on any error the defer-rollback restores
// the prior state.
func (h *Handler) rotateDaemonToken(
	ctx context.Context,
	workspaceUUID pgtype.UUID,
	daemonID string,
	userUUID pgtype.UUID,
	rawToken string,
	expiresAt time.Time,
) (db.DaemonToken, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return db.DaemonToken{}, err
	}
	defer tx.Rollback(ctx)

	q := h.Queries.WithTx(tx)

	if err := q.DeleteDaemonTokensByWorkspaceAndDaemon(ctx, db.DeleteDaemonTokensByWorkspaceAndDaemonParams{
		WorkspaceID: workspaceUUID,
		DaemonID:    daemonID,
	}); err != nil {
		return db.DaemonToken{}, err
	}

	row, err := q.CreateDaemonToken(ctx, db.CreateDaemonTokenParams{
		TokenHash:   auth.HashToken(rawToken),
		WorkspaceID: workspaceUUID,
		DaemonID:    daemonID,
		UserID:      userUUID,
		ExpiresAt:   pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
	if err != nil {
		return db.DaemonToken{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return db.DaemonToken{}, err
	}

	return row, nil
}

// uuidsEqual compares two pgtype.UUID values by their underlying byte array
// rather than by string formatting. Returns false when either side is invalid.
func uuidsEqual(a, b pgtype.UUID) bool {
	if !a.Valid || !b.Valid {
		return false
	}
	return a.Bytes == b.Bytes
}
