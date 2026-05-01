package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// --- in-memory fakes -----------------------------------------------------

type fakeGlobalChat struct {
	users        map[string]db.User
	sessions     map[string]db.GlobalChatSession // key = userUUID
	agents       map[string]db.Agent             // key = userUUID — kept for legacy single-agent semantics
	agentList    []db.Agent                      // multi-agent storage for V3 (twin + Claude Code + ...)
	messages     []db.GlobalChatMessage
	runtimes     map[string]db.AgentRuntime // key = uuidValue(owner_id) + "::" + name
	createParams []db.CreateGlobalAgentParams
}

func newFakeGlobalChat() *fakeGlobalChat {
	return &fakeGlobalChat{
		users:    map[string]db.User{},
		sessions: map[string]db.GlobalChatSession{},
		agents:   map[string]db.Agent{},
		runtimes: map[string]db.AgentRuntime{},
	}
}

func runtimeKey(ownerID pgtype.UUID, name string) string {
	return uuidValue(ownerID) + "::" + name
}

func (f *fakeGlobalChat) GetGlobalChatSessionByUser(ctx context.Context, userID pgtype.UUID) (db.GlobalChatSession, error) {
	if s, ok := f.sessions[uuidValue(userID)]; ok {
		return s, nil
	}
	return db.GlobalChatSession{}, pgx.ErrNoRows
}

func (f *fakeGlobalChat) CreateGlobalChatSession(ctx context.Context, arg db.CreateGlobalChatSessionParams) (db.GlobalChatSession, error) {
	s := db.GlobalChatSession{
		ID:      mustNewUUID(),
		UserID:  arg.UserID,
		AgentID: arg.AgentID,
		Title:   arg.Title,
	}
	f.sessions[uuidValue(arg.UserID)] = s
	return s, nil
}

