package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// --- in-memory fake for GlobalDispatchQueries -----------------------------

type fakeDispatchQueries struct {
	workspaces      map[string]db.Workspace             // by slug
	memberships     map[string]bool                     // key = userUUID + ":" + workspaceUUID
	mirrorSessions  map[string]db.ChatSession           // key = workspaceUUID + ":" + creatorUUID
	mirrorMessages  []db.ChatMessage
	globalAgents    map[string]db.Agent // key = userUUID
	workspaceAgents map[string][]db.Agent // key = workspaceUUID
	dispatchedAppends []db.AppendGlobalChatDispatchedToParams
}

func newFakeDispatch() *fakeDispatchQueries {
	return &fakeDispatchQueries{
		workspaces:      map[string]db.Workspace{},
		memberships:     map[string]bool{},
		mirrorSessions:  map[string]db.ChatSession{},
		globalAgents:    map[string]db.Agent{},
		workspaceAgents: map[string][]db.Agent{},
	}
}

func (f *fakeDispatchQueries) GetWorkspaceBySlug(ctx context.Context, slug string) (db.Workspace, error) {
	ws, ok := f.workspaces[slug]
	if !ok {
		return db.Workspace{}, pgx.ErrNoRows
	}
	return ws, nil
}

func (f *fakeDispatchQueries) GetMemberByUserAndWorkspace(ctx context.Context, arg db.GetMemberByUserAndWorkspaceParams) (db.Member, error) {
	if f.memberships[membershipKey(arg.UserID, arg.WorkspaceID)] {
		return db.Member{UserID: arg.UserID, WorkspaceID: arg.WorkspaceID, Role: "member"}, nil
	}
	return db.Member{}, pgx.ErrNoRows
}

func (f *fakeDispatchQueries) GetGlobalMirrorSession(ctx context.Context, arg db.GetGlobalMirrorSessionParams) (db.ChatSession, error) {
	sess, ok := f.mirrorSessions[mirrorKey(arg.WorkspaceID, arg.CreatorID)]
	if !ok {
		return db.ChatSession{}, pgx.ErrNoRows
	}
	return sess, nil
}

func (f *fakeDispatchQueries) CreateGlobalMirrorSession(ctx context.Context, arg db.CreateGlobalMirrorSessionParams) (db.ChatSession, error) {
	sess := db.ChatSession{
		ID:          mustNewUUID(),
		WorkspaceID: arg.WorkspaceID,
		AgentID:     arg.AgentID,
		CreatorID:   arg.CreatorID,
		Title:       "Cuong Global",
		Scope:       "global_mirror",
		Status:      "active",
	}
	f.mirrorSessions[mirrorKey(arg.WorkspaceID, arg.CreatorID)] = sess
	return sess, nil
}

func (f *fakeDispatchQueries) GetGlobalAgentByUser(ctx context.Context, userID pgtype.UUID) (db.Agent, error) {
	a, ok := f.globalAgents[uuidValue(userID)]
	if !ok {
		return db.Agent{}, pgx.ErrNoRows
	}
	return a, nil
}

func (f *fakeDispatchQueries) GetAgentInWorkspace(ctx context.Context, arg db.GetAgentInWorkspaceParams) (db.Agent, error) {
	for _, a := range f.workspaceAgents[uuidValue(arg.WorkspaceID)] {
		if a.ID == arg.ID {
			return a, nil
		}
	}
	return db.Agent{}, pgx.ErrNoRows
}

func (f *fakeDispatchQueries) ListAgents(ctx context.Context, workspaceID pgtype.UUID) ([]db.Agent, error) {
	return f.workspaceAgents[uuidValue(workspaceID)], nil
}

func (f *fakeDispatchQueries) InsertMirrorChatMessage(ctx context.Context, arg db.InsertMirrorChatMessageParams) (db.ChatMessage, error) {
	msg := db.ChatMessage{
		ID:            mustNewUUID(),
		ChatSessionID: arg.ChatSessionID,
		Role:          arg.Role,
		Content:       arg.Content,
		Metadata:      arg.Metadata,
	}
	f.mirrorMessages = append(f.mirrorMessages, msg)
	return msg, nil
}

func (f *fakeDispatchQueries) AppendGlobalChatDispatchedTo(ctx context.Context, arg db.AppendGlobalChatDispatchedToParams) error {
	f.dispatchedAppends = append(f.dispatchedAppends, arg)
	return nil
}

// --- helpers --------------------------------------------------------------

