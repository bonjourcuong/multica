package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/mention"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// GlobalAgentName is the conventional display name for the per-user
// orchestrator agent. The lookup is by (scope='global', user_id), so the
// name is cosmetic; users can rename it from settings without breaking the
// service.
const GlobalAgentName = "Cuong Pho"

// GlobalChatQueries is the slice of *db.Queries the GlobalChatService needs.
// Exposed as an interface so the same handler tests that mock the rest of
// the DB layer can mock this one too.
type GlobalChatQueries interface {
	GetGlobalChatSessionByUser(ctx context.Context, userID pgtype.UUID) (db.GlobalChatSession, error)
	CreateGlobalChatSession(ctx context.Context, arg db.CreateGlobalChatSessionParams) (db.GlobalChatSession, error)
	ListGlobalChatMessages(ctx context.Context, arg db.ListGlobalChatMessagesParams) ([]db.GlobalChatMessage, error)
	InsertGlobalChatMessage(ctx context.Context, arg db.InsertGlobalChatMessageParams) (db.GlobalChatMessage, error)

	GetGlobalAgentByUser(ctx context.Context, userID pgtype.UUID) (db.Agent, error)
	CreateGlobalAgent(ctx context.Context, arg db.CreateGlobalAgentParams) (db.Agent, error)

	GetUser(ctx context.Context, id pgtype.UUID) (db.User, error)
}

// GlobalChatBus is the subset of *events.Bus that GlobalChatService uses.
// Defined as an interface so tests can capture published events without a
// real bus.
type GlobalChatBus interface {
	Publish(events.Event)
}

// GlobalChatService is the thin facade over GlobalChatQueries that owns:
//
//  1. Bootstrapping the user's "Cuong Pho" global agent + session on first
//     use (idempotent EnsureSession).
//  2. Listing and persisting global-side messages.
//  3. Publishing realtime events on the per-user channel after each post.
//
// Cross-workspace dispatch is delegated to GlobalDispatchService; the
// GlobalChat handler composes the two.
type GlobalChatService struct {
	q   GlobalChatQueries
	bus GlobalChatBus
}

// NewGlobalChatService wires the facade.
func NewGlobalChatService(q GlobalChatQueries, bus GlobalChatBus) *GlobalChatService {
	return &GlobalChatService{q: q, bus: bus}
}

// EnsureSession looks up the user's global session, creating both the
// "Cuong Pho" agent row and the global_chat_session row if either is
// missing. Idempotent.
func (s *GlobalChatService) EnsureSession(ctx context.Context, userID pgtype.UUID) (db.GlobalChatSession, error) {
	if existing, err := s.q.GetGlobalChatSessionByUser(ctx, userID); err == nil {
		return existing, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return db.GlobalChatSession{}, fmt.Errorf("lookup global session: %w", err)
	}

	agent, err := s.ensureGlobalAgent(ctx, userID)
	if err != nil {
		return db.GlobalChatSession{}, err
	}

	created, err := s.q.CreateGlobalChatSession(ctx, db.CreateGlobalChatSessionParams{
		UserID:  userID,
		AgentID: agent.ID,
		Title:   GlobalAgentName,
	})
	if err != nil {
		return db.GlobalChatSession{}, fmt.Errorf("create global session: %w", err)
	}
	return created, nil
}

// GetSession returns the user's global session or pgx.ErrNoRows if it has
// not been bootstrapped. Callers that always want a session should call
// EnsureSession instead.
func (s *GlobalChatService) GetSession(ctx context.Context, userID pgtype.UUID) (db.GlobalChatSession, error) {
	return s.q.GetGlobalChatSessionByUser(ctx, userID)
}

// ListMessages returns the most recent messages of the user's global session
// in reverse-chronological order. cursor is the created_at of the last seen
// row (or zero/invalid for the first page). limit is clamped to [1, 200].
func (s *GlobalChatService) ListMessages(
	ctx context.Context,
	userID pgtype.UUID,
	cursor pgtype.Timestamptz,
	limit int32,
) ([]db.GlobalChatMessage, error) {
	sess, err := s.q.GetGlobalChatSessionByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.q.ListGlobalChatMessages(ctx, db.ListGlobalChatMessagesParams{
		GlobalSessionID: sess.ID,
		Limit:           clampLimit(limit),
		CursorCreated:   cursor,
	})
}

// PostUserMessage appends a user-authored message to the global session and
// publishes a realtime event on the per-user channel. Bootstraps the
// session if missing.
func (s *GlobalChatService) PostUserMessage(
	ctx context.Context,
	userID pgtype.UUID,
	body string,
) (db.GlobalChatMessage, error) {
	sess, err := s.EnsureSession(ctx, userID)
	if err != nil {
		return db.GlobalChatMessage{}, err
	}
	msg, err := s.q.InsertGlobalChatMessage(ctx, db.InsertGlobalChatMessageParams{
		GlobalSessionID: sess.ID,
		AuthorKind:      "user",
		AuthorID:        userID,
		Body:            body,
		Metadata:        []byte("{}"),
	})
	if err != nil {
		return db.GlobalChatMessage{}, fmt.Errorf("insert global message: %w", err)
	}
	s.publishMessage(userID, msg)
	return msg, nil
}

