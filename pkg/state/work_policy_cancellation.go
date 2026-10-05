package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// WorkCancellation is the durable receipt for one cancel-pending operation.
// The raw application key is never stored. Reusing an ID only returns the
// original receipt, so an event replay cannot cancel work admitted later.
type WorkCancellation struct {
	ID             string
	AppID          string
	PolicyName     string
	KeyDigest      []byte
	CancelledCount int64
	CreatedAt      time.Time
}

type WorkCancellationStore interface {
	CancelPendingKeyedInvocations(context.Context, string, string, string, string) (WorkCancellation, error)
	WorkCancellationByID(context.Context, string) (WorkCancellation, error)
}

func (s *PgStore) WorkCancellationByID(ctx context.Context, id string) (WorkCancellation, error) {
	row, err := sqlc.New().WorkAdmissionCancellation(ctx, s.pool, mustPgUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkCancellation{}, ErrNotFound
	}
	return workCancellationFromSQL(row), err
}

func (m *MemStore) WorkCancellationByID(_ context.Context, id string) (WorkCancellation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt, ok := m.workCancellations[id]
	if !ok {
		return WorkCancellation{}, ErrNotFound
	}
	return receipt, nil
}

func validateWorkCancellation(appID, policyName, canonicalKey, cancellationID string) (uuid.UUID, [32]byte, error) {
	if _, err := uuid.Parse(appID); err != nil {
		return uuid.Nil, [32]byte{}, fmt.Errorf("state: invalid app id: %w", err)
	}
	if err := (workpolicy.Policy{Name: policyName, MaxRunningPerKey: 1}).Validate(); err != nil {
		return uuid.Nil, [32]byte{}, err
	}
	digest, err := workpolicy.DigestKey(canonicalKey)
	if err != nil {
		return uuid.Nil, [32]byte{}, err
	}
	if cancellationID == "" {
		return uuid.New(), digest, nil
	}
	id, err := uuid.Parse(cancellationID)
	if err != nil {
		return uuid.Nil, [32]byte{}, fmt.Errorf("state: invalid cancellation id: %w", err)
	}
	return id, digest, nil
}

func (s *PgStore) CancelPendingKeyedInvocations(ctx context.Context, appID, policyName, canonicalKey, cancellationID string) (WorkCancellation, error) {
	id, digest, err := validateWorkCancellation(appID, policyName, canonicalKey, cancellationID)
	if err != nil {
		return WorkCancellation{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WorkCancellation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	out, err := cancelPendingKeyedInvocationsTx(ctx, tx, appID, policyName, digest[:], id.String())
	if err != nil {
		return WorkCancellation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return WorkCancellation{}, err
	}
	return out, nil
}

func (m *MemStore) CancelPendingKeyedInvocations(_ context.Context, appID, policyName, canonicalKey, cancellationID string) (WorkCancellation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cancelPendingKeyedInvocationsLocked(appID, policyName, canonicalKey, cancellationID)
}

func (m *MemStore) cancelPendingKeyedInvocationsLocked(appID, policyName, canonicalKey, cancellationID string) (WorkCancellation, error) {
	id, digest, err := validateWorkCancellation(appID, policyName, canonicalKey, cancellationID)
	if err != nil {
		return WorkCancellation{}, err
	}
	_, ok := m.apps[appID]
	if !ok {
		_, ok = m.apps[canonicalMemUUID(appID)]
	}
	if !ok {
		return WorkCancellation{}, ErrNotFound
	}
	if previous, ok := m.workCancellations[id.String()]; ok {
		if !sameMemUUID(previous.AppID, appID) || previous.PolicyName != policyName || !bytes.Equal(previous.KeyDigest, digest[:]) {
			return WorkCancellation{}, ErrConflict
		}
		return previous, nil
	}
	now := time.Now().UTC()
	receipt := WorkCancellation{ID: id.String(), AppID: canonicalMemUUID(appID),
		PolicyName: policyName, KeyDigest: append([]byte(nil), digest[:]...), CreatedAt: now}
	for id, inv := range m.invocations {
		if !sameMemUUID(inv.AppID, appID) || inv.WorkPolicyName != policyName ||
			!bytes.Equal(inv.WorkKeyDigest, digest[:]) || inv.State != InvocationPending {
			continue
		}
		inv.State = InvocationCancelled
		inv.CompletedAt = &now
		m.setInvocationLocked(id, inv)
		receipt.CancelledCount++
	}
	m.workCancellations[receipt.ID] = receipt
	return receipt, nil
}
