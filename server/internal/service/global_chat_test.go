package service

import (
	"context"
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
	users       map[string]db.User
	sessions    map[string]db.GlobalChatSession // key = userUUID
	agents      map[string]db.Agent             // key = userUUID
	messages    []db.GlobalChatMessage
	createParams []db.CreateGlobalAgentParams
}

func newFakeGlobalChat() *fakeGlobalChat {
	return &fakeGlobalChat{
		users:    map[string]db.User{},
		sessions: map[string]db.GlobalChatSession{},
		agents:   map[string]db.Agent{},
	}
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
	if a, ok := f.agents[uuidValue(userID)]; ok {
		return a, nil
	}
	return db.Agent{}, pgx.ErrNoRows
}

func (f *fakeGlobalChat) CreateGlobalAgent(ctx context.Context, arg db.CreateGlobalAgentParams) (db.Agent, error) {
	f.createParams = append(f.createParams, arg)
	a := db.Agent{
		ID:     mustNewUUID(),
		UserID: arg.UserID,
		Scope:  "global",
		Name:   arg.Name,
	}
	f.agents[uuidValue(arg.UserID)] = a
	return a, nil
}

func (f *fakeGlobalChat) GetUser(ctx context.Context, id pgtype.UUID) (db.User, error) {
	if u, ok := f.users[uuidValue(id)]; ok {
		return u, nil
	}
	return db.User{}, pgx.ErrNoRows
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
