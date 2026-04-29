package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ---------------------------------------------------------------------------
// Response types
// ---------------------------------------------------------------------------

// GlobalChatSessionResponse is the JSON shape for /api/global/chat/sessions/me.
type GlobalChatSessionResponse struct {
	ID         string  `json:"id"`
	UserID     string  `json:"user_id"`
	AgentID    string  `json:"agent_id"`
	Title      string  `json:"title"`
	CreatedAt  string  `json:"created_at"`
	ArchivedAt *string `json:"archived_at"`
}

// GlobalChatMessageResponse is the JSON shape for one global message.
type GlobalChatMessageResponse struct {
	ID              string   `json:"id"`
	GlobalSessionID string   `json:"global_session_id"`
	AuthorKind      string   `json:"author_kind"`
	AuthorID        string   `json:"author_id"`
	Body            string   `json:"body"`
	DispatchedTo    []byte   `json:"dispatched_to,omitempty"`
	CreatedAt       string   `json:"created_at"`
}

// GlobalChatPostResponse is what the frontend gets after POSTing a message.
// Includes the persisted message and the per-target dispatch outcome so the
// pane can render delivery state immediately, without waiting on the
// realtime event.
type GlobalChatPostResponse struct {
	Message  GlobalChatMessageResponse              `json:"message"`
	Dispatch []protocol.GlobalChatDispatchTarget    `json:"dispatch"`
	Mentions []GlobalChatMentionEcho                `json:"mentions"`
}

type GlobalChatMentionEcho struct {
	WorkspaceSlug string `json:"workspace_slug"`
	AgentName     string `json:"agent_name,omitempty"`
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// BootstrapGlobalSession is POST /api/global/chat/sessions. Idempotent —
// returns the existing session if one already exists for the user, otherwise
// creates the global agent + session.
func (h *Handler) BootstrapGlobalSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	sess, err := h.GlobalChat.EnsureSession(r.Context(), parseUUID(userID))
	if err != nil {
		slog.Error("bootstrap global session failed", "user_id", userID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to bootstrap global chat session")
		return
	}
	writeJSON(w, http.StatusOK, globalSessionToResponse(sess))
}

// GetGlobalSession is GET /api/global/chat/sessions/me. Returns 404 when the
// session has not been bootstrapped (the frontend should POST sessions to
// create one).
func (h *Handler) GetGlobalSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	sess, err := h.GlobalChat.GetSession(r.Context(), parseUUID(userID))
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "global chat session not found")
			return
		}
		slog.Error("get global session failed", "user_id", userID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load global chat session")
		return
	}
	writeJSON(w, http.StatusOK, globalSessionToResponse(sess))
}

// ListGlobalMessages is GET /api/global/chat/sessions/me/messages. Returns
// the most recent messages reverse-chronologically. Cursor (`?before=`) is
// the RFC3339 created_at of the last seen row.
func (h *Handler) ListGlobalMessages(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	limit := int32(50)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			limit = int32(v)
		}
	}

	var cursor pgtype.Timestamptz
	if raw := r.URL.Query().Get("before"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid before cursor (expected RFC3339)")
			return
		}
		cursor = pgtype.Timestamptz{Time: t, Valid: true}
	}

	rows, err := h.GlobalChat.ListMessages(r.Context(), parseUUID(userID), cursor, limit)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "global chat session not found")
			return
		}
		slog.Error("list global messages failed", "user_id", userID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list global chat messages")
		return
	}

	resp := make([]GlobalChatMessageResponse, len(rows))
	for i, m := range rows {
		resp[i] = globalMessageToResponse(m)
	}
	writeJSON(w, http.StatusOK, resp)
}

