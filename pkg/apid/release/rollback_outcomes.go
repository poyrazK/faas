package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/safetext"
	"github.com/onebox-faas/faas/pkg/state"
)

// Auditor records a best-effort security/audit event (apid's ADR-035 seam).
type Auditor interface {
	Emit(ctx context.Context, kind string, accountID *string, data map[string]any)
}

// OutcomeMetrics records one resolved rollback (wire.OpsMetrics).
type OutcomeMetrics interface {
	RollbackOutcome(trigger, outcome string, recovery time.Duration)
}

// Rollbacks verifies plain rollback outcomes (ADR-1446). A plain rollback
// used to be recorded as done when it was requested; the safe-release drill
// found rollbacks that never served while the records said otherwise. Each
// plain rollback now opens a ledger row, and Run resolves it from the
// target's state. Audit and Metrics may be nil; a Store without
// state.RollbackOutcomeStore records nothing.
type Rollbacks struct {
	Store   state.Store
	Log     *slog.Logger
	Audit   Auditor
	Metrics OutcomeMetrics
}

// Record opens the ledger row for a plain rollback. A failed write is logged
// and returns nil: the rollback is already queued, and only its receipt is
// missing.
func (s Rollbacks) Record(ctx context.Context, acct state.Account, app state.App, target state.Deployment, fromID, trigger string) *api.RollbackOperation {
	store, ok := s.Store.(state.RollbackOutcomeStore)
	if !ok {
		return nil
	}
	record, err := store.RecordRollbackRequest(ctx, state.RollbackRecord{
		AccountID: acct.ID, AppID: app.ID, Scope: target.Scope,
		TargetDeploymentID: target.ID, FromDeploymentID: fromID, Trigger: trigger,
	})
	if err != nil {
		s.Log.Warn("rollback: record outcome ledger row failed", "app", app.ID, "target", target.ID, "err", err)
		return nil
	}
	operation := RecordOperation(record)
	return &operation
}

// RecordOperation renders a ledger row in the receipt shape checked
// rollbacks already use, so `gregale rollback status` reads both.
func RecordOperation(r state.RollbackRecord) api.RollbackOperation {
	op := api.RollbackOperation{
		ID: r.ID, AppID: r.AppID, Scope: r.Scope,
		TargetDeploymentID: r.TargetDeploymentID, CurrentDeploymentID: r.FromDeploymentID,
		Status: r.Status, Code: r.ErrorCode, Error: r.Error, Trigger: r.Trigger,
		CreatedAt: r.RequestedAt, UpdatedAt: r.RequestedAt,
	}
	if r.RecoveryMS != nil {
		ms := *r.RecoveryMS
		op.RecoveryMS = &ms
	}
	if r.FinishedAt != nil {
		op.UpdatedAt = *r.FinishedAt
		if r.Status == state.RollbackStatusComplete {
			finished := *r.FinishedAt
			op.CompletedAt = &finished
		}
	}
	if r.AuditID > 0 {
		op.AuditID = strconv.FormatInt(r.AuditID, 10)
	}
	return op
}

// Operation reads one plain rollback receipt; ok is false when the ledger has
// no such row for the account and app.
func (s Rollbacks) Operation(ctx context.Context, accountID, appID, id string) (api.RollbackOperation, bool) {
	store, ok := s.Store.(state.RollbackOutcomeStore)
	if !ok {
		return api.RollbackOperation{}, false
	}
	record, err := store.RollbackRecordByID(ctx, accountID, appID, id)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			s.Log.Warn("rollback: read outcome ledger row failed", "rollback", id, "err", err)
		}
		return api.RollbackOperation{}, false
	}
	return RecordOperation(record), true
}

// rollbackVerdict is the outcome of one preparing rollback, or done=false
// while the target is still activating.
type rollbackVerdict struct {
	done      bool
	status    string
	recovery  time.Duration
	finished  time.Time
	errorCode string
	errorText string
}

// decideRollbackOutcome classifies a preparing rollback from its target.
// Completion is read from rollout_completed_at, which PrepareDeploymentRollback
// clears and promotion stamps, so a target that served and was then replaced
// before this check still counts as a completed rollback.
func decideRollbackOutcome(r state.RollbackRecord, target state.Deployment, targetErr error, now time.Time) rollbackVerdict {
	if errors.Is(targetErr, state.ErrNotFound) {
		return rollbackVerdict{done: true, status: state.RollbackStatusFailed, finished: now,
			errorCode: "rollback_target_missing", errorText: "the rollback target deployment no longer exists"}
	}
	if done := target.RolloutCompletedAt; done != nil && !done.Before(r.RequestedAt.Add(-time.Second)) &&
		(target.Status == state.DeployLive || target.Status == state.DeploySuperseded) {
		recovery := done.Sub(r.RequestedAt)
		if recovery < 0 {
			recovery = 0
		}
		return rollbackVerdict{done: true, status: state.RollbackStatusComplete, recovery: recovery, finished: *done}
	}
	switch target.Status {
	case state.DeployFailed, state.DeployCancelled, state.DeploySuperseded:
		code, text := target.ErrorCode, target.Error
		if code == "" {
			code = "rollback_target_not_activated"
		}
		if text == "" {
			text = fmt.Sprintf("the rollback target returned to %s without serving", target.Status)
		}
		return rollbackVerdict{done: true, status: state.RollbackStatusFailed, finished: now, errorCode: code, errorText: text}
	}
	if now.Sub(r.RequestedAt) >= api.RollbackOutcomeTimeout {
		return rollbackVerdict{done: true, status: state.RollbackStatusFailed, finished: now,
			errorCode: "rollback_timeout", errorText: fmt.Sprintf("the rollback target did not serve within %s", api.RollbackOutcomeTimeout)}
	}
	return rollbackVerdict{}
}

