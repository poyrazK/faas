package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

func cloneWorkCancellation(receipt WorkCancellation) WorkCancellation {
	receipt.KeyDigest = append([]byte(nil), receipt.KeyDigest...)
	return receipt
}

func (s *PgStore) WorkCancellationByID(ctx context.Context, id string) (WorkCancellation, error) {
	var receipt WorkCancellation
	err := s.pool.QueryRow(ctx, `select id, app_id, policy_name, key_digest, cancelled_count, created_at
		from invocation_work_cancellations where id = $1`, id).Scan(&receipt.ID, &receipt.AppID,
		&receipt.PolicyName, &receipt.KeyDigest, &receipt.CancelledCount, &receipt.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkCancellation{}, ErrNotFound
	}
	return receipt, err
}

func (m *MemStore) WorkCancellationByID(_ context.Context, id string) (WorkCancellation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt, ok := m.workCancellations[id]
	if !ok {
		return WorkCancellation{}, ErrNotFound
	}
	return cloneWorkCancellation(receipt), nil
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
	return s.cancelPendingWorkDigest(ctx, appID, policyName, id, digest, invocationWorkEnvironment{})
}

func (s *PgStore) cancelPendingWorkDigest(ctx context.Context, appID, policyName string, id uuid.UUID, digest [32]byte, info invocationWorkEnvironment) (WorkCancellation, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return WorkCancellation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if info.environment.ID != "" {
		if err := lockInvocationEnvironmentDB(ctx, tx, info.app.ID, info.app.AccountID, info.environment.ID); err != nil {
			return WorkCancellation{}, err
		}
	}
	// Use the same lane lock order as enqueue and claim. Creating a lane for a
	// key with no current work makes cancellation of an empty lane durable too.
	if _, err := tx.Exec(ctx, `insert into invocation_work_lanes (app_id, policy_name, key_digest)
		values ($1, $2, $3) on conflict do nothing`, appID, policyName, digest[:]); err != nil {
		return WorkCancellation{}, fmt.Errorf("state: cancellation lane insert: %w", err)
	}
	var locked int
	if err := tx.QueryRow(ctx, `select 1 from invocation_work_lanes
		where app_id = $1 and policy_name = $2 and key_digest = $3 for update`,
		appID, policyName, digest[:]).Scan(&locked); err != nil {
		return WorkCancellation{}, fmt.Errorf("state: cancellation lane lock: %w", err)
	}
	if info.environment.ID != "" {
		if err := registerWorkEnvironmentDomainDB(ctx, tx, info.app, info.environment.ID, policyName, "key", digest[:]); err != nil {
			return WorkCancellation{}, err
		}
	}
	receipt := WorkCancellation{ID: id.String(), AppID: appID, PolicyName: policyName, KeyDigest: digest[:]}
	var inserted string
	err = tx.QueryRow(ctx, `insert into invocation_work_cancellations
		(id, app_id, policy_name, key_digest) values ($1, $2, $3, $4)
		on conflict (id) do nothing returning id`, id, appID, policyName, digest[:]).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx, `select app_id, policy_name, key_digest, cancelled_count, created_at
			from invocation_work_cancellations where id = $1`, id).Scan(
			&receipt.AppID, &receipt.PolicyName, &receipt.KeyDigest,
			&receipt.CancelledCount, &receipt.CreatedAt); err != nil {
			return WorkCancellation{}, err
		}
		if receipt.AppID != appID || receipt.PolicyName != policyName || !bytes.Equal(receipt.KeyDigest, digest[:]) {
			return WorkCancellation{}, ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return WorkCancellation{}, err
		}
		return receipt, nil
	}
	if err != nil {
		return WorkCancellation{}, fmt.Errorf("state: cancellation receipt insert: %w", err)
	}
	// Only pending rows are affected. A dispatching worker retains its claim;
	// external effects from that worker still require app-level protection.
	tag, err := tx.Exec(ctx, `update invocations set state = 'cancelled',
		completed_at = clock_timestamp()
		where app_id = $1 and work_policy_name = $2 and work_key_digest = $3
		and state = 'pending'`, appID, policyName, digest[:])
	if err != nil {
		return WorkCancellation{}, fmt.Errorf("state: cancel pending work: %w", err)
	}
	receipt.CancelledCount = tag.RowsAffected()
	brokerTag, err := tx.Exec(ctx, `update trigger_records tr
		set state='cancelled', last_error='cancelled by work policy',
		claim_expires_at=null
		from triggers t where t.id=tr.trigger_id and t.app_id=$1
		  and tr.work_policy_name=$2 and tr.work_key_digest=$3
		  and tr.state in ('pending','retry')`, appID, policyName, digest[:])
	if err != nil {
		return WorkCancellation{}, fmt.Errorf("state: cancel pending broker work: %w", err)
	}
	receipt.CancelledCount += brokerTag.RowsAffected()
	if err := tx.QueryRow(ctx, `update invocation_work_cancellations
		set cancelled_count = $2 where id = $1 returning created_at`, id, receipt.CancelledCount).Scan(&receipt.CreatedAt); err != nil {
		return WorkCancellation{}, fmt.Errorf("state: cancellation receipt update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return WorkCancellation{}, err
	}
	return receipt, nil
}

func (m *MemStore) CancelPendingKeyedInvocations(ctx context.Context, appID, policyName, canonicalKey, cancellationID string) (WorkCancellation, error) {
	id, digest, err := validateWorkCancellation(appID, policyName, canonicalKey, cancellationID)
	if err != nil {
		return WorkCancellation{}, err
	}
	return m.cancelPendingWorkDigest(ctx, appID, policyName, id, digest, invocationWorkEnvironment{})
}

func (m *MemStore) cancelPendingWorkDigest(_ context.Context, appID, policyName string, id uuid.UUID, digest [32]byte, info invocationWorkEnvironment) (WorkCancellation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.apps[appID]
	if !ok {
		_, ok = m.apps[canonicalMemUUID(appID)]
	}
	if !ok {
		return WorkCancellation{}, ErrNotFound
	}
	if info.environment.ID != "" {
		env, exists := m.projectEnvironments[info.environment.ID]
		app := m.apps[info.app.ID]
		if !exists || env.AccountID != info.app.AccountID || env.ProjectID != app.ProjectID || app.Status == AppDeleted {
			return WorkCancellation{}, ErrInvocationEnvironmentWorkIsolation
		}
	}
	if previous, ok := m.workCancellations[id.String()]; ok {
		if !sameMemUUID(previous.AppID, appID) || previous.PolicyName != policyName || !bytes.Equal(previous.KeyDigest, digest[:]) {
			return WorkCancellation{}, ErrConflict
		}
		return cloneWorkCancellation(previous), nil
	}
	if err := m.registerWorkEnvironmentLocked(info, Invocation{AppID: appID, WorkPolicyName: policyName, WorkKeyDigest: digest[:]}); err != nil {
		return WorkCancellation{}, err
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
		m.invocations[id] = inv
		receipt.CancelledCount++
	}
	m.workCancellations[receipt.ID] = receipt
	return cloneWorkCancellation(receipt), nil
}