func mustNewUUID() pgtype.UUID {
	u, err := uuid.NewRandom()
	if err != nil {
		panic(err)
	}
	var pu pgtype.UUID
	pu.Bytes = u
	pu.Valid = true
	return pu
}

func membershipKey(user, workspace pgtype.UUID) string {
	return uuidValue(user) + ":" + uuidValue(workspace)
}

func mirrorKey(workspace, creator pgtype.UUID) string {
	return uuidValue(workspace) + ":" + uuidValue(creator)
}

func seedWorkspace(f *fakeDispatchQueries, slug string) db.Workspace {
	ws := db.Workspace{ID: mustNewUUID(), Slug: slug, Name: slug}
	f.workspaces[slug] = ws
	return ws
}

func seedMember(f *fakeDispatchQueries, user, workspace pgtype.UUID) {
	f.memberships[membershipKey(user, workspace)] = true
}

func seedGlobalAgent(f *fakeDispatchQueries, userID pgtype.UUID) db.Agent {
	a := db.Agent{ID: mustNewUUID(), UserID: userID, Scope: "global", Name: "Cuong Pho"}
	f.globalAgents[uuidValue(userID)] = a
	return a
}

func seedWorkspaceAgent(f *fakeDispatchQueries, workspaceID pgtype.UUID, name string) db.Agent {
	a := db.Agent{ID: mustNewUUID(), WorkspaceID: workspaceID, Scope: "workspace", Name: name}
	key := uuidValue(workspaceID)
	f.workspaceAgents[key] = append(f.workspaceAgents[key], a)
	return a
}

// --- tests ---------------------------------------------------------------

func TestDispatch_CreatesMirrorSessionOnFirstCall(t *testing.T) {
	ctx := context.Background()
	f := newFakeDispatch()

	user := mustNewUUID()
	ws := seedWorkspace(f, "fuchsia-b2b")
	seedMember(f, user, ws.ID)
	seedGlobalAgent(f, user)
	seedWorkspaceAgent(f, ws.ID, "Tony")

	svc := NewGlobalDispatchService(f)
	res, err := svc.DispatchToWorkspace(ctx, DispatchParams{
		UserID:        user,
		WorkspaceSlug: "fuchsia-b2b",
		Body:          "ping",
	})
	if err != nil {
		t.Fatalf("DispatchToWorkspace: %v", err)
	}

	if !res.MirrorSessionID.Valid || !res.MirrorMessageID.Valid {
		t.Fatalf("expected non-nil IDs, got %#v", res)
	}
	if len(f.mirrorSessions) != 1 {
		t.Fatalf("expected 1 mirror session, got %d", len(f.mirrorSessions))
	}
	if len(f.mirrorMessages) != 1 {
		t.Fatalf("expected 1 mirror message, got %d", len(f.mirrorMessages))
	}
	if f.mirrorMessages[0].Content != "ping" {
		t.Errorf("expected content 'ping', got %q", f.mirrorMessages[0].Content)
	}
}

func TestDispatch_PrefixesTargetAgentMention(t *testing.T) {
	ctx := context.Background()
	f := newFakeDispatch()

	user := mustNewUUID()
	ws := seedWorkspace(f, "fuchsia-b2b")
	seedMember(f, user, ws.ID)
	seedGlobalAgent(f, user)
	seedWorkspaceAgent(f, ws.ID, "Tony")

	svc := NewGlobalDispatchService(f)
	_, err := svc.DispatchToWorkspace(ctx, DispatchParams{
		UserID:          user,
		WorkspaceSlug:   "fuchsia-b2b",
		Body:            "push the carrousel",
		TargetAgentName: "Tony",
	})
	if err != nil {
		t.Fatalf("DispatchToWorkspace: %v", err)
	}
	if got := f.mirrorMessages[0].Content; !strings.HasPrefix(got, "@Tony ") {
		t.Errorf("expected body to start with '@Tony ', got %q", got)
	}
}