func (f *fakeGlobalChat) ListGlobalChatMessages(ctx context.Context, arg db.ListGlobalChatMessagesParams) ([]db.GlobalChatMessage, error) {
	out := []db.GlobalChatMessage{}
	for _, m := range f.messages {
		if m.GlobalSessionID == arg.GlobalSessionID {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeGlobalChat) InsertGlobalChatMessage(ctx context.Context, arg db.InsertGlobalChatMessageParams) (db.GlobalChatMessage, error) {
	m := db.GlobalChatMessage{
		ID:              mustNewUUID(),
		GlobalSessionID: arg.GlobalSessionID,
		AuthorKind:      arg.AuthorKind,
		AuthorID:        arg.AuthorID,
		Body:            arg.Body,
		Metadata:        arg.Metadata,
	}
	f.messages = append(f.messages, m)
	return m, nil
}

func (f *fakeGlobalChat) GetGlobalAgentByUser(ctx context.Context, userID pgtype.UUID) (db.Agent, error) {
	// Production query is ORDER BY created_at ASC; the agentList slice
	// preserves insertion order so the first match is the oldest agent —
	// matches the V1 semantic the tests assert against.
	for _, a := range f.agentList {
		if a.UserID == userID && a.Scope == "global" {
			return a, nil
		}
	}
	if a, ok := f.agents[uuidValue(userID)]; ok {
		return a, nil
	}
	return db.Agent{}, pgx.ErrNoRows
}

func (f *fakeGlobalChat) GetGlobalAgentByUserAndName(ctx context.Context, arg db.GetGlobalAgentByUserAndNameParams) (db.Agent, error) {
	for _, a := range f.agentList {
		if a.UserID == arg.UserID && a.Scope == "global" && a.Name == arg.Name {
			return a, nil
		}
	}
	return db.Agent{}, pgx.ErrNoRows
}

func (f *fakeGlobalChat) ListGlobalAgentsByUser(ctx context.Context, userID pgtype.UUID) ([]db.Agent, error) {
	out := []db.Agent{}
	for _, a := range f.agentList {
		if a.UserID == userID && a.Scope == "global" && !a.ArchivedAt.Valid {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeGlobalChat) CreateGlobalAgent(ctx context.Context, arg db.CreateGlobalAgentParams) (db.Agent, error) {
	f.createParams = append(f.createParams, arg)
	a := db.Agent{
		ID:        mustNewUUID(),
		UserID:    arg.UserID,
		Scope:     "global",
		Name:      arg.Name,
		RuntimeID: arg.RuntimeID,
	}
	// First insert per user populates the legacy single-slot map so old
	// tests that only check `agents` still pass; subsequent inserts
	// (V3 — Claude Code agent) only land in agentList.
	if _, exists := f.agents[uuidValue(arg.UserID)]; !exists {
		f.agents[uuidValue(arg.UserID)] = a
	}
	f.agentList = append(f.agentList, a)
	return a, nil
}

func (f *fakeGlobalChat) GetAgentRuntimeByOwnerAndName(ctx context.Context, arg db.GetAgentRuntimeByOwnerAndNameParams) (db.AgentRuntime, error) {
	if rt, ok := f.runtimes[runtimeKey(arg.OwnerID, arg.Name)]; ok {
		return rt, nil
	}
	return db.AgentRuntime{}, pgx.ErrNoRows
}

func (f *fakeGlobalChat) GetUser(ctx context.Context, id pgtype.UUID) (db.User, error) {
	if u, ok := f.users[uuidValue(id)]; ok {
		return u, nil
	}
	return db.User{}, pgx.ErrNoRows
}

// seedClaudeRuntime registers a `Claude (terminator-9999)` runtime owned
// by `user` so EnsureClaudeCodeGlobalAgent can succeed in tests.
func (f *fakeGlobalChat) seedClaudeRuntime(user pgtype.UUID) db.AgentRuntime {
	rt := db.AgentRuntime{
		ID:          mustNewUUID(),
		Name:        ClaudeCodeGlobalRuntimeName,
		OwnerID:     user,
		RuntimeMode: "local",
	}
	f.runtimes[runtimeKey(user, ClaudeCodeGlobalRuntimeName)] = rt
	return rt
}

type capturingBus struct {
	events []events.Event
}

func (c *capturingBus) Publish(e events.Event) {
	c.events = append(c.events, e)
}

// --- tests --------------------------------------------------------------

func TestEnsureSession_BootstrapsAgentAndSession(t *testing.T) {
	ctx := context.Background()
	user := mustNewUUID()
	f := newFakeGlobalChat()
	f.users[uuidValue(user)] = db.User{ID: user, Name: "Cuong", Email: "cuong@fuchsia.biz"}

	bus := &capturingBus{}
	svc := NewGlobalChatService(f, bus)

	sess, err := svc.EnsureSession(ctx, user)
	if err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if !sess.ID.Valid {
		t.Fatal("expected a valid session id")
	}
	if _, ok := f.agents[uuidValue(user)]; !ok {
		t.Fatal("expected global agent created")
	}
	if len(f.createParams) != 1 {
		t.Fatalf("expected 1 CreateGlobalAgent call, got %d", len(f.createParams))
	}
	if !strings.HasPrefix(f.createParams[0].Name, GlobalAgentName) {
		t.Errorf("expected agent name prefixed with %q, got %q", GlobalAgentName, f.createParams[0].Name)
	}
	if f.createParams[0].UserID != user {
		t.Error("expected agent bound to user")
	}
	if f.createParams[0].RuntimeID.Valid {
		t.Error("expected runtime-less twin (RuntimeID.Valid = false); migration 061 keeps agent.runtime_id nullable for this case (MUL-141)")
	}
}

func TestEnsureSession_Idempotent(t *testing.T) {
	ctx := context.Background()
	user := mustNewUUID()
	f := newFakeGlobalChat()
	f.users[uuidValue(user)] = db.User{ID: user, Name: "Cuong"}
	svc := NewGlobalChatService(f, &capturingBus{})

	first, err := svc.EnsureSession(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.EnsureSession(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Errorf("expected idempotent EnsureSession, got distinct ids")
	}
	if len(f.createParams) != 1 {
		t.Errorf("expected exactly one CreateGlobalAgent across two EnsureSession calls, got %d", len(f.createParams))
	}
}

func TestPostUserMessage_PublishesOnPerUserChannel(t *testing.T) {
	ctx := context.Background()
	user := mustNewUUID()
	f := newFakeGlobalChat()
	f.users[uuidValue(user)] = db.User{ID: user, Name: "Cuong"}
	bus := &capturingBus{}
	svc := NewGlobalChatService(f, bus)

	msg, err := svc.PostUserMessage(ctx, user, "ping @fuchsia-b2b")
	if err != nil {
		t.Fatal(err)
	}
	if msg.AuthorKind != "user" {
		t.Errorf("expected author_kind=user, got %q", msg.AuthorKind)
	}
	if msg.Body != "ping @fuchsia-b2b" {
		t.Errorf("body not persisted")
	}
	if len(bus.events) != 1 {
		t.Fatalf("expected 1 event published, got %d", len(bus.events))
	}
	e := bus.events[0]
	if e.Type != protocol.EventGlobalChatMessage {
		t.Errorf("unexpected event type: %s", e.Type)
	}
	if e.UserID != uuidValue(user) {
		t.Errorf("expected event UserID = author user, got %q", e.UserID)
	}
	payload, ok := e.Payload.(protocol.GlobalChatMessagePayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", e.Payload)
	}
	if payload.AuthorKind != "user" || payload.Body != "ping @fuchsia-b2b" {
		t.Errorf("payload mismatch: %+v", payload)
	}
}

func TestParseMentions_ProxiesMentionPackage(t *testing.T) {
	svc := NewGlobalChatService(newFakeGlobalChat(), &capturingBus{})
	got := svc.ParseMentions("@one and @two:Tony")
	if len(got) != 2 {
		t.Fatalf("expected 2 mentions, got %d", len(got))
	}
	if got[0].WorkspaceSlug != "one" || got[1].AgentName != "Tony" {
		t.Errorf("unexpected: %+v", got)
	}
}

// --- V3 (MUL-137) — agent picker bootstrap & validation -------------------

func TestEnsureClaudeCodeGlobalAgent_CreatesWhenMissing(t *testing.T) {
	ctx := context.Background()
	user := mustNewUUID()
	f := newFakeGlobalChat()
	f.users[uuidValue(user)] = db.User{ID: user, Name: "Cuong"}
	rt := f.seedClaudeRuntime(user)

	svc := NewGlobalChatService(f, &capturingBus{})
	agent, err := svc.EnsureClaudeCodeGlobalAgent(ctx, user)
	if err != nil {
		t.Fatalf("EnsureClaudeCodeGlobalAgent: %v", err)
	}
	if agent.Name != ClaudeCodeGlobalAgentName {
		t.Errorf("name = %q, want %q", agent.Name, ClaudeCodeGlobalAgentName)
	}
	if agent.RuntimeID != rt.ID {
		t.Errorf("runtime_id mismatch: got %v, want %v", agent.RuntimeID, rt.ID)
	}
	if len(f.createParams) != 1 {
		t.Fatalf("expected 1 CreateGlobalAgent call, got %d", len(f.createParams))
	}
	got := f.createParams[0]
	if got.Instructions != "" {
		t.Errorf("instructions must be empty; got %q", got.Instructions)
	}
	if string(got.RuntimeConfig) != claudeCodeGlobalAgentRuntimeConfig {
		t.Errorf("runtime_config = %s, want %s", got.RuntimeConfig, claudeCodeGlobalAgentRuntimeConfig)
	}
	if string(got.CustomEnv) != "{}" || string(got.CustomArgs) != "[]" {
		t.Errorf("custom_env/custom_args must be empty defaults; got env=%s args=%s", got.CustomEnv, got.CustomArgs)
	}
	if got.Visibility != "private" {
		t.Errorf("visibility = %q, want private", got.Visibility)
	}
	if got.MaxConcurrentTasks != 1 {
		t.Errorf("max_concurrent_tasks = %d, want 1", got.MaxConcurrentTasks)
	}
}

func TestEnsureClaudeCodeGlobalAgent_Idempotent(t *testing.T) {
	ctx := context.Background()
	user := mustNewUUID()
	f := newFakeGlobalChat()
	f.users[uuidValue(user)] = db.User{ID: user, Name: "Cuong"}
	f.seedClaudeRuntime(user)

	svc := NewGlobalChatService(f, &capturingBus{})
	first, err := svc.EnsureClaudeCodeGlobalAgent(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.EnsureClaudeCodeGlobalAgent(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Errorf("expected idempotent EnsureClaudeCodeGlobalAgent, got distinct IDs")
	}
	if len(f.createParams) != 1 {
		t.Errorf("expected one CreateGlobalAgent across two Ensure calls, got %d", len(f.createParams))
	}
}

func TestEnsureClaudeCodeGlobalAgent_RuntimeMissing(t *testing.T) {
	ctx := context.Background()
	user := mustNewUUID()
	f := newFakeGlobalChat()
	f.users[uuidValue(user)] = db.User{ID: user, Name: "Cuong"}
	// Intentionally NO runtime seeded.

	svc := NewGlobalChatService(f, &capturingBus{})
	_, err := svc.EnsureClaudeCodeGlobalAgent(ctx, user)
	if !errors.Is(err, ErrClaudeCodeRuntimeMissing) {
		t.Fatalf("expected ErrClaudeCodeRuntimeMissing, got %v", err)
	}
	if len(f.createParams) != 0 {
		t.Fatalf("expected no agent created when runtime missing, got %d", len(f.createParams))
	}
}

func TestEnsureSession_BootstrapsBothAgentsWhenRuntimeKnown(t *testing.T) {
	ctx := context.Background()
	user := mustNewUUID()
	f := newFakeGlobalChat()
	f.users[uuidValue(user)] = db.User{ID: user, Name: "Cuong"}
	f.seedClaudeRuntime(user)

	svc := NewGlobalChatService(f, &capturingBus{})
	if _, err := svc.EnsureSession(ctx, user); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	agents, _ := svc.ListGlobalAgents(ctx, user)
	if len(agents) != 2 {
		t.Fatalf("expected 2 global agents (twin + claude code), got %d", len(agents))
	}
	gotNames := map[string]bool{agents[0].Name: true, agents[1].Name: true}
	// Twin name has the user-suffixed shape "Cuong Pho (Cuong)"; just
	// assert both intended agents are present (twin via prefix match,
	// Claude Code by exact name).
	hasTwin := false
	for n := range gotNames {
		if strings.HasPrefix(n, GlobalAgentName) {
			hasTwin = true
		}
	}
	if !hasTwin {
		t.Errorf("twin missing; got names %v", gotNames)
	}
	if !gotNames[ClaudeCodeGlobalAgentName] {
		t.Errorf("claude code agent missing; got names %v", gotNames)
	}
}

func TestEnsureSession_TwinOnlyWhenRuntimeMissing(t *testing.T) {
	ctx := context.Background()
	user := mustNewUUID()
	f := newFakeGlobalChat()
	f.users[uuidValue(user)] = db.User{ID: user, Name: "Cuong"}
	// No runtime: Claude Code bootstrap is best-effort and must not block
	// the twin or the session creation.

	svc := NewGlobalChatService(f, &capturingBus{})
	if _, err := svc.EnsureSession(ctx, user); err != nil {
		t.Fatalf("EnsureSession should succeed when runtime is missing: %v", err)
	}
	agents, _ := svc.ListGlobalAgents(ctx, user)
	if len(agents) != 1 {
		t.Fatalf("expected only the twin when runtime missing, got %d agents", len(agents))
	}
}

func TestGetGlobalAgentForUser_ReturnsOwnedAgent(t *testing.T) {
	ctx := context.Background()
	user := mustNewUUID()
	f := newFakeGlobalChat()
	f.users[uuidValue(user)] = db.User{ID: user, Name: "Cuong"}
	f.seedClaudeRuntime(user)

	svc := NewGlobalChatService(f, &capturingBus{})
	if _, err := svc.EnsureSession(ctx, user); err != nil {
		t.Fatal(err)
	}
	agents, _ := svc.ListGlobalAgents(ctx, user)
	if len(agents) == 0 {
		t.Fatal("seed failed: no agents")
	}
	got, err := svc.GetGlobalAgentForUser(ctx, user, agents[0].ID)
	if err != nil {
		t.Fatalf("GetGlobalAgentForUser: %v", err)
	}
	if got.ID != agents[0].ID {
		t.Errorf("returned wrong agent")
	}
}

func TestGetGlobalAgentForUser_RejectsCrossUserAgent(t *testing.T) {
	ctx := context.Background()
	owner := mustNewUUID()
	stranger := mustNewUUID()
	f := newFakeGlobalChat()
	f.users[uuidValue(owner)] = db.User{ID: owner, Name: "Cuong"}
	f.users[uuidValue(stranger)] = db.User{ID: stranger, Name: "Stranger"}
	f.seedClaudeRuntime(owner)

	svc := NewGlobalChatService(f, &capturingBus{})
	if _, err := svc.EnsureSession(ctx, owner); err != nil {
		t.Fatal(err)
	}
	ownerAgents, _ := svc.ListGlobalAgents(ctx, owner)
	if len(ownerAgents) == 0 {
		t.Fatal("seed failed")
	}
	// Stranger asks about owner's agent -> not found.
	_, err := svc.GetGlobalAgentForUser(ctx, stranger, ownerAgents[0].ID)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows for cross-user lookup, got %v", err)
	}
}

func TestPublishDispatched_EmitsEvent(t *testing.T) {
	user := mustNewUUID()
	msgID := mustNewUUID()
	bus := &capturingBus{}
	svc := NewGlobalChatService(newFakeGlobalChat(), bus)

	svc.PublishDispatched(user, msgID, []protocol.GlobalChatDispatchTarget{
		{WorkspaceSlug: "fuchsia-b2b", MirrorSessionID: "x", MirrorMessageID: "y"},
	})

	if len(bus.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(bus.events))
	}
	if bus.events[0].Type != protocol.EventGlobalChatDispatched {
		t.Errorf("wrong type: %s", bus.events[0].Type)
	}
	if bus.events[0].UserID != uuidValue(user) {
		t.Errorf("wrong UserID: %s", bus.events[0].UserID)
	}
}
