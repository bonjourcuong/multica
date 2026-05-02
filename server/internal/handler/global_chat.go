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
//
// TaskID and AgentID are populated only when the request carried an
// optional `agent_id` (V3 picker — MUL-137); they let the FE render a
// "agent is typing" indicator until the daemon completes the task.
type GlobalChatPostResponse struct {
	Message  GlobalChatMessageResponse           `json:"message"`
	Dispatch []protocol.GlobalChatDispatchTarget `json:"dispatch"`
	Mentions []GlobalChatMentionEcho             `json:"mentions"`
	TaskID   string                              `json:"task_id,omitempty"`
	AgentID  string                              `json:"agent_id,omitempty"`
}

// GlobalMirrorSummaryResponse is one row of GET /api/global/chat/mirrors.
// Mirrors `GlobalMirrorSummary` in packages/core/types/global-chat.ts: the
// frontend client is coded against this exact shape, plus an `unread_count`
// extension (issue MUL-100 DoD) the tile UI can adopt incrementally.
//
// `mirror_session_id` and `last_message_at` are nullable: a workspace the
// user belongs to but has never dispatched into has no mirror session yet.
type GlobalMirrorSummaryResponse struct {
	WorkspaceID     string  `json:"workspace_id"`
	WorkspaceSlug   string  `json:"workspace_slug"`
	WorkspaceName   string  `json:"workspace_name"`
	MirrorSessionID *string `json:"mirror_session_id"`
	LastMessageAt   *string `json:"last_message_at"`
	UnreadCount     int32   `json:"unread_count"`
}

