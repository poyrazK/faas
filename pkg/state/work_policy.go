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

func nullableWorkSequence(sequence int64) any {
	if sequence == 0 {
		return nil
	}
	return sequence
}

func nullableWorkFairnessLimit(limit int) any {
	if limit == 0 {
		return nil
	}
	return limit
}

func workFairnessDigest(policy workpolicy.Policy, canonicalKey string, fairnessKeys []string) ([]byte, error) {
	if len(fairnessKeys) > 1 {
		return nil, ErrInvalidArgument
	}
	if policy.MaxRunningPerFairnessKey == 0 {
		return nil, nil
	}
	fairnessKey := canonicalKey
	if len(fairnessKeys) == 1 {
		fairnessKey = fairnessKeys[0]
	}
	digest, err := workpolicy.DigestKey(fairnessKey)
	if err != nil {
		return nil, err
	}
	return digest[:], nil
}

// PendingQueueWorkInLane counts queue rows that keep_latest would replace.
// Callers use this to avoid rejecting a replacement at the queue depth cap.
type PendingQueueWorkCounter interface {
	PendingQueueWorkInLane(ctx context.Context, appID, policyName string, keyDigest []byte) (int, error)
}

func (s *PgStore) PendingQueueWorkInLane(ctx context.Context, appID, policyName string, keyDigest []byte) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		select count(*) from invocations
		where app_id = $1 and source = 'queue' and state = 'pending'
		  and work_policy_name = $2 and work_key_digest = $3`, appID, policyName, keyDigest).Scan(&n)
	return n, err
}

func (m *MemStore) PendingQueueWorkInLane(_ context.Context, appID, policyName string, keyDigest []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, inv := range m.invocations {
		if inv.AppID == appID && inv.Source == InvocationQueue && inv.State == InvocationPending &&
			inv.WorkPolicyName == policyName && bytes.Equal(inv.WorkKeyDigest, keyDigest) {
			n++
		}
	}
	return n, nil
}

// ExpirePendingKeyedInvocations makes pending TTLs effective even when a
// source due time is later than the expiry and no later row enters the lane.
func (s *PgStore) ExpirePendingKeyedInvocations(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = 64
	}
	invocationLimit := limit
	if limit > 1 {
		invocationLimit = (limit + 1) / 2
	}
	var expired int
	err := s.pool.QueryRow(ctx, `
		with due as (
			select id from invocations
			where work_policy_name is not null and state = 'pending'
			  and work_expires_at <= $1
			order by work_expires_at, id
			for update skip locked limit $2
		), updated as (
			update invocations i set state = 'expired', outcome = 'expired',
			  completed_at = $1
			from due where i.id = due.id returning i.id
		)
		select count(*) from updated`, now.UTC(), invocationLimit).Scan(&expired)
	if err != nil {
		return 0, fmt.Errorf("state: expire pending keyed work: %w", err)
	}
	var brokerExpired int
	err = s.pool.QueryRow(ctx, `
		with due as (
			select id from trigger_records
			where work_policy_name is not null and state in ('pending','retry')
			  and work_expires_at <= $1
			order by work_expires_at, id
			for update skip locked limit $2
		), updated as (
			update trigger_records tr set state='expired',
			  last_error='work policy pending deadline expired', claim_expires_at=null
			from due where tr.id=due.id returning tr.id
		)
		select count(*) from updated`, now.UTC(), limit-expired).Scan(&brokerExpired)
	if err != nil {
		return expired, fmt.Errorf("state: expire pending keyed broker work: %w", err)
	}
	return expired + brokerExpired, nil
}

func (m *MemStore) ExpirePendingKeyedInvocations(_ context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = 64
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	expired := 0
	for id, inv := range m.invocations {
		if inv.WorkPolicyName == "" || inv.State != InvocationPending ||
			inv.WorkExpiresAt == nil || inv.WorkExpiresAt.After(now) {
			continue
		}
		inv.State = InvocationExpired
		outcome := OutcomeExpired
		inv.Outcome = &outcome
		inv.CompletedAt = &now
		m.invocations[id] = inv
		expired++
		if expired >= limit {
			break
		}
	}
	return expired, nil
}

// EnqueueKeyedInvocation admits one invocation into a named app lane. A
// repeated producer ID returns its original row without replacing later work.
// The selector is resolved by the producer; only its digest reaches storage.
func (s *PgStore) EnqueueKeyedInvocation(ctx context.Context, inv Invocation, policy workpolicy.Policy, canonicalKey string, fairnessKeys ...string) (Invocation, error) {
	if err := policy.Validate(); err != nil {
		return Invocation{}, err
	}
	digest, err := workpolicy.DigestKey(canonicalKey)
	if err != nil {
		return Invocation{}, err
	}
	fairnessDigest, err := workFairnessDigest(policy, canonicalKey, fairnessKeys)
	if err != nil {
		return Invocation{}, err
	}
	if inv.ID == "" {
		inv.ID = uuid.NewString()
	} else if _, err := uuid.Parse(inv.ID); err != nil {
		return Invocation{}, fmt.Errorf("state: invocation id: %w", err)
	}
	if inv.AppID == "" || inv.AccountID == "" {
		return Invocation{}, fmt.Errorf("state: keyed invocation requires app and account")
	}
	if inv.State != "" && inv.State != InvocationPending {
		return Invocation{}, fmt.Errorf("state: keyed invocation must start pending")
	}
	inv.WorkPolicyName = policy.Name
	inv.WorkKeyDigest = digest[:]
	inv.WorkFairnessDigest = fairnessDigest
	inv.WorkFairnessLimit = policy.MaxRunningPerFairnessKey
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Invocation{}, fmt.Errorf("state: keyed enqueue begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The lane row is both a durable sequence and the serialization lock
	// shared with every keyed claim. Its lock order is lane then account cap.
	if _, err := tx.Exec(ctx, `
		insert into invocation_work_lanes (app_id, policy_name, key_digest)
		values ($1, $2, $3) on conflict do nothing`, inv.AppID, policy.Name, digest[:]); err != nil {
		return Invocation{}, fmt.Errorf("state: keyed enqueue lane: %w", err)
	}
	var sequence int64
	if err := tx.QueryRow(ctx, `
		select next_sequence from invocation_work_lanes
		where app_id = $1 and policy_name = $2 and key_digest = $3
		for update`, inv.AppID, policy.Name, digest[:]).Scan(&sequence); err != nil {
		return Invocation{}, fmt.Errorf("state: keyed enqueue lock: %w", err)
	}
	existing, err := scanInvocation(tx.QueryRow(ctx, `select `+invocationSelectCols+` from invocations where id = $1`, inv.ID))
	if err == nil {
		if existing.AppID != inv.AppID || existing.WorkPolicyName != policy.Name ||
			!bytes.Equal(existing.WorkKeyDigest, digest[:]) {
			return Invocation{}, ErrConflict
		}
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Invocation{}, fmt.Errorf("state: keyed enqueue idempotency: %w", err)
	}
	now := time.Now().UTC()
	inv.CreatedAt = now
	inv.DueAt = policy.AvailableAt(now, inv.DueAt)
	inv.WorkExpiresAt = policy.ExpiresAt(now)
	inv.WorkSequence = sequence
	if policy.PendingUpdates == workpolicy.PendingKeepLatest {
		if _, err := tx.Exec(ctx, `
			update invocations set state = 'superseded', outcome = 'superseded', completed_at = now(),
			       last_error = 'superseded by newer work'
			where app_id = $1 and work_policy_name = $2 and work_key_digest = $3
			  and state = 'pending'`, inv.AppID, policy.Name, digest[:]); err != nil {
			return Invocation{}, fmt.Errorf("state: keyed enqueue supersede: %w", err)
		}
		if _, err := tx.Exec(ctx, `update trigger_records tr
			set state='superseded', last_error='superseded by newer work',
			claim_expires_at=null
			from triggers t where t.id=tr.trigger_id and t.app_id=$1
			  and tr.work_policy_name=$2 and tr.work_key_digest=$3
			  and tr.state in ('pending','retry')`, inv.AppID, policy.Name, digest[:]); err != nil {
			return Invocation{}, fmt.Errorf("state: keyed enqueue supersede broker records: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		update invocation_work_lanes set next_sequence = next_sequence + 1
		where app_id = $1 and policy_name = $2 and key_digest = $3`,
		inv.AppID, policy.Name, digest[:]); err != nil {
		return Invocation{}, fmt.Errorf("state: keyed enqueue sequence: %w", err)
	}
	out, err := enqueueInvocationRow(ctx, tx, inv)
	if err != nil {
		return Invocation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Invocation{}, fmt.Errorf("state: keyed enqueue commit: %w", err)
	}
	return out, nil
}

