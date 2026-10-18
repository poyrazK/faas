package faas

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"time"
)

// EventAcknowledgmentIntentStoredRetryPolicySchema adds nullable versioned policy
// columns to an existing application-owned intent table. Apply once explicitly
// in an application migration transaction before upgrading Go intent workers.
// Fresh installs use EventAcknowledgmentIntentSchema. Helpers never execute DDL.
//
//go:embed event_acknowledgment_intent_stored_retry_policy_schema.sql
var EventAcknowledgmentIntentStoredRetryPolicySchema string

// EventAcknowledgmentIntentStoredRetryPolicy is payload-free immutable metadata.
// Nil in an intent status means the intent uses worker fallback settings.
type EventAcknowledgmentIntentStoredRetryPolicy struct {
	Version          int   `json:"version"`
	InitialBackoffMS int64 `json:"initial_backoff_ms"`
	MaxBackoffMS     int64 `json:"max_backoff_ms"`
	// MaxDeliveryAttempts is the immutable initial v2 budget; zero for v1.
	MaxDeliveryAttempts int64 `json:"max_delivery_attempts,omitempty"`
}

func eventIntentStoredRetryPolicy(version, initial, maximum sql.NullInt64) (*EventAcknowledgmentIntentStoredRetryPolicy, error) {
	if !version.Valid && !initial.Valid && !maximum.Valid {
		return nil, nil
	}
	if !version.Valid || !initial.Valid || !maximum.Valid || version.Int64 != 1 || initial.Int64 < 1 || initial.Int64 > 3600000 || maximum.Int64 < initial.Int64 || maximum.Int64 > 3600000 {
		return nil, ErrEventAcknowledgmentIntentStore
	}
	return &EventAcknowledgmentIntentStoredRetryPolicy{Version: 1, InitialBackoffMS: initial.Int64, MaxBackoffMS: maximum.Int64}, nil
}

func (p EventAcknowledgmentIntentStoredRetryPolicy) retryPolicy() EventAcknowledgmentIntentRetryPolicy {
	return EventAcknowledgmentIntentRetryPolicy{InitialBackoff: time.Duration(p.InitialBackoffMS) * time.Millisecond, MaxBackoff: time.Duration(p.MaxBackoffMS) * time.Millisecond}
}

func eventIntentRetryPolicyBinding(policy EventAcknowledgmentIntentRetryPolicy) *EventAcknowledgmentIntentStoredRetryPolicy {
	return &EventAcknowledgmentIntentStoredRetryPolicy{Version: 1, InitialBackoffMS: policy.InitialBackoff.Milliseconds(), MaxBackoffMS: policy.MaxBackoff.Milliseconds()}
}

func eventIntentRetryPolicyValues(policy *EventAcknowledgmentIntentStoredRetryPolicy) (any, any, any, any) {
	if policy == nil {
		return nil, nil, nil, nil
	}
	var budget any
	if policy.Version == 2 {
		budget = policy.MaxDeliveryAttempts
	}
	return policy.Version, policy.InitialBackoffMS, policy.MaxBackoffMS, budget
}

// SaveCompletionIntentWithRetryPolicy stages a committed receipt and its saved
// policy atomically without business work. Repeating a matching binding preserves
// the intent. A different binding, or an existing unbound intent, conflicts.
func (w *EventAcknowledgmentWorker) SaveCompletionIntentWithRetryPolicy(ctx context.Context, db *sql.DB, policy EventAcknowledgmentIntentRetryPolicy) (EventAcknowledgmentIntentResult, error) {
	policy, err := eventIntentRetryPolicy(policy)
	if err != nil {
		return EventAcknowledgmentIntentResult{}, err
	}
	return w.saveCompletionIntent(ctx, db, eventIntentRetryPolicyBinding(policy))
}

// RunDurableWithRetryPolicy atomically commits business writes, their receipt,
// the saved completion and its immutable policy. Recovery sends the callback.
func (w *EventAcknowledgmentWorker) RunDurableWithRetryPolicy(ctx context.Context, db *sql.DB, policy EventAcknowledgmentIntentRetryPolicy, handler func(context.Context, EventConsumerSQLTransaction) (json.RawMessage, error)) (EventAcknowledgmentIntentResult, error) {
	policy, err := eventIntentRetryPolicy(policy)
	if err != nil {
		return EventAcknowledgmentIntentResult{}, err
	}
	return w.runDurable(ctx, db, nil, eventIntentRetryPolicyBinding(policy), handler)
}

// RunDurableWithProcessingLeaseAndRetryPolicy additionally applies the existing
// local processing fence, without renewing it during the final transaction.
func (w *EventAcknowledgmentWorker) RunDurableWithProcessingLeaseAndRetryPolicy(ctx context.Context, db *sql.DB, lease EventConsumerProcessingLease, policy EventAcknowledgmentIntentRetryPolicy, handler func(context.Context, EventConsumerSQLTransaction) (json.RawMessage, error)) (EventAcknowledgmentIntentResult, error) {
	policy, err := eventIntentRetryPolicy(policy)
	if err != nil {
		return EventAcknowledgmentIntentResult{}, err
	}
	return w.runDurable(ctx, db, &lease, eventIntentRetryPolicyBinding(policy), handler)
}

// SaveRetryIntentWithRetryPolicy requires known rollback and stopped, exclusive
// job ownership. It saves the original negative action and immutable policy.
func (w *EventAcknowledgmentWorker) SaveRetryIntentWithRetryPolicy(ctx context.Context, db *sql.DB, reason string, policy EventAcknowledgmentIntentRetryPolicy) (EventAcknowledgmentIntentStatus, error) {
	policy, err := eventIntentRetryPolicy(policy)
	if err != nil {
		return EventAcknowledgmentIntentStatus{}, err
	}
	return w.saveNegativeIntent(ctx, db, "retry", reason, eventIntentRetryPolicyBinding(policy))
}

// SaveDeadLetterIntentWithRetryPolicy has the same rollback/ownership contract.
// The retry policy schedules delivery of that decision, not a new disposition.
func (w *EventAcknowledgmentWorker) SaveDeadLetterIntentWithRetryPolicy(ctx context.Context, db *sql.DB, reason string, policy EventAcknowledgmentIntentRetryPolicy) (EventAcknowledgmentIntentStatus, error) {
	policy, err := eventIntentRetryPolicy(policy)
	if err != nil {
		return EventAcknowledgmentIntentStatus{}, err
	}
	return w.saveNegativeIntent(ctx, db, "dead-letter", reason, eventIntentRetryPolicyBinding(policy))
}
