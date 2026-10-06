package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ HistoricalAlertRollbackStore = (*PgStore)(nil)

func appendHistoricalAlertAudit(ctx context.Context, tx pgx.Tx, r api.AlertRollback, phase string) (int64, error) {
	a := historicalAlertAudit(ctx, r, phase)
	return sqlc.New().AppendRolloutRecoveryAudit(ctx, tx, sqlc.AppendRolloutRecoveryAuditParams{DeploymentID: mustPgUUID(a.DeploymentID.String()), AccountID: optionalPgUUID(a.AccountID), AlertRuleID: optionalPgUUID(a.AlertRuleID), Kind: string(a.Kind), Actor: a.Actor, At: pgtype.Timestamptz{Time: a.At, Valid: true}, Data: a.Data})
}
func (s *PgStore) requestHistoricalAlertRollbackTx(ctx context.Context, tx pgx.Tx, r api.AlertRollback) (api.AlertRollback, error) {
	f, err := alertRollbackFactsTx(ctx, tx, r.RuleID)
	if err != nil {
		return r, err
	}
	if !historicalAlertWindowOpen(r, f, time.Now().UTC()) {
		return r, ErrAlertRollbackChanged
	}
	r, qualified, err := historicalAlertEvidenceTx(ctx, tx, r)
	if err != nil {
		return r, err
	}
	if !qualified {
		return r, saveAlertRollback(ctx, tx, r)
	}
	won, err := sqlc.New().ClaimHistoricalAlertRollback(ctx, tx, sqlc.ClaimHistoricalAlertRollbackParams{DeploymentID: mustPgUUID(r.CandidateDeploymentID), FireID: mustPgUUID(r.ID)})
	if err != nil {
		return r, err
	}
	if won != 1 {
		return r, ErrAlertRollbackChanged
	}
	op, err := s.createCheckedRollbackTx(ctx, tx, r.AccountID, r.AppID, r.PredecessorDeploymentID, r.CandidateDeploymentID, r.Reason, r.ID)
	if err != nil {
		return r, err
	}
	// Target preparation can wait on deployment locks. Recheck the clock and
	// cutover under those locks; failure rolls back the claim and preparation.
	f, err = alertRollbackFactsTx(ctx, tx, r.RuleID)
	if err != nil {
		return r, err
	}
	now := time.Now().UTC()
	if !now.Before(r.FiredAt.Add(api.AlertRollbackEvidenceMaxCheckDelay)) {
		return r, ErrAlertRollbackEvidenceExpired
	}
	if !historicalAlertCutoverMatches(r, f) || !historicalAlertWindowOpen(r, f, now) {
		return r, ErrAlertRollbackChanged
	}
	r = alertRollbackProgress(r, "pending", "", nil)
	r.RollbackOperationID, r.RollbackPhase = op.ID, op.Status
	if _, err = appendHistoricalAlertAudit(ctx, tx, r, "intent"); err != nil {
		return r, err
	}
	return r, saveAlertRollback(ctx, tx, r)
}

func historicalAlertEvidenceTx(ctx context.Context, tx pgx.Tx, r api.AlertRollback) (api.AlertRollback, bool, error) {
	var requests, serverErrors int64
	var last *time.Time
	e := r.DeploymentEvidence
	if historicalAlertEvidenceSupported(e) && e.WindowStart.Before(e.WindowEnd) {
		row, err := sqlc.New().ReadHistoricalAlertDeploymentEvidence(ctx, tx, sqlc.ReadHistoricalAlertDeploymentEvidenceParams{
			AccountID: mustPgUUID(r.AccountID), AppID: mustPgUUID(r.AppID), DeploymentID: mustPgUUID(r.CandidateDeploymentID),
			WindowStart: pgtype.Timestamptz{Time: e.WindowStart, Valid: true}, WindowEnd: pgtype.Timestamptz{Time: e.WindowEnd, Valid: true}})
		if err != nil {
			return r, false, err
		}
		requests, serverErrors = row.Requests, row.ServerErrors
		if row.LastSampleAt.Valid {
			last = &row.LastSampleAt.Time
		}
	}
	r, qualified := qualifyHistoricalAlertEvidence(r, requests, serverErrors, last, true, time.Now().UTC())
	return r, qualified, nil
}
func (s *PgStore) RefreshHistoricalAlertRollback(ctx context.Context, snapshot api.AlertRollback) (api.AlertRollback, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return snapshot, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.LockCheckedRollbackApp(ctx, tx, sqlc.LockCheckedRollbackAppParams{AppID: mustPgUUID(snapshot.AppID), AccountID: mustPgUUID(snapshot.AccountID)}); err != nil {
		return snapshot, mapErr(err)
	}
	r, err := decodeAlertRollback(q.LockAlertRollback(ctx, tx, mustPgUUID(snapshot.ID)))
	if err != nil {
		return r, err
	}
	if alertRollbackTerminal(r) {
		return r, nil
	}
	if !r.Historical || r.RollbackOperationID == "" || r.AppID != snapshot.AppID || r.AccountID != snapshot.AccountID {
		return r, ErrAlertRollbackChanged
	}
	op, err := decodeCheckedRollback(q.LockCheckedRollback(ctx, tx, sqlc.LockCheckedRollbackParams{ID: mustPgUUID(r.RollbackOperationID), AppID: mustPgUUID(r.AppID)}))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return r, err
	}
	candidate, err := rollbackFacts(ctx, tx, r.AppID, r.CandidateDeploymentID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return r, err
	}
	target, err := rollbackFacts(ctx, tx, r.AppID, r.PredecessorDeploymentID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return r, err
	}
	r = projectHistoricalAlertRollback(r, op, candidate, target)
	if r.Status == "complete" {
		id, err := appendHistoricalAlertAudit(ctx, tx, r, "complete")
		if err != nil {
			return r, err
		}
		r = completeAlertRollback(r, id)
	}
	if err = saveAlertRollback(ctx, tx, r); err != nil {
		return r, err
	}
	return r, tx.Commit(ctx)
}
