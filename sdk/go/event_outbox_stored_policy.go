package faas

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"time"
)

// EventOutboxStoredRetryPolicySchema upgrades application-owned policy/control
// storage. Apply explicitly in a migration transaction before upgrading workers.
//
//go:embed event_outbox_stored_retry_policy_schema.sql
var EventOutboxStoredRetryPolicySchema string

// EventOutboxStoredRetryPolicy describes the current per-publication v1 policy.
// Revision changes only through a guarded command; version identifies semantics.
type EventOutboxStoredRetryPolicy struct {
	Version              int       `json:"version"`
	Revision             int64     `json:"revision"`
	InitialBackoffMillis int64     `json:"initial_backoff_millis"`
	MaxBackoffMillis     int64     `json:"max_backoff_millis"`
	MaxAttempts          int64     `json:"max_attempts"`
	UpdatedAt            time.Time `json:"updated_at"`
}

const eventOutboxStoredPolicyColumns = `version,revision,initial_backoff_ms,max_backoff_ms,max_attempts,updated_at,initial_digest`

func eventOutboxStoredPolicy(row eventIntentScanner) (EventOutboxStoredRetryPolicy, []byte, error) {
	var out EventOutboxStoredRetryPolicy
	var digest []byte
	err := row.Scan(&out.Version, &out.Revision, &out.InitialBackoffMillis, &out.MaxBackoffMillis, &out.MaxAttempts, &out.UpdatedAt, &digest)
	if err == nil && (out.Version != 1 || out.Revision < 1 || len(digest) != 32 || out.InitialBackoffMillis < 1 || out.MaxBackoffMillis < out.InitialBackoffMillis || out.MaxBackoffMillis > 3600000 || out.MaxAttempts < 0 || out.MaxAttempts > 2147483647) {
		err = ErrEventOutboxStore
	}
	return out, digest, err
}
func (p EventOutboxStoredRetryPolicy) recoveryPolicy() *EventOutboxRetryPolicy {
	return &EventOutboxRetryPolicy{InitialBackoff: time.Duration(p.InitialBackoffMillis) * time.Millisecond, MaxBackoff: time.Duration(p.MaxBackoffMillis) * time.Millisecond, MaxAttempts: p.MaxAttempts}
}
func eventOutboxPolicyDigest(p EventOutboxRetryPolicy) [32]byte {
	// Fixed v1 framing, independent of Go duration JSON encoding or field names.
	body, _ := json.Marshal([4]int64{1, p.InitialBackoff.Milliseconds(), p.MaxBackoff.Milliseconds(), p.MaxAttempts})
	return sha256.Sum256(append([]byte("gregale-event-outbox-policy-v1\n"), body...))
}

// SaveEventOutboxWithRetryPolicy stages content and policy atomically with the
// caller's business writes. Roll back on any error. Repeated saves compare the
// initial policy digest, preserving later audited updates instead of resetting it.
func SaveEventOutboxWithRetryPolicy(ctx context.Context, tx *sql.Tx, accountID, appID, slug string, request AppPublishEventRequest, retryWindow time.Duration, policy EventOutboxRetryPolicy) (EventOutboxStatus, error) {
	var zero EventOutboxStatus
	if ctx == nil || tx == nil {
		return zero, ErrInvalidEventConsumerRequest
	}
	policy, err := eventOutboxRetryPolicy(policy)
	if err != nil {
		return zero, err
	}
	status, err := SaveEventOutbox(ctx, tx, accountID, appID, slug, request, retryWindow)
	if err != nil {
		return zero, err
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return zero, err
	}
	digest := eventOutboxPolicyDigest(policy)
	_, err = tx.ExecContext(ctx, `INSERT INTO public.gregale_event_outbox_retry_policies(account_id,app_id,producer_key,version,revision,initial_backoff_ms,max_backoff_ms,max_attempts,initial_digest) SELECT account_id,app_id,producer_key,1,1,$4::bigint,$5::bigint,$6::bigint,$7 FROM public.gregale_event_outbox WHERE account_id=$1::uuid AND app_id=$2::uuid AND producer_key=$3 AND state IN ('pending','unconfirmed','blocked') AND attempts=0 AND claim_token IS NULL ON CONFLICT(account_id,app_id,producer_key) DO NOTHING`, account, app, request.Key, policy.InitialBackoff.Milliseconds(), policy.MaxBackoff.Milliseconds(), policy.MaxAttempts, digest[:])
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	_, initial, err := eventOutboxStoredPolicy(tx.QueryRowContext(ctx, `SELECT `+eventOutboxStoredPolicyColumns+` FROM public.gregale_event_outbox_retry_policies WHERE account_id=$1::uuid AND app_id=$2::uuid AND producer_key=$3`, account, app, request.Key))
	if errors.Is(err, sql.ErrNoRows) {
		return zero, ErrEventOutboxConflict
	}
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	if !bytes.Equal(initial, digest[:]) {
		return zero, ErrEventOutboxConflict
	}
	return status, nil
}

// GetEventOutboxRetryPolicy reads the current policy, not a control receipt.
// ErrNotFound means no retained binding; never infer that publication failed.
func GetEventOutboxRetryPolicy(ctx context.Context, db *sql.DB, accountID, appID, key string) (EventOutboxStoredRetryPolicy, error) {
	var zero EventOutboxStoredRetryPolicy
	if ctx == nil || db == nil || !eventOutboxKey(key) {
		return zero, ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return zero, err
	}
	policy, _, err := eventOutboxStoredPolicy(db.QueryRowContext(ctx, `SELECT `+eventOutboxStoredPolicyColumns+` FROM public.gregale_event_outbox_retry_policies WHERE account_id=$1::uuid AND app_id=$2::uuid AND producer_key=$3`, account, app, key))
	if errors.Is(err, sql.ErrNoRows) {
		return zero, ErrNotFound
	}
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	return policy, nil
}