// lockKeyedClaimTx holds the lane until the caller commits its claim. The
// oldest active row wins, including a pending retry whose due time is later.
func lockKeyedClaimTx(ctx context.Context, tx pgx.Tx, id, appID, policyName string, digest []byte) error {
	return lockWorkLaneClaimTx(ctx, tx, id, appID, policyName, digest, false)
}

func lockWorkLaneClaimTx(ctx context.Context, tx pgx.Tx, id, appID, policyName string, digest []byte, broker bool) error {
	if policyName == "" {
		return nil
	}
	var locked int
	if err := tx.QueryRow(ctx, `
		select 1 from invocation_work_lanes
		where app_id = $1 and policy_name = $2 and key_digest = $3
		for update`, appID, policyName, digest).Scan(&locked); err != nil {
		return fmt.Errorf("state: keyed claim lane lock: %w", err)
	}
	// Expiry is evaluated while holding the lane lock. A newer row can
	// clear an expired older head even if that head was never due.
	expiredRows, err := tx.Exec(ctx, `
		update invocations set state = 'expired', outcome = 'expired', completed_at = clock_timestamp()
		where app_id = $1 and work_policy_name = $2 and work_key_digest = $3
		  and state = 'pending' and work_expires_at <= clock_timestamp()`,
		appID, policyName, digest)
	if err != nil {
		return fmt.Errorf("state: keyed claim expiry: %w", err)
	}
	expiredBrokerRows, err := tx.Exec(ctx, `update trigger_records tr
		set state='expired', last_error='work policy pending deadline expired',
		claim_expires_at=null
		from triggers t where t.id=tr.trigger_id and t.app_id=$1
		  and tr.work_policy_name=$2 and tr.work_key_digest=$3
		  and tr.state in ('pending','retry')
		  and tr.work_expires_at <= clock_timestamp()`, appID, policyName, digest)
	if err != nil {
		return fmt.Errorf("state: keyed claim broker expiry: %w", err)
	}
	commitExpiry := func() error {
		if expiredRows.RowsAffected() == 0 && expiredBrokerRows.RowsAffected() == 0 {
			return nil
		}
		return tx.Commit(ctx)
	}
	var oldestID string
	var due bool
	var oldestState string
	if err := tx.QueryRow(ctx, `
		select id, state, due from (
		  select id::text, state, due_at <= clock_timestamp() as due,
		         work_sequence from invocations
		  where app_id=$1 and work_policy_name=$2 and work_key_digest=$3
		    and state in ('pending','dispatching')
		  union all
		  select tr.id::text, tr.state, true as due, tr.work_sequence
		  from trigger_records tr join triggers t on t.id=tr.trigger_id
		  where t.app_id=$1 and tr.work_policy_name=$2 and tr.work_key_digest=$3
		    and tr.state in ('pending','retry','claimed')
		) work order by work_sequence limit 1`, appID, policyName, digest).Scan(&oldestID, &oldestState, &due); err != nil {
		if err == pgx.ErrNoRows {
			if commitErr := commitExpiry(); commitErr != nil {
				return commitErr
			}
			return ErrNotFound
		}
		return fmt.Errorf("state: keyed claim oldest: %w", err)
	}
	claimableState := oldestState == string(InvocationPending)
	if broker {
		claimableState = oldestState == "pending" || oldestState == "retry" || oldestState == "claimed"
	}
	if oldestID != id || !claimableState || !due {
		if err := commitExpiry(); err != nil {
			return err
		}
		return ErrConflict
	}
	return nil
}

