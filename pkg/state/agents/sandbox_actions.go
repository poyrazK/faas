package agents

// Browser actions (ADR-20261117065818 §2): a gregale_browser call acts on a
// live browser session. apid records the action; agentd, which holds the
// session, runs the session's actions one at a time and records each end
// under the session's lease; apid publishes the end into the call's thread.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/agents/sqlc"
)

// Sandbox action states.
const (
	SandboxActionPending = "pending"
	SandboxActionRunning = "running"
	SandboxActionDone    = "done"
)

// SandboxActionArgs are an action's validated fields; each kind uses its
// own (ADR-20261117065818 §1).
type SandboxActionArgs struct {
	URL       string `json:"url,omitempty"`
	Ref       int    `json:"ref,omitempty"`
	Text      string `json:"text,omitempty"`
	Submit    bool   `json:"submit,omitempty"`
	Key       string `json:"key,omitempty"`
	Direction string `json:"direction,omitempty"`
	MaxChars  int    `json:"max_chars,omitempty"`
}

// SandboxAction is an action as agentd sends it to its session.
type SandboxAction struct {
	ID        string
	SessionID string
	Seq       int
	Kind      string
	Args      SandboxActionArgs
}

// CreateSandboxAction records an action as session's next, linked to call.
// A call already recorded returns its action with existing set, so a
// retried invocation acts once.
func (s *Store) CreateSandboxAction(ctx context.Context, accountID, sessionID, kind string, args SandboxActionArgs,
	call SandboxTaskCall) (a SandboxAction, existing bool, err error) {
	if !isUUID(sessionID) || !isUUID(call.EndpointID) || call.ThreadID == "" || call.ToolCallID == "" || kind == "" {
		return SandboxAction{}, false, errors.New("create sandbox action: incomplete action")
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return SandboxAction{}, false, fmt.Errorf("encode sandbox action: %w", err)
	}
	row, err := s.q.InsertSandboxAction(ctx, s.pool, sqlc.InsertSandboxActionParams{SessionID: sessionID, AccountID: accountID,
		Kind: kind, Args: raw, EndpointID: call.EndpointID, ThreadID: call.ThreadID, ToolCallID: call.ToolCallID,
		IssuedAt: pgtype.Timestamptz{Time: call.IssuedAt, Valid: true}})
	if err == nil {
		return SandboxAction{ID: row.ID, SessionID: sessionID, Seq: int(row.Seq), Kind: kind, Args: args}, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return SandboxAction{}, false, fmt.Errorf("insert sandbox action: %w", err)
	}
	found, err := s.q.SandboxActionByCall(ctx, s.pool, sqlc.SandboxActionByCallParams{EndpointID: call.EndpointID,
		ThreadID: call.ThreadID, ToolCallID: call.ToolCallID})
	if err != nil {
		return SandboxAction{}, false, fmt.Errorf("read sandbox action: %w", err)
	}
	return SandboxAction{ID: found.ID, SessionID: found.SessionID, Seq: int(found.Seq), Kind: kind, Args: args}, true, nil
}

// NextSandboxActions returns, for each of sessionIDs that runs no action,
// its next pending one.
func (s *Store) NextSandboxActions(ctx context.Context, sessionIDs []string) ([]SandboxAction, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	rows, err := s.q.NextSandboxActions(ctx, s.pool, sessionIDs)
	if err != nil {
		return nil, fmt.Errorf("list next sandbox actions: %w", err)
	}
	out := make([]SandboxAction, 0, len(rows))
	for _, row := range rows {
		a := SandboxAction{ID: row.ID, SessionID: row.SessionID, Seq: int(row.Seq), Kind: row.Kind}
		if err := json.Unmarshal(row.Args, &a.Args); err != nil {
			return nil, fmt.Errorf("decode sandbox action %s: %w", row.ID, err)
		}
		out = append(out, a)
	}
	return out, nil
}

// StartSandboxAction marks a pending action running under its session's
// lease. It reports false when the action is not pending or the lease is
// gone.
func (s *Store) StartSandboxAction(ctx context.Context, id, token string) (bool, error) {
	n, err := s.q.StartSandboxAction(ctx, s.pool, sqlc.StartSandboxActionParams{ID: id, LeaseToken: token})
	if err != nil {
		return false, fmt.Errorf("start sandbox action: %w", err)
	}
	return n == 1, nil
}

// FinishSandboxAction records a running action's end under its session's
// lease. A nil exitCode is an action that never ran to its end.
func (s *Store) FinishSandboxAction(ctx context.Context, id, token string, exitCode *int, output []byte) error {
	code := pgtype.Int4{}
	if exitCode != nil {
		code = pgtype.Int4{Int32: int32(*exitCode), Valid: true}
	}
	if _, err := s.q.FinishSandboxAction(ctx, s.pool, sqlc.FinishSandboxActionParams{ExitCode: code, Output: output, ID: id,
		LeaseToken: token}); err != nil {
		return fmt.Errorf("finish sandbox action: %w", err)
	}
	return nil
}

// EndOpenSandboxActions ends a session's actions in states without an end:
// running ones on a fresh claim, whose VM is gone, and every open one as
// the session ends.
func (s *Store) EndOpenSandboxActions(ctx context.Context, sessionID, token string, states ...string) error {
	if _, err := s.q.EndOpenSandboxActions(ctx, s.pool, sqlc.EndOpenSandboxActionsParams{SessionID: sessionID, States: states,
		LeaseToken: token}); err != nil {
		return fmt.Errorf("end sandbox actions: %w", err)
	}
	return nil
}
