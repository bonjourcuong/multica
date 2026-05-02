package protocol

// Event types for WebSocket communication between server, web clients, and daemon.
const (
	// Issue events
	EventIssueCreated = "issue:created"
	EventIssueUpdated = "issue:updated"
	EventIssueDeleted = "issue:deleted"

	// Comment events
	EventCommentCreated       = "comment:created"
	EventCommentUpdated       = "comment:updated"
	EventCommentDeleted       = "comment:deleted"
	EventReactionAdded          = "reaction:added"
	EventReactionRemoved        = "reaction:removed"
	EventIssueReactionAdded     = "issue_reaction:added"
	EventIssueReactionRemoved   = "issue_reaction:removed"

	// Agent events
	EventAgentStatus   = "agent:status"
	EventAgentCreated  = "agent:created"
	EventAgentArchived = "agent:archived"
	EventAgentRestored = "agent:restored"

	// Task events (server <-> daemon)
	EventTaskDispatch  = "task:dispatch"
	EventTaskProgress  = "task:progress"
	EventTaskCompleted = "task:completed"
	EventTaskFailed    = "task:failed"
	EventTaskMessage   = "task:message"
	EventTaskCancelled = "task:cancelled"

	// Inbox events
	EventInboxNew           = "inbox:new"
	EventInboxRead          = "inbox:read"
	EventInboxArchived      = "inbox:archived"
	EventInboxBatchRead     = "inbox:batch-read"
	EventInboxBatchArchived = "inbox:batch-archived"

	// Workspace events
	EventWorkspaceUpdated = "workspace:updated"
	EventWorkspaceDeleted = "workspace:deleted"

	// Member events
	EventMemberAdded   = "member:added"
	EventMemberUpdated = "member:updated"
	EventMemberRemoved = "member:removed"

	// Subscriber events
	EventSubscriberAdded   = "subscriber:added"
	EventSubscriberRemoved = "subscriber:removed"

	// Activity events
	EventActivityCreated = "activity:created"

	// Skill events
	EventSkillCreated = "skill:created"
	EventSkillUpdated = "skill:updated"
	EventSkillDeleted = "skill:deleted"

	// Chat events
	EventChatMessage     = "chat:message"
	EventChatDone        = "chat:done"
	EventChatSessionRead = "chat:session_read"

	// Project events
	EventProjectCreated = "project:created"
	EventProjectUpdated = "project:updated"
	EventProjectDeleted = "project:deleted"

	// Label events
	EventLabelCreated       = "label:created"
	EventLabelUpdated       = "label:updated"
	EventLabelDeleted       = "label:deleted"
	EventIssueLabelsChanged = "issue_labels:changed"

	// Pin events
	EventPinCreated   = "pin:created"
	EventPinDeleted   = "pin:deleted"
	EventPinReordered = "pin:reordered"

	// Invitation events
	EventInvitationCreated  = "invitation:created"
	EventInvitationAccepted = "invitation:accepted"
	EventInvitationDeclined = "invitation:declined"
	EventInvitationRevoked  = "invitation:revoked"

	// Autopilot events
	EventAutopilotCreated  = "autopilot:created"
	EventAutopilotUpdated  = "autopilot:updated"
	EventAutopilotDeleted  = "autopilot:deleted"
	EventAutopilotRunStart = "autopilot:run_start"
	EventAutopilotRunDone  = "autopilot:run_done"

	// Daemon events
	EventDaemonHeartbeat = "daemon:heartbeat"
	EventDaemonRegister  = "daemon:register"

	// Global chat events (per-user, no workspace_id)
	EventGlobalChatMessage    = "global_chat:message"
	EventGlobalChatDispatched = "global_chat:dispatched"
	EventGlobalChatTaskUpdate = "global_chat:task_update"
)

// GlobalChatMessagePayload is the realtime payload pushed when a new
// global_chat_message lands. The frontend uses it to refresh the global
// chat pane without a roundtrip.
type GlobalChatMessagePayload struct {
	GlobalSessionID string `json:"global_session_id"`
	MessageID       string `json:"message_id"`
	AuthorKind      string `json:"author_kind"`
	AuthorID        string `json:"author_id"`
	Body            string `json:"body"`
	CreatedAt       string `json:"created_at"`
}

// GlobalChatDispatchedPayload is broadcast after a global message has been
// dispatched into one or more workspace mirror sessions. Frontends use it to
// flag the global message as "delivered" or surface the per-target outcome.
type GlobalChatDispatchedPayload struct {
	GlobalMessageID string                  `json:"global_message_id"`
	Targets         []GlobalChatDispatchTarget `json:"targets"`
}

type GlobalChatDispatchTarget struct {
	WorkspaceSlug   string `json:"workspace_slug"`
	WorkspaceID     string `json:"workspace_id"`
	MirrorSessionID string `json:"mirror_session_id"`
	MirrorMessageID string `json:"mirror_message_id"`
	Error           string `json:"error,omitempty"`
}

// GlobalChatTaskUpdatePayload is the realtime payload pushed when a
// global-chat task changes lifecycle state (dispatched / completed /
// failed / cancelled). Mirrors what `task:dispatch` / `task:failed` etc.
// already do for workspace-bound tasks, but routed on the per-user channel
// because global tasks have no `workspace_id` (MUL-192). The frontend uses
// `status` to clear the "agent is thinking…" indicator on terminal states
// (failed / cancelled) without waiting for the next poll refresh.
type GlobalChatTaskUpdatePayload struct {
	GlobalSessionID string `json:"global_session_id"`
	TaskID          string `json:"task_id"`
	AgentID         string `json:"agent_id"`
	Status          string `json:"status"`
	Error           string `json:"error,omitempty"`
}