// lockFairnessClaimTx serializes claims for distinct work lanes that share
// one application-defined fairness group. The cap is snapshotted on the
// candidate row, while every active claim in the group counts against it.
func lockFairnessClaimTx(ctx context.Context, tx pgx.Tx, appID, policyName string, digest []byte, limit int) error {
	if limit == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `insert into invocation_work_fairness_lanes
		(app_id, policy_name, fairness_digest) values ($1, $2, $3)
		on conflict do nothing`, appID, policyName, digest); err != nil {
		return fmt.Errorf("state: fairness lane insert: %w", err)
	}
	var locked int
	if err := tx.QueryRow(ctx, `select 1 from invocation_work_fairness_lanes
		where app_id = $1 and policy_name = $2 and fairness_digest = $3
		for update`, appID, policyName, digest).Scan(&locked); err != nil {
		return fmt.Errorf("state: fairness lane lock: %w", err)
	}
	var running int
	if err := tx.QueryRow(ctx, `select
		(select count(*) from invocations
		 where app_id=$1 and work_policy_name=$2 and work_fairness_digest=$3
		   and state='dispatching' and lease_expires_at > clock_timestamp()) +
		(select count(*) from trigger_records tr join triggers t on t.id=tr.trigger_id
		 where t.app_id=$1 and tr.work_policy_name=$2 and tr.work_fairness_digest=$3
		   and tr.state='claimed' and tr.claim_expires_at > clock_timestamp())`,
		appID, policyName, digest).Scan(&running); err != nil {
		return fmt.Errorf("state: fairness running count: %w", err)
	}
	if running >= limit {
		return ErrConflict
	}
	return nil
}

