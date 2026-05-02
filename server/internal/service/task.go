package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/mention"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/redact"
)

type TaskService struct {
	Queries   *db.Queries
	TxStarter TxStarter
	Hub       *realtime.Hub
	Bus       *events.Bus
}

func NewTaskService(q *db.Queries, tx TxStarter, hub *realtime.Hub, bus *events.Bus) *TaskService {
	return &TaskService{Queries: q, TxStarter: tx, Hub: hub, Bus: bus}
}

// EnqueueTaskForIssue creates a queued task for an agent-assigned issue.
// No context snapshot is stored — the agent fetches all data it needs at
// runtime via the multica CLI.
func (s *TaskService) EnqueueTaskForIssue(ctx context.Context, issue db.Issue, triggerCommentID ...pgtype.UUID) (db.AgentTaskQueue, error) {
	if !issue.AssigneeID.Valid {
		slog.Error("task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "error", "issue has no assignee")
		return db.AgentTaskQueue{}, fmt.Errorf("issue has no assignee")
	}

	agent, err := s.Queries.GetAgent(ctx, issue.AssigneeID)
	if err != nil {
		slog.Error("task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "error", err)
		return db.AgentTaskQueue{}, fmt.Errorf("load agent: %w", err)
	}
	if agent.ArchivedAt.Valid {
		slog.Debug("task enqueue skipped: agent is archived", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agent.ID))
		return db.AgentTaskQueue{}, fmt.Errorf("agent is archived")
	}
	if !agent.RuntimeID.Valid {
		slog.Error("task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "error", "agent has no runtime")
		return db.AgentTaskQueue{}, fmt.Errorf("agent has no runtime")
	}

	var commentID pgtype.UUID
	if len(triggerCommentID) > 0 {
		commentID = triggerCommentID[0]
	}

	task, err := s.Queries.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID:          issue.AssigneeID,
		RuntimeID:        agent.RuntimeID,
		IssueID:          issue.ID,
		Priority:         priorityToInt(issue.Priority),
		TriggerCommentID: commentID,
	})
	if err != nil {
		slog.Error("task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "error", err)
		return db.AgentTaskQueue{}, fmt.Errorf("create task: %w", err)
	}

	slog.Info("task enqueued", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(issue.AssigneeID))
	return task, nil
}

// EnqueueTaskForMention creates a queued task for a mentioned agent on an issue.
// Unlike EnqueueTaskForIssue, this takes an explicit agent ID rather than
// deriving it from the issue assignee.
func (s *TaskService) EnqueueTaskForMention(ctx context.Context, issue db.Issue, agentID pgtype.UUID, triggerCommentID pgtype.UUID) (db.AgentTaskQueue, error) {
	agent, err := s.Queries.GetAgent(ctx, agentID)
	if err != nil {
		slog.Error("mention task enqueue failed: agent not found", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID), "error", err)
		return db.AgentTaskQueue{}, fmt.Errorf("load agent: %w", err)
	}
	if agent.ArchivedAt.Valid {
		slog.Debug("mention task enqueue skipped: agent is archived", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID))
		return db.AgentTaskQueue{}, fmt.Errorf("agent is archived")
	}
	if !agent.RuntimeID.Valid {
		slog.Error("mention task enqueue failed: agent has no runtime", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID))
		return db.AgentTaskQueue{}, fmt.Errorf("agent has no runtime")
	}

	task, err := s.Queries.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID:          agentID,
		RuntimeID:        agent.RuntimeID,
		IssueID:          issue.ID,
		Priority:         priorityToInt(issue.Priority),
		TriggerCommentID: triggerCommentID,
	})
	if err != nil {
		slog.Error("mention task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID), "error", err)
		return db.AgentTaskQueue{}, fmt.Errorf("create task: %w", err)
	}

	slog.Info("mention task enqueued", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID))
	return task, nil
}

// EnqueueChatTask creates a queued task for a chat session.
// Unlike issue tasks, chat tasks have no issue_id.
func (s *TaskService) EnqueueChatTask(ctx context.Context, chatSession db.ChatSession) (db.AgentTaskQueue, error) {
	agent, err := s.Queries.GetAgent(ctx, chatSession.AgentID)
	if err != nil {
		slog.Error("chat task enqueue failed", "chat_session_id", util.UUIDToString(chatSession.ID), "error", err)
		return db.AgentTaskQueue{}, fmt.Errorf("load agent: %w", err)
	}
	if agent.ArchivedAt.Valid {
		return db.AgentTaskQueue{}, fmt.Errorf("agent is archived")
	}
	if !agent.RuntimeID.Valid {
		return db.AgentTaskQueue{}, fmt.Errorf("agent has no runtime")
	}

	task, err := s.Queries.CreateChatTask(ctx, db.CreateChatTaskParams{
		AgentID:       chatSession.AgentID,
		RuntimeID:     agent.RuntimeID,
		Priority:      2, // medium priority for chat
		ChatSessionID: chatSession.ID,
	})
	if err != nil {
		slog.Error("chat task enqueue failed", "chat_session_id", util.UUIDToString(chatSession.ID), "error", err)
		return db.AgentTaskQueue{}, fmt.Errorf("create chat task: %w", err)
	}

	slog.Info("chat task enqueued", "task_id", util.UUIDToString(task.ID), "chat_session_id", util.UUIDToString(chatSession.ID), "agent_id", util.UUIDToString(chatSession.AgentID))
	return task, nil
}

// EnqueueGlobalChatTask creates a queued task for a global chat session.
// Mirrors EnqueueChatTask but binds the task to global_session_id instead
// of chat_session_id, and takes the agent ID explicitly because the
// global session itself is twin-pinned (V1 design) and the V3 picker
// per-message overrides which agent answers a given message.
//
// Caller is responsible for verifying the agent is one of the user's
// global agents (use GlobalChatService.GetGlobalAgentForUser); this
// function only checks the runtime/archive invariants the daemon needs
// to actually run the task.
func (s *TaskService) EnqueueGlobalChatTask(ctx context.Context, sess db.GlobalChatSession, agentID pgtype.UUID) (db.AgentTaskQueue, error) {
	agent, err := s.Queries.GetAgent(ctx, agentID)
	if err != nil {
		slog.Error("global chat task enqueue failed", "global_session_id", util.UUIDToString(sess.ID), "error", err)
		return db.AgentTaskQueue{}, fmt.Errorf("load agent: %w", err)
	}
	if agent.ArchivedAt.Valid {
		return db.AgentTaskQueue{}, fmt.Errorf("agent is archived")
	}
	if !agent.RuntimeID.Valid {
		return db.AgentTaskQueue{}, fmt.Errorf("agent has no runtime")
	}

	task, err := s.Queries.CreateGlobalChatTask(ctx, db.CreateGlobalChatTaskParams{
		AgentID:         agentID,
		RuntimeID:       agent.RuntimeID,
		Priority:        2, // medium priority for chat
		GlobalSessionID: pgtype.UUID{Bytes: sess.ID.Bytes, Valid: true},
	})
	if err != nil {
		slog.Error("global chat task enqueue failed", "global_session_id", util.UUIDToString(sess.ID), "error", err)
		return db.AgentTaskQueue{}, fmt.Errorf("create global chat task: %w", err)
	}

	slog.Info("global chat task enqueued",
		"task_id", util.UUIDToString(task.ID),
		"global_session_id", util.UUIDToString(sess.ID),
		"agent_id", util.UUIDToString(agentID),
	)
	return task, nil
}

