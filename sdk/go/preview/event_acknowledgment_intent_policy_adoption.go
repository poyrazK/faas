package preview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"strings"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

// EventAcknowledgmentIntentPolicyAdoptionSchema creates optional application-owned
// command receipts. Install explicitly beside the upgraded intent table before
// adoption. Neither existing staging nor recovery requires this additional table.
// Helpers never install or prune it.
//
//go:embed event_acknowledgment_intent_policy_adoption_schema.sql
var EventAcknowledgmentIntentPolicyAdoptionSchema string

// EventAcknowledgmentIntentAdoptRetryPolicyOptions guards a one-time binding of an
// unbound intent. Preserve the command ID and exact original options for retries,
// including after an unknown COMMIT. ExpectedUpdatedAt comes from intent metadata
// at PostgreSQL microsecond precision; ExpectedDeliveryAttempts includes all claims.
type EventAcknowledgmentIntentAdoptRetryPolicyOptions struct {
	CommandID                string
	ExpectedUpdatedAt        time.Time
	ExpectedDeliveryAttempts int64
	Policy                   EventAcknowledgmentIntentRetryPolicy
}

// EventAcknowledgmentIntentPolicyAdoptionReceipt is a retained local command
// outcome, not current delivery evidence. Intent is the historical metadata at
// successful adoption; rejected commands have no snapshot. It contains no callback
// token, result, request digest or recovery capability. Retain receipts for the
// entire command retry horizon, independently of intent retention.
type EventAcknowledgmentIntentPolicyAdoptionReceipt struct {
	CommandID        string                           `json:"command_id"`
	InvocationID     string                           `json:"invocation_id"`
	ReplayGeneration int64                            `json:"replay_generation"`
	Attempt          int                              `json:"attempt"`
	Outcome          string                           `json:"outcome"`
	RecordedAt       time.Time                        `json:"recorded_at"`
	Intent           *EventAcknowledgmentIntentStatus `json:"intent,omitempty"`
}

// Fixed v1 JSON framing also defines the digest contract for future SDK parity.
// Scope/command ID are the receipt key; the request binds the action and all inputs.
type eventIntentPolicyAdoptionRequest struct {
	Version                  int       `json:"version"`
	Action                   string    `json:"action"`
	InvocationID             string    `json:"invocation_id"`
	ReplayGeneration         int64     `json:"replay_generation"`
	Attempt                  int       `json:"attempt"`
	ExpectedUpdatedAt        time.Time `json:"expected_updated_at"`
	ExpectedDeliveryAttempts int64     `json:"expected_delivery_attempts"`
	PolicyVersion            int       `json:"policy_version"`
	InitialBackoffMS         int64     `json:"initial_backoff_ms"`
	MaxBackoffMS             int64     `json:"max_backoff_ms"`
}

func eventIntentAdoptionRequest(invocationID string, generation int64, attempt int, options EventAcknowledgmentIntentAdoptRetryPolicyOptions) (eventIntentPolicyAdoptionRequest, [32]byte, error) {
	var zero eventIntentPolicyAdoptionRequest
	var noDigest [32]byte
	invocationID = strings.ToLower(invocationID)
	if !operationUUID(invocationID) || generation < 0 || attempt < 1 || int64(attempt) > 2147483647 || options.ExpectedDeliveryAttempts < 0 || options.ExpectedDeliveryAttempts > 2147483647 || options.ExpectedUpdatedAt.IsZero() || options.ExpectedUpdatedAt.Year() < 1 || options.ExpectedUpdatedAt.Year() > 9999 || options.ExpectedUpdatedAt.Nanosecond()%1000 != 0 {
		return zero, noDigest, ErrInvalidEventConsumerRequest
	}
	policy, err := eventIntentRetryPolicy(options.Policy)
	if err != nil {
		return zero, noDigest, err
	}
	request := eventIntentPolicyAdoptionRequest{1, "adopt_retry_policy", invocationID, generation, attempt, options.ExpectedUpdatedAt.UTC(), options.ExpectedDeliveryAttempts, 1, policy.InitialBackoff.Milliseconds(), policy.MaxBackoff.Milliseconds()}
	body, err := json.Marshal(request)
	if err != nil {
		return zero, noDigest, ErrInvalidEventConsumerRequest
	}
	digest := sha256.Sum256(append([]byte("gregale-event-ack-intent-policy-adoption-v1\n"), body...))
	return request, digest, nil
}

func eventIntentAdoptionOutcomeError(outcome string) error {
	switch outcome {
	case "adopted":
		return nil
	case "conflict":
		return ErrEventAcknowledgmentIntentConflict
	case "not_found":
		return faas.ErrNotFound
	default:
		return ErrEventAcknowledgmentIntentStore
	}
}

const eventIntentAdoptionColumns = `command_id::text,invocation_id::text,replay_generation,attempt,outcome,recorded_at,intent_status,request_digest`

