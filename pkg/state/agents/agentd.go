package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/agents/sqlc"
)

// agentd's side of the store (ADR-20261027090014 §1): it claims queued runs under a
// lease token and owns every later transition. Each write is fenced on the
// token, so a replica that lost its lease changes nothing.

// ErrLeaseLost means the run's lease is no longer this caller's: it expired,
// another replica holds it, or the run already left the expected state.
var ErrLeaseLost = errors.New("agents: lease lost")

// claimWindow is how many queued runs one claim looks at, so accounts at
// their concurrency limit do not hold back the accounts behind them.
const claimWindow = 64

// Claim is a run agentd claimed, with the version it runs.
type Claim struct {
	RunID        string
	AccountID    string
	Plan         string
	AgentID      string
	AgentName    string
	AgentVersion int
	TenantID     string
	Prompt       *string
	Inputs       json.RawMessage
	RAMMB        int
	TimeoutSec   int
	Attempt      int
	Harness      Harness
	Template     string
	// Integrations and AIBudgetMillicents come from the version (ADR-20261027090010
	// §7); a zero budget takes the plan's default.
	Integrations       map[string]string
	AIBudgetMillicents int64
	// Checkout is the tree the run asked for, if any (ADR-20261027090010 §3).
	Checkout   *Checkout
	LeaseToken string
}

// ClaimRun moves the oldest claimable queued run to starting under token.
// A run is claimable when its account has fewer live runs than maxLive
// returns for its plan. found is false when nothing is claimable.
func (s *Store) ClaimRun(ctx context.Context, owner, token string, ttl time.Duration, maxLive func(plan string) int) (claim Claim, found bool, err error) {
	var runID string
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		candidates, err := s.q.ListClaimableAgentRuns(ctx, tx, claimWindow)
		if err != nil {
			return fmt.Errorf("list claimable runs: %w", err)
		}
		for _, c := range candidates {
			ok, err := s.claimable(ctx, tx, c.AccountID, maxLive)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			n, err := s.q.StartAgentRun(ctx, tx, sqlc.StartAgentRunParams{LeaseToken: token, LeaseOwner: owner, TtlSeconds: ttl.Seconds(), RunID: c.ID})
			if err != nil {
				return fmt.Errorf("start run: %w", err)
			}
			if n == 1 {
				runID = c.ID
				return nil
			}
		}
		return nil
	})
	if err != nil || runID == "" {
		return Claim{}, false, err
	}
	row, err := s.q.GetAgentRunClaim(ctx, s.pool, runID)
	if err != nil {
		return Claim{}, false, fmt.Errorf("read claimed run: %w", err)
	}
	claim, err = claimFrom(row, token)
	return claim, err == nil, err
}

// claimable takes the account's lock without waiting and checks its load.
func (s *Store) claimable(ctx context.Context, tx pgx.Tx, accountID string, maxLive func(string) int) (bool, error) {
	locked, err := s.q.TryLockAccountAgents(ctx, tx, accountID)
	if err != nil {
		return false, fmt.Errorf("lock account: %w", err)
	}
	if !locked {
		return false, nil
	}
	load, err := s.q.AccountAgentLoad(ctx, tx, accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read account load: %w", err)
	}
	return int(load.Live) < maxLive(load.Plan), nil
}

// MarkRunning records the placement and the run's model spend cap, and moves
// the run to running: from here a lost lease fails the run instead of
// queueing it again. The AI gateway enforces the cap (ADR-20261027090010 §7).
func (s *Store) MarkRunning(ctx context.Context, runID, token, nodeID, instanceID string, aiBudgetMillicents int64) error {
	if aiBudgetMillicents < 1 {
		return fmt.Errorf("agents: run %s needs a positive model spend cap", runID)
	}
	n, err := s.q.MarkAgentRunRunning(ctx, s.pool, sqlc.MarkAgentRunRunningParams{NodeID: nodeID, InstanceID: instanceID,
		AiBudgetMillicents: aiBudgetMillicents, RunID: runID, LeaseToken: token})
	return fenced(n, err, "mark run running")
}

// RecordBoot records how long the VM took to boot.
func (s *Store) RecordBoot(ctx context.Context, runID, token string, boot time.Duration) error {
	n, err := s.q.RecordAgentRunBoot(ctx, s.pool, sqlc.RecordAgentRunBootParams{BootMs: int32(boot.Milliseconds()), RunID: runID, LeaseToken: token})
	return fenced(n, err, "record boot")
}

// Renew extends the lease and reports whether a cancel was requested.
func (s *Store) Renew(ctx context.Context, runID, token string, ttl time.Duration) (cancelRequested bool, err error) {
	cancelRequested, err = s.q.RenewAgentRunLease(ctx, s.pool, sqlc.RenewAgentRunLeaseParams{TtlSeconds: ttl.Seconds(), RunID: runID, LeaseToken: token})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrLeaseLost
	}
	if err != nil {
		return false, fmt.Errorf("renew lease: %w", err)
	}
	return cancelRequested, nil
}