// CancelTasksForIssue cancels every active task on the issue, reconciles each
// affected agent's status, and broadcasts task:cancelled events so frontends
// clear their live cards.
//
// Before #1587 this path was "cancel rows and return" — issue-status flips
// (e.g. user marks the issue `done` or `cancelled` while a task is still
// running) left the agent stuck at status="working" indefinitely, requiring a
// manual `multica agent update <id> --status idle` to unwedge. Matches the
// pattern already used by CancelTask and RerunIssue.
func (s *TaskService) CancelTasksForIssue(ctx context.Context, issueID pgtype.UUID) error {
	cancelled, err := s.Queries.CancelAgentTasksByIssue(ctx, issueID)
	if err != nil {
		return err
	}
	for _, t := range cancelled {
		s.ReconcileAgentStatus(ctx, t.AgentID)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, t)
	}
	return nil
}

// CancelTasksByTriggerComment cancels active tasks whose trigger is the given
// comment. Called from DeleteComment so an agent does not run with the
// now-deleted content already embedded in its prompt. Must be invoked BEFORE
// the comment row is deleted because the FK ON DELETE SET NULL would
// otherwise nullify trigger_comment_id and we'd lose the ability to find
// the affected tasks.
func (s *TaskService) CancelTasksByTriggerComment(ctx context.Context, commentID pgtype.UUID) error {
	cancelled, err := s.Queries.CancelAgentTasksByTriggerComment(ctx, commentID)
	if err != nil {
		return err
	}
	for _, t := range cancelled {
		s.ReconcileAgentStatus(ctx, t.AgentID)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, t)
	}
	return nil
}

// CancelTask cancels a single task by ID. It broadcasts a task:cancelled event
// so frontends can update immediately.
func (s *TaskService) CancelTask(ctx context.Context, taskID pgtype.UUID) (*db.AgentTaskQueue, error) {
	task, err := s.Queries.CancelAgentTask(ctx, taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, err := s.Queries.GetAgentTask(ctx, taskID)
		if err != nil {
			return nil, fmt.Errorf("cancel task: %w", err)
		}
		return &existing, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cancel task: %w", err)
	}

	slog.Info("task cancelled", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(task.IssueID))

	// Reconcile agent status
	s.ReconcileAgentStatus(ctx, task.AgentID)

	// Broadcast cancellation as a task:failed event so frontends clear the live card
	s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, task)

	return &task, nil
}

// ClaimTask atomically claims the next queued task for an agent,
// respecting max_concurrent_tasks.
func (s *TaskService) ClaimTask(ctx context.Context, agentID pgtype.UUID) (*db.AgentTaskQueue, error) {
	start := time.Now()
	var (
		outcome                                                              = "unknown"
		getAgentMs, countRunningMs, claimAgentMs, updateStatusMs, dispatchMs int64
	)
	defer func() {
		s.maybeLogClaimSlow(agentID, outcome, start, getAgentMs, countRunningMs, claimAgentMs, updateStatusMs, dispatchMs)
	}()

	t0 := start
	agent, err := s.Queries.GetAgent(ctx, agentID)
	getAgentMs = time.Since(t0).Milliseconds()
	if err != nil {
		outcome = "error_get_agent"
		return nil, fmt.Errorf("agent not found: %w", err)
	}

	t0 = time.Now()
	running, err := s.Queries.CountRunningTasks(ctx, agentID)
	countRunningMs = time.Since(t0).Milliseconds()
	if err != nil {
		outcome = "error_count_running"
		return nil, fmt.Errorf("count running tasks: %w", err)
	}
	if running >= int64(agent.MaxConcurrentTasks) {
		slog.Debug("task claim: no capacity", "agent_id", util.UUIDToString(agentID), "running", running, "max", agent.MaxConcurrentTasks)
		outcome = "no_capacity"
		return nil, nil // No capacity
	}

	t0 = time.Now()
	task, err := s.Queries.ClaimAgentTask(ctx, agentID)
	claimAgentMs = time.Since(t0).Milliseconds()
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			slog.Debug("task claim: no tasks available", "agent_id", util.UUIDToString(agentID))
			outcome = "no_tasks"
			return nil, nil // No tasks available
		}
		outcome = "error_claim"
		return nil, fmt.Errorf("claim task: %w", err)
	}

	slog.Info("task claimed", "task_id", util.UUIDToString(task.ID), "agent_id", util.UUIDToString(agentID))

	// Refresh agent status from active tasks. This avoids a stale unconditional
	// working write racing after a just-cancelled claim.
	t0 = time.Now()
	s.ReconcileAgentStatus(ctx, agentID)
	updateStatusMs = time.Since(t0).Milliseconds()

	// Broadcast task:dispatch. ResolveTaskWorkspaceID inside this path can
	// re-query issue/chat_session/autopilot_run, so it can also be a real
	// contributor to claim latency.
	t0 = time.Now()
	s.broadcastTaskDispatch(ctx, task)
	dispatchMs = time.Since(t0).Milliseconds()

	outcome = "claimed"
	return &task, nil
}

// ClaimTaskForRuntime claims the next runnable task for a runtime while
// still respecting each agent's max_concurrent_tasks limit.
func (s *TaskService) ClaimTaskForRuntime(ctx context.Context, runtimeID pgtype.UUID) (*db.AgentTaskQueue, error) {
	start := time.Now()
	var (
		outcome          = "no_task"
		listMs, loopMs   int64
		listCount, tried int
		claimedFlag      bool
	)
	defer func() {
		totalMs := time.Since(start).Milliseconds()
		if totalMs < 300 {
			return
		}
		slog.Info("claim_for_runtime slow",
			"runtime_id", util.UUIDToString(runtimeID),
			"outcome", outcome,
			"total_ms", totalMs,
			"list_pending_ms", listMs,
			"list_pending_count", listCount,
			"agents_tried", tried,
			"claim_loop_ms", loopMs,
			"claimed", claimedFlag,
		)
	}()

	t0 := start
	tasks, err := s.Queries.ListPendingTasksByRuntime(ctx, runtimeID)
	listMs = time.Since(t0).Milliseconds()
	listCount = len(tasks)
	if err != nil {
		outcome = "error_list"
		return nil, fmt.Errorf("list pending tasks: %w", err)
	}

	loopStart := time.Now()
	triedAgents := map[string]struct{}{}
	var claimed *db.AgentTaskQueue
	for _, candidate := range tasks {
		agentKey := util.UUIDToString(candidate.AgentID)
		if _, seen := triedAgents[agentKey]; seen {
			continue
		}
		triedAgents[agentKey] = struct{}{}
		tried++

		task, err := s.ClaimTask(ctx, candidate.AgentID)
		if err != nil {
			loopMs = time.Since(loopStart).Milliseconds()
			outcome = "error_claim"
			return nil, err
		}
		if task != nil && task.RuntimeID == runtimeID {
			claimed = task
			break
		}
	}
	loopMs = time.Since(loopStart).Milliseconds()
	if claimed != nil {
		claimedFlag = true
		outcome = "claimed"
	}

	return claimed, nil
}