func eventIntentAdoptionReceipt(row eventIntentScanner) (EventAcknowledgmentIntentPolicyAdoptionReceipt, []byte, error) {
	var out EventAcknowledgmentIntentPolicyAdoptionReceipt
	var body sql.NullString
	var digest []byte
	err := row.Scan(&out.CommandID, &out.InvocationID, &out.ReplayGeneration, &out.Attempt, &out.Outcome, &out.RecordedAt, &body, &digest)
	if err != nil {
		return out, nil, err
	}
	if !operationUUID(out.CommandID) || !operationUUID(out.InvocationID) || out.ReplayGeneration < 0 || out.Attempt < 1 || len(digest) != 32 || out.RecordedAt.IsZero() || errors.Is(eventIntentAdoptionOutcomeError(out.Outcome), ErrEventAcknowledgmentIntentStore) || (out.Outcome == "adopted") != body.Valid {
		return out, nil, ErrEventAcknowledgmentIntentStore
	}
	if body.Valid {
		var status EventAcknowledgmentIntentStatus
		if len(body.String) == 0 || len(body.String) > 8192 || json.Unmarshal([]byte(body.String), &status) != nil || !validEventIntentAdoptionStatus(status, out) {
			return out, nil, ErrEventAcknowledgmentIntentStore
		}
		out.Intent = &status
	}
	return out, digest, nil
}

func validEventIntentAdoptionStatus(s EventAcknowledgmentIntentStatus, r EventAcknowledgmentIntentPolicyAdoptionReceipt) bool {
	if s.InvocationID != r.InvocationID || s.ReplayGeneration != r.ReplayGeneration || s.Attempt != r.Attempt || (s.State != "pending" && s.State != "unconfirmed") || s.DeliveryAttempts < 0 || s.DeliveryAttempts > 2147483647 || s.NextAttemptAt.IsZero() || s.CreatedAt.IsZero() || s.UpdatedAt.IsZero() || s.RetryPolicy == nil || s.RetryPolicy.MaxDeliveryAttempts != 0 || s.DeliveryAttemptLimit != 0 {
		return false
	}
	if s.Action == "complete" {
		if s.Reason != "" {
			return false
		}
	} else if (s.Action != "retry" && s.Action != "dead-letter") || !eventWorkerReason(s.Reason) {
		return false
	}
	if s.LastCode != "" && s.LastCode != "unconfirmed" {
		return false
	}
	p := s.RetryPolicy
	_, err := eventIntentStoredRetryPolicy(sql.NullInt64{Int64: int64(p.Version), Valid: true}, sql.NullInt64{Int64: p.InitialBackoffMS, Valid: true}, sql.NullInt64{Int64: p.MaxBackoffMS, Valid: true})
	return err == nil
}

// AdoptEventAcknowledgmentIntentRetryPolicy binds a policy once to an existing
// unbound pending/unconfirmed intent with matching timestamp/count and no live claim.
// It preserves action, callback data, attempt count, due time and expired claim
// metadata. Only policy columns and UpdatedAt change. Matching command retries
// return the historical receipt even after settlement, supersession or pruning.
// Guard conflicts and missing rows are retained with their sentinel error. A
// changed request under the same owner/command ID conflicts without overwriting it.
// An unknown COMMIT returns no receipt: retry the exact original command to resolve
// it. This function sends no HTTP, runs no business work and grants no claim.
func AdoptEventAcknowledgmentIntentRetryPolicy(ctx context.Context, db *sql.DB, accountID, appID, invocationID string, generation int64, attempt int, options EventAcknowledgmentIntentAdoptRetryPolicyOptions) (EventAcknowledgmentIntentPolicyAdoptionReceipt, error) {
	var zero EventAcknowledgmentIntentPolicyAdoptionReceipt
	commandID := strings.ToLower(options.CommandID)
	if ctx == nil || db == nil || !operationUUID(commandID) {
		return zero, ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return zero, err
	}
	request, digest, err := eventIntentAdoptionRequest(invocationID, generation, attempt, options)
	if err != nil {
		return zero, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return zero, eventIntentStoreError(ctx)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "gregale.event-ack-intent-policy-adoption.v1:"+account+":"+app+":"+commandID); err != nil {
		return zero, eventIntentStoreError(ctx)
	}
	saved, savedDigest, err := eventIntentAdoptionReceipt(tx.QueryRowContext(ctx, `SELECT `+eventIntentAdoptionColumns+` FROM public.gregale_event_ack_intent_policy_adoptions WHERE account_id=$1::uuid AND app_id=$2::uuid AND command_id=$3::uuid`, account, app, commandID))
	if err == nil {
		if saved.InvocationID != request.InvocationID || saved.ReplayGeneration != request.ReplayGeneration || saved.Attempt != request.Attempt || !bytes.Equal(savedDigest, digest[:]) {
			return zero, ErrEventAcknowledgmentIntentConflict
		}
		if saved.Intent != nil && (saved.Intent.DeliveryAttempts != request.ExpectedDeliveryAttempts || !saved.Intent.UpdatedAt.After(request.ExpectedUpdatedAt) || saved.Intent.RetryPolicy.InitialBackoffMS != request.InitialBackoffMS || saved.Intent.RetryPolicy.MaxBackoffMS != request.MaxBackoffMS) {
			return zero, eventIntentStoreError(ctx)
		}
		return saved, eventIntentAdoptionOutcomeError(saved.Outcome)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return zero, eventIntentStoreError(ctx)
	}
	return commitEventIntentAdoption(ctx, tx, account, app, commandID, request, digest)
}