// EventOutboxControlResult names the shared typed metadata result of controls.
type EventOutboxControlResult = EventOutboxReconciliationResult

type EventOutboxSetRetryPolicyOptions struct {
	CommandID         string
	ExpectedUpdatedAt time.Time
	ExpectedAttempts  int64
	ExpectedRevision  int64 // zero adopts an unbound publication
	Policy            EventOutboxRetryPolicy
}

// SetEventOutboxRetryPolicy adopts or revises unresolved, unexpired work without
// a live claim. A command receipt and policy/row updates commit atomically. It
// preserves attempts, content, namespace, due time, deadline and blocked state.
func SetEventOutboxRetryPolicy(ctx context.Context, db *sql.DB, accountID, appID, key string, options EventOutboxSetRetryPolicyOptions) (EventOutboxControlResult, error) {
	var zero EventOutboxControlResult
	if ctx == nil || db == nil || options.CommandID == "" || options.ExpectedUpdatedAt.IsZero() || options.ExpectedAttempts < 0 || options.ExpectedRevision < 0 {
		return zero, ErrInvalidEventConsumerRequest
	}
	policy, err := eventOutboxRetryPolicy(options.Policy)
	if err != nil {
		return zero, err
	}
	request := struct {
		Version           int       `json:"version"`
		ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
		ExpectedAttempts  int64     `json:"expected_attempts"`
		ExpectedRevision  int64     `json:"expected_revision"`
		InitialMillis     int64     `json:"initial_ms"`
		MaxMillis         int64     `json:"max_ms"`
		MaxAttempts       int64     `json:"max_attempts"`
	}{1, options.ExpectedUpdatedAt.UTC(), options.ExpectedAttempts, options.ExpectedRevision, policy.InitialBackoff.Milliseconds(), policy.MaxBackoff.Milliseconds(), policy.MaxAttempts}
	receipt, err := runEventOutboxControl(ctx, db, accountID, appID, key, options.CommandID, "set_retry_policy", request, func(tx *sql.Tx) (EventOutboxReconciliationResult, error) {
		return setEventOutboxRetryPolicy(ctx, tx, accountID, appID, key, options, policy)
	})
	return receipt.Result, err
}

func setEventOutboxRetryPolicy(ctx context.Context, tx *sql.Tx, accountID, appID, key string, options EventOutboxSetRetryPolicyOptions, policy EventOutboxRetryPolicy) (EventOutboxControlResult, error) {
	out := EventOutboxControlResult{Outcome: "rejected"}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return out, err
	}
	status, err := eventOutboxStatus(tx.QueryRowContext(ctx, `SELECT `+eventOutboxStatusColumns+` FROM public.gregale_event_outbox WHERE account_id=$1::uuid AND app_id=$2::uuid AND producer_key=$3 AND state IN ('pending','unconfirmed','blocked') AND updated_at=$4::timestamptz AND attempts=$5::bigint AND retry_until>clock_timestamp() AND (claim_expires_at IS NULL OR claim_expires_at<=clock_timestamp()) FOR UPDATE`, account, app, key, options.ExpectedUpdatedAt, options.ExpectedAttempts))
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrEventOutboxConflict
	}
	if err != nil {
		return out, eventOutboxStoreError(ctx)
	}
	out.Publication = status
	current, _, err := eventOutboxStoredPolicy(tx.QueryRowContext(ctx, `SELECT `+eventOutboxStoredPolicyColumns+` FROM public.gregale_event_outbox_retry_policies WHERE account_id=$1::uuid AND app_id=$2::uuid AND producer_key=$3`, account, app, key))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, eventOutboxStoreError(ctx)
	}
	if current.Revision != options.ExpectedRevision {
		return out, ErrEventOutboxConflict
	}
	digest := eventOutboxPolicyDigest(policy)
	updated, _, err := eventOutboxStoredPolicy(tx.QueryRowContext(ctx, `INSERT INTO public.gregale_event_outbox_retry_policies(account_id,app_id,producer_key,version,revision,initial_backoff_ms,max_backoff_ms,max_attempts,initial_digest) VALUES($1::uuid,$2::uuid,$3,1,1,$4::bigint,$5::bigint,$6::bigint,$7) ON CONFLICT(account_id,app_id,producer_key) DO UPDATE SET revision=gregale_event_outbox_retry_policies.revision+1,version=1,initial_backoff_ms=EXCLUDED.initial_backoff_ms,max_backoff_ms=EXCLUDED.max_backoff_ms,max_attempts=EXCLUDED.max_attempts,updated_at=clock_timestamp() RETURNING `+eventOutboxStoredPolicyColumns, account, app, key, policy.InitialBackoff.Milliseconds(), policy.MaxBackoff.Milliseconds(), policy.MaxAttempts, digest[:]))
	if err != nil {
		return out, eventOutboxStoreError(ctx)
	}
	status, err = eventOutboxStatus(tx.QueryRowContext(ctx, `UPDATE public.gregale_event_outbox SET claim_token=NULL,claim_expires_at=NULL,updated_at=clock_timestamp() WHERE account_id=$1::uuid AND app_id=$2::uuid AND producer_key=$3 RETURNING `+eventOutboxStatusColumns, account, app, key))
	if err != nil {
		return out, eventOutboxStoreError(ctx)
	}
	out.Outcome = "policy_updated"
	out.Publication = status
	out.RetryPolicy = &updated
	return out, nil
}