// maybeLogClaimSlow emits one structured log per ClaimTask call when its total
// latency exceeds 300ms, so the prod tail can be diagnosed without flooding
// logs at normal poll rates. Called via defer so it captures the full path
// including post-claim updateAgentStatus / broadcastTaskDispatch (both of
// which can hit the DB) and any error exit.
func (s *TaskService) maybeLogClaimSlow(agentID pgtype.UUID, outcome string, start time.Time, getAgentMs, countRunningMs, claimAgentMs, updateStatusMs, dispatchMs int64) {
	totalMs := time.Since(start).Milliseconds()
	if totalMs < 300 {
		return
	}
	slog.Info("claim_task slow",
		"agent_id", util.UUIDToString(agentID),
		"outcome", outcome,
		"total_ms", totalMs,
		"get_agent_ms", getAgentMs,
		"count_running_ms", countRunningMs,
		"claim_agent_ms", claimAgentMs,
		"update_status_ms", updateStatusMs,
		"dispatch_ms", dispatchMs,
	)
}

// StartTask transitions a dispatched task to running.
// Issue status is NOT changed here — the agent manages it via the CLI.
func (s *TaskService) StartTask(ctx context.Context, taskID pgtype.UUID) (*db.AgentTaskQueue, error) {
	task, err := s.Queries.StartAgentTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("start task: %w", err)
	}

	slog.Info("task started", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(task.IssueID))
	return &task, nil
}

// CompleteTask marks a task as completed.
// Issue status is NOT changed here — the agent manages it via the CLI.
//
// For chat tasks, CompleteAgentTask and the chat_session resume-pointer
// update run in a single transaction. This closes a race where the next
// queued chat message could be claimed in the window between the task
// flipping to 'completed' and chat_session.session_id being refreshed,
// causing the new task to resume against a stale (or NULL) session.
func (s *TaskService) CompleteTask(ctx context.Context, taskID pgtype.UUID, result []byte, sessionID, workDir string) (*db.AgentTaskQueue, error) {
	var task db.AgentTaskQueue
	if err := s.runInTx(ctx, func(qtx *db.Queries) error {
		t, err := qtx.CompleteAgentTask(ctx, db.CompleteAgentTaskParams{
			ID:        taskID,
			Result:    result,
			SessionID: pgtype.Text{String: sessionID, Valid: sessionID != ""},
			WorkDir:   pgtype.Text{String: workDir, Valid: workDir != ""},
		})
		if err != nil {
			return err
		}
		task = t

		if t.ChatSessionID.Valid {
			// COALESCE in SQL guarantees empty inputs don't wipe the
			// existing resume pointer; we still surface DB errors.
			if err := qtx.UpdateChatSessionSession(ctx, db.UpdateChatSessionSessionParams{
				ID:        t.ChatSessionID,
				SessionID: pgtype.Text{String: sessionID, Valid: sessionID != ""},
				WorkDir:   pgtype.Text{String: workDir, Valid: workDir != ""},
			}); err != nil {
				return fmt.Errorf("update chat session resume pointer: %w", err)
			}
		}
		return nil
	}); err != nil {
		// When parallel agents race, a task may already be completed,
		// cancelled, or failed by the time this call runs. The UPDATE
		// … WHERE status = 'running' returns no rows in that case.
		// Treat it as an idempotent success — same pattern as CancelTask.
		if existing, lookupErr := s.Queries.GetAgentTask(ctx, taskID); lookupErr == nil {
			if errors.Is(err, pgx.ErrNoRows) {
				slog.Info("complete task: already finalized",
					"task_id", util.UUIDToString(taskID),
					"current_status", existing.Status,
					"agent_id", util.UUIDToString(existing.AgentID),
				)
				return &existing, nil
			}
			slog.Warn("complete task failed",
				"task_id", util.UUIDToString(taskID),
				"current_status", existing.Status,
				"issue_id", util.UUIDToString(existing.IssueID),
				"chat_session_id", util.UUIDToString(existing.ChatSessionID),
				"agent_id", util.UUIDToString(existing.AgentID),
				"error", err,
			)
		} else {
			slog.Warn("complete task failed: task not found",
				"task_id", util.UUIDToString(taskID),
				"lookup_error", lookupErr,
			)
		}
		return nil, fmt.Errorf("complete task: %w", err)
	}

	slog.Info("task completed", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(task.IssueID))

	// Invariant: every completed issue task must have at least one agent
	// comment on the issue, so the user always sees something when a run
	// ends. If the agent posted a comment during execution (result, progress
	// ping, or CLI reply), HasAgentCommentedSince returns true and we skip.
	// Otherwise, synthesize one from the final output. For comment-triggered
	// tasks, TriggerCommentID threads the fallback under the original comment;
	// for assignment-triggered tasks it is NULL and the fallback is top-level.
	// Chat tasks have no IssueID and are handled separately below.
	if task.IssueID.Valid {
		agentCommented, _ := s.Queries.HasAgentCommentedSince(ctx, db.HasAgentCommentedSinceParams{
			IssueID:  task.IssueID,
			AuthorID: task.AgentID,
			Since:    task.StartedAt,
		})
		if !agentCommented {
			var payload protocol.TaskCompletedPayload
			if err := json.Unmarshal(result, &payload); err == nil {
				if payload.Output != "" {
					s.createAgentComment(ctx, task.IssueID, task.AgentID, redact.Text(payload.Output), "comment", task.TriggerCommentID)
				}
			}
		}
	}

	// For chat tasks, save assistant reply and broadcast chat:done. The
	// resume pointer was already persisted inside the transaction above.
	if task.ChatSessionID.Valid {
		var payload protocol.TaskCompletedPayload
		if err := json.Unmarshal(result, &payload); err == nil && payload.Output != "" {
			if _, err := s.Queries.CreateChatMessage(ctx, db.CreateChatMessageParams{
				ChatSessionID: task.ChatSessionID,
				Role:          "assistant",
				Content:       redact.Text(payload.Output),
				TaskID:        task.ID,
			}); err != nil {
				slog.Error("failed to save assistant chat message", "task_id", util.UUIDToString(task.ID), "error", err)
			} else {
				// Event-driven unread: stamp unread_since on the first unread
				// assistant message. No-op if the session already has unread.
				// If the user is actively viewing the session, the frontend's
				// auto-mark-read effect will clear this within a tick.
				if err := s.Queries.SetUnreadSinceIfNull(ctx, task.ChatSessionID); err != nil {
					slog.Warn("failed to set unread_since", "chat_session_id", util.UUIDToString(task.ChatSessionID), "error", err)
				}
			}
		}
		s.broadcastChatDone(ctx, task)
	}

	// Global chat tasks (V3 picker): same writeback shape as workspace
	// chat tasks but the message lands in `global_chat_message` and the
	// realtime event flies on the per-user channel instead of a workspace
	// channel. Looking up the session here (one extra read, not in the
	// hot path because completion is the terminal call per task) is
	// cheaper than threading user_id through the daemon protocol.
	if task.GlobalSessionID.Valid {
		s.writeGlobalChatAgentReply(ctx, task, result)
	}

	// Reconcile agent status
	s.ReconcileAgentStatus(ctx, task.AgentID)

	// Broadcast
	s.broadcastTaskEvent(ctx, protocol.EventTaskCompleted, task)

	return &task, nil
}

