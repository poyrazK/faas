package agents

// Sandbox tasks (ADR-20261116182615 §3): a session that runs one platform
// command and ends. apid admits a task for a gregale_browse call, linked to
// the call's thread; agentd records the command's end as it finishes the
// session; apid publishes the result into the thread and marks the link
// delivered.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/agents/sqlc"
)

// SandboxTaskBrowse is the browse task: gregale-browser fetch <url>.
const SandboxTaskBrowse = "browse"

// SandboxTask is a session's task: its kind and validated arguments.
type SandboxTask struct {
	Kind     string
	URL      string
	MaxChars int
}

// SandboxTaskCall is the thread and tool call a task answers.
type SandboxTaskCall struct {
	EndpointID string
	ThreadID   string
	ToolCallID string
	IssuedAt   time.Time
}

// CreateSandboxTask admits a task session linked to call. A call already
// linked returns its session with existing set, so a retried invocation
// admits nothing more.
func (s *Store) CreateSandboxTask(ctx context.Context, a SandboxAdmission, call SandboxTaskCall) (sb Sandbox, existing bool, err error) {
	if a.Task == nil || a.Task.Kind == "" || a.Task.URL == "" || a.Task.MaxChars < 1 {
		return Sandbox{}, false, errors.New("create sandbox task: incomplete task")
	}
	if !isUUID(call.EndpointID) || call.ThreadID == "" || call.ToolCallID == "" {
		return Sandbox{}, false, errors.New("create sandbox task: incomplete call")
	}
	labels, err := json.Marshal(nonNilLabels(a.Labels))
	if err != nil {
		return Sandbox{}, false, fmt.Errorf("encode labels: %w", err)
	}
	var id string
	err = s.locked(ctx, a.AccountID, func(tx pgx.Tx) error {
		found, err := s.q.SandboxTaskCallByCall(ctx, tx, sqlc.SandboxTaskCallByCallParams{EndpointID: call.EndpointID,
			ThreadID: call.ThreadID, ToolCallID: call.ToolCallID})
		if err == nil {
			id, existing = found, true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read sandbox task call: %w", err)
		}
		if id, err = s.insertSandbox(ctx, tx, a, labels); err != nil {
			return err
		}
		if err := s.q.InsertSandboxTaskCall(ctx, tx, sqlc.InsertSandboxTaskCallParams{SandboxID: id, AccountID: a.AccountID,
			EndpointID: call.EndpointID, ThreadID: call.ThreadID, ToolCallID: call.ToolCallID,
			IssuedAt: pgtype.Timestamptz{Time: call.IssuedAt, Valid: true}}); err != nil {
			return fmt.Errorf("insert sandbox task call: %w", err)
		}
		return nil
	})
	if err != nil {
		return Sandbox{}, false, err
	}
	sb, err = s.Sandbox(ctx, a.AccountID, id)
	return sb, existing, err
}

// RecordSandboxTaskResult records a task's exit code and output under the
// session's lease, once. A session that is not a task, or whose result is
// already recorded, records nothing.
func (s *Store) RecordSandboxTaskResult(ctx context.Context, id, token string, exitCode int, output []byte) error {
	if output == nil {
		output = []byte{}
	}
	if _, err := s.q.RecordSandboxTaskResult(ctx, s.pool, sqlc.RecordSandboxTaskResultParams{ExitCode: int32(exitCode), Output: output,
		SessionID: id, LeaseToken: token}); err != nil {
		return fmt.Errorf("record sandbox task result: %w", err)
	}
	return nil
}

// SandboxTaskEnd is an ended task whose result apid has not yet published
// into its thread. ExitCode is nil when the command never reported.
type SandboxTaskEnd struct {
	SandboxID      string
	AccountID      string
	Call           SandboxTaskCall
	State          string
	OutcomeCode    string
	OutcomeMessage string
	Kind           string
	URL            string
	ExitCode       *int
	Output         []byte
}

// UndeliveredSandboxTasks lists up to limit ended tasks not yet published.
func (s *Store) UndeliveredSandboxTasks(ctx context.Context, limit int) ([]SandboxTaskEnd, error) {
	rows, err := s.q.ListUndeliveredSandboxTaskCalls(ctx, s.pool, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("list undelivered sandbox tasks: %w", err)
	}
	out := make([]SandboxTaskEnd, 0, len(rows))
	for _, row := range rows {
		end := SandboxTaskEnd{SandboxID: row.SandboxID, AccountID: row.AccountID, State: row.State, OutcomeCode: row.OutcomeCode,
			OutcomeMessage: row.OutcomeMessage, Kind: row.TaskKind, URL: row.TaskUrl, Output: row.TaskOutput,
			Call: SandboxTaskCall{EndpointID: row.EndpointID, ThreadID: row.ThreadID, ToolCallID: row.ToolCallID, IssuedAt: row.IssuedAt.Time.UTC()}}
		if row.TaskExitCode.Valid {
			code := int(row.TaskExitCode.Int32)
			end.ExitCode = &code
		}
		out = append(out, end)
	}
	return out, nil
}

// MarkSandboxTaskDelivered records that a task's result reached its thread,
// or that it never will.
func (s *Store) MarkSandboxTaskDelivered(ctx context.Context, sandboxID string) error {
	if err := s.q.MarkSandboxTaskCallDelivered(ctx, s.pool, sandboxID); err != nil {
		return fmt.Errorf("mark sandbox task delivered: %w", err)
	}
	return nil
}

// SandboxTaskResult is a task session's arguments and, once it ended, its
// result.
type SandboxTaskResult struct {
	State          string
	Kind           string
	URL            string
	OutcomeCode    string
	OutcomeMessage string
	ExitCode       *int
	Output         []byte
}

// SandboxTaskResult reads the account's task session id. A session that is
// not a task is ErrNotFound.
func (s *Store) SandboxTaskResult(ctx context.Context, accountID, id string) (SandboxTaskResult, error) {
	if !isUUID(id) {
		return SandboxTaskResult{}, ErrNotFound
	}
	row, err := s.q.GetSandboxTaskResult(ctx, s.pool, sqlc.GetSandboxTaskResultParams{SessionID: id, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && row.TaskKind == "" {
		return SandboxTaskResult{}, ErrNotFound
	}
	if err != nil {
		return SandboxTaskResult{}, fmt.Errorf("read sandbox task: %w", err)
	}
	out := SandboxTaskResult{State: row.State, Kind: row.TaskKind, URL: row.TaskUrl, OutcomeCode: row.OutcomeCode,
		OutcomeMessage: row.OutcomeMessage, Output: row.TaskOutput}
	if row.TaskExitCode.Valid {
		code := int(row.TaskExitCode.Int32)
		out.ExitCode = &code
	}
	return out, nil
}
