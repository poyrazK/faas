package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ AlertRollbackStore = (*PgStore)(nil)

func optionalPgUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return mustPgUUID(id.String())
}
func alertRollbackFactsTx(ctx context.Context, tx pgx.Tx, ruleID string) (alertRollbackFacts, error) {
	var f alertRollbackFacts
	raw, err := sqlc.New().ReadAlertRollbackFireFacts(ctx, tx, mustPgUUID(ruleID))
	if err != nil {
		return f, mapErr(err)
	}
	err = json.Unmarshal(raw, &f)
	return f, err
}
func (s *PgStore) captureAlertRollbackTx(ctx context.Context, tx pgx.Tx, ruleID, id string, observed float64, at time.Time) error {
	f, err := alertRollbackFactsTx(ctx, tx, ruleID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if f.Action != AlertActionRollback || !alertRollbackMetricAllowed(f.Metric) {
		return nil
	}
	if !api.IsFiniteFloat(observed) {
		return ErrInvalidArgument
	}
	r := captureAlertRollback(id, f, observed, at)
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	appID := pgtype.UUID{}
	if r.AppID != "" {
		appID = mustPgUUID(r.AppID)
	}
	return sqlc.New().InsertAlertRollback(ctx, tx, sqlc.InsertAlertRollbackParams{FireID: mustPgUUID(id), AppID: appID, Status: r.Status, Receipt: raw})
}
func (s *PgStore) ReadAlertRollback(ctx context.Context, id string) (api.AlertRollback, error) {
	return decodeAlertRollback(sqlc.New().ReadAlertRollback(ctx, s.pool, mustPgUUID(id)))
}
func (s *PgStore) GetAlertRollback(ctx context.Context, acct, app, id string) (api.AlertRollback, error) {
	return decodeAlertRollback(sqlc.New().GetAlertRollback(ctx, s.pool, sqlc.GetAlertRollbackParams{FireID: mustPgUUID(id), AppID: mustPgUUID(app), AccountID: mustPgUUID(acct)}))
}
func decodeAlertRollbacks(rows [][]byte, err error) ([]api.AlertRollback, error) {
	if err != nil {
		return nil, err
	}
	out := []api.AlertRollback{}
	for _, raw := range rows {
		r, err := decodeAlertRollback(raw, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
func (s *PgStore) ListAlertRollbacks(ctx context.Context, acct, app string) ([]api.AlertRollback, error) {
	return decodeAlertRollbacks(sqlc.New().ListAlertRollbacks(ctx, s.pool, sqlc.ListAlertRollbacksParams{AppID: mustPgUUID(app), AccountID: mustPgUUID(acct), BatchSize: api.AlertRollbackBatchSize}))
}
func (s *PgStore) ListPendingAlertRollbacks(ctx context.Context) ([]api.AlertRollback, error) {
	return decodeAlertRollbacks(sqlc.New().ListPendingAlertRollbacks(ctx, s.pool, api.AlertRollbackBatchSize))
}
func saveAlertRollback(ctx context.Context, tx pgx.Tx, r api.AlertRollback) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return sqlc.New().SaveAlertRollback(ctx, tx, sqlc.SaveAlertRollbackParams{FireID: mustPgUUID(r.ID), Status: r.Status, Receipt: raw})
}
func (s *PgStore) UpdateAlertRollback(ctx context.Context, r api.AlertRollback, status, code string, blockers []api.BindingCheckFinding) error {
	if status != "blocked" && status != "failed" {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := decodeAlertRollback(sqlc.New().LockAlertRollback(ctx, tx, mustPgUUID(r.ID)))
	if err != nil {
		return err
	}
	if alertRollbackTerminal(current) || current.ServiceRequestID != r.ServiceRequestID || current.RollbackOperationID != r.RollbackOperationID {
		return nil
	}
	if err = saveAlertRollback(ctx, tx, alertRollbackProgress(current, status, code, blockers)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *PgStore) CommitAlertRollback(ctx context.Context, r api.AlertRollback) (api.AlertRollback, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return r, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = sqlc.New().LockCheckedRollbackApp(ctx, tx, sqlc.LockCheckedRollbackAppParams{AppID: mustPgUUID(r.AppID), AccountID: mustPgUUID(r.AccountID)}); err != nil {
		return r, mapErr(err)
	}
	current, err := decodeAlertRollback(sqlc.New().LockAlertRollback(ctx, tx, mustPgUUID(r.ID)))
	if err != nil {
		return r, err
	}
	if current.AppID != r.AppID || current.AccountID != r.AccountID {
		return r, ErrAlertRollbackChanged
	}
	if alertRollbackTerminal(current) {
		return current, nil
	}
	if current.ServiceRequestID != "" || current.RollbackOperationID != "" {
		return current, nil
	}
	if _, err = sqlc.New().LockAlertRollbackRule(ctx, tx, mustPgUUID(current.RuleID)); err != nil {
		return r, mapErr(err)
	}
	f, err := alertRollbackFactsTx(ctx, tx, current.RuleID)
	if err != nil {
		return r, err
	}
	fresh := captureAlertRollback(current.ID, f, current.ObservedValue, current.FiredAt)
	if !alertRollbackPairMatches(fresh, current) {
		return r, ErrAlertRollbackChanged
	}
	if current.Historical {
		current, err = s.requestHistoricalAlertRollbackTx(ctx, tx, current)
		if err != nil {
			return r, err
		}
		return current, tx.Commit(ctx)
	}
	if current.Service {
		current, err = s.requestServiceAlertRollbackTx(ctx, tx, current)
		if err != nil {
			return r, err
		}
		return current, tx.Commit(ctx)
	}
	audit := alertRollbackAudit(ctx, current)
	_, auditID, err := s.recoverRolloutTx(ctx, tx, current.AppID, current.CandidateDeploymentID, current.PredecessorDeploymentID, "abort", current.Reason, nil, &audit)
	if err != nil {
		return r, err
	}
	current = completeAlertRollback(current, auditID)
	if err = saveAlertRollback(ctx, tx, current); err != nil {
		return r, err
	}
	if err = sqlc.New().NotifyAlertRollbackTraffic(ctx, tx, sqlc.NotifyAlertRollbackTrafficParams{AppID: current.AppID, CandidateID: current.CandidateDeploymentID}); err != nil {
		return r, err
	}
	return current, tx.Commit(ctx)
}