// writeGlobalChatAgentReply persists the agent's reply to the global
// chat thread and publishes the per-user realtime event so the global
// pane refreshes. Called from CompleteTask when task.GlobalSessionID is
// set; quietly logs and returns on transient errors so a failed event
// publish never blocks the overall task completion path.
func (s *TaskService) writeGlobalChatAgentReply(ctx context.Context, task db.AgentTaskQueue, result []byte) {
	var payload protocol.TaskCompletedPayload
	if err := json.Unmarshal(result, &payload); err != nil || payload.Output == "" {
		return
	}
	sess, err := s.Queries.GetGlobalChatSession(ctx, task.GlobalSessionID)
	if err != nil {
		slog.Error("global chat reply: lookup session failed",
			"task_id", util.UUIDToString(task.ID),
			"global_session_id", util.UUIDToString(task.GlobalSessionID),
			"error", err,
		)
		return
	}
	body := redact.Text(payload.Output)
	msg, err := s.Queries.InsertGlobalChatMessage(ctx, db.InsertGlobalChatMessageParams{
		GlobalSessionID: task.GlobalSessionID,
		AuthorKind:      "agent",
		AuthorID:        task.AgentID,
		Body:            body,
		Metadata:        []byte("{}"),
	})
	if err != nil {
		slog.Error("global chat reply: insert message failed",
			"task_id", util.UUIDToString(task.ID),
			"global_session_id", util.UUIDToString(task.GlobalSessionID),
			"error", err,
		)
		return
	}
	s.Bus.Publish(events.Event{
		Type:      protocol.EventGlobalChatMessage,
		UserID:    util.UUIDToString(sess.UserID),
		ActorType: msg.AuthorKind,
		ActorID:   util.UUIDToString(msg.AuthorID),
		Payload: protocol.GlobalChatMessagePayload{
			GlobalSessionID: util.UUIDToString(msg.GlobalSessionID),
			MessageID:       util.UUIDToString(msg.ID),
			AuthorKind:      msg.AuthorKind,
			AuthorID:        util.UUIDToString(msg.AuthorID),
			Body:            msg.Body,
			CreatedAt:       util.TimestampToString(msg.CreatedAt),
		},
	})
}

// FailTask marks a task as failed.
// Issue status is NOT changed here — the agent manages it via the CLI.
//
// sessionID/workDir are optional: when the agent established a real session
// before failing (e.g. crashed mid-conversation, was cancelled, or hit a
// tool error), the daemon should pass them so we can preserve the resume
// pointer on both the task row and the chat_session — otherwise the next
// chat turn would silently start a brand-new session and lose memory.
//
// failureReason is a coarse classifier consumed by the auto-retry path.
// Pass "" when unknown (treated as 'agent_error').
func (s *TaskService) FailTask(ctx context.Context, taskID pgtype.UUID, errMsg, sessionID, workDir, failureReason string) (*db.AgentTaskQueue, error) {
	var task db.AgentTaskQueue
	if err := s.runInTx(ctx, func(qtx *db.Queries) error {
		t, err := qtx.FailAgentTask(ctx, db.FailAgentTaskParams{
			ID:            taskID,
			Error:         pgtype.Text{String: errMsg, Valid: true},
			FailureReason: pgtype.Text{String: failureReason, Valid: failureReason != ""},
			SessionID:     pgtype.Text{String: sessionID, Valid: sessionID != ""},
			WorkDir:       pgtype.Text{String: workDir, Valid: workDir != ""},
		})
		if err != nil {
			return err
		}
		task = t

		if t.ChatSessionID.Valid {
			if err := qtx.UpdateChatSessionSession(ctx, db.UpdateChatSessionSessionParams{
				ID:        t.ChatSessionID,
				SessionID: pgtype.Text{String: sessionID, Valid: sessionID != ""},
				WorkDir:   pgtype.Text{String: workDir, Valid: workDir != ""},
			}); err != nil {
				return fmt.Errorf("update chat session resume pointer: %w", err)
			}
		}
		return nil
	}); err != nil {
		if existing, lookupErr := s.Queries.GetAgentTask(ctx, taskID); lookupErr == nil {
			if errors.Is(err, pgx.ErrNoRows) {
				slog.Info("fail task: already finalized",
					"task_id", util.UUIDToString(taskID),
					"current_status", existing.Status,
					"agent_id", util.UUIDToString(existing.AgentID),
				)
				return &existing, nil
			}
			slog.Warn("fail task failed",
				"task_id", util.UUIDToString(taskID),
				"current_status", existing.Status,
				"issue_id", util.UUIDToString(existing.IssueID),
				"chat_session_id", util.UUIDToString(existing.ChatSessionID),
				"agent_id", util.UUIDToString(existing.AgentID),
				"error", err,
			)
		} else {
			slog.Warn("fail task failed: task not found",
				"task_id", util.UUIDToString(taskID),
				"lookup_error", lookupErr,
			)
		}
		return nil, fmt.Errorf("fail task: %w", err)
	}

	slog.Warn("task failed", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(task.IssueID), "error", errMsg, "failure_reason", failureReason)

	// Auto-retry eligible failures (orphan, timeout, runtime_offline,
	// runtime_recovery). The helper itself enforces attempt < max_attempts
	// and only triggers for issue/chat tasks.
	retried, _ := s.MaybeRetryFailedTask(ctx, task)

	// Skip the per-failure system comment when we'll immediately retry —
	// the new task will surface its own status to the user, and we don't
	// want to spam the issue with "task timed out" messages on every
	// daemon hiccup.
	if errMsg != "" && task.IssueID.Valid && retried == nil {
		s.createAgentComment(ctx, task.IssueID, task.AgentID, redact.Text(errMsg), "system", task.TriggerCommentID)
	}
	// Reconcile agent status
	s.ReconcileAgentStatus(ctx, task.AgentID)

	// Broadcast
	s.broadcastTaskEvent(ctx, protocol.EventTaskFailed, task)

	return &task, nil
}

// retryableReasons enumerates failure reasons that the auto-retry path is
// allowed to act on. Agent-side errors (compile failures, model rejections,
// etc.) are intentionally excluded — those are real problems that the user
// should see, not infrastructure flakiness.
var retryableReasons = map[string]bool{
	"runtime_offline":  true,
	"runtime_recovery": true,
	"timeout":          true,
}

