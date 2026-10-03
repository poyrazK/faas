package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

type KeyedTriggerRecordStore interface {
	InsertKeyedTriggerRecord(context.Context, string, string, []byte, []byte, []byte,
		AppWorkPolicy, string, ...string) (string, error)
}

// TriggerWorkPoisoner records a malformed work-key delivery as a terminal
// broker receipt. created=false means another admission already owns this
// stable identity; callers must honor that original receipt instead.
type TriggerWorkPoisoner interface {
	RejectInvalidTriggerWorkRecord(context.Context, string, string, []byte, []byte, []byte, string) (bool, string, error)
}

type PendingTriggerDeadLetterRouter interface {
	RoutePendingTriggerDeadLetterByItem(context.Context, string, string, string, []byte) (bool, error)
}

// RoutePendingTriggerDeadLetterByItem fences rate-limit and pre-claim poison
// decisions against a concurrent keyed claim or replacement. It only moves a
// pending/retry receipt; an active or already-terminal row is left intact.
func (s *PgStore) RoutePendingTriggerDeadLetterByItem(ctx context.Context,
	triggerID, item, reason string, detail []byte) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("state: pending trigger dead-letter begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id, appID string
	var policyName *string
	var digest []byte
	err = tx.QueryRow(ctx, `select r.id, t.app_id, r.work_policy_name, r.work_key_digest
		from trigger_records r join triggers t on t.id=r.trigger_id
		where r.trigger_id=$1 and r.item_identifier=$2`, triggerID, item).Scan(
		&id, &appID, &policyName, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("state: pending trigger dead-letter lookup: %w", err)
	}
	if policyName != nil {
		var locked int
		if err := tx.QueryRow(ctx, `select 1 from invocation_work_lanes
			where app_id=$1 and policy_name=$2 and key_digest=$3 for update`,
			appID, *policyName, digest).Scan(&locked); err != nil {
			return false, fmt.Errorf("state: pending trigger dead-letter lane lock: %w", err)
		}
	}
	var updated string
	err = tx.QueryRow(ctx, `update trigger_records set state='dead_letter',
		attempts=attempts+1, last_error=$2, last_dispatched_at=clock_timestamp(),
		claim_expires_at=null
		where id=$1 and state in ('pending','retry') returning id`, id, string(detail)).Scan(&updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("state: pending trigger dead-letter update: %w", err)
	}
	if _, err := tx.Exec(ctx, `insert into trigger_dead_letter
		(record_id,trigger_id,reason,routed_to,detail)
		values ($1,$2,$3,'drop',$4::jsonb) on conflict (record_id) do nothing`,
		id, triggerID, reason, triggerDeadLetterDetail(detail)); err != nil {
		return false, fmt.Errorf("state: pending trigger dead-letter receipt: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("state: pending trigger dead-letter commit: %w", err)
	}
	return true, nil
}

func (s *PgStore) RejectInvalidTriggerWorkRecord(ctx context.Context, triggerID, itemIdentifier string,
	payload, headers, metadata []byte, reason string) (bool, string, error) {
	if payload == nil {
		payload = []byte("{}")
	}
	if !json.Valid(payload) {
		payload, _ = json.Marshal(string(payload))
	}
	if headers == nil {
		headers = []byte("{}")
	}
	if metadata == nil {
		metadata = []byte("{}")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, "", fmt.Errorf("state: invalid trigger work begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id string
	err = tx.QueryRow(ctx, `insert into trigger_records
		(trigger_id, item_identifier, payload, headers, metadata)
		values ($1,$2,$3::jsonb,$4::jsonb,$5::jsonb)
		on conflict (trigger_id,item_identifier) do nothing returning id`,
		triggerID, itemIdentifier, payload, headers, metadata).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		var existingState string
		if err := tx.QueryRow(ctx, `select state from trigger_records
			where trigger_id=$1 and item_identifier=$2`, triggerID, itemIdentifier).Scan(&existingState); err != nil {
			return false, "", fmt.Errorf("state: invalid trigger work prior receipt: %w", err)
		}
		return false, existingState, nil
	}
	if err != nil {
		return false, "", fmt.Errorf("state: invalid trigger work insert: %w", err)
	}
	if _, err := tx.Exec(ctx, `insert into trigger_dead_letter
		(record_id,trigger_id,reason,routed_to,detail)
		values ($1,$2,'poison_record','drop',$3::jsonb)`,
		id, triggerID, triggerDeadLetterDetail([]byte(reason))); err != nil {
		return false, "", fmt.Errorf("state: invalid trigger work receipt: %w", err)
	}
	if _, err := tx.Exec(ctx, `update trigger_records set state='dead_letter',
		attempts=attempts+1, last_error=$2, last_dispatched_at=clock_timestamp()
		where id=$1`, id, reason); err != nil {
		return false, "", fmt.Errorf("state: invalid trigger work terminal: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, "", fmt.Errorf("state: invalid trigger work commit: %w", err)
	}
	return true, "dead_letter", nil
}

// InsertKeyedTriggerRecord admits a broker record into the same app work lane
// used by invocations. The policy and revision must be the effective binding
// snapshot resolved for this delivery. The broker's stable record identity
// is (triggerID, itemIdentifier); replaying it cannot replace newer work.
func (s *PgStore) InsertKeyedTriggerRecord(ctx context.Context, triggerID, itemIdentifier string,
	payload, headers, metadata []byte, policy AppWorkPolicy, canonicalKey string,
	fairnessKeys ...string) (string, error) {
	if err := policy.Policy.Validate(); err != nil {
		return "", err
	}
	if policy.AppID == "" || policy.Revision <= 0 || itemIdentifier == "" {
		return "", ErrInvalidArgument
	}
	digest, err := workpolicy.DigestKey(canonicalKey)
	if err != nil {
		return "", err
	}
	fairnessDigest, err := workFairnessDigest(policy.Policy, canonicalKey, fairnessKeys)
	if err != nil {
		return "", err
	}
	if payload == nil {
		payload = []byte("{}")
	}
	if headers == nil {
		headers = []byte("{}")
	}
	if metadata == nil {
		metadata = []byte("{}")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", fmt.Errorf("state: keyed trigger admission begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var appID string
	err = tx.QueryRow(ctx, `select app_id from triggers where id=$1`, triggerID).Scan(&appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("state: keyed trigger lookup: %w", err)
	}
	if appID != policy.AppID {
		return "", ErrConflict
	}
	// Check idempotency before taking the lane lock. A second check under the
	// lock and the unique insert below cover concurrent admission.
	var existing string
	err = tx.QueryRow(ctx, `select id from trigger_records
		where trigger_id=$1 and item_identifier=$2`, triggerID, itemIdentifier).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("state: keyed trigger idempotency: %w", err)
	}
	if _, err := tx.Exec(ctx, `insert into invocation_work_lanes
		(app_id, policy_name, key_digest) values ($1,$2,$3)
		on conflict do nothing`, appID, policy.Policy.Name, digest[:]); err != nil {
		return "", fmt.Errorf("state: keyed trigger lane insert: %w", err)
	}
	var sequence int64
	err = tx.QueryRow(ctx, `select next_sequence from invocation_work_lanes
		where app_id=$1 and policy_name=$2 and key_digest=$3 for update`,
		appID, policy.Policy.Name, digest[:]).Scan(&sequence)
	if err != nil {
		return "", fmt.Errorf("state: keyed trigger lane lock: %w", err)
	}
	err = tx.QueryRow(ctx, `select id from trigger_records
		where trigger_id=$1 and item_identifier=$2`, triggerID, itemIdentifier).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("state: keyed trigger locked idempotency: %w", err)
	}
	if policy.Policy.PendingUpdates == workpolicy.PendingKeepLatest {
		if _, err := tx.Exec(ctx, `update invocations
			set state='superseded', outcome='superseded', completed_at=clock_timestamp(),
			last_error='superseded by newer work'
			where app_id=$1 and work_policy_name=$2 and work_key_digest=$3
			  and state='pending'`, appID, policy.Policy.Name, digest[:]); err != nil {
			return "", fmt.Errorf("state: keyed trigger replace invocations: %w", err)
		}
		if _, err := tx.Exec(ctx, `update trigger_records tr
			set state='superseded', last_error='superseded by newer work',
			claim_expires_at=null
			from triggers t
			where t.id=tr.trigger_id and t.app_id=$1
			  and tr.work_policy_name=$2 and tr.work_key_digest=$3
			  and tr.state in ('pending','retry')`, appID, policy.Policy.Name, digest[:]); err != nil {
			return "", fmt.Errorf("state: keyed trigger replace broker records: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `update invocation_work_lanes
		set next_sequence=next_sequence+1
		where app_id=$1 and policy_name=$2 and key_digest=$3`,
		appID, policy.Policy.Name, digest[:]); err != nil {
		return "", fmt.Errorf("state: keyed trigger sequence: %w", err)
	}
	now := time.Now().UTC()
	dueAt := policy.Policy.AvailableAt(now, time.Time{})
	expiresAt := policy.Policy.ExpiresAt(now)
	err = tx.QueryRow(ctx, `insert into trigger_records
		(trigger_id, item_identifier, payload, headers, metadata, next_fire_at,
		 received_at, work_policy_name, work_key_digest, work_sequence,
		 work_policy_revision, work_fairness_digest, work_fairness_limit, work_expires_at)
		values ($1,$2,$3::jsonb,$4::jsonb,$5::jsonb,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		on conflict (trigger_id, item_identifier) do nothing returning id`,
		triggerID, itemIdentifier, payload, headers, metadata, dueAt, now,
		policy.Policy.Name, digest[:], sequence, policy.Revision, fairnessDigest,
		nullableWorkFairnessLimit(policy.Policy.MaxRunningPerFairnessKey), expiresAt).Scan(&existing)
	if errors.Is(err, pgx.ErrNoRows) {
		// A duplicate under another key won the unique record identity race.
		// Roll back replacement and the lane sequence before reading its row.
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
			return "", fmt.Errorf("state: keyed trigger duplicate rollback: %w", rollbackErr)
		}
		if err := s.pool.QueryRow(ctx, `select id from trigger_records
			where trigger_id=$1 and item_identifier=$2`, triggerID, itemIdentifier).Scan(&existing); err != nil {
			return "", fmt.Errorf("state: keyed trigger duplicate lookup: %w", err)
		}
		return existing, nil
	}
	if err != nil {
		return "", fmt.Errorf("state: keyed trigger insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("state: keyed trigger commit: %w", err)
	}
	return existing, nil
}
