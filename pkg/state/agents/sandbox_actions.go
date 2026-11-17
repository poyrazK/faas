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
	"time"

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
	// Screenshot asks for the viewport too (ADR-20261117065818 §5).
	Screenshot bool `json:"screenshot,omitempty"`
}

// SandboxScreenshot is an action's kept screenshot: its artifact key and
// size. The zero value is none.
type SandboxScreenshot struct {
	Key   string
	Bytes int64
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

// FinishSandboxAction records a running action's end, and its screenshot
// if one was kept, under its session's lease. A nil exitCode is an action
// that never ran to its end.
func (s *Store) FinishSandboxAction(ctx context.Context, id, token string, exitCode *int, output []byte, shot SandboxScreenshot) error {
	code := pgtype.Int4{}
	if exitCode != nil {
		code = pgtype.Int4{Int32: int32(*exitCode), Valid: true}
	}
	params := sqlc.FinishSandboxActionParams{ExitCode: code, Output: output, ID: id, LeaseToken: token}
	if shot.Key != "" && shot.Bytes > 0 {
		params.ScreenshotKey = pgtype.Text{String: shot.Key, Valid: true}
		params.ScreenshotBytes = pgtype.Int8{Int64: shot.Bytes, Valid: true}
	}
	if _, err := s.q.FinishSandboxAction(ctx, s.pool, params); err != nil {
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

// CreateBrowserSession admits a gregale_browser session and records its
// first action (a navigate) linked to call, in one transaction
// (ADR-20261117065818 §3). A call already recorded returns its session with
// existing set, so a retried open admits nothing more.
func (s *Store) CreateBrowserSession(ctx context.Context, a SandboxAdmission, args SandboxActionArgs, call SandboxTaskCall) (sb Sandbox,
	existing bool, err error) {
	if !a.Browser || a.Task != nil || args.URL == "" {
		return Sandbox{}, false, errors.New("create browser session: a browser session opens on a url")
	}
	if !isUUID(call.EndpointID) || call.ThreadID == "" || call.ToolCallID == "" {
		return Sandbox{}, false, errors.New("create browser session: incomplete call")
	}
	labels, err := json.Marshal(nonNilLabels(a.Labels))
	if err != nil {
		return Sandbox{}, false, fmt.Errorf("encode labels: %w", err)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return Sandbox{}, false, fmt.Errorf("encode sandbox action: %w", err)
	}
	var id string
	err = s.locked(ctx, a.AccountID, func(tx pgx.Tx) error {
		found, err := s.q.SandboxActionByCall(ctx, tx, sqlc.SandboxActionByCallParams{EndpointID: call.EndpointID,
			ThreadID: call.ThreadID, ToolCallID: call.ToolCallID})
		if err == nil {
			id, existing = found.SessionID, true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read sandbox action: %w", err)
		}
		if id, err = s.insertSandbox(ctx, tx, a, labels); err != nil {
			return err
		}
		if _, err := s.q.InsertSandboxAction(ctx, tx, sqlc.InsertSandboxActionParams{SessionID: id, AccountID: a.AccountID,
			Kind: "navigate", Args: raw, EndpointID: call.EndpointID, ThreadID: call.ThreadID, ToolCallID: call.ToolCallID,
			IssuedAt: pgtype.Timestamptz{Time: call.IssuedAt, Valid: true}}); err != nil {
			return fmt.Errorf("insert sandbox action: %w", err)
		}
		return nil
	})
	if err != nil {
		return Sandbox{}, false, err
	}
	sb, err = s.Sandbox(ctx, a.AccountID, id)
	return sb, existing, err
}

// SandboxThread is the thread a gregale_browser session answers, its state
// and how many actions it has had.
type SandboxThread struct {
	State      string
	EndpointID string
	ThreadID   string
	Actions    int
}

// BrowserSessionThread reads the account's gregale_browser session id. A
// session gregale_browser did not open is ErrNotFound.
func (s *Store) BrowserSessionThread(ctx context.Context, accountID, id string) (SandboxThread, error) {
	if !isUUID(id) {
		return SandboxThread{}, ErrNotFound
	}
	row, err := s.q.SandboxActionThread(ctx, s.pool, sqlc.SandboxActionThreadParams{SessionID: id, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return SandboxThread{}, ErrNotFound
	}
	if err != nil {
		return SandboxThread{}, fmt.Errorf("read browser session: %w", err)
	}
	return SandboxThread{State: row.State, EndpointID: row.EndpointID, ThreadID: row.ThreadID, Actions: int(row.Actions)}, nil
}

// SandboxActionEnd is an ended action whose result apid has not yet
// published into its thread. ExitCode is nil when the action never ran to
// its end.
type SandboxActionEnd struct {
	SandboxAction
	AccountID    string
	Call         SandboxTaskCall
	ExitCode     *int
	Output       []byte
	SessionState string
	OutcomeCode  string
	// ScreenshotBytes is the kept screenshot's size, or 0.
	ScreenshotBytes int64
}

// UndeliveredSandboxActions lists up to limit ended actions not yet
// published.
func (s *Store) UndeliveredSandboxActions(ctx context.Context, limit int) ([]SandboxActionEnd, error) {
	rows, err := s.q.ListUndeliveredSandboxActions(ctx, s.pool, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("list undelivered sandbox actions: %w", err)
	}
	out := make([]SandboxActionEnd, 0, len(rows))
	for _, row := range rows {
		end := SandboxActionEnd{SandboxAction: SandboxAction{ID: row.ID, SessionID: row.SessionID, Seq: int(row.Seq), Kind: row.Kind},
			AccountID: row.AccountID, Output: row.Output, SessionState: row.SessionState, OutcomeCode: row.OutcomeCode,
			ScreenshotBytes: row.ScreenshotBytes,
			Call:            SandboxTaskCall{EndpointID: row.EndpointID, ThreadID: row.ThreadID, ToolCallID: row.ToolCallID, IssuedAt: row.IssuedAt.Time.UTC()}}
		if err := json.Unmarshal(row.Args, &end.Args); err != nil {
			return nil, fmt.Errorf("decode sandbox action %s: %w", row.ID, err)
		}
		if row.ExitCode.Valid {
			code := int(row.ExitCode.Int32)
			end.ExitCode = &code
		}
		out = append(out, end)
	}
	return out, nil
}

// MarkSandboxActionDelivered records that an action's result reached its
// thread, or that it never will.
func (s *Store) MarkSandboxActionDelivered(ctx context.Context, id string) error {
	if err := s.q.MarkSandboxActionDelivered(ctx, s.pool, id); err != nil {
		return fmt.Errorf("mark sandbox action delivered: %w", err)
	}
	return nil
}

// SandboxActionResult is one action as GET /v1/sandboxes/{id}/actions/{seq}
// shows it.
type SandboxActionResult struct {
	Kind       string
	Args       SandboxActionArgs
	State      string
	ExitCode   *int
	Output     []byte
	Screenshot SandboxScreenshot
}

// SandboxActionResult reads the account's session id's action seq.
func (s *Store) SandboxActionResult(ctx context.Context, accountID, id string, seq int) (SandboxActionResult, error) {
	if !isUUID(id) || seq < 1 {
		return SandboxActionResult{}, ErrNotFound
	}
	row, err := s.q.GetSandboxAction(ctx, s.pool, sqlc.GetSandboxActionParams{SessionID: id, AccountID: accountID, Seq: int32(seq)})
	if errors.Is(err, pgx.ErrNoRows) {
		return SandboxActionResult{}, ErrNotFound
	}
	if err != nil {
		return SandboxActionResult{}, fmt.Errorf("read sandbox action: %w", err)
	}
	out := SandboxActionResult{Kind: row.Kind, State: row.State, Output: row.Output,
		Screenshot: SandboxScreenshot{Key: row.ScreenshotKey, Bytes: row.ScreenshotBytes}}
	if err := json.Unmarshal(row.Args, &out.Args); err != nil {
		return SandboxActionResult{}, fmt.Errorf("decode sandbox action: %w", err)
	}
	if row.ExitCode.Valid {
		code := int(row.ExitCode.Int32)
		out.ExitCode = &code
	}
	return out, nil
}

// ExpiredSandboxScreenshot is a screenshot past its retention.
type ExpiredSandboxScreenshot struct {
	ActionID string
	Key      string
}

// ExpiredSandboxScreenshots lists up to limit screenshots of sessions that
// ended at least retention ago (ADR-20261117065818 §5).
func (s *Store) ExpiredSandboxScreenshots(ctx context.Context, retention time.Duration, limit int) ([]ExpiredSandboxScreenshot, error) {
	rows, err := s.q.ExpiredSandboxScreenshots(ctx, s.pool, sqlc.ExpiredSandboxScreenshotsParams{RetentionS: int32(retention.Seconds()),
		MaxRows: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("list expired sandbox screenshots: %w", err)
	}
	out := make([]ExpiredSandboxScreenshot, 0, len(rows))
	for _, row := range rows {
		out = append(out, ExpiredSandboxScreenshot{ActionID: row.ID, Key: row.ScreenshotKey})
	}
	return out, nil
}

// ClearSandboxScreenshot forgets a deleted screenshot's key.
func (s *Store) ClearSandboxScreenshot(ctx context.Context, actionID string) error {
	if err := s.q.ClearSandboxScreenshot(ctx, s.pool, actionID); err != nil {
		return fmt.Errorf("clear sandbox screenshot: %w", err)
	}
	return nil
}