// PostAgentMessage appends a message authored by the user's Cuong Pho
// agent. Used by the runtime / agent tool callbacks (not the user-driven
// HTTP path).
func (s *GlobalChatService) PostAgentMessage(
	ctx context.Context,
	userID, agentID pgtype.UUID,
	body string,
	metadata []byte,
) (db.GlobalChatMessage, error) {
	sess, err := s.EnsureSession(ctx, userID)
	if err != nil {
		return db.GlobalChatMessage{}, err
	}
	if metadata == nil {
		metadata = []byte("{}")
	}
	msg, err := s.q.InsertGlobalChatMessage(ctx, db.InsertGlobalChatMessageParams{
		GlobalSessionID: sess.ID,
		AuthorKind:      "agent",
		AuthorID:        agentID,
		Body:            body,
		Metadata:        metadata,
	})
	if err != nil {
		return db.GlobalChatMessage{}, fmt.Errorf("insert global agent message: %w", err)
	}
	s.publishMessage(userID, msg)
	return msg, nil
}

// ParseMentions extracts every @workspace[:agent] reference from body. Used
// by handlers / agent tool to fan out a global message into workspace
// mirror sessions.
func (s *GlobalChatService) ParseMentions(body string) []mention.WorkspaceMention {
	return mention.ParseWorkspaceMentions(body)
}

func (s *GlobalChatService) ensureGlobalAgent(ctx context.Context, userID pgtype.UUID) (db.Agent, error) {
	if existing, err := s.q.GetGlobalAgentByUser(ctx, userID); err == nil {
		return existing, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return db.Agent{}, fmt.Errorf("lookup global agent: %w", err)
	}

	user, err := s.q.GetUser(ctx, userID)
	if err != nil {
		return db.Agent{}, fmt.Errorf("lookup user: %w", err)
	}
	displayName := GlobalAgentName
	if user.Name != "" {
		displayName = fmt.Sprintf("%s (%s)", GlobalAgentName, user.Name)
	}

	agent, err := s.q.CreateGlobalAgent(ctx, db.CreateGlobalAgentParams{
		Name:               displayName,
		Description:        "Global orchestrator (digital twin).",
		AvatarUrl:          pgtype.Text{},
		RuntimeMode:        "cloud",
		RuntimeConfig:      []byte("{}"),
		RuntimeID:          pgtype.UUID{},
		Visibility:         "private",
		MaxConcurrentTasks: 1,
		OwnerID:            userID,
		Instructions:       defaultGlobalAgentInstructions,
		CustomEnv:          []byte("{}"),
		CustomArgs:         []byte("[]"),
		McpConfig:          []byte("{}"),
		Model:              pgtype.Text{},
		UserID:             userID,
	})
	if err != nil {
		return db.Agent{}, fmt.Errorf("create global agent: %w", err)
	}
	return agent, nil
}

func (s *GlobalChatService) publishMessage(userID pgtype.UUID, msg db.GlobalChatMessage) {
	if s.bus == nil {
		return
	}
	s.bus.Publish(events.Event{
		Type:      protocol.EventGlobalChatMessage,
		UserID:    uuidValue(userID),
		ActorType: msg.AuthorKind,
		ActorID:   uuidValue(msg.AuthorID),
		Payload: protocol.GlobalChatMessagePayload{
			GlobalSessionID: uuidValue(msg.GlobalSessionID),
			MessageID:       uuidValue(msg.ID),
			AuthorKind:      msg.AuthorKind,
			AuthorID:        uuidValue(msg.AuthorID),
			Body:            msg.Body,
			CreatedAt:       timestamptzString(msg.CreatedAt),
		},
	})
}

// PublishDispatched broadcasts a global_chat:dispatched event after a
// message has been fanned out to one or more workspace mirror sessions.
// Helper exposed to the handler so the dispatch result is surfaced live in
// the global pane.
func (s *GlobalChatService) PublishDispatched(
	userID, globalMessageID pgtype.UUID,
	targets []protocol.GlobalChatDispatchTarget,
) {
	if s.bus == nil {
		return
	}
	s.bus.Publish(events.Event{
		Type:    protocol.EventGlobalChatDispatched,
		UserID:  uuidValue(userID),
		ActorID: uuidValue(userID),
		Payload: protocol.GlobalChatDispatchedPayload{
			GlobalMessageID: uuidValue(globalMessageID),
			Targets:         targets,
		},
	})
}

// defaultGlobalAgentInstructions is the bootstrap system prompt for a
// freshly-created Cuong Pho. Operators / users can edit it from the agent
// settings page after creation.
const defaultGlobalAgentInstructions = `You are the user's global orchestrator (digital twin).

Tools:
  - cross_ws_query: read across every workspace the user belongs to.
  - cross_ws_dispatch: post a message into the "Cuong Global" mirror
    session of a workspace the user belongs to.

Rules:
  - Never dispatch into a workspace the user is not a member of.
  - Quote @workspace[:agent] explicitly when you fan out work.
  - Do not invent workspace slugs; only use slugs the user mentioned or
    that you observed via cross_ws_query.
`
