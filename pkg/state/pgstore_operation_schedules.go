// adr: 20261018161833
package state

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func operationScheduleFromPG(row sqlc.CustomerOperationSchedule) OperationSchedule {
	pgTime := func(t pgtype.Timestamptz) *time.Time {
		if !t.Valid {
			return nil
		}
		v := t.Time.UTC()
		return &v
	}
	out := OperationSchedule{AccountID: pgUUIDString(row.AccountID), Input: append([]byte(nil), row.Input...), LeaseToken: pgUUIDStringNullable(row.LeaseToken)}
	out.OperationScheduleResponse = api.OperationScheduleResponse{
		ID: pgUUIDString(row.ID), AppID: pgUUIDString(row.AppID), PlatformTenantID: pgUUIDString(row.PlatformTenantID), DefinitionID: pgUUIDString(row.DefinitionID),
		Kind: row.Kind, At: pgTime(row.At), Cron: row.Cron, Timezone: row.Timezone, State: row.State, NextFireAt: pgTime(row.NextFireAt), Fires: row.Fires,
		LastFiredAt: pgTime(row.LastFiredAt), LastOperationID: pgUUIDStringNullable(row.LastOperationID), LastError: row.LastError,
		CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
	}
	return out
}

func (s *PgStore) CreateOperationSchedule(ctx context.Context, in OperationScheduleInput) (OperationSchedule, error) {
	account, err := operationUUID(in.AccountID)
	if err != nil {
		return OperationSchedule{}, err
	}
	tenant, err := operationUUID(in.PlatformTenantID)
	if err != nil {
		return OperationSchedule{}, err
	}
	acct, err := s.AccountByID(ctx, in.AccountID)
	if err != nil {
		return OperationSchedule{}, err
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.Operations.Allowed || limits.Operations.SchedulesPerAccount <= 0 {
		return OperationSchedule{}, NewOperationLimitError("schedules_per_account", 0, 1)
	}
	now := in.Now.UTC().Truncate(time.Microsecond)
	kind, timezone, first, err := validateOperationScheduleRequest(in.Request, limits.Operations, now)
	if err != nil {
		return OperationSchedule{}, err
	}
	def, err := s.OperationDefinitionByID(ctx, in.AccountID, in.Request.DefinitionID)
	if err != nil {
		return OperationSchedule{}, err
	}
	if err := validateNewOperationInput(def, canonicalOperationScheduleInput(in.Request.Input), limits); err != nil {
		return OperationSchedule{}, err
	}
	customer, err := s.GetPlatformTenant(ctx, in.AccountID, in.PlatformTenantID)
	if err != nil {
		return OperationSchedule{}, err
	}
	if customer.Status != PlatformTenantActive {
		return OperationSchedule{}, ErrPlatformTenantSuspended
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return OperationSchedule{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	// The account row lock serializes the live-schedule count with the insert.
	if _, err := q.LockCustomerOperationAccount(ctx, tx, account); err != nil {
		return OperationSchedule{}, mapErr(err)
	}
	live, err := q.CountLiveOperationSchedules(ctx, tx, account)
	if err != nil {
		return OperationSchedule{}, err
	}
	if live >= int64(limits.Operations.SchedulesPerAccount) {
		return OperationSchedule{}, NewOperationLimitError("schedules_per_account", int64(limits.Operations.SchedulesPerAccount), live+1)
	}
	params := sqlc.InsertOperationScheduleParams{ID: mustPgUUID(uuid.NewString()), AccountID: account, AppID: mustPgUUID(def.AppID), PlatformTenantID: tenant,
		DefinitionID: mustPgUUID(def.ID), Input: canonicalOperationScheduleInput(in.Request.Input), Kind: kind, Cron: in.Request.Cron, Timezone: timezone,
		NextFireAt: pgtype.Timestamptz{Time: first, Valid: true}, Now: pgtype.Timestamptz{Time: now, Valid: true}}
	if kind == api.OperationScheduleOnce {
		params.At = pgtype.Timestamptz{Time: first, Valid: true}
	}
	row, err := q.InsertOperationSchedule(ctx, tx, params)
	if err != nil {
		return OperationSchedule{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return OperationSchedule{}, err
	}
	return operationScheduleFromPG(row), nil
}

func (s *PgStore) OperationSchedule(ctx context.Context, accountID, tenantID, id string) (OperationSchedule, error) {
	account, err := operationUUID(accountID)
	if err != nil {
		return OperationSchedule{}, err
	}
	scheduleID, err := uuid.Parse(id)
	if err != nil {
		return OperationSchedule{}, ErrNotFound
	}
	row, err := sqlc.New().GetOperationSchedule(ctx, s.pool, sqlc.GetOperationScheduleParams{ID: mustPgUUID(scheduleID.String()), AccountID: account, TenantID: tenantID})
	if err != nil {
		return OperationSchedule{}, mapErr(err)
	}
	return operationScheduleFromPG(row), nil
}

func (s *PgStore) ListOperationSchedules(ctx context.Context, accountID, tenantID, appID string) ([]OperationSchedule, error) {
	account, err := operationUUID(accountID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ListOperationSchedules(ctx, s.pool, sqlc.ListOperationSchedulesParams{AccountID: account, TenantID: tenantID, AppID: appID, RowLimit: operationScheduleListMax})
	if err != nil {
		return nil, err
	}
	out := make([]OperationSchedule, 0, len(rows))
	for _, row := range rows {
		out = append(out, operationScheduleFromPG(row))
	}
	return out, nil
}

func (s *PgStore) ChangeOperationSchedule(ctx context.Context, accountID, tenantID, id, action string, now time.Time) (OperationSchedule, error) {
	account, err := operationUUID(accountID)
	if err != nil {
		return OperationSchedule{}, err
	}
	scheduleID, err := uuid.Parse(id)
	if err != nil {
		return OperationSchedule{}, ErrNotFound
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return OperationSchedule{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.LockOperationSchedule(ctx, tx, sqlc.LockOperationScheduleParams{ID: mustPgUUID(scheduleID.String()), AccountID: account, TenantID: tenantID})
	if err != nil {
		return OperationSchedule{}, mapErr(err)
	}
	state, next, err := operationScheduleChange(operationScheduleFromPG(row), action, now.UTC())
	if err != nil {
		return OperationSchedule{}, err
	}
	row, err = q.SetOperationScheduleState(ctx, tx, sqlc.SetOperationScheduleStateParams{ID: row.ID, State: state, NextFireAt: nullableTimestamptzPtr(next), Now: pgtype.Timestamptz{Time: now.UTC(), Valid: true}})
	if err != nil {
		return OperationSchedule{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return OperationSchedule{}, err
	}
	return operationScheduleFromPG(row), nil
}

func (s *PgStore) ClaimDueOperationSchedules(ctx context.Context, now time.Time, limit int, lease time.Duration) ([]OperationSchedule, error) {
	if limit <= 0 || lease <= 0 {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ClaimDueOperationSchedules(ctx, s.pool, sqlc.ClaimDueOperationSchedulesParams{LeaseToken: mustPgUUID(uuid.NewString()),
		LeaseUntil: pgtype.Timestamptz{Time: now.Add(lease).UTC(), Valid: true}, Now: pgtype.Timestamptz{Time: now.UTC(), Valid: true}, RowLimit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]OperationSchedule, 0, len(rows))
	for _, row := range rows {
		out = append(out, operationScheduleFromPG(row))
	}
	return out, nil
}

// FireOperationSchedule admits the claimed schedule's Operation and advances
// the schedule in one transaction.
func (s *PgStore) FireOperationSchedule(ctx context.Context, fire OperationScheduleFire) (OperationSchedule, error) {
	scheduleID, err := operationUUID(fire.Schedule.ID)
	if err != nil {
		return OperationSchedule{}, err
	}
	lease, err := operationUUID(fire.Schedule.LeaseToken)
	if err != nil {
		return OperationSchedule{}, err
	}
	refusal := fire.Refusal
	// Read the immutable definition before taking a transaction connection, as
	// AdmitOperation does; the admission rechecks it inside the transaction.
	var hint OperationDefinition
	if refusal == "" {
		hint, err = s.OperationDefinitionByID(ctx, fire.Schedule.AccountID, fire.Schedule.DefinitionID)
		if reason, ok := operationScheduleRefusal(err); ok {
			refusal = reason
		} else if err != nil {
			return OperationSchedule{}, err
		}
	}
	now := fire.Now.UTC().Truncate(time.Microsecond)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return OperationSchedule{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.LockLeasedOperationSchedule(ctx, tx, sqlc.LockLeasedOperationScheduleParams{ID: scheduleID, LeaseToken: lease})
	if err != nil {
		return OperationSchedule{}, mapErr(err)
	}
	schedule := operationScheduleFromPG(row)
	fireAt := *schedule.NextFireAt
	operationID := ""
	if refusal == "" {
		var op Operation
		op, refusal, err = admitScheduledOperationTx(ctx, s, tx, schedule, fireAt, hint)
		if err != nil {
			return OperationSchedule{}, err
		}
		operationID = op.ID
	}
	state, next, err := operationScheduleAfterFire(schedule, refusal == "", now)
	if err != nil {
		return OperationSchedule{}, err
	}
	fired := int64(0)
	if refusal == "" {
		fired = 1
	}
	params := sqlc.RecordOperationScheduleFireParams{ID: scheduleID, State: state, NextFireAt: nullableTimestamptzPtr(next), Fired: fired,
		FiredAt: pgtype.Timestamptz{Time: fireAt, Valid: true}, LastError: truncateOperationScheduleError(refusal), Now: pgtype.Timestamptz{Time: now, Valid: true}}
	if operationID != "" {
		params.LastOperationID = mustPgUUID(operationID)
	}
	row, err = q.RecordOperationScheduleFire(ctx, tx, params)
	if err != nil {
		return OperationSchedule{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return OperationSchedule{}, err
	}
	return operationScheduleFromPG(row), nil
}

// admitScheduledOperationTx admits one fire in a savepoint, so a refused
// admission rolls back alone and the schedule still records the refusal.
func admitScheduledOperationTx(ctx context.Context, s *PgStore, tx pgx.Tx, schedule OperationSchedule, fireAt time.Time, hint OperationDefinition) (Operation, string, error) {
	savepoint, err := tx.Begin(ctx)
	if err != nil {
		return Operation{}, "", err
	}
	admission := OperationAdmission{AccountID: schedule.AccountID, DefinitionID: schedule.DefinitionID, PlatformTenantID: schedule.PlatformTenantID,
		IdempotencyKey: operationScheduleIdempotencyKey(schedule.ID, fireAt), Input: schedule.Input}
	op, _, err := s.admitOperationTx(ctx, savepoint, mustPgUUID(schedule.AccountID), mustPgUUID(schedule.DefinitionID), mustPgUUID(schedule.PlatformTenantID), admission, hint)
	if err != nil {
		_ = savepoint.Rollback(ctx)
		if refusal, ok := operationScheduleRefusal(err); ok {
			return Operation{}, refusal, nil
		}
		return Operation{}, "", fmt.Errorf("state: fire operation schedule: %w", err)
	}
	if err := savepoint.Commit(ctx); err != nil {
		return Operation{}, "", err
	}
	return op, "", nil
}

// canonicalOperationScheduleInput makes an omitted or null input the empty
// object, as a direct start without input would send.
func canonicalOperationScheduleInput(raw []byte) []byte {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return []byte("{}")
	}
	return append([]byte(nil), raw...)
}
