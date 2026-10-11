package state

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ CheckedRollbackStore = (*PgStore)(nil)

func (s *PgStore) GetCheckedRollback(ctx context.Context, acct, app, id string) (api.RollbackOperation, error) {
	return decodeCheckedRollback(sqlc.New().ReadCheckedRollback(ctx, s.pool, sqlc.ReadCheckedRollbackParams{ID: mustPgUUID(id), AppID: mustPgUUID(app), AccountID: mustPgUUID(acct)}))
}
func (s *PgStore) CheckedRollbackForTarget(ctx context.Context, id string) (api.RollbackOperation, error) {
	return decodeCheckedRollback(sqlc.New().CheckedRollbackForTarget(ctx, s.pool, mustPgUUID(id)))
}
func (s *PgStore) ListPendingCheckedRollbacks(ctx context.Context) ([]api.RollbackOperation, error) {
	rows, err := sqlc.New().ListPendingCheckedRollbacks(ctx, s.pool, api.ServiceBindingCheckBatchSize)
	if err != nil {
		return nil, err
	}
	out := []api.RollbackOperation{}
	for _, raw := range rows {
		r, err := decodeCheckedRollback(raw, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
func saveCheckedRollback(ctx context.Context, tx pgx.Tx, r api.RollbackOperation) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return sqlc.New().SaveCheckedRollback(ctx, tx, sqlc.SaveCheckedRollbackParams{ID: mustPgUUID(r.ID), Status: r.Status, Receipt: raw})
}
func rollbackFacts(ctx context.Context, tx pgx.Tx, app, id string) (Deployment, error) {
	raw, err := sqlc.New().CheckedRollbackTargetFacts(ctx, tx, sqlc.CheckedRollbackTargetFactsParams{DeploymentID: mustPgUUID(id), AppID: mustPgUUID(app)})
	if err != nil {
		return Deployment{}, mapErr(err)
	}

	var facts struct {
		ID               string                `json:"id"`
		AppID            string                `json:"app_id"`
		Scope            string                `json:"scope"`
		Status           DeploymentStatus      `json:"status"`
		TrafficPercent   int                   `json:"traffic_percent"`
		CanaryTotalSteps int                   `json:"canary_total_steps"`
		CanaryStep       int                   `json:"canary_step"`
		RolloutState     string                `json:"rollout_state"`
		RootfsKey        string                `json:"rootfs_key"`
		ImageDigest      string                `json:"image_digest"`
		Runtime          json.RawMessage       `json:"environment_workload_runtime"`
		Handoff          ServiceRolloutHandoff `json:"service_rollout_handoff"`
	}
	err = json.Unmarshal(raw, &facts)
	d := Deployment{ID: facts.ID, AppID: facts.AppID, Scope: facts.Scope, Status: facts.Status, TrafficPercent: facts.TrafficPercent, CanaryTotalSteps: facts.CanaryTotalSteps, CanaryStep: facts.CanaryStep, RolloutState: facts.RolloutState, RootfsKey: facts.RootfsKey, ImageDigest: facts.ImageDigest, ServiceRolloutHandoff: facts.Handoff}
	if len(facts.Runtime) > 0 && string(facts.Runtime) != "null" && string(facts.Runtime) != "{}" {
		d.EnvironmentWorkloadRuntime = string(facts.Runtime)
	}

	return d, err
}
func rollbackCurrentMatches(ctx context.Context, tx pgx.Tx, r api.RollbackOperation) error {
	matches, err := sqlc.New().CheckedRollbackCurrentMatches(ctx, tx, sqlc.CheckedRollbackCurrentMatchesParams{CurrentID: mustPgUUID(r.CurrentDeploymentID), AppID: mustPgUUID(r.AppID), Scope: r.Scope, TargetID: mustPgUUID(r.TargetDeploymentID)})
	if err != nil {
		return err
	}
	if !matches.Bool {
		return ErrCheckedRollbackChanged
	}
	return nil
}
func (s *PgStore) lockRollbackApp(ctx context.Context, tx pgx.Tx, app string) error {
	a, err := s.AppByID(ctx, app)
	if err != nil {
		return err
	}
	_, err = sqlc.New().LockCheckedRollbackApp(ctx, tx, sqlc.LockCheckedRollbackAppParams{AppID: mustPgUUID(app), AccountID: mustPgUUID(a.AccountID)})
	return mapErr(err)
}
func (s *PgStore) CreateCheckedRollback(ctx context.Context, acct, app, target, current, reason string) (api.RollbackOperation, error) {
	if err := validateCheckedRollbackArguments(acct, app, target, current, reason); err != nil {
		return api.RollbackOperation{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.RollbackOperation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	r, err := s.createCheckedRollbackTx(ctx, tx, acct, app, target, current, reason, "")
	if err != nil {
		return r, err
	}
	return r, tx.Commit(ctx)
}
func (s *PgStore) createCheckedRollbackTx(ctx context.Context, tx pgx.Tx, acct, app, target, current, reason, id string) (api.RollbackOperation, error) {
	q := sqlc.New()
	if _, err := q.LockCheckedRollbackApp(ctx, tx, sqlc.LockCheckedRollbackAppParams{AppID: mustPgUUID(app), AccountID: mustPgUUID(acct)}); err != nil {
		return api.RollbackOperation{}, mapErr(err)
	}
	d, err := rollbackFacts(ctx, tx, app, target)
	if err != nil {
		return api.RollbackOperation{}, err
	}
	if d.EnvironmentWorkloadHeld() || d.Status != DeploySuperseded && (d.Status != DeployLive || d.TrafficPercent != 0) {
		return api.RollbackOperation{}, ErrNoRollbackTarget
	}
	a, err := s.AppByID(ctx, app)
	if err != nil {
		return api.RollbackOperation{}, err
	}
	r := newCheckedRollback(app, d.Scope, target, current, reason, a.Manifest.ExecutionMode == api.ExecutionModeService)
	if id != "" {
		r.ID = id
	}
	if _, err := sqlc.New().LockCheckedRollbackScope(ctx, tx, sqlc.LockCheckedRollbackScopeParams{AppID: mustPgUUID(r.AppID), Scope: r.Scope}); err != nil {
		return r, err
	}
	if err := rollbackCurrentMatches(ctx, tx, r); err != nil {
		return r, err
	}
	raw, _ := json.Marshal(r)
	err = q.InsertCheckedRollback(ctx, tx, sqlc.InsertCheckedRollbackParams{ID: mustPgUUID(r.ID), AppID: mustPgUUID(app), Scope: r.Scope, TargetID: mustPgUUID(target), CurrentID: mustPgUUID(current), Status: r.Status, Receipt: raw})
	if err != nil {
		return r, mapErr(err)
	}
	handoff, _ := json.Marshal(rollbackHandoff(r))
	rolloutState := "pending"
	if r.Service {
		rolloutState = "rolling_out"
	}
	if err = q.PrepareCheckedRollbackTarget(ctx, tx, sqlc.PrepareCheckedRollbackTargetParams{TargetID: mustPgUUID(target), RolloutState: rolloutState, Handoff: handoff}); err != nil {
		return r, err
	}
	payload, _ := json.Marshal(map[string]string{"app_id": app, "deployment_id": target})
	if err = db.EnqueueDurableNotificationTx(ctx, tx, db.NotifySnapshotPrime, string(payload)); err != nil {
		return r, err
	}
	return r, nil
}
func (s *PgStore) markCheckedRollbackReadyTx(ctx context.Context, tx pgx.Tx, d Deployment, r api.RollbackOperation) error {
	locked, err := decodeCheckedRollback(sqlc.New().LockCheckedRollback(ctx, tx, sqlc.LockCheckedRollbackParams{ID: mustPgUUID(r.ID), AppID: mustPgUUID(r.AppID)}))
	if err != nil {
		return err
	}
	r = locked
	if r.Status != "preparing" || d.Status != DeploySnapshotting || d.TrafficPercent != 0 {
		return ErrCheckedRollbackChanged
	}
	if _, err = sqlc.New().LockCheckedRollbackScope(ctx, tx, sqlc.LockCheckedRollbackScopeParams{AppID: mustPgUUID(r.AppID), Scope: r.Scope}); err != nil {
		return err
	}
	if err = rollbackCurrentMatches(ctx, tx, r); err != nil {
		return err
	}
	if err = sqlc.New().MarkCheckedRollbackReady(ctx, tx, mustPgUUID(d.ID)); err != nil {
		return err
	}
	snap, policy, err := s.captureDeploymentOpenAPISnapshotTx(ctx, tx, d)
	if err != nil {
		return err
	}
	if err = persistDeploymentSnapshotsDBTX(ctx, tx, snap, policy, true); err != nil {
		return err
	}
	r.Status = "ready"
	r.UpdatedAt = time.Now().UTC()
	return saveCheckedRollback(ctx, tx, r)
}
func (s *PgStore) CommitCheckedRollback(ctx context.Context, snapshot api.RollbackOperation) (api.RollbackOperation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return snapshot, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = pgLockProductionLifecycleApp(ctx, tx, snapshot.AppID); err != nil {
		return snapshot, err
	}
	if err = s.lockRollbackApp(ctx, tx, snapshot.AppID); err != nil {
		return snapshot, err
	}
	q := sqlc.New()
	r, err := decodeCheckedRollback(q.LockCheckedRollback(ctx, tx, sqlc.LockCheckedRollbackParams{ID: mustPgUUID(snapshot.ID), AppID: mustPgUUID(snapshot.AppID)}))
	if err != nil {
		return r, err
	}
	if r.TargetDeploymentID != snapshot.TargetDeploymentID || r.CurrentDeploymentID != snapshot.CurrentDeploymentID || r.Status != "ready" && r.Status != "blocked" {
		return r, ErrCheckedRollbackChanged
	}
	d, err := rollbackFacts(ctx, tx, r.AppID, r.TargetDeploymentID)
	if err != nil {
		return r, err
	}
	if d.Status != DeployLive || d.TrafficPercent != 0 {
		return r, ErrCheckedRollbackChanged
	}
	if _, err = q.LockCheckedRollbackScope(ctx, tx, sqlc.LockCheckedRollbackScopeParams{AppID: mustPgUUID(r.AppID), Scope: r.Scope}); err != nil {
		return r, err
	}
	if err = rollbackCurrentMatches(ctx, tx, r); err != nil {
		return r, err
	}
	ctx = withCheckedRollback(ctx, r.ID)
	if _, err = q.AuthorizeCheckedRollback(ctx, tx, r.ID); err != nil {
		return r, err
	}
	if err = s.authorizeProductionLifecycle(ctx, tx, d.ID, false); err != nil {
		return r, err
	}
	if err = pgAuthorizeBindingRelease(ctx, tx); err != nil {
		return r, err
	}
	if r.Service {
		d, err = s.beginServiceRolloutCutoverTx(WithServiceRolloutBindingRequest(ctx, r.ID), tx, d.ID)
		if err != nil {
			return r, err
		}
		r.Status = "routing"
		r.AuditID = d.ServiceRolloutHandoff.BindingsCheck.AuditID
	} else {
		if err = q.RetainCheckedRollbackPredecessor(ctx, tx, mustPgUUID(r.CurrentDeploymentID)); err != nil {
			return r, err
		}
		if err = q.CutoverCheckedRollbackTarget(ctx, tx, mustPgUUID(d.ID)); err != nil {
			return r, mapErr(err)
		}
		if err = q.RetireCheckedRollbackSiblings(ctx, tx, sqlc.RetireCheckedRollbackSiblingsParams{AppID: mustPgUUID(r.AppID), Scope: r.Scope, TargetID: mustPgUUID(d.ID)}); err != nil {
			return r, err
		}
		data, _ := json.Marshal(map[string]any{"request_id": r.ID, "target_deployment_id": r.TargetDeploymentID, "current_deployment_id": r.CurrentDeploymentID, "phase": "complete", "reason": r.Reason, "binding_fences": bindingReleaseFences(ctx)})
		audit, err := q.AppendServiceRolloutBindingAudit(ctx, tx, sqlc.AppendServiceRolloutBindingAuditParams{DeploymentID: mustPgUUID(d.ID), AppID: mustPgUUID(r.AppID), Kind: string(DeployRolledBack), Actor: "apid:checked_rollback", Data: data})
		if err != nil {
			return r, err
		}
		r.Status = "complete"
		now := time.Now().UTC()
		r.CompletedAt = &now
		r.AuditID = strconv.FormatInt(audit, 10)
	}
	r.Code = ""
	r.Blockers = nil
	r.UpdatedAt = time.Now().UTC()
	if err = saveCheckedRollback(ctx, tx, r); err != nil {
		return r, err
	}
	if err = q.NotifyCheckedRollbackCutover(ctx, tx, sqlc.NotifyCheckedRollbackCutoverParams{AppID: r.AppID, TargetID: r.TargetDeploymentID, CurrentID: r.CurrentDeploymentID}); err != nil {
		return r, err
	}

	if err = tx.Commit(ctx); err != nil {
		return r, fmt.Errorf("commit checked rollback: %w", err)
	}
	return r, nil
}
func (s *PgStore) UpdateCheckedRollback(ctx context.Context, snapshot api.RollbackOperation, status, code string, blockers []api.BindingCheckFinding) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = s.lockRollbackApp(ctx, tx, snapshot.AppID); err != nil {
		return err
	}
	q := sqlc.New()
	r, err := decodeCheckedRollback(q.LockCheckedRollback(ctx, tx, sqlc.LockCheckedRollbackParams{ID: mustPgUUID(snapshot.ID), AppID: mustPgUUID(snapshot.AppID)}))
	if err != nil {
		return err
	}
	if r.Status != snapshot.Status || rollbackTerminal(r) || !validRollbackStatusUpdate(r, status) {
		return ErrCheckedRollbackChanged
	}
	d, err := rollbackFacts(ctx, tx, r.AppID, r.TargetDeploymentID)
	if err != nil {
		return err
	}
	h := d.ServiceRolloutHandoff
	if status == "complete" && (r.Status != "routing" || d.Status != DeployLive || d.TrafficPercent != 100 || d.RolloutState != "complete" || h.Phase != "complete" || h.Action != "promote" || h.PredecessorDeploymentID != r.CurrentDeploymentID || h.BindingsCheck == nil || h.BindingsCheck.RequestID != r.ID || h.BindingsCheck.AuditID != r.AuditID) {
		return ErrCheckedRollbackChanged
	}
	if status == "failed" && d.TrafficPercent == 0 {
		if err = q.FailCheckedRollbackTarget(ctx, tx, mustPgUUID(d.ID)); err != nil {
			return err
		}
	}
	r = blockedRollback(r, status, code, blockers)
	if err = saveCheckedRollback(ctx, tx, r); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
