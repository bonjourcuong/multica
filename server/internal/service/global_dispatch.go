package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// GlobalDispatchQueries is the slice of *db.Queries the dispatch service
// depends on. Defined as an interface so tests can swap in a mock without a
// real Postgres.
type GlobalDispatchQueries interface {
	GetWorkspaceBySlug(ctx context.Context, slug string) (db.Workspace, error)
	GetMemberByUserAndWorkspace(ctx context.Context, arg db.GetMemberByUserAndWorkspaceParams) (db.Member, error)
	GetGlobalMirrorSession(ctx context.Context, arg db.GetGlobalMirrorSessionParams) (db.ChatSession, error)
	CreateGlobalMirrorSession(ctx context.Context, arg db.CreateGlobalMirrorSessionParams) (db.ChatSession, error)
	GetGlobalAgentByUser(ctx context.Context, userID pgtype.UUID) (db.Agent, error)
	GetAgentInWorkspace(ctx context.Context, arg db.GetAgentInWorkspaceParams) (db.Agent, error)
	ListAgents(ctx context.Context, workspaceID pgtype.UUID) ([]db.Agent, error)
	InsertMirrorChatMessage(ctx context.Context, arg db.InsertMirrorChatMessageParams) (db.ChatMessage, error)
	AppendGlobalChatDispatchedTo(ctx context.Context, arg db.AppendGlobalChatDispatchedToParams) error
}

// DispatchParams carries the inputs of one cross-workspace dispatch.
type DispatchParams struct {
	UserID          pgtype.UUID
	WorkspaceSlug   string
	Body            string
	TargetAgentName string // optional, e.g. "Tony"

	// OriginGlobalMessageID, when set, links the resulting mirror message
	// back to the originating global_chat_message via metadata.global_origin
	// and appends a row to that message's dispatched_to audit trail.
	OriginGlobalMessageID pgtype.UUID
	OriginGlobalSessionID pgtype.UUID
}

// DispatchResult is what DispatchToWorkspace returns to its caller.
type DispatchResult struct {
	WorkspaceID     pgtype.UUID
	MirrorSessionID pgtype.UUID
	MirrorMessageID pgtype.UUID
}

// Sentinel errors so callers can map to the right HTTP status.
var (
	ErrWorkspaceNotFound = errors.New("workspace not found")
	ErrNotWorkspaceMember = errors.New("user is not a member of this workspace")
	ErrGlobalAgentMissing = errors.New("global agent not provisioned for user")
)

// GlobalDispatchService relays a global-chat user/agent message into a
// per-workspace "Cuong Global" mirror chat session, creating the mirror
// session on first use.
//
// All membership/permissions are enforced here, never bypassed: the caller
// supplies the acting user's ID and the service verifies they are a member of
// the target workspace before any mirror writes happen.
type GlobalDispatchService struct {
	q GlobalDispatchQueries
}

// NewGlobalDispatchService wires the service to a query implementation. In
// production callers pass *db.Queries; tests pass an in-memory fake.
func NewGlobalDispatchService(q GlobalDispatchQueries) *GlobalDispatchService {
	return &GlobalDispatchService{q: q}
}

