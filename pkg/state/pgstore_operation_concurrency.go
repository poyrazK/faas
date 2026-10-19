// adr: 20261018210500
package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func operationConcurrencyPolicyFromPG(row sqlc.CustomerOperationConcurrencyPolicy) api.OperationConcurrencyPolicy {
	return api.OperationConcurrencyPolicy{ID: pgUUIDString(row.ID), AppID: pgUUIDString(row.AppID), Scope: row.Scope, Operation: row.Operation,
		PlatformTenantID: pgUUIDStringNullable(row.PlatformTenantID), MaxRunning: int(row.MaxRunning),
		CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC()}
}

// operationConcurrencyLimitTx returns the customer's effective limit. The
// caller holds the account's Operation lock.
func operationConcurrencyLimitTx(ctx context.Context, tx pgx.Tx, accountID, appID, scope, name, tenantID string) (int, bool, error) {
	if !api.OperationConcurrencyEnabled(accountID) {
		return 0, false, nil
	}
	limit, err := sqlc.New().EffectiveOperationConcurrencyLimit(ctx, tx, sqlc.EffectiveOperationConcurrencyLimitParams{AppID: mustPgUUID(appID),
		Scope: scope, Operation: name, PlatformTenantID: mustPgUUID(tenantID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return int(limit), true, nil
}

func operationInFlightTx(ctx context.Context, tx pgx.Tx, appID, scope, name, tenantID string) (int64, error) {
	return sqlc.New().CountOperationsInFlight(ctx, tx, sqlc.CountOperationsInFlightParams{PlatformTenantID: mustPgUUID(tenantID),
		AppID: mustPgUUID(appID), Scope: scope, Operation: name})
}

// operationConcurrencyHoldTx decides, under admission's account lock, whether
// a new Operation queues.
func operationConcurrencyHoldTx(ctx context.Context, tx pgx.Tx, op Operation, def OperationDefinition) (bool, error) {
	if !operationConcurrencyApplies(def) {
		return false, nil
	}
	limit, limited, err := operationConcurrencyLimitTx(ctx, tx, op.AccountID, def.AppID, def.Scope, def.Spec.Name, op.PlatformTenantID)
	if err != nil || !limited {
		return false, err
	}
	queued, err := sqlc.New().CountQueuedOperations(ctx, tx, sqlc.CountQueuedOperationsParams{AppID: mustPgUUID(def.AppID), Scope: def.Scope,
		Operation: def.Spec.Name, PlatformTenantID: mustPgUUID(op.PlatformTenantID)})
	if err != nil {
		return false, err
	}
	inFlight, err := operationInFlightTx(ctx, tx, def.AppID, def.Scope, def.Spec.Name, op.PlatformTenantID)
	if err != nil {
		return false, err
	}
	return operationConcurrencyHold(limit, limited, inFlight, queued), nil
}

func queueOperationTx(ctx context.Context, tx pgx.Tx, op Operation, def OperationDefinition) error {
	return sqlc.New().InsertQueuedOperation(ctx, tx, sqlc.InsertQueuedOperationParams{OperationID: mustPgUUID(op.ID), AccountID: mustPgUUID(op.AccountID),
		AppID: mustPgUUID(def.AppID), Scope: def.Scope, Operation: def.Spec.Name, PlatformTenantID: mustPgUUID(op.PlatformTenantID),
		QueuedAt: pgtype.Timestamptz{Time: op.CreatedAt, Valid: true}})
}

func (s *PgStore) PutOperationConcurrencyPolicy(ctx context.Context, in OperationConcurrencyPolicyInput) (api.OperationConcurrencyPolicy, bool, error) {
	scope, err := validateOperationConcurrencyRequest(in.Request)
	if err != nil {
		return api.OperationConcurrencyPolicy{}, false, err
	}
	account, err := operationUUID(in.AccountID)
	if err != nil {
		return api.OperationConcurrencyPolicy{}, false, err
	}
	acct, err := s.AccountByID(ctx, in.AccountID)
	if err != nil {
		return api.OperationConcurrencyPolicy{}, false, err
	}
	if in.Request.PlatformTenantID != "" {
		if _, err := s.GetPlatformTenant(ctx, in.AccountID, in.Request.PlatformTenantID); err != nil {
			return api.OperationConcurrencyPolicy{}, false, err
		}
	}
	now := pgtype.Timestamptz{Time: in.Now.UTC().Truncate(time.Microsecond), Valid: true}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return api.OperationConcurrencyPolicy{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.LockCustomerOperationAccount(ctx, tx, account); err != nil {
		return api.OperationConcurrencyPolicy{}, false, mapErr(err)
	}
	app := mustPgUUID(in.AppID)
	row, err := q.GetOperationConcurrencyPolicyByKey(ctx, tx, sqlc.GetOperationConcurrencyPolicyByKeyParams{AppID: app, Scope: scope,
		Operation: in.Request.Operation, TenantID: in.Request.PlatformTenantID})
	created := errors.Is(err, pgx.ErrNoRows)
	switch {
	case err == nil:
		row, err = q.UpdateOperationConcurrencyPolicy(ctx, tx, sqlc.UpdateOperationConcurrencyPolicyParams{ID: row.ID, MaxRunning: int32(in.Request.MaxRunning), Now: now})
	case created:
		limit := api.MustLimitsFor(acct.Plan).Operations.ConcurrencyPoliciesPerApp
		var count int64
		if count, err = q.CountOperationConcurrencyPolicies(ctx, tx, app); err == nil && count >= int64(limit) {
			return api.OperationConcurrencyPolicy{}, false, NewOperationLimitError("concurrency_policies_per_app", int64(limit), count+1)
		}
		if err == nil {
			var tenant pgtype.UUID
			if in.Request.PlatformTenantID != "" {
				tenant = mustPgUUID(in.Request.PlatformTenantID)
			}
			row, err = q.InsertOperationConcurrencyPolicy(ctx, tx, sqlc.InsertOperationConcurrencyPolicyParams{ID: mustPgUUID(uuid.NewString()), AccountID: account,
				AppID: app, Scope: scope, Operation: in.Request.Operation, PlatformTenantID: tenant, MaxRunning: int32(in.Request.MaxRunning), Now: now})
		}
	}
	if err != nil {
		return api.OperationConcurrencyPolicy{}, false, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return api.OperationConcurrencyPolicy{}, false, err
	}
	return operationConcurrencyPolicyFromPG(row), created, nil
}

func (s *PgStore) DeleteOperationConcurrencyPolicy(ctx context.Context, accountID, appID, id string) error {
	policy, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	n, err := sqlc.New().DeleteOperationConcurrencyPolicy(ctx, s.pool, sqlc.DeleteOperationConcurrencyPolicyParams{ID: mustPgUUID(policy.String()),
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID)})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) OperationConcurrency(ctx context.Context, accountID, appID string) (api.OperationConcurrencyResponse, error) {
	q := sqlc.New()
	rows, err := q.ListOperationConcurrencyPolicies(ctx, s.pool, sqlc.ListOperationConcurrencyPoliciesParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID)})
	if err != nil {
		return api.OperationConcurrencyResponse{}, err
	}
	out := api.OperationConcurrencyResponse{Policies: make([]api.OperationConcurrencyPolicy, 0, len(rows)), Queues: []api.OperationConcurrencyUsage{}}
	for _, row := range rows {
		out.Policies = append(out.Policies, operationConcurrencyPolicyFromPG(row))
	}
	counts, err := q.ListAppOperationQueueCounts(ctx, s.pool, sqlc.ListAppOperationQueueCountsParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), RowLimit: operationQueueUsageGroups})
	if err != nil {
		return api.OperationConcurrencyResponse{}, err
	}
	for _, c := range counts {
		usage, err := s.operationQueueUsage(ctx, accountID, appID, c)
		if err != nil {
			return api.OperationConcurrencyResponse{}, err
		}
		out.Queues = append(out.Queues, usage)
	}
	return out, nil
}

