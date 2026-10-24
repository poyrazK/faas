package faas

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
)

// EventAcknowledgmentIntentRetryBudgetSchema upgrades an existing v1-policy intent
// table. Apply once explicitly in an application migration transaction before
// upgrading Go intent helpers. Fresh installs use EventAcknowledgmentIntentSchema.
// Helpers never execute DDL. Budgeted scopes require current budget-aware Go,
// Node or Python workers; exclude older hosts before enabling v2 bindings.
//
//go:embed event_acknowledgment_intent_retry_budget_schema.sql
var EventAcknowledgmentIntentRetryBudgetSchema string

// EventAcknowledgmentIntentRetryBudget binds capped jitter and an initial total
// recovery-claim limit. MaxDeliveryAttempts is 1..2147483647, including crashed or
// uncertain committed claims, not HTTP sends. Resume grants a larger total limit
// without changing this initial binding or resetting delivery attempts.
type EventAcknowledgmentIntentRetryBudget struct {
	Policy              EventAcknowledgmentIntentRetryPolicy
	MaxDeliveryAttempts int64
}

var ErrEventAcknowledgmentIntentParked = errors.New("acknowledgment intent retry budget exhausted")

// EventAcknowledgmentIntentParkedError reports a known committed parking mutation
// during Claim. It contains metadata only and no usable recovery capability.
type EventAcknowledgmentIntentParkedError struct {
	Intent EventAcknowledgmentIntentStatus
}

func (e *EventAcknowledgmentIntentParkedError) Error() string {
	return ErrEventAcknowledgmentIntentParked.Error()
}
func (e *EventAcknowledgmentIntentParkedError) Unwrap() error {
	return ErrEventAcknowledgmentIntentParked
}

func eventIntentStoredBudgetPolicy(version, initial, maximum, budget, limit sql.NullInt64) (*EventAcknowledgmentIntentStoredRetryPolicy, error) {
	if !budget.Valid && !limit.Valid {
		return eventIntentStoredRetryPolicy(version, initial, maximum)
	}
	if !version.Valid || version.Int64 != 2 || !budget.Valid || !limit.Valid || budget.Int64 < 1 || budget.Int64 > 2147483647 || limit.Int64 < budget.Int64 || limit.Int64 > 2147483647 {
		return nil, ErrEventAcknowledgmentIntentStore
	}
	policy, err := eventIntentStoredRetryPolicy(sql.NullInt64{Int64: 1, Valid: true}, initial, maximum)
	if err != nil {
		return nil, err
	}
	policy.Version, policy.MaxDeliveryAttempts = 2, budget.Int64
	return policy, nil
}

func eventIntentBudgetStatus(s EventAcknowledgmentIntentStatus) bool {
	p := s.RetryPolicy
	if p == nil || p.Version == 1 {
		if s.DeliveryAttemptLimit != 0 || p != nil && p.MaxDeliveryAttempts != 0 {
			return false
		}
	} else if p.Version != 2 || p.MaxDeliveryAttempts < 1 || p.MaxDeliveryAttempts > 2147483647 || s.DeliveryAttemptLimit < p.MaxDeliveryAttempts || s.DeliveryAttemptLimit > 2147483647 {
		return false
	}
	return (s.State == "parked") == (s.LastCode == "attempts_exhausted") && (s.State != "parked" || p != nil && p.Version == 2 && s.DeliveryAttempts >= s.DeliveryAttemptLimit && s.ClaimExpiresAt == nil)
}

func eventIntentRetryBudget(budget EventAcknowledgmentIntentRetryBudget) (*EventAcknowledgmentIntentStoredRetryPolicy, error) {
	policy, err := eventIntentRetryPolicy(budget.Policy)
	if err != nil || budget.MaxDeliveryAttempts < 1 || budget.MaxDeliveryAttempts > 2147483647 {
		return nil, ErrInvalidEventConsumerRequest
	}
	binding := eventIntentRetryPolicyBinding(policy)
	binding.Version, binding.MaxDeliveryAttempts = 2, budget.MaxDeliveryAttempts
	return binding, nil
}

func eventIntentParkedStatus(err error) EventAcknowledgmentIntentStatus {
	var parked *EventAcknowledgmentIntentParkedError
	if errors.As(err, &parked) {
		return parked.Intent
	}
	return EventAcknowledgmentIntentStatus{}
}