// PostGlobalMessage is POST /api/global/chat/sessions/me/messages. Persists
// the user message, fans out to each `@workspace[:agent]` mention, and
// returns the persisted message + per-target dispatch outcome.
func (h *Handler) PostGlobalMessage(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var req struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Body == "" {
		writeError(w, http.StatusBadRequest, "body is required")
		return
	}

	user := parseUUID(userID)
	msg, err := h.GlobalChat.PostUserMessage(r.Context(), user, req.Body)
	if err != nil {
		slog.Error("post global message failed", "user_id", userID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to post global message")
		return
	}

	// Fan out to mentions. Each target's outcome is independent: a failed
	// dispatch surfaces as Error on the target row but does not abort
	// other targets or roll back the global message.
	mentions := h.GlobalChat.ParseMentions(req.Body)
	echoes := make([]GlobalChatMentionEcho, len(mentions))
	for i, m := range mentions {
		echoes[i] = GlobalChatMentionEcho{
			WorkspaceSlug: m.WorkspaceSlug,
			AgentName:     m.AgentName,
		}
	}

	dispatch := make([]protocol.GlobalChatDispatchTarget, 0, len(mentions))
	for _, m := range mentions {
		target := protocol.GlobalChatDispatchTarget{
			WorkspaceSlug: m.WorkspaceSlug,
		}
		res, err := h.GlobalDispatch.DispatchToWorkspace(r.Context(), service.DispatchParams{
			UserID:                user,
			WorkspaceSlug:         m.WorkspaceSlug,
			Body:                  req.Body,
			TargetAgentName:       m.AgentName,
			OriginGlobalMessageID: msg.ID,
			OriginGlobalSessionID: msg.GlobalSessionID,
		})
		if err != nil {
			target.Error = humanizeDispatchError(err, m.WorkspaceSlug)
		} else {
			target.WorkspaceID = uuidToString(res.WorkspaceID)
			target.MirrorSessionID = uuidToString(res.MirrorSessionID)
			target.MirrorMessageID = uuidToString(res.MirrorMessageID)
		}
		dispatch = append(dispatch, target)
	}
	if len(dispatch) > 0 {
		h.GlobalChat.PublishDispatched(user, msg.ID, dispatch)
	}

	writeJSON(w, http.StatusCreated, GlobalChatPostResponse{
		Message:  globalMessageToResponse(msg),
		Dispatch: dispatch,
		Mentions: echoes,
	})
}

// CrossWorkspaceQueryRequest is the body of POST /api/global/chat/cross-ws-query
// — exposed as an HTTP endpoint so the agent runtime / frontend can
// reuse the cross-WS read tool that the global agent uses internally.
type CrossWorkspaceQueryRequest struct {
	OpenOnly bool     `json:"open_only"`
	Statuses []string `json:"statuses"`
	Limit    int32    `json:"limit"`
}

// QueryCrossWorkspaceIssues is POST /api/global/chat/cross-ws-query. Lists
// open issues across every workspace the caller belongs to.
//
// Authorization: membership filter is enforced inside the SQL JOIN — same
// helper as the existing /api/issues/cross-workspace endpoint.
func (h *Handler) QueryCrossWorkspaceIssues(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req CrossWorkspaceQueryRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	rows, err := h.CrossWorkspaceQuery.ListIssuesAcrossWorkspaces(r.Context(), parseUUID(userID), service.ListIssuesFilters{
		OpenOnly: req.OpenOnly,
		Statuses: req.Statuses,
		Limit:    req.Limit,
	})
	if err != nil {
		slog.Error("cross-ws query failed", "user_id", userID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list cross-workspace issues")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"issues": rows})
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func globalSessionToResponse(s db.GlobalChatSession) GlobalChatSessionResponse {
	resp := GlobalChatSessionResponse{
		ID:        uuidToString(s.ID),
		UserID:    uuidToString(s.UserID),
		AgentID:   uuidToString(s.AgentID),
		Title:     s.Title,
		CreatedAt: timestampToString(s.CreatedAt),
	}
	if s.ArchivedAt.Valid {
		resp.ArchivedAt = timestampToPtr(s.ArchivedAt)
	}
	return resp
}

func globalMessageToResponse(m db.GlobalChatMessage) GlobalChatMessageResponse {
	resp := GlobalChatMessageResponse{
		ID:              uuidToString(m.ID),
		GlobalSessionID: uuidToString(m.GlobalSessionID),
		AuthorKind:      m.AuthorKind,
		AuthorID:        uuidToString(m.AuthorID),
		Body:            m.Body,
		CreatedAt:       timestampToString(m.CreatedAt),
	}
	if len(m.DispatchedTo) > 0 && string(m.DispatchedTo) != "[]" {
		resp.DispatchedTo = m.DispatchedTo
	}
	return resp
}

// humanizeDispatchError maps service errors to user-facing strings.
func humanizeDispatchError(err error, slug string) string {
	switch {
	case errors.Is(err, service.ErrWorkspaceNotFound):
		return "Workspace `@" + slug + "` introuvable."
	case errors.Is(err, service.ErrNotWorkspaceMember):
		return "Je n'ai pas accès à `@" + slug + "`."
	case errors.Is(err, service.ErrGlobalAgentMissing):
		return "Global agent not provisioned. Refresh the page to bootstrap."
	default:
		return err.Error()
	}
}