func commitEventIntentAdoption(ctx context.Context, tx *sql.Tx, account, app, commandID string, request eventIntentPolicyAdoptionRequest, digest [32]byte) (EventAcknowledgmentIntentPolicyAdoptionReceipt, error) {
	var zero EventAcknowledgmentIntentPolicyAdoptionReceipt
	status, actionErr := adoptEventIntentPolicy(ctx, tx, account, app, request)
	out := EventAcknowledgmentIntentPolicyAdoptionReceipt{CommandID: commandID, InvocationID: request.InvocationID, ReplayGeneration: request.ReplayGeneration, Attempt: request.Attempt}
	var body any
	switch {
	case actionErr == nil:
		out.Outcome, out.Intent = "adopted", &status
		if !validEventIntentAdoptionStatus(status, out) {
			return zero, eventIntentStoreError(ctx)
		}
		encoded, err := json.Marshal(status)
		if err != nil || len(encoded) > 8192 {
			return zero, eventIntentStoreError(ctx)
		}
		body = string(encoded)
	case errors.Is(actionErr, ErrEventAcknowledgmentIntentConflict):
		out.Outcome = "conflict"
	case errors.Is(actionErr, faas.ErrNotFound):
		out.Outcome = "not_found"
	default:
		return zero, actionErr
	}
	err := tx.QueryRowContext(ctx, `INSERT INTO public.gregale_event_ack_intent_policy_adoptions(account_id,app_id,command_id,invocation_id,replay_generation,attempt,request_digest,outcome,intent_status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9) RETURNING recorded_at`, account, app, commandID, request.InvocationID, request.ReplayGeneration, request.Attempt, digest[:], out.Outcome, body).Scan(&out.RecordedAt)
	if err != nil {
		return zero, eventIntentStoreError(ctx)
	}
	if err := tx.Commit(); err != nil {
		return zero, eventIntentCommitError(ctx)
	}
	return out, actionErr
}

func adoptEventIntentPolicy(ctx context.Context, tx *sql.Tx, account, app string, request eventIntentPolicyAdoptionRequest) (EventAcknowledgmentIntentStatus, error) {
	var zero EventAcknowledgmentIntentStatus
	// Same invocation lock/order as staging, followed by the row lock shared with
	// recovery. No consumer-inbox lock is needed: adoption never changes an action.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "gregale.event-ack-intent.v1:"+account+":"+app+":"+request.InvocationID); err != nil {
		return zero, eventIntentStoreError(ctx)
	}
	_, err := eventIntentStatus(tx.QueryRowContext(ctx, `SELECT `+eventIntentStatusColumns+` FROM public.gregale_event_acknowledgment_intents WHERE account_id=$1::uuid AND app_id=$2::uuid AND invocation_id=$3::uuid AND replay_generation=$4 AND attempt=$5 FOR UPDATE`, account, app, request.InvocationID, request.ReplayGeneration, request.Attempt))
	if errors.Is(err, sql.ErrNoRows) {
		return zero, faas.ErrNotFound
	}
	if err != nil {
		return zero, eventIntentStoreError(ctx)
	}
	status, err := eventIntentStatus(tx.QueryRowContext(ctx, `UPDATE public.gregale_event_acknowledgment_intents SET retry_policy_version=1,retry_initial_backoff_ms=$8::bigint,retry_max_backoff_ms=$9::bigint,updated_at=greatest(clock_timestamp(),updated_at+interval '1 microsecond') WHERE account_id=$1::uuid AND app_id=$2::uuid AND invocation_id=$3::uuid AND replay_generation=$4 AND attempt=$5 AND updated_at=$6::timestamptz AND delivery_attempts=$7::bigint AND state IN ('pending','unconfirmed') AND retry_policy_version IS NULL AND retry_initial_backoff_ms IS NULL AND retry_max_backoff_ms IS NULL AND retry_max_delivery_attempts IS NULL AND delivery_attempt_limit IS NULL AND (claim_expires_at IS NULL OR claim_expires_at<=clock_timestamp()) RETURNING `+eventIntentStatusColumns, account, app, request.InvocationID, request.ReplayGeneration, request.Attempt, request.ExpectedUpdatedAt, request.ExpectedDeliveryAttempts, request.InitialBackoffMS, request.MaxBackoffMS))
	if errors.Is(err, sql.ErrNoRows) {
		return zero, ErrEventAcknowledgmentIntentConflict
	}
	if err != nil {
		return zero, eventIntentStoreError(ctx)
	}
	return status, nil
}
