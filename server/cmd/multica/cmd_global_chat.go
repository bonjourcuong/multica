package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

// globalChatCmd is the entrypoint for global-chat-specific commands. The only
// subcommand today is `reply`, used by orchestrator agents running inside a
// daemon-managed global chat task to stream interim replies into the user's
// chat pane (MUL-158). The final terminal output of the task is still
// written automatically by the server when the daemon reports completion;
// `reply` is for *interactive* updates while the task is still running.
var globalChatCmd = &cobra.Command{
	Use:     "global-chat",
	Aliases: []string{"global"},
	Short:   "Work with the user-level global chat",
}

var globalChatReplyCmd = &cobra.Command{
	Use:   "reply",
	Short: "Post an agent reply to the user's global chat session",
	Long: `Post an agent reply to the user's global chat session.

Intended to be invoked by an orchestrator agent running inside a daemon-managed
global chat task. The CLI reads the agent identity from MULTICA_AGENT_ID
(set by the daemon), so an agent typically only needs --content or
--content-stdin. Pass --agent-id to override.

The server resolves the session implicitly from the authenticated user
(via the daemon's PAT), writes the message as authored by the agent, and
broadcasts the per-user realtime event so the chat pane updates
immediately.`,
	RunE: runGlobalChatReply,
}

func init() {
	globalChatCmd.AddCommand(globalChatReplyCmd)

	globalChatReplyCmd.Flags().String("content", "", "Reply content (decodes \\n, \\r, \\t, \\\\; pipe via --content-stdin for multi-line bodies or to preserve literal backslashes)")
	globalChatReplyCmd.Flags().Bool("content-stdin", false, "Read reply content from stdin (preserves multi-line content verbatim)")
	globalChatReplyCmd.Flags().String("agent-id", "", "Override the authoring agent ID (defaults to MULTICA_AGENT_ID)")
	globalChatReplyCmd.Flags().String("output", "json", "Output format: table or json")
}

func runGlobalChatReply(cmd *cobra.Command, _ []string) error {
	content, hasContent, err := resolveTextFlag(cmd, "content")
	if err != nil {
		return err
	}
	if !hasContent {
		return fmt.Errorf("--content or --content-stdin is required")
	}

	agentID, _ := cmd.Flags().GetString("agent-id")
	if agentID == "" {
		agentID = os.Getenv("MULTICA_AGENT_ID")
	}
	if agentID == "" {
		return fmt.Errorf("agent_id is required: pass --agent-id or run inside a daemon task that sets MULTICA_AGENT_ID")
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	body := map[string]any{
		"content":  content,
		"agent_id": agentID,
	}

	var result map[string]any
	if err := client.PostJSON(ctx, "/api/global/chat/sessions/me/messages/agent-reply", body, &result); err != nil {
		return fmt.Errorf("post global chat reply: %w", err)
	}

	fmt.Fprintln(os.Stderr, "Reply posted to global chat.")

	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		return nil
	}
	return cli.PrintJSON(os.Stdout, result)
}
