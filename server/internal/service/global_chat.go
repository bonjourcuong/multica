package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/mention"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// GlobalAgentName is the conventional display name for the per-user
// digital-twin agent. The lookup is by (scope='global', user_id, name=
// GlobalAgentName) — V3 dropped the "one global agent per user" unique
// index so the twin is now resolved by name, not by being the sole row.
const GlobalAgentName = "Cuong Pho"

// ClaudeCodeGlobalAgentName is the V3 default global agent: a raw Claude
// Code CLI on terminator-9999 with no persona / no project system prompt.
// Lookup is by (scope='global', user_id, name=ClaudeCodeGlobalAgentName);
// EnsureClaudeCodeGlobalAgent provisions it on first EnsureSession call.
const ClaudeCodeGlobalAgentName = "Claude Code (terminator-9999)"

// ClaudeCodeGlobalRuntimeName is the runtime row this agent binds to. The
// runtime itself is provisioned out-of-band when the user starts the
// Claude daemon on terminator-9999; the bootstrap helper just resolves
// the row by (owner_id, name) and refuses to create the agent if the
// runtime is not registered yet.
const ClaudeCodeGlobalRuntimeName = "Claude (terminator-9999)"

const claudeCodeGlobalAgentDescription = "Raw Claude Code CLI on terminator-9999. No Marvel persona, no project system prompt."

// claudeCodeGlobalAgentRuntimeConfig is the work_dir override the daemon
// reads to choose where `claude` runs. Hardcoded to /root so the agent can
// `cd` into any of Cuong's repos / PKM. Not user-tunable in V3 — that's
// what the agent settings page will be for in V4.
const claudeCodeGlobalAgentRuntimeConfig = `{"working_dir":"/root"}`

// claudeCodeGlobalAgentMcpEnvVar is the env var JARVIS sets to override
// the bootstrap MCP config without a code change. Empty / unset = use the
// default below. Value must be valid JSON; invalid JSON falls back to the
// default with a WARN log so a typo can't brick provisioning.
const claudeCodeGlobalAgentMcpEnvVar = "MULTICA_CLAUDE_CODE_GLOBAL_AGENT_MCP_CONFIG"

// defaultClaudeCodeGlobalAgentMcpConfig mirrors the two MCPs Cuong
// already uses on his local Claude Code (honcho self-hosted + jcodemunch
// for code exploration). JARVIS confirms the shape and can override via
// claudeCodeGlobalAgentMcpEnvVar if either entry needs different args.
const defaultClaudeCodeGlobalAgentMcpConfig = `{"mcpServers":{"honcho":{"command":"npx","args":["-y","@honcho-ai/mcp@latest"]},"jcodemunch":{"command":"jcodemunch-mcp","args":["serve"]}}}`

// GlobalChatQueries is the slice of *db.Queries the GlobalChatService needs.
// Exposed as an interface so the same handler tests that mock the rest of
// the DB layer can mock this one too.
type GlobalChatQueries interface {
	GetGlobalChatSessionByUser(ctx context.Context, userID pgtype.UUID) (db.GlobalChatSession, error)
	CreateGlobalChatSession(ctx context.Context, arg db.CreateGlobalChatSessionParams) (db.GlobalChatSession, error)
	ListGlobalChatMessages(ctx context.Context, arg db.ListGlobalChatMessagesParams) ([]db.GlobalChatMessage, error)
	InsertGlobalChatMessage(ctx context.Context, arg db.InsertGlobalChatMessageParams) (db.GlobalChatMessage, error)

	GetGlobalAgentByUser(ctx context.Context, userID pgtype.UUID) (db.Agent, error)
	GetGlobalAgentByUserAndName(ctx context.Context, arg db.GetGlobalAgentByUserAndNameParams) (db.Agent, error)
	ListGlobalAgentsByUser(ctx context.Context, userID pgtype.UUID) ([]db.Agent, error)
	CreateGlobalAgent(ctx context.Context, arg db.CreateGlobalAgentParams) (db.Agent, error)

	GetAgentRuntimeByOwnerAndName(ctx context.Context, arg db.GetAgentRuntimeByOwnerAndNameParams) (db.AgentRuntime, error)

	GetUser(ctx context.Context, id pgtype.UUID) (db.User, error)
}

// ErrClaudeCodeRuntimeMissing is returned by EnsureClaudeCodeGlobalAgent
// when the user has not yet registered a `Claude (terminator-9999)`
// runtime. The bootstrap is intentionally non-creating — we don't want to
// mint a phantom agent that can never run. Callers (notably
// EnsureSession) treat this as soft: they log + skip, then the user sees
// only the legacy twin in the picker until they bring the runtime up.
var ErrClaudeCodeRuntimeMissing = errors.New("claude code global runtime not registered")

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

