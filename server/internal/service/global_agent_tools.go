package service

// Canonical tool names for the Cuong Pho global orchestrator. Surfaced as
// constants so the daemon prompt builder and HTTP endpoint docs share a
// single source of truth.
//
// The actual tool execution is the daemon's responsibility: the daemon
// composes the agent prompt and translates each tool call to an HTTP
// request against the corresponding /api/global/chat/* endpoint.
//
// HTTP mapping:
//
//   ToolCrossWorkspaceQuery   →  POST /api/global/chat/cross-ws-query
//   ToolCrossWorkspaceDispatch → POST /api/global/chat/sessions/me/messages
//
// The dispatch tool is implicit: when the agent posts a message containing
// `@workspace[:agent]` mentions, the server's PostGlobalMessage handler
// fans out to each target via GlobalDispatchService. The agent doesn't
// call a separate dispatch endpoint — it sends a message and uses
// mentions to address workspaces.
const (
	ToolCrossWorkspaceQuery    = "cross_ws_query"
	ToolCrossWorkspaceDispatch = "cross_ws_dispatch"
)

// GlobalAgentToolDescriptor describes one tool exposed to the global
// orchestrator agent. The daemon iterates over GlobalAgentTools to build
// the agent's tool catalog at runtime; nothing here ever runs server-side.
type GlobalAgentToolDescriptor struct {
	Name        string
	Description string
	HTTPMethod  string
	HTTPPath    string
}

// GlobalAgentTools is the canonical list of tools the Cuong Pho agent has
// access to. Append-only — adding a new tool here does NOT register it
// with anything; the daemon needs a corresponding implementation on its
// side. This list is the contract.
var GlobalAgentTools = []GlobalAgentToolDescriptor{
	{
		Name:        ToolCrossWorkspaceQuery,
		Description: "List open issues across every workspace the bound user belongs to. Membership-filtered by the SQL JOIN.",
		HTTPMethod:  "POST",
		HTTPPath:    "/api/global/chat/cross-ws-query",
	},
	{
		Name:        ToolCrossWorkspaceDispatch,
		Description: "Dispatch a message into the Cuong Global mirror session of a workspace by appending an `@workspace[:agent]` mention to the global message body. The server fans out automatically.",
		HTTPMethod:  "POST",
		HTTPPath:    "/api/global/chat/sessions/me/messages",
	},
}