func (s *PgStore) operationQueueUsage(ctx context.Context, accountID, appID string, c sqlc.ListAppOperationQueueCountsRow) (api.OperationConcurrencyUsage, error) {
	tenant := pgUUIDString(c.PlatformTenantID)
	usage := api.OperationConcurrencyUsage{Scope: c.Scope, Operation: c.Operation, PlatformTenantID: tenant, Queued: c.Queued}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return usage, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	limit, limited, err := operationConcurrencyLimitTx(ctx, tx, accountID, appID, c.Scope, c.Operation, tenant)
	if err != nil {
		return usage, err
	}
	if limited {
		usage.Limit = limit
	}
	usage.InFlight, err = operationInFlightTx(ctx, tx, appID, c.Scope, c.Operation, tenant)
	return usage, err
}

func (s *PgStore) CustomerOperationQueue(ctx context.Context, accountID, tenantID string) ([]api.OperationQueueEntry, error) {
	if _, err := uuid.Parse(tenantID); err != nil {
		return nil, ErrNotFound
	}
	rows, err := sqlc.New().ListCustomerQueuedOperations(ctx, s.pool, sqlc.ListCustomerQueuedOperationsParams{AccountID: mustPgUUID(accountID),
		PlatformTenantID: mustPgUUID(tenantID), RowLimit: operationQueueGroupRows})
	if err != nil {
		return nil, err
	}
	out := make([]api.OperationQueueEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, api.OperationQueueEntry{OperationID: pgUUIDString(row.OperationID), AppID: pgUUIDString(row.AppID), Scope: row.Scope,
			Operation: row.Operation, Position: row.Position, QueuedAt: row.QueuedAt.Time.UTC()})
	}
	return out, nil
}