// Requeue returns a starting run, which reached no VM, to the queue.
func (s *Store) Requeue(ctx context.Context, runID, token string) error {
	n, err := s.q.RequeueAgentRun(ctx, s.pool, sqlc.RequeueAgentRunParams{RunID: runID, LeaseToken: token})
	return fenced(n, err, "requeue run")
}

// OutputFile is one output file's manifest entry.
type OutputFile struct {
	Name   string `json:"name"`
	Size   int    `json:"size"`
	SHA256 string `json:"sha256"`
	// Key names the file's contents in the artifact store; empty when the
	// replica that finished the run kept no contents.
	Key string `json:"key,omitempty"`
}

// Finish is a run's terminal record.
type Finish struct {
	State          string
	OutcomeCode    string
	OutcomeMessage string
	ExitCode       *int
	Signal         *int
	Result         json.RawMessage
	Files          []OutputFile
	LogTail        string
	LogTruncated   bool
	// DeliveriesOff marks a replica that cannot deliver: a succeeded run's
	// declared deliveries fail at once instead of waiting their window.
	DeliveriesOff bool
}

// FinishRun records a terminal state and the run's outputs in one
// transaction, fenced on the lease. A run that succeeded and declares
// deliveries moves to delivering instead, and ends with its deliveries.
func (s *Store) FinishRun(ctx context.Context, accountID, runID, token string, f Finish) error {
	if !terminalState(f.State) {
		return fmt.Errorf("agents: %q is not a terminal state", f.State)
	}
	files, err := json.Marshal(nonNilFiles(f.Files))
	if err != nil {
		return fmt.Errorf("encode output files: %w", err)
	}
	var result []byte
	if len(f.Result) > 0 {
		result = f.Result
	}
	outputs := sqlc.InsertAgentRunOutputsParams{RunID: runID, AccountID: accountID, Result: result,
		Files: files, LogTail: f.LogTail, LogTruncated: f.LogTruncated}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		delivering, err := s.beginDelivering(ctx, tx, accountID, runID, token, f, outputs)
		if err != nil || delivering {
			return err
		}
		n, err := s.q.FinishAgentRun(ctx, tx, sqlc.FinishAgentRunParams{State: f.State, OutcomeCode: optText(f.OutcomeCode),
			OutcomeMessage: optText(f.OutcomeMessage), ExitCode: optPtrInt(f.ExitCode), Signal: optPtrInt(f.Signal), RunID: runID, LeaseToken: token})
		if err := fenced(n, err, "finish run"); err != nil {
			return err
		}
		if err := s.q.InsertAgentRunOutputs(ctx, tx, outputs); err != nil {
			return fmt.Errorf("store outputs: %w", err)
		}
		return s.notifyFinished(ctx, tx, accountID, runID)
	})
}

// notifyFinished fans agent_run.finished out to the account's subscribed
// receivers in the finishing transaction, so a finished run is always
// announced and never twice (ADR-20261027090010 §8). It reads what it announces from
// the transaction: the run, its outputs and its deliveries.
func (s *Store) notifyFinished(ctx context.Context, tx pgx.Tx, accountID, runID string) error {
	row, err := s.q.GetAgentRun(ctx, tx, sqlc.GetAgentRunParams{RunID: runID, AccountID: accountID})
	if err != nil {
		return fmt.Errorf("read finished run: %w", err)
	}
	run := runFrom(row)
	payload := api.AgentRunFinishedWebhookPayload{RunID: run.ID, Agent: run.AgentName, AgentID: run.AgentID,
		AgentVersion: run.AgentVersion, Tenant: run.TenantID, Trigger: run.Trigger, State: run.State, ExitCode: run.ExitCode}
	if run.FinishedAt != nil {
		payload.FinishedAt = *run.FinishedAt
	}
	if run.OutcomeCode != "" {
		payload.Outcome = &api.AgentRunOutcome{Code: run.OutcomeCode, Message: run.OutcomeMessage}
	}
	out, err := s.outputs(ctx, tx, accountID, runID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	payload.LogTruncated = out.LogTruncated
	// Only a succeeded run delivers its result (ADR-20261027090010 §8); any other end
	// is announced with its state and outcome alone.
	if run.State == StateSucceeded {
		if len(out.Result) > 0 && len(out.Result) <= api.AgentRunWebhookResultMaxBytes {
			payload.Result = out.Result
		} else if len(out.Result) > 0 {
			payload.ResultOmitted = true
		}
		for _, file := range out.Files {
			payload.Files = append(payload.Files, api.AgentOutputFile{Name: file.Name, Size: file.Size, SHA256: file.SHA256})
		}
	}
	if payload.Deliveries, err = s.runDeliveries(ctx, tx, accountID, runID); err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode run webhook: %w", err)
	}
	if _, err := s.q.InsertAgentRunFinishedDeliveries(ctx, tx, sqlc.InsertAgentRunFinishedDeliveriesParams{Payload: body, RunID: runID,
		AccountID: accountID}); err != nil {
		return fmt.Errorf("queue run webhooks: %w", err)
	}
	return nil
}

