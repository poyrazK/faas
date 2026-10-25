// adr:1354 — dispatch membership comes from the durable ledger read.
package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Private store capability: caller-controlled Invocation fields never certify
// canary membership or its absence. Read this with the existing environment owner.
type invocationDispatchAuthority struct {
	EnvironmentID, AppID, AccountID  string
	QueueBindingID, QueueName, Scope string
	QueueCanary                      bool
}
type invocationDispatchAuthorityReader interface {
	invocationDispatchAuthority(context.Context, string) (invocationDispatchAuthority, error)
}

func (s *PgStore) invocationDispatchAuthority(ctx context.Context, id string) (invocationDispatchAuthority, error) {
	return readInvocationDispatchAuthority(ctx, s.pool, id)
}

// ADR-570: the invocation version snapshot reads the ledger row in its own
// committed view, so the snapshot path keeps these checks.
func (s invocationVersionPolicyReader) invocationDispatchAuthority(ctx context.Context, id string) (invocationDispatchAuthority, error) {
	return readInvocationDispatchAuthority(ctx, s.tx, id)
}

func readInvocationDispatchAuthority(ctx context.Context, db sqlc.DBTX, id string) (invocationDispatchAuthority, error) {
	if !canonicalStateUUID(id) {
		return invocationDispatchAuthority{}, ErrInvalidArgument
	}
	row, err := sqlc.New().ReadInvocationDispatchAuthority(ctx, db, mustPgUUID(id))
	if err != nil {
		return invocationDispatchAuthority{}, mapErr(err)
	}
	return invocationDispatchAuthority{EnvironmentID: row.EnvironmentID, AppID: pgUUIDString(row.AppID), AccountID: pgUUIDString(row.AccountID), QueueCanary: row.QueueCanary, QueueBindingID: row.QueueBindingID, QueueName: row.QueueName, Scope: row.DeploymentScope}, nil
}

func (m *MemStore) invocationDispatchAuthority(_ context.Context, id string) (invocationDispatchAuthority, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.invocationDispatchAuthorityLocked(id)
}

// The snapshot already holds the store lock.
func (s memInvocationVersionReader) invocationDispatchAuthority(_ context.Context, id string) (invocationDispatchAuthority, error) {
	return s.store.invocationDispatchAuthorityLocked(id)
}

func (m *MemStore) invocationDispatchAuthorityLocked(id string) (invocationDispatchAuthority, error) {
	inv, ok := m.invocations[id]
	if !ok {
		return invocationDispatchAuthority{}, ErrNotFound
	}
	_, canary := m.queueBindingCanaries[id]
	return invocationDispatchAuthority{EnvironmentID: inv.EnvironmentID, AppID: inv.AppID, AccountID: inv.AccountID, QueueCanary: canary, QueueBindingID: inv.QueueBindingID, QueueName: inv.QueueName, Scope: inv.DeploymentScope}, nil
}