func parkEventIntent(ctx context.Context, tx *sql.Tx, s *eventAcknowledgmentIntentClaim) (EventAcknowledgmentIntentClaim, error) {
	var zero EventAcknowledgmentIntentClaim
	status, err := eventIntentStatus(tx.QueryRowContext(ctx, `UPDATE public.gregale_event_acknowledgment_intents SET state='parked',last_code='attempts_exhausted',claim_token=NULL,claim_expires_at=NULL,updated_at=greatest(clock_timestamp(),updated_at+interval '1 microsecond') WHERE account_id=$1::uuid AND app_id=$2::uuid AND invocation_id=$3::uuid AND replay_generation=$4 AND attempt=$5 AND state IN ('pending','unconfirmed') AND retry_policy_version=2 AND delivery_attempts>=delivery_attempt_limit AND (claim_expires_at IS NULL OR claim_expires_at<=clock_timestamp()) RETURNING `+eventIntentStatusColumns, s.account, s.app, s.invocation, s.generation, s.attempt))
	if err != nil {
		return zero, eventIntentStoreError(ctx)
	}
	if err := tx.Commit(); err != nil {
		return zero, eventIntentCommitError(ctx)
	}
	return zero, &EventAcknowledgmentIntentParkedError{Intent: status}
}

// RunDurableWithRetryBudget atomically saves business writes, their receipt and a
// new v2 completion binding. Repeated staging compares the initial budget even
// after a resume raises the current limit. It sends no terminal HTTP callback.
func (w *EventAcknowledgmentWorker) RunDurableWithRetryBudget(ctx context.Context, db *sql.DB, budget EventAcknowledgmentIntentRetryBudget, handler func(context.Context, EventConsumerSQLTransaction) (json.RawMessage, error)) (EventAcknowledgmentIntentResult, error) {
	binding, err := eventIntentRetryBudget(budget)
	if err != nil {
		return EventAcknowledgmentIntentResult{}, err
	}
	return w.runDurable(ctx, db, nil, binding, handler)
}

// RunDurableWithProcessingLeaseAndRetryBudget additionally fences local preparation.
func (w *EventAcknowledgmentWorker) RunDurableWithProcessingLeaseAndRetryBudget(ctx context.Context, db *sql.DB, lease EventConsumerProcessingLease, budget EventAcknowledgmentIntentRetryBudget, handler func(context.Context, EventConsumerSQLTransaction) (json.RawMessage, error)) (EventAcknowledgmentIntentResult, error) {
	binding, err := eventIntentRetryBudget(budget)
	if err != nil {
		return EventAcknowledgmentIntentResult{}, err
	}
	return w.runDurable(ctx, db, &lease, binding, handler)
}

// SaveCompletionIntentWithRetryBudget stages a committed receipt without business work.
func (w *EventAcknowledgmentWorker) SaveCompletionIntentWithRetryBudget(ctx context.Context, db *sql.DB, budget EventAcknowledgmentIntentRetryBudget) (EventAcknowledgmentIntentResult, error) {
	binding, err := eventIntentRetryBudget(budget)
	if err != nil {
		return EventAcknowledgmentIntentResult{}, err
	}
	return w.saveCompletionIntent(ctx, db, binding)
}

// SaveRetryIntentWithRetryBudget requires known rollback and stopped/exclusive job ownership.
func (w *EventAcknowledgmentWorker) SaveRetryIntentWithRetryBudget(ctx context.Context, db *sql.DB, reason string, budget EventAcknowledgmentIntentRetryBudget) (EventAcknowledgmentIntentStatus, error) {
	binding, err := eventIntentRetryBudget(budget)
	if err != nil {
		return EventAcknowledgmentIntentStatus{}, err
	}
	return w.saveNegativeIntent(ctx, db, "retry", reason, binding)
}

// SaveDeadLetterIntentWithRetryBudget retains the original explicit negative decision.
func (w *EventAcknowledgmentWorker) SaveDeadLetterIntentWithRetryBudget(ctx context.Context, db *sql.DB, reason string, budget EventAcknowledgmentIntentRetryBudget) (EventAcknowledgmentIntentStatus, error) {
	binding, err := eventIntentRetryBudget(budget)
	if err != nil {
		return EventAcknowledgmentIntentStatus{}, err
	}
	return w.saveNegativeIntent(ctx, db, "dead-letter", reason, binding)
}