// ExpireLeases queues expired starting runs again and fails expired
// running ones, which may have reached a VM and are never replayed.
func (s *Store) ExpireLeases(ctx context.Context) (requeued, failed int64, err error) {
	if requeued, err = s.q.RequeueExpiredStartingAgentRuns(ctx, s.pool); err != nil {
		return 0, 0, fmt.Errorf("requeue expired runs: %w", err)
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := s.q.FailExpiredRunningAgentRuns(ctx, tx)
		if err != nil {
			return fmt.Errorf("fail expired runs: %w", err)
		}
		for _, row := range rows {
			if err := s.notifyFinished(ctx, tx, row.AccountID, row.ID); err != nil {
				return err
			}
		}
		failed = int64(len(rows))
		return nil
	})
	if err != nil {
		return requeued, 0, err
	}
	return requeued, failed, nil
}

// Outputs is a finished run's stored outputs and its deliveries.
type Outputs struct {
	Result       json.RawMessage
	Files        []OutputFile
	LogTail      string
	LogTruncated bool
	Deliveries   []api.AgentRunDelivery
}

// RunOutputs reads a finished run's outputs and deliveries.
func (s *Store) RunOutputs(ctx context.Context, accountID, runID string) (Outputs, error) {
	if !isUUID(runID) {
		return Outputs{}, ErrNotFound
	}
	out, err := s.outputs(ctx, s.pool, accountID, runID)
	if err != nil {
		return Outputs{}, err
	}
	out.Deliveries, err = s.runDeliveries(ctx, s.pool, accountID, runID)
	return out, err
}

func (s *Store) outputs(ctx context.Context, db sqlc.DBTX, accountID, runID string) (Outputs, error) {
	row, err := s.q.GetAgentRunOutputs(ctx, db, sqlc.GetAgentRunOutputsParams{RunID: runID, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Outputs{}, ErrNotFound
	}
	if err != nil {
		return Outputs{}, fmt.Errorf("read outputs: %w", err)
	}
	out := Outputs{Result: row.Result, LogTail: row.LogTail, LogTruncated: row.LogTruncated}
	if err := json.Unmarshal(row.Files, &out.Files); err != nil {
		return Outputs{}, fmt.Errorf("decode output files: %w", err)
	}
	return out, nil
}

func claimFrom(row sqlc.GetAgentRunClaimRow, token string) (Claim, error) {
	var models, integrations map[string]string
	if err := json.Unmarshal(row.HarnessModels, &models); err != nil {
		return Claim{}, fmt.Errorf("decode harness models: %w", err)
	}
	if err := json.Unmarshal(row.ModelIntegrations, &integrations); err != nil {
		return Claim{}, fmt.Errorf("decode model integrations: %w", err)
	}
	c := Claim{RunID: row.ID, AccountID: row.AccountID, Plan: row.Plan, AgentID: row.AgentID, AgentName: row.AgentName,
		AgentVersion: int(row.AgentVersion), TenantID: row.TenantID, Inputs: row.Inputs, RAMMB: int(row.RamMb.Int32),
		TimeoutSec: int(row.TimeoutSec), Attempt: int(row.Attempt), Template: row.PromptTemplate, LeaseToken: token,
		Integrations: nilIfEmpty(integrations), AIBudgetMillicents: row.AiBudgetMillicents.Int64,
		Checkout: checkoutFrom(row.CheckoutRepository, row.CheckoutCommit, row.CheckoutInstallationID),
		Harness:  Harness{Preset: row.HarnessPreset.String, Image: row.HarnessImage.String, Run: row.HarnessRun, Models: models}}
	if row.Prompt.Valid {
		prompt := row.Prompt.String
		c.Prompt = &prompt
	}
	return c, nil
}

func fenced(n int64, err error, op string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if n != 1 {
		return ErrLeaseLost
	}
	return nil
}

func terminalState(state string) bool {
	return Run{State: state}.Terminal()
}

func nonNilFiles(files []OutputFile) []OutputFile {
	if files == nil {
		return []OutputFile{}
	}
	return files
}

func optPtrInt(n *int) pgtype.Int4 {
	if n == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*n), Valid: true}
}