// MaybeRetryFailedTask spawns a fresh queued attempt for a recently-failed
// task when the failure was infrastructure-shaped (daemon crash, runtime
// went offline, dispatch/run timeout) and the task hasn't exhausted its
// max_attempts budget. The child task inherits agent/runtime/issue/chat
// links and the parent's session_id/work_dir so the agent can resume the
// conversation when the backend supports it. Returns the new task, or nil
// when no retry was created.
//
// Autopilot tasks are NOT auto-retried here; the autopilot scheduler owns
// its own re-run cadence and we don't want to double-fire it.
func (s *TaskService) MaybeRetryFailedTask(ctx context.Context, parent db.AgentTaskQueue) (*db.AgentTaskQueue, error) {
	if parent.Status != "failed" {
		return nil, nil
	}
	reason := ""
	if parent.FailureReason.Valid {
		reason = parent.FailureReason.String
	}
	if !retryableReasons[reason] {
		return nil, nil
	}
	if parent.Attempt >= parent.MaxAttempts {
		slog.Info("task auto-retry skipped: budget exhausted",
			"task_id", util.UUIDToString(parent.ID),
			"attempt", parent.Attempt,
			"max_attempts", parent.MaxAttempts,
		)
		return nil, nil
	}
	if parent.AutopilotRunID.Valid {
		// Autopilot has its own retry semantics; do not double-trigger.
		return nil, nil
	}
	if !parent.IssueID.Valid && !parent.ChatSessionID.Valid {
		return nil, nil
	}

	child, err := s.Queries.CreateRetryTask(ctx, parent.ID)
	if err != nil {
		slog.Warn("task auto-retry failed",
			"parent_task_id", util.UUIDToString(parent.ID),
			"reason", reason,
			"error", err,
		)
		return nil, err
	}
	slog.Info("task auto-retry enqueued",
		"parent_task_id", util.UUIDToString(parent.ID),
		"child_task_id", util.UUIDToString(child.ID),
		"reason", reason,
		"attempt", child.Attempt,
		"max_attempts", child.MaxAttempts,
	)
	s.broadcastTaskEvent(ctx, protocol.EventTaskDispatch, child)
	return &child, nil
}

// RerunIssue creates a fresh queued task for the agent currently assigned
// to the issue. Used by the manual rerun endpoint. Carries the most recent
// session_id/work_dir on the issue (across any status) so the new run
// resumes from where the prior one left off when the backend supports it.
//
// Only tasks belonging to the issue's current assignee are cancelled.
// Tasks owned by other agents on the same issue (e.g. a parallel
// @-mention agent) are left alone — rerun must not collateral-cancel
// them.
func (s *TaskService) RerunIssue(ctx context.Context, issueID pgtype.UUID, triggerCommentID pgtype.UUID) (*db.AgentTaskQueue, error) {
	issue, err := s.Queries.GetIssue(ctx, issueID)
	if err != nil {
		return nil, fmt.Errorf("load issue: %w", err)
	}
	if !issue.AssigneeID.Valid || issue.AssigneeType.String != "agent" {
		return nil, fmt.Errorf("issue is not assigned to an agent")
	}
	// Cancel only the assignee's active/queued tasks on this issue. This
	// covers both the unique-index conflict (queued/dispatched) and a
	// stuck running task without touching other agents on the issue.
	cancelled, err := s.Queries.CancelAgentTasksByIssueAndAgent(ctx, db.CancelAgentTasksByIssueAndAgentParams{
		IssueID: issueID,
		AgentID: issue.AssigneeID,
	})
	if err != nil {
		slog.Warn("rerun: cancel prior tasks failed",
			"issue_id", util.UUIDToString(issueID),
			"agent_id", util.UUIDToString(issue.AssigneeID),
			"error", err,
		)
	}
	for _, t := range cancelled {
		s.ReconcileAgentStatus(ctx, t.AgentID)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, t)
	}

	task, err := s.EnqueueTaskForIssue(ctx, issue, triggerCommentID)
	if err != nil {
		return nil, err
	}
	slog.Info("issue rerun enqueued",
		"task_id", util.UUIDToString(task.ID),
		"issue_id", util.UUIDToString(issueID),
		"agent_id", util.UUIDToString(issue.AssigneeID),
		"cancelled_prior", len(cancelled),
	)
	return &task, nil
}

// HandleFailedTasks runs the post-failure side effects for a batch of
// freshly-failed tasks: optional auto-retry, task:failed event broadcast,
// agent status reconciliation, and (when an issue has no remaining active
// task and isn't being retried) resetting the issue back to todo so the
// daemon can pick it up again.
//
// All callers that surface a task as failed — sweepers, FailTask,
// recover-orphans — funnel through here so the same UI-consistency
// guarantees apply on every code path.
func (s *TaskService) HandleFailedTasks(ctx context.Context, tasks []db.AgentTaskQueue) int {
	if len(tasks) == 0 {
		return 0
	}

	affectedAgents := make(map[string]pgtype.UUID)
	processedIssues := make(map[string]bool)
	retriedIssues := make(map[string]bool)
	retried := 0

	for _, t := range tasks {
		// Auto-retry first so the issue stays in_progress rather than
		// flapping todo → in_progress within a tick.
		if child, _ := s.MaybeRetryFailedTask(ctx, t); child != nil {
			retried++
			if t.IssueID.Valid {
				retriedIssues[util.UUIDToString(t.IssueID)] = true
			}
		}

		failureReason := "agent_error"
		if t.FailureReason.Valid && t.FailureReason.String != "" {
			failureReason = t.FailureReason.String
		}

		// Global-chat tasks have no workspace_id by design; publish on the
		// originating user's channel instead so the per-user "agent is
		// thinking…" indicator clears on failure (MUL-192). Skip the
		// workspace publish below — there's no workspace to fan out to.
		if t.GlobalSessionID.Valid {
			s.broadcastGlobalTaskEvent(ctx, t, "failed")
			affectedAgents[util.UUIDToString(t.AgentID)] = t.AgentID
			continue
		}

		workspaceID := ""
		if t.IssueID.Valid {
			if issue, err := s.Queries.GetIssue(ctx, t.IssueID); err == nil {
				workspaceID = util.UUIDToString(issue.WorkspaceID)
				// Reset stuck in_progress issues only when no other active
				// task exists for the issue and no retry was just enqueued.
				issueKey := util.UUIDToString(t.IssueID)
				if issue.Status == "in_progress" && !processedIssues[issueKey] && !retriedIssues[issueKey] {
					processedIssues[issueKey] = true
					hasActive, checkErr := s.Queries.HasActiveTaskForIssue(ctx, t.IssueID)
					if checkErr != nil {
						slog.Warn("handle failed tasks: active check failed",
							"issue_id", issueKey,
							"error", checkErr,
						)
					} else if !hasActive {
						if _, updateErr := s.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
							ID:     t.IssueID,
							Status: "todo",
						}); updateErr != nil {
							slog.Warn("handle failed tasks: reset stuck issue failed",
								"issue_id", issueKey,
								"error", updateErr,
							)
						}
					}
				}
			}
		}
		if workspaceID == "" {
			// Workspace lookup failed inline (non-issue task or missing
			// issue row). Fall back to the resolver for chat/autopilot.
			// Global tasks return ("", TaskKindGlobal, nil) — they have
			// no workspace channel by design and are handled by MUL-192's
			// per-user broadcast helper. Unknown is logged and skipped
			// (orphaned row or future entity-type pathway not yet wired).
			ws, kind, err := s.ResolveTaskWorkspaceID(ctx, t)
			if err != nil {
				slog.Warn("handle failed tasks: resolve workspace failed",
					"task_id", util.UUIDToString(t.ID),
					"kind", string(kind),
					"error", err,
				)
			} else if kind == TaskKindUnknown {
				slog.Warn("handle failed tasks: task has no recognized entity-type FK",
					"task_id", util.UUIDToString(t.ID),
				)
			}
			workspaceID = ws
		}

		if workspaceID != "" {
			s.Bus.Publish(events.Event{
				Type:        protocol.EventTaskFailed,
				WorkspaceID: workspaceID,
				ActorType:   "system",
				Payload: map[string]any{
					"task_id":        util.UUIDToString(t.ID),
					"agent_id":       util.UUIDToString(t.AgentID),
					"issue_id":       util.UUIDToString(t.IssueID),
					"status":         "failed",
					"failure_reason": failureReason,
				},
			})
		}

		affectedAgents[util.UUIDToString(t.AgentID)] = t.AgentID
	}

	for _, agentID := range affectedAgents {
		s.ReconcileAgentStatus(ctx, agentID)
	}
	return retried
}

