package state

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ServiceAlertRollbackStore = (*PgStore)(nil)

func appendAlertServiceAudit(ctx context.Context, tx pgx.Tx, r api.AlertRollback, phase string) (int64, error) {
	a := alertServiceRollbackAudit(ctx, r, phase)
	return sqlc.New().AppendRolloutRecoveryAudit(ctx, tx, sqlc.AppendRolloutRecoveryAuditParams{DeploymentID: mustPgUUID(a.DeploymentID.String()), AccountID: optionalPgUUID(a.AccountID), AlertRuleID: optionalPgUUID(a.AlertRuleID), Kind: string(a.Kind), Actor: a.Actor, At: pgtype.Timestamptz{Time: a.At, Valid: true}, Data: a.Data})
}

func (s *PgStore) requestServiceAlertRollbackTx(ctx context.Context, tx pgx.Tx, r api.AlertRollback) (api.AlertRollback, error) {
	target, rows, err := s.loadAndLockServiceRollout(ctx, tx, r.CandidateDeploymentID)
	if err != nil {
		return r, err
	}
	target.ServiceRolloutHandoff.PredecessorDeploymentID = r.PredecessorDeploymentID
	previous, found := previousServiceRolloutRow(target, rows)
	if !found || previous.id != r.PredecessorDeploymentID {
		return r, ErrAlertRollbackChanged
	}
	r, target, err = queueAlertServiceRollback(r, target)
	if err != nil {
		return r, err
	}
	if err = saveServiceHandoff(ctx, tx, target); err != nil {
		return r, err
	}
	auditID, err := appendAlertServiceAudit(ctx, tx, r, "requested")
	if err != nil {
		return r, err
	}
	r.AuditID = strconv.FormatInt(auditID, 10)
	if err = saveAlertRollback(ctx, tx, r); err != nil {
		return r, err
	}
	err = sqlc.New().NotifyAlertServiceRollback(ctx, tx, sqlc.NotifyAlertServiceRollbackParams{AppID: r.AppID, CandidateID: r.CandidateDeploymentID})
	return r, err
}

func (s *PgStore) RefreshServiceAlertRollback(ctx context.Context, snapshot api.AlertRollback) (api.AlertRollback, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return snapshot, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = sqlc.New().LockCheckedRollbackApp(ctx, tx, sqlc.LockCheckedRollbackAppParams{AppID: mustPgUUID(snapshot.AppID), AccountID: mustPgUUID(snapshot.AccountID)}); err != nil {
		return snapshot, mapErr(err)
	}
	r, err := decodeAlertRollback(sqlc.New().LockAlertRollback(ctx, tx, mustPgUUID(snapshot.ID)))
	if err != nil {
		return r, err
	}
	if alertRollbackTerminal(r) {
		return r, nil
	}
	if !r.Service || r.ServiceRequestID == "" || r.AppID != snapshot.AppID || r.AccountID != snapshot.AccountID {
		return r, ErrAlertRollbackChanged
	}
	candidate, err := rollbackFacts(ctx, tx, r.AppID, r.CandidateDeploymentID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return r, err
	}
	predecessor, err := rollbackFacts(ctx, tx, r.AppID, r.PredecessorDeploymentID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return r, err
	}
	r = projectAlertServiceRollback(r, candidate, predecessor)
	if r.Status == "complete" {
		auditID, err := appendAlertServiceAudit(ctx, tx, r, "complete")
		if err != nil {
			return r, err
		}
		r = finishAlertServiceRollback(r, auditID)
	}
	if err = saveAlertRollback(ctx, tx, r); err != nil {
		return r, err
	}
	return r, tx.Commit(ctx)
}