// Run resolves preparing rollbacks every RollbackOutcomeCheckInterval until
// ctx ends.
func (s Rollbacks) Run(ctx context.Context) {
	ticker := time.NewTicker(api.RollbackOutcomeCheckInterval)
	defer ticker.Stop()
	for {
		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := s.Sweep(bounded, time.Now().UTC()); err != nil && ctx.Err() == nil {
			s.Log.Warn("rollback outcome sweep", "err", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Sweep resolves every preparing rollback whose target has served, failed, or
// run out of time.
func (s Rollbacks) Sweep(ctx context.Context, now time.Time) error {
	store, ok := s.Store.(state.RollbackOutcomeStore)
	if !ok {
		return nil
	}
	rows, err := store.PreparingRollbacks(ctx, api.RollbackOutcomeBatchSize)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := s.resolve(ctx, store, r, now); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.Log.Warn("rollback outcome: resolve", "rollback", r.ID, "target", r.TargetDeploymentID, "err", err)
		}
	}
	return nil
}

func (s Rollbacks) resolve(ctx context.Context, store state.RollbackOutcomeStore, r state.RollbackRecord, now time.Time) error {
	target, err := s.Store.DeploymentByID(ctx, r.TargetDeploymentID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return fmt.Errorf("load rollback target: %w", err)
	}
	verdict := decideRollbackOutcome(r, target, err, now)
	if !verdict.done {
		return nil
	}
	outcome := state.RollbackOutcome{
		Status: verdict.status, FinishedAt: verdict.finished,
		ErrorCode: verdict.errorCode, Error: verdict.errorText,
		Audit: rollbackOutcomeAudit(r, verdict, now),
	}
	if verdict.status == state.RollbackStatusComplete {
		ms := verdict.recovery.Milliseconds()
		outcome.RecoveryMS = &ms
	}
	record, finished, err := store.FinishRollback(ctx, r.ID, outcome)
	if err != nil || !finished {
		return err
	}
	if s.Metrics != nil {
		s.Metrics.RollbackOutcome(r.Trigger, verdict.status, verdict.recovery)
	}
	fields := map[string]any{
		"app_id": r.AppID, "rollback_id": r.ID, "from": r.FromDeploymentID, "to": r.TargetDeploymentID,
		"trigger": r.Trigger, "status": record.Status,
	}
	if verdict.status == state.RollbackStatusComplete {
		fields["recovery_ms"] = *outcome.RecoveryMS
		s.emit(ctx, "app.rollback_completed", r.AccountID, fields)
		s.Log.Info("rollback outcome: previous release restored", "app", r.AppID, "rollback", r.ID,
			"target", r.TargetDeploymentID, "trigger", r.Trigger, "recovery_ms", *outcome.RecoveryMS)
		return nil
	}
	fields["error_code"], fields["error"] = record.ErrorCode, record.Error
	s.emit(ctx, "app.rollback_failed", r.AccountID, fields)
	// A failed rollback leaves the release it meant to replace serving; an
	// auto-rollback failure means a known-bad release is still live.
	s.Log.Error("rollback outcome: previous release NOT restored", "app", r.AppID, "rollback", r.ID,
		"target", r.TargetDeploymentID, "trigger", r.Trigger, "error_code", record.ErrorCode, "error", record.Error)
	return nil
}

func (s Rollbacks) emit(ctx context.Context, kind, accountID string, fields map[string]any) {
	if s.Audit != nil {
		s.Audit.Emit(ctx, kind, &accountID, fields)
	}
}

func rollbackOutcomeAudit(r state.RollbackRecord, v rollbackVerdict, now time.Time) state.DeploymentAudit {
	data := struct {
		RollbackID string `json:"rollback_id"`
		From       string `json:"from"`
		To         string `json:"to"`
		Trigger    string `json:"trigger"`
		Phase      string `json:"phase"`
		RecoveryMS int64  `json:"recovery_ms,omitempty"`
		ErrorCode  string `json:"error_code,omitempty"`
	}{RollbackID: r.ID, From: r.FromDeploymentID, To: r.TargetDeploymentID, Trigger: r.Trigger,
		Phase: v.status, ErrorCode: v.errorCode}
	if v.status == state.RollbackStatusComplete {
		data.Phase = "completed"
		data.RecoveryMS = v.recovery.Milliseconds()
	}
	entry := state.DeploymentAudit{
		Kind: state.DeployRolledBack, Actor: "apid:rollback_outcome", At: now,
		Data: json.RawMessage(safetext.JSONObject(data)),
	}
	if id, err := uuid.Parse(r.TargetDeploymentID); err == nil {
		entry.DeploymentID = id
	}
	if id, err := uuid.Parse(r.AccountID); err == nil {
		entry.AccountID = &id
	}
	return entry
}