// DispatchToWorkspace ensures the per-user mirror session for (user,
// workspace) exists and appends one chat message authored by the user's
// global agent. Membership is checked before any write happens.
func (s *GlobalDispatchService) DispatchToWorkspace(ctx context.Context, p DispatchParams) (DispatchResult, error) {
	zero := DispatchResult{}

	ws, err := s.q.GetWorkspaceBySlug(ctx, p.WorkspaceSlug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, fmt.Errorf("%w: %s", ErrWorkspaceNotFound, p.WorkspaceSlug)
		}
		return zero, fmt.Errorf("lookup workspace %q: %w", p.WorkspaceSlug, err)
	}

	if _, err := s.q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID:      p.UserID,
		WorkspaceID: ws.ID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, fmt.Errorf("%w: %s", ErrNotWorkspaceMember, p.WorkspaceSlug)
		}
		return zero, fmt.Errorf("lookup membership: %w", err)
	}

	globalAgent, err := s.q.GetGlobalAgentByUser(ctx, p.UserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, ErrGlobalAgentMissing
		}
		return zero, fmt.Errorf("lookup global agent: %w", err)
	}

	mirrorAgentID, err := s.resolveMirrorAgentID(ctx, ws.ID, p.TargetAgentName)
	if err != nil {
		return zero, err
	}

	session, err := s.ensureMirrorSession(ctx, ws.ID, p.UserID, mirrorAgentID)
	if err != nil {
		return zero, err
	}

	body := p.Body
	if p.TargetAgentName != "" {
		body = fmt.Sprintf("@%s %s", p.TargetAgentName, p.Body)
	}

	metadata, err := json.Marshal(map[string]any{
		"global_origin": map[string]any{
			"global_chat_message_id": uuidValue(p.OriginGlobalMessageID),
			"global_session_id":      uuidValue(p.OriginGlobalSessionID),
			"user_id":                uuidValue(p.UserID),
			"global_agent_id":        uuidValue(globalAgent.ID),
		},
	})
	if err != nil {
		return zero, fmt.Errorf("encode mirror metadata: %w", err)
	}

	msg, err := s.q.InsertMirrorChatMessage(ctx, db.InsertMirrorChatMessageParams{
		ChatSessionID: session.ID,
		Role:          "assistant", // global-side dispatch lands as an "assistant" turn in the workspace mirror
		Content:       body,
		Metadata:      metadata,
	})
	if err != nil {
		return zero, fmt.Errorf("insert mirror message: %w", err)
	}

	if p.OriginGlobalMessageID.Valid {
		entry, err := json.Marshal(map[string]any{
			"workspace_id":      uuidValue(ws.ID),
			"mirror_session_id": uuidValue(session.ID),
			"mirror_message_id": uuidValue(msg.ID),
		})
		if err != nil {
			return zero, fmt.Errorf("encode dispatched_to entry: %w", err)
		}
		// We embed the entry inside a JSON array so jsonb || jsonb appends a
		// single element, not the keys of an object.
		arr, _ := json.Marshal([]json.RawMessage{entry})
		if err := s.q.AppendGlobalChatDispatchedTo(ctx, db.AppendGlobalChatDispatchedToParams{
			ID:    p.OriginGlobalMessageID,
			Entry: arr,
		}); err != nil {
			return zero, fmt.Errorf("append dispatched_to: %w", err)
		}
	}

	return DispatchResult{
		WorkspaceID:     ws.ID,
		MirrorSessionID: session.ID,
		MirrorMessageID: msg.ID,
	}, nil
}

func (s *GlobalDispatchService) ensureMirrorSession(ctx context.Context, workspaceID, userID, agentID pgtype.UUID) (db.ChatSession, error) {
	sess, err := s.q.GetGlobalMirrorSession(ctx, db.GetGlobalMirrorSessionParams{
		WorkspaceID: workspaceID,
		CreatorID:   userID,
	})
	if err == nil {
		return sess, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.ChatSession{}, fmt.Errorf("lookup mirror session: %w", err)
	}

	created, err := s.q.CreateGlobalMirrorSession(ctx, db.CreateGlobalMirrorSessionParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
		CreatorID:   userID,
	})
	if err != nil {
		return db.ChatSession{}, fmt.Errorf("create mirror session: %w", err)
	}
	return created, nil
}

// resolveMirrorAgentID picks the workspace-side agent that "owns" the mirror
// session row. Identity here is cosmetic — the resident agents listening on
// the session pick up messages by chat scope, not by agent_id. We pick the
// caller's targeted agent when present, otherwise any active workspace agent.
// Returns an error only if the workspace has zero agents.
func (s *GlobalDispatchService) resolveMirrorAgentID(ctx context.Context, workspaceID pgtype.UUID, targetAgentName string) (pgtype.UUID, error) {
	agents, err := s.q.ListAgents(ctx, workspaceID)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("list workspace agents: %w", err)
	}
	if len(agents) == 0 {
		return pgtype.UUID{}, fmt.Errorf("workspace has no active agents to host the mirror session")
	}
	if targetAgentName != "" {
		for _, a := range agents {
			if a.Name == targetAgentName {
				return a.ID, nil
			}
		}
	}
	return agents[0].ID, nil
}

// uuidValue returns the canonical string of a UUID, or "" when invalid. Used
// inside JSON metadata so consumers do not have to deal with pgtype.UUID
// wrappers.
func uuidValue(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