// globalMirrorsLimit caps the per-call mirror summary count. A user belongs
// to a bounded number of workspaces in practice; the cap exists so a freak
// case (bot user with thousands of memberships) cannot fan the query out
// without bound. ADR'd at 200 — bigger means open a separate paginated
// endpoint, this one is "all my workspaces in one shot".
const globalMirrorsLimit = 200

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
//
// V3 (MUL-137) added an optional `agent_id` to the request body: when
// present, the message is enqueued as a global chat task targeted at
// that agent so a runtime authors the reply (the V1 mention fan-out
// continues to run independently). MUL-156 extends this so the handler
// also enqueues a default task — targeted at the session's twin — when
// `agent_id` is omitted, otherwise the orchestrator never receives a
// non-mention message and the global chat sits silent.
func (h *Handler) PostGlobalMessage(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var req struct {
		Body    string `json:"body"`
		AgentID string `json:"agent_id"`
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

	// Validate the optional agent_id BEFORE persisting / fanning out.
	// Failing late (after the user message lands) would create a "ghost
	// turn" with no reply and complicate retry semantics on the FE.
	var pickedAgent *db.Agent
	if req.AgentID != "" {
		agentUUID := parseUUID(req.AgentID)
		if !agentUUID.Valid {
			writeError(w, http.StatusBadRequest, "invalid agent_id")
			return
		}
		agent, err := h.GlobalChat.GetGlobalAgentForUser(r.Context(), user, agentUUID)
		if err != nil {
			if isNotFound(err) {
				// Collapse "doesn't exist" + "exists but workspace agent"
				// + "exists but other user" into 404 — picker probing
				// must not enumerate cross-user agent IDs.
				writeError(w, http.StatusNotFound, "agent not found")
				return
			}
			slog.Error("post global message: agent lookup failed",
				"user_id", userID, "agent_id", req.AgentID, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to load agent")
			return
		}
		if agent.ArchivedAt.Valid {
			writeError(w, http.StatusUnprocessableEntity, "agent is archived")
			return
		}
		pickedAgent = &agent
	}

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

	// Enqueue the agent task LAST so a runtime/queue failure doesn't roll
	// back the user's message or the mention fan-out (both of which
	// already succeeded). The frontend gets task_id back so it can show a
	// pending indicator while the daemon picks up the task.
	//
	// MUL-156: when no explicit agent_id was sent, default to the
	// session's bound twin so every message reaches the orchestrator,
	// not just messages from clients that hit the V3 picker.
	resp := GlobalChatPostResponse{
		Message:  globalMessageToResponse(msg),
		Dispatch: dispatch,
		Mentions: echoes,
	}
	sess, err := h.GlobalChat.GetSession(r.Context(), user)
	if err != nil {
		slog.Error("post global message: load session for task failed",
			"user_id", userID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load global chat session")
		return
	}
	taskAgentID := pgtype.UUID{}
	if pickedAgent != nil {
		taskAgentID = pickedAgent.ID
	} else {
		taskAgentID = sess.AgentID
	}
	if taskAgentID.Valid {
		task, err := h.TaskService.EnqueueGlobalChatTask(r.Context(), sess, taskAgentID)
		if err != nil {
			// When the default twin is missing a runtime (legacy row not
			// yet reseeded), don't 500 the user — the message is already
			// persisted and the mention fan-out has run. Log + continue
			// so the FE still receives a 201 and can render the message.
			if pickedAgent == nil {
				slog.Warn("post global message: default twin task skipped",
					"user_id", userID, "agent_id", uuidToString(taskAgentID), "error", err)
			} else {
				slog.Error("post global message: enqueue task failed",
					"user_id", userID, "agent_id", req.AgentID, "error", err)
				writeError(w, http.StatusInternalServerError, "failed to enqueue agent task: "+err.Error())
				return
			}
		} else {
			resp.TaskID = uuidToString(task.ID)
			resp.AgentID = uuidToString(taskAgentID)
		}
	}

	writeJSON(w, http.StatusCreated, resp)
}

// PostGlobalAgentReply is POST /api/global/chat/sessions/me/messages/agent-reply.
// Backs the CLI `multica global-chat reply` command (MUL-158): an
// orchestrator agent running inside a daemon-managed global chat task
// calls this to stream interim replies into the user's chat pane while
// the task is still running. The CLI authenticates with the daemon's PAT
// (so requireUserID resolves to the agent's owner) and passes the agent
// id either in the request body or via the X-Agent-ID header that the
// CLI sets automatically from MULTICA_AGENT_ID.
//
// This is intentionally separate from the workspace-chat completion
// writeback (TaskService.CompleteTask → writeGlobalChatAgentReply) which
// posts the agent's final terminal output exactly once when the task
// completes. PostGlobalAgentReply is for *interactive* replies during
// the task — the CompleteTask writeback still fires at the end with the
// agent's final output.
func (h *Handler) PostGlobalAgentReply(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var req struct {
		Content  string          `json:"content"`
		AgentID  string          `json:"agent_id"`
		Metadata json.RawMessage `json:"metadata,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}

	// Allow X-Agent-ID header as a fallback so the CLI doesn't have to
	// repeat MULTICA_AGENT_ID in every body. Body field wins when both
	// are set so an explicit caller can override the env-derived default.
	if req.AgentID == "" {
		req.AgentID = r.Header.Get("X-Agent-ID")
	}
	if req.AgentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id is required (set in body or X-Agent-ID header)")
		return
	}
	agentUUID := parseUUID(req.AgentID)
	if !agentUUID.Valid {
		writeError(w, http.StatusBadRequest, "invalid agent_id")
		return
	}

	var metadata []byte
	if len(req.Metadata) > 0 {
		metadata = []byte(req.Metadata)
	}

	msg, err := h.GlobalChat.PostAgentReply(r.Context(), parseUUID(userID), agentUUID, req.Content, metadata)
	if err != nil {
		if isNotFound(err) {
			// Collapse "agent doesn't exist" + "not your global agent"
			// into 404 so a probing caller can't enumerate cross-user
			// agent IDs.
			writeError(w, http.StatusNotFound, "agent not found")
			return
		}
		slog.Error("post global agent reply failed",
			"user_id", userID, "agent_id", req.AgentID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to post agent reply")
		return
	}

	writeJSON(w, http.StatusCreated, globalMessageToResponse(msg))
}

// ListGlobalChatAgents is GET /api/global/chat/agents. Returns every
// non-archived global agent owned by the caller, in the same shape the
// FE already consumes for `/api/agents`. No workspace middleware: the
// router mounts this in the user-scoped group, and ListGlobalAgents
// scopes to user_id at the SQL layer so a probing caller can't see
// another user's global agents.
func (h *Handler) ListGlobalChatAgents(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	rows, err := h.GlobalChat.ListGlobalAgents(r.Context(), parseUUID(userID))
	if err != nil {
		slog.Error("list global chat agents failed", "user_id", userID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list global chat agents")
		return
	}
	resp := make([]AgentResponse, 0, len(rows))
	for _, a := range rows {
		resp = append(resp, agentToResponse(a))
	}
	writeJSON(w, http.StatusOK, resp)
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

// ListGlobalMirrors is GET /api/global/chat/mirrors. Returns one row per
// workspace the caller is a member of, sorted by recent mirror activity
// (workspaces with no mirror yet sink to the tail). Backs the global-chat
// tile grid: the frontend uses `mirror_session_id` to subscribe to the
// per-workspace realtime channel and `last_message_at` / `unread_count`
// to render activity hints.
//
// Authorization: membership filter is enforced inside the SQL JOIN — same
// pattern as ListCrossWorkspaceIssues. A caller with zero memberships
// receives an empty list (status 200), never a 403.
func (h *Handler) ListGlobalMirrors(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	rows, err := h.Queries.ListGlobalMirrorsByUser(r.Context(), db.ListGlobalMirrorsByUserParams{
		UserID: parseUUID(userID),
		Limit:  globalMirrorsLimit,
	})
	if err != nil {
		slog.Error("list global mirrors failed", "user_id", userID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list global mirrors")
		return
	}

	resp := make([]GlobalMirrorSummaryResponse, len(rows))
	for i, row := range rows {
		resp[i] = GlobalMirrorSummaryResponse{
			WorkspaceID:     uuidToString(row.WorkspaceID),
			WorkspaceSlug:   row.WorkspaceSlug,
			WorkspaceName:   row.WorkspaceName,
			MirrorSessionID: uuidToPtr(row.MirrorSessionID),
			LastMessageAt:   timestampToPtr(row.LastMessageAt),
			UnreadCount:     row.UnreadCount,
		}
	}
	writeJSON(w, http.StatusOK, resp)
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