// runInTx executes fn inside a single DB transaction. If TxStarter is nil
// (e.g. some tests construct TaskService directly), fn runs against the
// regular Queries handle without transactional guarantees.
func (s *TaskService) runInTx(ctx context.Context, fn func(*db.Queries) error) error {
	if s.TxStarter == nil {
		return fn(s.Queries)
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := fn(s.Queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ReportProgress broadcasts a progress update via the event bus.
func (s *TaskService) ReportProgress(ctx context.Context, taskID string, workspaceID string, summary string, step, total int) {
	s.Bus.Publish(events.Event{
		Type:        protocol.EventTaskProgress,
		WorkspaceID: workspaceID,
		ActorType:   "system",
		ActorID:     "",
		TaskID:      taskID,
		Payload: protocol.TaskProgressPayload{
			TaskID:  taskID,
			Summary: summary,
			Step:    step,
			Total:   total,
		},
	})
}

// ReconcileAgentStatus refreshes agent status from the current active task set.
func (s *TaskService) ReconcileAgentStatus(ctx context.Context, agentID pgtype.UUID) {
	agent, err := s.Queries.RefreshAgentStatusFromTasks(ctx, agentID)
	if err != nil {
		return
	}
	slog.Debug("agent status reconciled", "agent_id", util.UUIDToString(agentID), "status", agent.Status)
	s.publishAgentStatus(agent)
}

func (s *TaskService) updateAgentStatus(ctx context.Context, agentID pgtype.UUID, status string) {
	agent, err := s.Queries.UpdateAgentStatus(ctx, db.UpdateAgentStatusParams{
		ID:     agentID,
		Status: status,
	})
	if err != nil {
		return
	}
	s.publishAgentStatus(agent)
}

func (s *TaskService) publishAgentStatus(agent db.Agent) {
	s.Bus.Publish(events.Event{
		Type:        protocol.EventAgentStatus,
		WorkspaceID: util.UUIDToString(agent.WorkspaceID),
		ActorType:   "system",
		ActorID:     "",
		Payload:     map[string]any{"agent": agentToMap(agent)},
	})
}

// LoadAgentSkills loads an agent's skills with their files for task execution.
func (s *TaskService) LoadAgentSkills(ctx context.Context, agentID pgtype.UUID) []AgentSkillData {
	skills, err := s.Queries.ListAgentSkills(ctx, agentID)
	if err != nil || len(skills) == 0 {
		return nil
	}

	result := make([]AgentSkillData, 0, len(skills))
	for _, sk := range skills {
		data := AgentSkillData{Name: sk.Name, Content: sk.Content}
		files, _ := s.Queries.ListSkillFiles(ctx, sk.ID)
		for _, f := range files {
			data.Files = append(data.Files, AgentSkillFileData{Path: f.Path, Content: f.Content})
		}
		result = append(result, data)
	}
	return result
}

// AgentSkillData represents a skill for task execution responses.
type AgentSkillData struct {
	Name    string               `json:"name"`
	Content string               `json:"content"`
	Files   []AgentSkillFileData `json:"files,omitempty"`
}

// AgentSkillFileData represents a supporting file within a skill.
type AgentSkillFileData struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func priorityToInt(p string) int32 {
	switch p {
	case "urgent":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func (s *TaskService) broadcastTaskDispatch(ctx context.Context, task db.AgentTaskQueue) {
	var payload map[string]any
	if task.Context != nil {
		json.Unmarshal(task.Context, &payload)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["task_id"] = util.UUIDToString(task.ID)
	payload["runtime_id"] = util.UUIDToString(task.RuntimeID)
	payload["issue_id"] = util.UUIDToString(task.IssueID)
	payload["agent_id"] = util.UUIDToString(task.AgentID)

	workspaceID, kind, err := s.ResolveTaskWorkspaceID(ctx, task)
	if err != nil {
		slog.Warn("broadcast task dispatch: resolve workspace failed",
			"task_id", util.UUIDToString(task.ID),
			"kind", string(kind),
			"error", err,
		)
		return
	}
	switch kind {
	case TaskKindIssue, TaskKindChat, TaskKindAutopilot:
		s.Bus.Publish(events.Event{
			Type:        protocol.EventTaskDispatch,
			WorkspaceID: workspaceID,
			ActorType:   "system",
			ActorID:     "",
			Payload:     payload,
		})
	case TaskKindGlobal:
		// Global tasks broadcast on the per-user channel (MUL-192) —
		// the runtime-context payload above is workspace-shaped and is
		// intentionally dropped; the lifecycle ping carries the fields
		// the global pane needs.
		s.broadcastGlobalTaskEvent(ctx, task, "dispatched")
	case TaskKindUnknown:
		slog.Warn("broadcast task dispatch: task has no recognized entity-type FK",
			"task_id", util.UUIDToString(task.ID),
		)
	}
}

func (s *TaskService) broadcastTaskEvent(ctx context.Context, eventType string, task db.AgentTaskQueue) {
	workspaceID, kind, err := s.ResolveTaskWorkspaceID(ctx, task)
	if err != nil {
		slog.Warn("broadcast task event: resolve workspace failed",
			"task_id", util.UUIDToString(task.ID),
			"event", eventType,
			"kind", string(kind),
			"error", err,
		)
		return
	}
	switch kind {
	case TaskKindIssue, TaskKindChat, TaskKindAutopilot:
		payload := map[string]any{
			"task_id":  util.UUIDToString(task.ID),
			"agent_id": util.UUIDToString(task.AgentID),
			"issue_id": util.UUIDToString(task.IssueID),
			"status":   task.Status,
		}
		if task.ChatSessionID.Valid {
			payload["chat_session_id"] = util.UUIDToString(task.ChatSessionID)
		}
		s.Bus.Publish(events.Event{
			Type:        eventType,
			WorkspaceID: workspaceID,
			ActorType:   "system",
			ActorID:     "",
			Payload:     payload,
		})
	case TaskKindGlobal:
		// MUL-192: per-user global-chat lifecycle ping. Maps the
		// workspace-side event type to a status token the global pane
		// uses to clear the "agent is thinking…" indicator (MUL-159)
		// on terminal states without waiting for the next poll.
		s.broadcastGlobalTaskEvent(ctx, task, globalTaskStatusFor(eventType, task))
	case TaskKindUnknown:
		slog.Warn("broadcast task event: task has no recognized entity-type FK",
			"task_id", util.UUIDToString(task.ID),
			"event", eventType,
		)
	}
}

// TaskKind classifies an agent_task_queue row by which entity-type FK
// pins it to a scope. Adding a new entity-type column on agent_task_queue
// MUST add a new TaskKind value and a branch in TaskKindOf and
// ResolveTaskWorkspaceID — otherwise every consumer silently drops to
// the Unknown branch (the bug class behind MUL-182 and MUL-192).
type TaskKind string

const (
	// TaskKindUnknown is returned when none of the recognized entity-type
	// FKs is set on the task row. Either the row is genuinely orphaned
	// or a new entity-type pathway has been added without updating the
	// resolver. Callers must treat this as an error / 404.
	TaskKindUnknown TaskKind = "unknown"
	// TaskKindIssue: task is pinned to an issue, scoped to issue.workspace_id.
	TaskKindIssue TaskKind = "issue"
	// TaskKindChat: task is pinned to a workspace-scoped chat session,
	// scoped to chat_session.workspace_id.
	TaskKindChat TaskKind = "chat"
	// TaskKindAutopilot: task was enqueued by an autopilot run, scoped
	// to autopilot.workspace_id.
	TaskKindAutopilot TaskKind = "autopilot"
	// TaskKindGlobal: V3 global chat task (MUL-137). Tenantless by
	// design — no workspace_id. Isolation must be enforced via runtime
	// ownership, not workspace match.
	TaskKindGlobal TaskKind = "global"
)

// TaskKindOf classifies a task by which entity-type FK is set on its row.
// Returns TaskKindUnknown when none of the recognized FKs is set. The
// order matches the historical resolver: Issue > Chat > Autopilot >
// Global, so a row that defensively carries multiple FKs (none should
// today) reports the most workspace-scoped kind first.
func TaskKindOf(task db.AgentTaskQueue) TaskKind {
	switch {
	case task.IssueID.Valid:
		return TaskKindIssue
	case task.ChatSessionID.Valid:
		return TaskKindChat
	case task.AutopilotRunID.Valid:
		return TaskKindAutopilot
	case task.GlobalSessionID.Valid:
		return TaskKindGlobal
	default:
		return TaskKindUnknown
	}
}

// globalTaskStatusFor maps a workspace-side lifecycle event type to the
// terminal-state token used in `GlobalChatTaskUpdatePayload.Status`. Falls
// back to the row's current status so an unknown event type still emits a
// meaningful payload instead of an empty string.
func globalTaskStatusFor(eventType string, task db.AgentTaskQueue) string {
	switch eventType {
	case protocol.EventTaskDispatch:
		return "dispatched"
	case protocol.EventTaskCompleted:
		return "completed"
	case protocol.EventTaskFailed:
		return "failed"
	case protocol.EventTaskCancelled:
		return "cancelled"
	default:
		return task.Status
	}
}

// ResolveTaskWorkspaceID returns the workspace_id the task is scoped to,
// the entity-type kind that pins it to that scope, and an error if the
// underlying entity row could not be loaded.
//
// Returned (workspaceID, kind, err) values per kind:
//   - Issue / Chat / Autopilot: workspaceID is the resolved workspace
//     UUID; err is non-nil iff the linked entity row is missing.
//   - Global: workspaceID is "" by design — global chat is tenantless;
//     callers must gate on runtime ownership, not workspace match.
//   - Unknown: workspaceID is "" and err is nil; callers must treat
//     this as 404.
//
// Adding a new entity-type FK on agent_task_queue requires extending
// TaskKind, TaskKindOf, and the switch below. The typed return forces
// every consumer to update its branching, which closes the silent-drop
// class of bug seen in MUL-182.
func (s *TaskService) ResolveTaskWorkspaceID(ctx context.Context, task db.AgentTaskQueue) (string, TaskKind, error) {
	kind := TaskKindOf(task)
	switch kind {
	case TaskKindIssue:
		issue, err := s.Queries.GetIssue(ctx, task.IssueID)
		if err != nil {
			return "", kind, fmt.Errorf("get issue: %w", err)
		}
		return util.UUIDToString(issue.WorkspaceID), kind, nil
	case TaskKindChat:
		cs, err := s.Queries.GetChatSession(ctx, task.ChatSessionID)
		if err != nil {
			return "", kind, fmt.Errorf("get chat session: %w", err)
		}
		return util.UUIDToString(cs.WorkspaceID), kind, nil
	case TaskKindAutopilot:
		run, err := s.Queries.GetAutopilotRun(ctx, task.AutopilotRunID)
		if err != nil {
			return "", kind, fmt.Errorf("get autopilot run: %w", err)
		}
		ap, err := s.Queries.GetAutopilot(ctx, run.AutopilotID)
		if err != nil {
			return "", kind, fmt.Errorf("get autopilot: %w", err)
		}
		return util.UUIDToString(ap.WorkspaceID), kind, nil
	case TaskKindGlobal:
		return "", kind, nil
	default:
		return "", TaskKindUnknown, nil
	}
}

func (s *TaskService) broadcastChatDone(ctx context.Context, task db.AgentTaskQueue) {
	workspaceID, kind, err := s.ResolveTaskWorkspaceID(ctx, task)
	if err != nil {
		slog.Warn("broadcast chat done: resolve workspace failed",
			"task_id", util.UUIDToString(task.ID),
			"kind", string(kind),
			"error", err,
		)
		return
	}
	switch kind {
	case TaskKindChat, TaskKindIssue, TaskKindAutopilot:
		s.Bus.Publish(events.Event{
			Type:          protocol.EventChatDone,
			WorkspaceID:   workspaceID,
			ActorType:     "system",
			ActorID:       "",
			ChatSessionID: util.UUIDToString(task.ChatSessionID),
			Payload: protocol.ChatDonePayload{
				ChatSessionID: util.UUIDToString(task.ChatSessionID),
				TaskID:        util.UUIDToString(task.ID),
			},
		})
	case TaskKindGlobal:
		// MUL-192: chat:done has no global-chat parallel — the per-user
		// `global_chat:message` published by writeGlobalChatAgentReply is
		// already the authoritative "agent reply landed" signal. Emit
		// the lifecycle ping defensively so a future caller funnelling
		// global tasks here can't regress to a silent workspace-key
		// drop. Today this branch is unreachable: CompleteTask gates
		// broadcastChatDone on task.ChatSessionID.Valid.
		s.broadcastGlobalTaskEvent(ctx, task, "completed")
	case TaskKindUnknown:
		slog.Warn("broadcast chat done: task has no recognized entity-type FK",
			"task_id", util.UUIDToString(task.ID),
		)
	}
}

// broadcastGlobalTaskEvent emits a global-chat lifecycle event on the
// originating user's per-user channel for tasks where GlobalSessionID is
// set. ResolveTaskWorkspaceID returns "" for global tasks (workspace_id
// is NULL by design — see MUL-182) and every workspace-keyed broadcast
// helper used to silently drop the event, leaving the global-chat
// "agent is thinking…" indicator (MUL-159) stuck on failure / cancel
// until the next polling refetch caught up. Mirrors the per-user publish
// shape writeGlobalChatAgentReply already uses for the happy path.
func (s *TaskService) broadcastGlobalTaskEvent(ctx context.Context, task db.AgentTaskQueue, status string) {
	if !task.GlobalSessionID.Valid {
		return
	}
	sess, err := s.Queries.GetGlobalChatSession(ctx, task.GlobalSessionID)
	if err != nil {
		slog.Warn("global task event: lookup session failed",
			"task_id", util.UUIDToString(task.ID),
			"global_session_id", util.UUIDToString(task.GlobalSessionID),
			"status", status,
			"error", err,
		)
		return
	}
	errMsg := ""
	if task.Error.Valid {
		errMsg = task.Error.String
	}
	s.Bus.Publish(events.Event{
		Type:      protocol.EventGlobalChatTaskUpdate,
		UserID:    util.UUIDToString(sess.UserID),
		ActorType: "system",
		TaskID:    util.UUIDToString(task.ID),
		Payload: protocol.GlobalChatTaskUpdatePayload{
			GlobalSessionID: util.UUIDToString(task.GlobalSessionID),
			TaskID:          util.UUIDToString(task.ID),
			AgentID:         util.UUIDToString(task.AgentID),
			Status:          status,
			Error:           errMsg,
		},
	})
}

func (s *TaskService) broadcastIssueUpdated(issue db.Issue) {
	prefix := s.getIssuePrefix(issue.WorkspaceID)
	s.Bus.Publish(events.Event{
		Type:        protocol.EventIssueUpdated,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "system",
		ActorID:     "",
		Payload:     map[string]any{"issue": issueToMap(issue, prefix)},
	})
}

func (s *TaskService) getIssuePrefix(workspaceID pgtype.UUID) string {
	ws, err := s.Queries.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		return ""
	}
	return ws.IssuePrefix
}

func (s *TaskService) createAgentComment(ctx context.Context, issueID, agentID pgtype.UUID, content, commentType string, parentID pgtype.UUID) {
	if content == "" {
		return
	}
	// Look up issue to get workspace ID for mention expansion and broadcasting.
	issue, err := s.Queries.GetIssue(ctx, issueID)
	if err != nil {
		return
	}
	// Resolve thread root: if parentID points to a reply (has its own parent),
	// use that parent instead so the comment lands in the top-level thread.
	if parentID.Valid {
		if parent, err := s.Queries.GetComment(ctx, parentID); err == nil && parent.ParentID.Valid {
			parentID = parent.ParentID
		}
	}
	// Expand bare issue identifiers (e.g. MUL-117) into mention links.
	content = mention.ExpandIssueIdentifiers(ctx, s.Queries, issue.WorkspaceID, content)
	comment, err := s.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issueID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "agent",
		AuthorID:    agentID,
		Content:     content,
		Type:        commentType,
		ParentID:    parentID,
	})
	if err != nil {
		return
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventCommentCreated,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "agent",
		ActorID:     util.UUIDToString(agentID),
		Payload: map[string]any{
			"comment": map[string]any{
				"id":          util.UUIDToString(comment.ID),
				"issue_id":    util.UUIDToString(comment.IssueID),
				"author_type": comment.AuthorType,
				"author_id":   util.UUIDToString(comment.AuthorID),
				"content":     comment.Content,
				"type":        comment.Type,
				"parent_id":   util.UUIDToPtr(comment.ParentID),
				"created_at":  comment.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
			},
			"issue_title":  issue.Title,
			"issue_status": issue.Status,
		},
	})
}

func issueToMap(issue db.Issue, issuePrefix string) map[string]any {
	return map[string]any{
		"id":              util.UUIDToString(issue.ID),
		"workspace_id":    util.UUIDToString(issue.WorkspaceID),
		"number":          issue.Number,
		"identifier":      issuePrefix + "-" + strconv.Itoa(int(issue.Number)),
		"title":           issue.Title,
		"description":     util.TextToPtr(issue.Description),
		"status":          issue.Status,
		"priority":        issue.Priority,
		"assignee_type":   util.TextToPtr(issue.AssigneeType),
		"assignee_id":     util.UUIDToPtr(issue.AssigneeID),
		"creator_type":    issue.CreatorType,
		"creator_id":      util.UUIDToString(issue.CreatorID),
		"parent_issue_id": util.UUIDToPtr(issue.ParentIssueID),
		"position":        issue.Position,
		"due_date":        util.TimestampToPtr(issue.DueDate),
		"created_at":      util.TimestampToString(issue.CreatedAt),
		"updated_at":      util.TimestampToString(issue.UpdatedAt),
	}
}

// agentToMap builds a simple map for broadcasting agent status updates.
func agentToMap(a db.Agent) map[string]any {
	var rc any
	if a.RuntimeConfig != nil {
		json.Unmarshal(a.RuntimeConfig, &rc)
	}
	return map[string]any{
		"id":                   util.UUIDToString(a.ID),
		"workspace_id":         util.UUIDToString(a.WorkspaceID),
		"runtime_id":           util.UUIDToString(a.RuntimeID),
		"name":                 a.Name,
		"description":          a.Description,
		"avatar_url":           util.TextToPtr(a.AvatarUrl),
		"runtime_mode":         a.RuntimeMode,
		"runtime_config":       rc,
		"visibility":           a.Visibility,
		"status":               a.Status,
		"max_concurrent_tasks": a.MaxConcurrentTasks,
		"owner_id":             util.UUIDToPtr(a.OwnerID),
		"skills":               []any{},
		"created_at":           util.TimestampToString(a.CreatedAt),
		"updated_at":           util.TimestampToString(a.UpdatedAt),
		"archived_at":          util.TimestampToPtr(a.ArchivedAt),
		"archived_by":          util.UUIDToPtr(a.ArchivedBy),
	}
}
