package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type OperationStreamStore interface {
	AcquireOperationStream(context.Context, string, string, string) (string, error)
	RenewOperationStream(context.Context, string, string) error
	ReleaseOperationStream(context.Context, string) error
}

type operationStreamLease struct {
	AccountID, OperationID string
	ExpiresAt              time.Time
}

func operationStreamLimit(op Operation, plan api.Plan) int {
	limits := api.MustLimitsFor(plan).Operations
	if !limits.Allowed {
		return op.PlanLimits.SubscriptionsPerAccount
	}
	return limits.SubscriptionsPerAccount
}

func (m *MemStore) AcquireOperationStream(_ context.Context, accountID, tenantID, operationID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	op, exists := data.operations[operationID]
	if !exists || op.AccountID != accountID || (tenantID != "" && op.PlatformTenantID != tenantID) {
		return "", ErrNotFound
	}
	now := time.Now().UTC()
	if !op.ExpiresAt.After(now) {
		return "", ErrOperationExpired
	}
	if data.streams == nil {
		data.streams = map[string]operationStreamLease{}
	}
	count := 0
	for id, lease := range data.streams {
		if !lease.ExpiresAt.After(now) {
			delete(data.streams, id)
		} else if lease.AccountID == accountID {
			count++
		}
	}
	if count >= operationStreamLimit(op, m.accounts[accountID].Plan) {
		return "", NewOperationLimitError("subscriptions_per_account", int64(operationStreamLimit(op, m.accounts[accountID].Plan)), int64(count)+1)
	}
	id := newOperationID()
	data.streams[id] = operationStreamLease{AccountID: accountID, OperationID: operationID, ExpiresAt: now.Add(api.OperationStreamLease)}
	return id, nil
}

func (m *MemStore) RenewOperationStream(_ context.Context, accountID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	lease, exists := data.streams[id]
	if !exists || lease.AccountID != accountID || !lease.ExpiresAt.After(time.Now()) {
		return ErrNotFound
	}
	if _, exists := data.operations[lease.OperationID]; !exists {
		return ErrNotFound
	}
	lease.ExpiresAt = time.Now().UTC().Add(api.OperationStreamLease)
	data.streams[id] = lease
	return nil
}

func (m *MemStore) ReleaseOperationStream(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.operationMemoryLocked().streams, id)
	return nil
}

func (s *PgStore) AcquireOperationStream(ctx context.Context, accountID, tenantID, operationID string) (string, error) {
	account, err := operationUUID(accountID)
	if err != nil {
		return "", err
	}
	id, err := operationUUID(operationID)
	if err != nil {
		return "", err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	plan, err := q.LockCustomerOperationAccount(ctx, tx, account)
	if err != nil {
		return "", mapErr(err)
	}
	raw, err := q.GetCustomerOperation(ctx, tx, sqlc.GetCustomerOperationParams{ID: id, AccountID: account, TenantID: tenantID})
	if err != nil {
		return "", mapErr(err)
	}
	op, err := operationPGRecord(raw)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	if !op.ExpiresAt.After(now) {
		return "", ErrOperationExpired
	}
	cutoff := pgtype.Timestamptz{Time: now, Valid: true}
	if err := q.PruneAccountCustomerOperationStreams(ctx, tx, sqlc.PruneAccountCustomerOperationStreamsParams{AccountID: account, Now: cutoff}); err != nil {
		return "", err
	}
	count, err := q.CountCustomerOperationStreams(ctx, tx, account)
	if err != nil {
		return "", err
	}
	if count >= int64(operationStreamLimit(op, api.Plan(plan))) {
		return "", NewOperationLimitError("subscriptions_per_account", int64(operationStreamLimit(op, api.Plan(plan))), count+1)
	}
	leaseID := newOperationID()
	lease, _ := operationUUID(leaseID)
	if err := q.InsertCustomerOperationStream(ctx, tx, sqlc.InsertCustomerOperationStreamParams{ID: lease, AccountID: account, OperationID: id, ExpiresAt: pgtype.Timestamptz{Time: now.Add(api.OperationStreamLease), Valid: true}}); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return leaseID, nil
}

func (s *PgStore) RenewOperationStream(ctx context.Context, accountID, id string) error {
	account, err := operationUUID(accountID)
	if err != nil {
		return err
	}
	lease, err := operationUUID(id)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	n, err := sqlc.New().RenewCustomerOperationStream(ctx, s.pool, sqlc.RenewCustomerOperationStreamParams{ID: lease, AccountID: account, Now: pgtype.Timestamptz{Time: now, Valid: true}, ExpiresAt: pgtype.Timestamptz{Time: now.Add(api.OperationStreamLease), Valid: true}})
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

func (s *PgStore) ReleaseOperationStream(ctx context.Context, id string) error {
	lease, err := operationUUID(id)
	if err != nil {
		return err
	}
	return sqlc.New().DeleteCustomerOperationStream(ctx, s.pool, lease)
}