func (m *MemStore) EnqueueKeyedInvocation(_ context.Context, inv Invocation, policy workpolicy.Policy, canonicalKey string, fairnessKeys ...string) (Invocation, error) {
	if err := policy.Validate(); err != nil {
		return Invocation{}, err
	}
	digest, err := workpolicy.DigestKey(canonicalKey)
	if err != nil {
		return Invocation{}, err
	}
	fairnessDigest, err := workFairnessDigest(policy, canonicalKey, fairnessKeys)
	if err != nil {
		return Invocation{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apps[inv.AppID]; !ok {
		return Invocation{}, fmt.Errorf("state: invocation for unknown app %q", inv.AppID)
	}
	if inv.State != "" && inv.State != InvocationPending {
		return Invocation{}, fmt.Errorf("state: keyed invocation must start pending")
	}
	if inv.ID == "" {
		inv.ID = newID()
	}
	if existing, ok := m.invocations[inv.ID]; ok {
		if existing.AppID != inv.AppID || existing.WorkPolicyName != policy.Name ||
			!bytes.Equal(existing.WorkKeyDigest, digest[:]) {
			return Invocation{}, ErrConflict
		}
		return existing, nil
	}
	now := time.Now().UTC()
	inv.WorkPolicyName = policy.Name
	inv.WorkKeyDigest = digest[:]
	inv.WorkFairnessDigest = fairnessDigest
	inv.WorkFairnessLimit = policy.MaxRunningPerFairnessKey
	inv.WorkExpiresAt = policy.ExpiresAt(now)
	inv.CreatedAt = now
	inv.DueAt = policy.AvailableAt(now, inv.DueAt)
	inv.State = InvocationPending
	for id, old := range m.invocations {
		if old.AppID != inv.AppID || old.WorkPolicyName != policy.Name ||
			!bytes.Equal(old.WorkKeyDigest, digest[:]) {
			continue
		}
		if old.WorkSequence >= inv.WorkSequence {
			inv.WorkSequence = old.WorkSequence + 1
		}
		if policy.PendingUpdates == workpolicy.PendingKeepLatest && old.State == InvocationPending {
			old.State = InvocationSuperseded
			outcome := OutcomeSuperseded
			old.Outcome = &outcome
			old.CompletedAt = &now
			old.LastError = "superseded by newer work"
			m.invocations[id] = old
		}
	}
	if inv.WorkSequence == 0 {
		inv.WorkSequence = 1
	}
	m.invocations[inv.ID] = inv
	return inv, nil
}

func (m *MemStore) keyedClaimAllowedLocked(inv Invocation, now time.Time) error {
	if inv.WorkPolicyName == "" {
		return nil
	}
	for id, old := range m.invocations {
		if old.AppID == inv.AppID && old.WorkPolicyName == inv.WorkPolicyName &&
			bytes.Equal(old.WorkKeyDigest, inv.WorkKeyDigest) && old.State == InvocationPending &&
			old.WorkExpiresAt != nil && !old.WorkExpiresAt.After(now) {
			old.State = InvocationExpired
			outcome := OutcomeExpired
			old.Outcome = &outcome
			old.CompletedAt = &now
			m.invocations[id] = old
			if id == inv.ID {
				return ErrNotFound
			}
		}
	}
	for _, old := range m.invocations {
		if old.ID == inv.ID || old.AppID != inv.AppID ||
			old.WorkPolicyName != inv.WorkPolicyName ||
			!bytes.Equal(old.WorkKeyDigest, inv.WorkKeyDigest) {
			continue
		}
		if (old.State == InvocationPending || old.State == InvocationDispatching) &&
			(old.WorkSequence < inv.WorkSequence || old.State == InvocationDispatching) {
			return ErrConflict
		}
	}
	if inv.DueAt.After(now) {
		return ErrConflict
	}
	return nil
}

func (m *MemStore) fairnessClaimAllowedLocked(inv Invocation, now time.Time) error {
	if inv.WorkFairnessLimit == 0 {
		return nil
	}
	running := 0
	for _, old := range m.invocations {
		if old.AppID == inv.AppID && old.WorkPolicyName == inv.WorkPolicyName &&
			bytes.Equal(old.WorkFairnessDigest, inv.WorkFairnessDigest) &&
			old.State == InvocationDispatching && old.LeaseExpiresAt != nil && old.LeaseExpiresAt.After(now) {
			running++
		}
	}
	if running >= inv.WorkFairnessLimit {
		return ErrConflict
	}
	return nil
}