// EnsureSession looks up the user's global session, creating the legacy
// "Cuong Pho" twin agent and the global_chat_session row if missing.
// Also opportunistically bootstraps the V3 default Claude Code agent so
// the FE picker lists both options on first load — failure to bootstrap
// the Claude Code agent is logged and swallowed (the legacy twin still
// works), per ErrClaudeCodeRuntimeMissing semantics. Idempotent.
//
// agent_id on global_chat_session still points at the twin so existing
// V1 code paths (cross-ws dispatch, mention fan-out) keep working
// untouched. The picker selection is per-message via the agent_id field
// on POST /api/global/chat/sessions/me/messages, not per-session.
func (s *GlobalChatService) EnsureSession(ctx context.Context, userID pgtype.UUID) (db.GlobalChatSession, error) {
	sess, err := s.q.GetGlobalChatSessionByUser(ctx, userID)
	switch {
	case err == nil:
		s.bootstrapClaudeCodeAgentBestEffort(ctx, userID)
		return sess, nil
	case errors.Is(err, pgx.ErrNoRows):
		// fall through to creation below
	default:
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

	s.bootstrapClaudeCodeAgentBestEffort(ctx, userID)
	return created, nil
}

// bootstrapClaudeCodeAgentBestEffort calls EnsureClaudeCodeGlobalAgent
// and swallows ErrClaudeCodeRuntimeMissing (logged at INFO — this is the
// expected state on a fresh install where the user hasn't run the daemon
// on terminator-9999 yet). All other errors log at WARN; the bootstrap
// is best-effort because the EnsureSession contract is "twin works", and
// the picker degrades to twin-only when the second agent isn't available.
func (s *GlobalChatService) bootstrapClaudeCodeAgentBestEffort(ctx context.Context, userID pgtype.UUID) {
	if _, err := s.EnsureClaudeCodeGlobalAgent(ctx, userID); err != nil {
		if errors.Is(err, ErrClaudeCodeRuntimeMissing) {
			slog.Info("claude code global agent bootstrap skipped: runtime not registered",
				"user_id", uuidValue(userID))
			return
		}
		slog.Warn("claude code global agent bootstrap failed",
			"user_id", uuidValue(userID), "error", err)
	}
}

// EnsureClaudeCodeGlobalAgent provisions the V3 default global agent.
// Idempotent on (scope='global', user_id, name=ClaudeCodeGlobalAgentName).
// Returns ErrClaudeCodeRuntimeMissing when the runtime row the agent
// would bind to does not exist for the user — bootstrapping a runtime-
// less agent would mint a row that can never be claimed by any daemon.
func (s *GlobalChatService) EnsureClaudeCodeGlobalAgent(ctx context.Context, userID pgtype.UUID) (db.Agent, error) {
	if existing, err := s.q.GetGlobalAgentByUserAndName(ctx, db.GetGlobalAgentByUserAndNameParams{
		UserID: userID,
		Name:   ClaudeCodeGlobalAgentName,
	}); err == nil {
		return existing, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return db.Agent{}, fmt.Errorf("lookup claude code global agent: %w", err)
	}

	runtime, err := s.q.GetAgentRuntimeByOwnerAndName(ctx, db.GetAgentRuntimeByOwnerAndNameParams{
		OwnerID: userID,
		Name:    ClaudeCodeGlobalRuntimeName,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Agent{}, ErrClaudeCodeRuntimeMissing
		}
		return db.Agent{}, fmt.Errorf("lookup claude code runtime: %w", err)
	}

	agent, err := s.q.CreateGlobalAgent(ctx, db.CreateGlobalAgentParams{
		Name:               ClaudeCodeGlobalAgentName,
		Description:        claudeCodeGlobalAgentDescription,
		AvatarUrl:          pgtype.Text{},
		RuntimeMode:        runtime.RuntimeMode,
		RuntimeConfig:      []byte(claudeCodeGlobalAgentRuntimeConfig),
		RuntimeID:          pgtype.UUID{Bytes: runtime.ID.Bytes, Valid: true},
		Visibility:         "private",
		MaxConcurrentTasks: 1,
		OwnerID:            userID,
		Instructions:       "",
		CustomEnv:          []byte("{}"),
		CustomArgs:         []byte("[]"),
		McpConfig:          []byte(claudeCodeGlobalAgentMcpConfig()),
		Model:              pgtype.Text{},
		UserID:             userID,
	})
	if err != nil {
		return db.Agent{}, fmt.Errorf("create claude code global agent: %w", err)
	}
	return agent, nil
}

// ListGlobalAgents returns every non-archived global agent owned by the
// user. Backs `GET /api/global/chat/agents`. Order matches the underlying
// query: created_at ASC (twin first, Claude Code second, then any future
// adds). The handler reuses agentToResponse so the FE keeps the same
// AgentResponse shape it already consumes for `/api/agents`.
func (s *GlobalChatService) ListGlobalAgents(ctx context.Context, userID pgtype.UUID) ([]db.Agent, error) {
	return s.q.ListGlobalAgentsByUser(ctx, userID)
}

// GetGlobalAgentForUser fetches the agent by ID and asserts it is a
// global agent owned by the caller. Used by the message POST handler to
// validate the optional agent_id before enqueueing a task. Returns
// pgx.ErrNoRows when the row doesn't exist OR when it's a workspace
// agent OR when it's a global agent for a different user — collapsing
// all three into "not found from your point of view" so a probing
// caller can't enumerate agent IDs across users.
func (s *GlobalChatService) GetGlobalAgentForUser(ctx context.Context, userID, agentID pgtype.UUID) (db.Agent, error) {
	agents, err := s.q.ListGlobalAgentsByUser(ctx, userID)
	if err != nil {
		return db.Agent{}, fmt.Errorf("list global agents: %w", err)
	}
	for _, a := range agents {
		if a.ID == agentID {
			return a, nil
		}
	}
	return db.Agent{}, pgx.ErrNoRows
}

// claudeCodeGlobalAgentMcpConfig resolves the bootstrap MCP config: env
// var override if set and valid JSON, otherwise the bundled default.
// Invalid JSON in the env falls back to the default with a WARN log so a
// typo doesn't brick provisioning.
func claudeCodeGlobalAgentMcpConfig() string {
	if v := os.Getenv(claudeCodeGlobalAgentMcpEnvVar); v != "" {
		if json.Valid([]byte(v)) {
			return v
		}
		slog.Warn("invalid mcp_config env override; falling back to default",
			"env_var", claudeCodeGlobalAgentMcpEnvVar)
	}
	return defaultClaudeCodeGlobalAgentMcpConfig
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

// PostAgentReply persists an interim agent message into the user's global
// chat session and publishes the per-user realtime event. Backs the CLI
// `multica global-chat reply` command (MUL-158): orchestrators running
// inside a daemon-managed task call this to stream replies into the chat
// pane, alongside the final `CompleteTask` writeback that lands the
// session's terminal output. agentID must be one of the user's global
// agents — workspace agents and other users' globals are rejected with
// pgx.ErrNoRows so a probing caller cannot enumerate cross-user IDs
// (same collapsing as GetGlobalAgentForUser).
func (s *GlobalChatService) PostAgentReply(
	ctx context.Context,
	userID, agentID pgtype.UUID,
	body string,
	metadata []byte,
) (db.GlobalChatMessage, error) {
	if _, err := s.GetGlobalAgentForUser(ctx, userID, agentID); err != nil {
		return db.GlobalChatMessage{}, err
	}
	return s.PostAgentMessage(ctx, userID, agentID, body, metadata)
}

// ParseMentions extracts every @workspace[:agent] reference from body. Used
// by handlers / agent tool to fan out a global message into workspace
// mirror sessions.
func (s *GlobalChatService) ParseMentions(body string) []mention.WorkspaceMention {
	return mention.ParseWorkspaceMentions(body)
}

func (s *GlobalChatService) ensureGlobalAgent(ctx context.Context, userID pgtype.UUID) (db.Agent, error) {
	// V1 wrote the twin under either "Cuong Pho" or "Cuong Pho (Name)".
	// We look up the bare name first (V3 default), then the legacy
	// composite shape as a fallback so a pre-V3 row keeps being reused
	// instead of being shadowed by a duplicate. After V3 the bare name
	// is canonical; the composite form only exists on legacy rows.
	if existing, err := s.q.GetGlobalAgentByUserAndName(ctx, db.GetGlobalAgentByUserAndNameParams{
		UserID: userID,
		Name:   GlobalAgentName,
	}); err == nil {
		return existing, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return db.Agent{}, fmt.Errorf("lookup global agent: %w", err)
	}
	if existing, err := s.q.GetGlobalAgentByUser(ctx, userID); err == nil {
		// Oldest global agent for this user — in legacy rows this is
		// the only one, and it's the twin (Claude Code is always newer
		// because EnsureSession provisions it last).
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