func TestDispatch_ReusesExistingMirrorSession(t *testing.T) {
	ctx := context.Background()
	f := newFakeDispatch()

	user := mustNewUUID()
	ws := seedWorkspace(f, "fuchsia-b2b")
	seedMember(f, user, ws.ID)
	seedGlobalAgent(f, user)
	seedWorkspaceAgent(f, ws.ID, "Tony")

	svc := NewGlobalDispatchService(f)
	first, err := svc.DispatchToWorkspace(ctx, DispatchParams{UserID: user, WorkspaceSlug: "fuchsia-b2b", Body: "one"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.DispatchToWorkspace(ctx, DispatchParams{UserID: user, WorkspaceSlug: "fuchsia-b2b", Body: "two"})
	if err != nil {
		t.Fatal(err)
	}
	if first.MirrorSessionID != second.MirrorSessionID {
		t.Errorf("expected mirror session to be reused")
	}
	if len(f.mirrorSessions) != 1 {
		t.Errorf("expected 1 mirror session after two dispatches, got %d", len(f.mirrorSessions))
	}
	if len(f.mirrorMessages) != 2 {
		t.Errorf("expected 2 mirror messages, got %d", len(f.mirrorMessages))
	}
}

func TestDispatch_RejectsNonMember(t *testing.T) {
	ctx := context.Background()
	f := newFakeDispatch()

	owner := mustNewUUID()
	stranger := mustNewUUID()
	ws := seedWorkspace(f, "fuchsia-b2b")
	seedMember(f, owner, ws.ID)
	seedGlobalAgent(f, stranger)
	seedWorkspaceAgent(f, ws.ID, "Tony")

	svc := NewGlobalDispatchService(f)
	_, err := svc.DispatchToWorkspace(ctx, DispatchParams{
		UserID:        stranger,
		WorkspaceSlug: "fuchsia-b2b",
		Body:          "ping",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrNotWorkspaceMember) {
		t.Errorf("expected ErrNotWorkspaceMember, got %v", err)
	}
	if len(f.mirrorSessions) != 0 {
		t.Errorf("expected no mirror session created, got %d", len(f.mirrorSessions))
	}
	if len(f.mirrorMessages) != 0 {
		t.Errorf("expected no mirror message created, got %d", len(f.mirrorMessages))
	}
}

func TestDispatch_UnknownWorkspace(t *testing.T) {
	ctx := context.Background()
	f := newFakeDispatch()

	user := mustNewUUID()
	seedGlobalAgent(f, user)

	svc := NewGlobalDispatchService(f)
	_, err := svc.DispatchToWorkspace(ctx, DispatchParams{
		UserID:        user,
		WorkspaceSlug: "ghost",
		Body:          "ping",
	})
	if !errors.Is(err, ErrWorkspaceNotFound) {
		t.Errorf("expected ErrWorkspaceNotFound, got %v", err)
	}
}

func TestDispatch_GlobalAgentMissing(t *testing.T) {
	ctx := context.Background()
	f := newFakeDispatch()

	user := mustNewUUID()
	ws := seedWorkspace(f, "fuchsia-b2b")
	seedMember(f, user, ws.ID)
	seedWorkspaceAgent(f, ws.ID, "Tony")

	svc := NewGlobalDispatchService(f)
	_, err := svc.DispatchToWorkspace(ctx, DispatchParams{
		UserID:        user,
		WorkspaceSlug: "fuchsia-b2b",
		Body:          "ping",
	})
	if !errors.Is(err, ErrGlobalAgentMissing) {
		t.Errorf("expected ErrGlobalAgentMissing, got %v", err)
	}
}

func TestDispatch_AppendsDispatchedToWhenOriginProvided(t *testing.T) {
	ctx := context.Background()
	f := newFakeDispatch()

	user := mustNewUUID()
	ws := seedWorkspace(f, "fuchsia-b2b")
	seedMember(f, user, ws.ID)
	seedGlobalAgent(f, user)
	seedWorkspaceAgent(f, ws.ID, "Tony")

	originMsg := mustNewUUID()
	originSess := mustNewUUID()
	svc := NewGlobalDispatchService(f)
	res, err := svc.DispatchToWorkspace(ctx, DispatchParams{
		UserID:                user,
		WorkspaceSlug:         "fuchsia-b2b",
		Body:                  "ping",
		OriginGlobalMessageID: originMsg,
		OriginGlobalSessionID: originSess,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.dispatchedAppends) != 1 {
		t.Fatalf("expected 1 dispatched_to append, got %d", len(f.dispatchedAppends))
	}
	if f.dispatchedAppends[0].ID != originMsg {
		t.Errorf("expected append targeted at origin message")
	}
	var got []map[string]string
	if err := json.Unmarshal(f.dispatchedAppends[0].Entry, &got); err != nil {
		t.Fatalf("invalid entry json: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if got[0]["mirror_message_id"] != uuidValue(res.MirrorMessageID) {
		t.Errorf("entry mirror_message_id mismatch")
	}
	if got[0]["mirror_session_id"] != uuidValue(res.MirrorSessionID) {
		t.Errorf("entry mirror_session_id mismatch")
	}
	if got[0]["workspace_id"] != uuidValue(ws.ID) {
		t.Errorf("entry workspace_id mismatch")
	}
}