// ReleaseQueuedOperations walks every customer queue. Each queue releases in
// its own transaction under the account's Operation lock, the lock admission
// takes, so a release never overshoots a limit admission is also filling.
func (s *PgStore) ReleaseQueuedOperations(ctx context.Context, now time.Time) (int, error) {
	groups, err := sqlc.New().ListOperationQueueGroups(ctx, s.pool, operationQueueUsageGroups)
	if err != nil {
		return 0, err
	}
	released := 0
	var errs error
	for _, group := range groups {
		n, err := s.releaseOperationQueue(ctx, group, now.UTC().Truncate(time.Microsecond))
		released += n
		errs = errors.Join(errs, err)
	}
	return released, errs
}

func (s *PgStore) releaseOperationQueue(ctx context.Context, g sqlc.ListOperationQueueGroupsRow, now time.Time) (int, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.LockCustomerOperationAccount(ctx, tx, g.AccountID); err != nil {
		return 0, mapErr(err)
	}
	accountID, appID, tenantID := pgUUIDString(g.AccountID), pgUUIDString(g.AppID), pgUUIDString(g.PlatformTenantID)
	limit, limited, err := operationConcurrencyLimitTx(ctx, tx, accountID, appID, g.Scope, g.Operation, tenantID)
	if err != nil {
		return 0, err
	}
	inFlight, err := operationInFlightTx(ctx, tx, appID, g.Scope, g.Operation, tenantID)
	if err != nil {
		return 0, err
	}
	rows, err := q.ListQueuedOperationsForGroup(ctx, tx, sqlc.ListQueuedOperationsForGroupParams{AppID: g.AppID, Scope: g.Scope, Operation: g.Operation,
		PlatformTenantID: g.PlatformTenantID, RowLimit: operationQueueGroupRows})
	if err != nil {
		return 0, err
	}
	slots := operationQueueSlots(limit, limited, inFlight, len(rows))
	released := 0
	at := pgtype.Timestamptz{Time: now, Valid: true}
	for _, row := range rows {
		if api.OperationState(row.State) != api.OperationAccepted {
			// Cancelled or expired while queued: nothing to start.
			if err := q.DeleteQueuedOperation(ctx, tx, row.OperationID); err != nil {
				return 0, err
			}
			continue
		}
		if released >= slots {
			continue
		}
		switch row.ExecutionKind {
		case "http":
			err = q.ReleaseQueuedInvocation(ctx, tx, sqlc.ReleaseQueuedInvocationParams{ID: row.CurrentInvocationID, Now: at})
		case "workflow":
			err = q.ReleaseQueuedWorkflowRun(ctx, tx, sqlc.ReleaseQueuedWorkflowRunParams{ID: row.WorkflowRunID, Now: at})
		case "job":
			// The job claim skips Operations with a queue row; deleting it
			// below is the release.
		default:
			err = fmt.Errorf("state: queued %s operation cannot be released", row.ExecutionKind)
		}
		if err == nil {
			err = q.DeleteQueuedOperation(ctx, tx, row.OperationID)
		}
		if err != nil {
			return 0, err
		}
		released++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return released, nil
}

func (s *PgStore) OperationQueueStats(ctx context.Context) (int64, *time.Time, error) {
	row, err := sqlc.New().OperationQueueStats(ctx, s.pool)
	if err != nil {
		return 0, nil, err
	}
	if !row.Oldest.Valid {
		return row.Queued, nil, nil
	}
	oldest := row.Oldest.Time.UTC()
	return row.Queued, &oldest, nil
}
