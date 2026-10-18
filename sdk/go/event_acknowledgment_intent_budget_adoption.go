package faas

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
)

// EventAcknowledgmentIntentBudgetAdoptionSchema creates optional application-owned
// command receipts. Install explicitly beside the budget-upgraded intent table.
// Neither staging nor recovery nor the v1 adoption/resume commands require it.
// Helpers never execute DDL or prune history.
//
//go:embed event_acknowledgment_intent_budget_adoption_schema.sql
var EventAcknowledgmentIntentBudgetAdoptionSchema string

// EventAcknowledgmentIntentAdoptRetryBudgetOptions guards an unbound/v1 intent.
// Nil ExpectedPolicy requires an unbound row. Otherwise it must be the exact v1
// metadata and Budget must retain its timing. The total must exceed consumed
// claims. Save these original inputs and the command UUID durably before calling;
// an uncertain COMMIT is resolved by retrying them unchanged.
type EventAcknowledgmentIntentAdoptRetryBudgetOptions struct {
	CommandID                string
	ExpectedUpdatedAt        time.Time
	ExpectedDeliveryAttempts int64
	ExpectedPolicy           *EventAcknowledgmentIntentStoredRetryPolicy
	Budget                   EventAcknowledgmentIntentRetryBudget
}

// EventAcknowledgmentIntentBudgetAdoptionReceipt is historical local metadata,
// not current ownership, a recovery capability or remote callback confirmation.
// Only adopted outcomes carry an intent snapshot. Retain receipts independently
// of intents through every possible retry of the original command.
type EventAcknowledgmentIntentBudgetAdoptionReceipt struct {
	CommandID        string                           `json:"command_id"`
	InvocationID     string                           `json:"invocation_id"`
	ReplayGeneration int64                            `json:"replay_generation"`
	Attempt          int                              `json:"attempt"`
	Outcome          string                           `json:"outcome"`
	RecordedAt       time.Time                        `json:"recorded_at"`
	Intent           *EventAcknowledgmentIntentStatus `json:"intent,omitempty"`
}

// Ordered, compact JSON is the SDK-independent v1 command digest contract.
// Zero expected policy fields mean unbound; version 1 requires exact timing.
type eventIntentBudgetAdoptionRequest struct {
	Version                  int       `json:"version"`
	Action                   string    `json:"action"`
	InvocationID             string    `json:"invocation_id"`
	ReplayGeneration         int64     `json:"replay_generation"`
	Attempt                  int       `json:"attempt"`
	ExpectedUpdatedAt        time.Time `json:"expected_updated_at"`
	ExpectedDeliveryAttempts int64     `json:"expected_delivery_attempts"`
	ExpectedPolicyVersion    int       `json:"expected_policy_version"`
	ExpectedInitialBackoffMS int64     `json:"expected_initial_backoff_ms"`
	ExpectedMaxBackoffMS     int64     `json:"expected_max_backoff_ms"`
	PolicyVersion            int       `json:"policy_version"`
	InitialBackoffMS         int64     `json:"initial_backoff_ms"`
	MaxBackoffMS             int64     `json:"max_backoff_ms"`
	MaxDeliveryAttempts      int64     `json:"max_delivery_attempts"`
}

func eventIntentBudgetAdoptionTime(t time.Time) bool {
	utc := t.UTC()
	return !utc.IsZero() && utc.Year() >= 1 && utc.Year() <= 9999 && utc.Nanosecond()%1000 == 0
}

func eventIntentBudgetAdoptionRequestFor(invocationID string, generation int64, attempt int, options EventAcknowledgmentIntentAdoptRetryBudgetOptions) (eventIntentBudgetAdoptionRequest, [32]byte, error) {
	var zero eventIntentBudgetAdoptionRequest
	var noDigest [32]byte
	invocationID = strings.ToLower(invocationID)
	if !operationUUID(invocationID) || generation < 0 || attempt < 1 || int64(attempt) > 2147483647 || !eventIntentBudgetAdoptionTime(options.ExpectedUpdatedAt) || options.ExpectedDeliveryAttempts < 0 || options.ExpectedDeliveryAttempts > 2147483647 {
		return zero, noDigest, ErrInvalidEventConsumerRequest
	}
	binding, err := eventIntentRetryBudget(options.Budget)
	if err != nil || binding.MaxDeliveryAttempts <= options.ExpectedDeliveryAttempts {
		return zero, noDigest, ErrInvalidEventConsumerRequest
	}
	request := eventIntentBudgetAdoptionRequest{
		Version: 1, Action: "adopt_retry_budget", InvocationID: invocationID,
		ReplayGeneration: generation, Attempt: attempt, ExpectedUpdatedAt: options.ExpectedUpdatedAt.UTC(),
		ExpectedDeliveryAttempts: options.ExpectedDeliveryAttempts, PolicyVersion: 2,
		InitialBackoffMS: binding.InitialBackoffMS, MaxBackoffMS: binding.MaxBackoffMS,
		MaxDeliveryAttempts: binding.MaxDeliveryAttempts,
	}
	if options.ExpectedPolicy != nil {
		expected := *options.ExpectedPolicy // Copy all caller-owned metadata before I/O.
		_, err := eventIntentStoredRetryPolicy(sql.NullInt64{Int64: int64(expected.Version), Valid: true}, sql.NullInt64{Int64: expected.InitialBackoffMS, Valid: true}, sql.NullInt64{Int64: expected.MaxBackoffMS, Valid: true})
		if err != nil || expected.MaxDeliveryAttempts != 0 || expected.InitialBackoffMS != binding.InitialBackoffMS || expected.MaxBackoffMS != binding.MaxBackoffMS {
			return zero, noDigest, ErrInvalidEventConsumerRequest
		}
		request.ExpectedPolicyVersion, request.ExpectedInitialBackoffMS, request.ExpectedMaxBackoffMS = expected.Version, expected.InitialBackoffMS, expected.MaxBackoffMS
	}
	body, err := json.Marshal(request)
	if err != nil {
		return zero, noDigest, ErrInvalidEventConsumerRequest
	}
	return request, sha256.Sum256(append([]byte("gregale-event-ack-intent-budget-adoption-v1\n"), body...)), nil
}

const eventIntentBudgetAdoptionColumns = `command_id::text,invocation_id::text,replay_generation,attempt,outcome,recorded_at,intent_status,request_digest`
const eventIntentBudgetAdoptionStatusColumns = `target.invocation_id::text,target.replay_generation,target.attempt,target.action,target.reason,target.state,target.delivery_attempts,target.next_attempt_at,target.claim_expires_at,target.last_code,target.created_at,target.updated_at,target.retry_policy_version,target.retry_initial_backoff_ms,target.retry_max_backoff_ms,target.retry_max_delivery_attempts,target.delivery_attempt_limit`

func validEventIntentBudgetAdoptionStatus(s EventAcknowledgmentIntentStatus, r EventAcknowledgmentIntentBudgetAdoptionReceipt) bool {
	if s.InvocationID != r.InvocationID || s.ReplayGeneration != r.ReplayGeneration || s.Attempt != r.Attempt || (s.State != "pending" && s.State != "unconfirmed") || (s.LastCode != "" && s.LastCode != "unconfirmed") || s.DeliveryAttempts < 0 || s.DeliveryAttempts > 2147483647 || !eventIntentBudgetAdoptionTime(s.NextAttemptAt) || !eventIntentBudgetAdoptionTime(s.CreatedAt) || !eventIntentBudgetAdoptionTime(s.UpdatedAt) || s.ClaimExpiresAt != nil && !eventIntentBudgetAdoptionTime(*s.ClaimExpiresAt) || s.RetryPolicy == nil || s.RetryPolicy.Version != 2 || s.RetryPolicy.MaxDeliveryAttempts != s.DeliveryAttemptLimit || s.DeliveryAttempts >= s.DeliveryAttemptLimit || !eventIntentBudgetStatus(s) {
		return false
	}
	if s.Action == "complete" {
		if s.Reason != "" {
			return false
		}
	} else if (s.Action != "retry" && s.Action != "dead-letter") || !eventWorkerReason(s.Reason) {
		return false
	}
	p := s.RetryPolicy
	_, err := eventIntentStoredBudgetPolicy(sql.NullInt64{Int64: 2, Valid: true}, sql.NullInt64{Int64: p.InitialBackoffMS, Valid: true}, sql.NullInt64{Int64: p.MaxBackoffMS, Valid: true}, sql.NullInt64{Int64: p.MaxDeliveryAttempts, Valid: true}, sql.NullInt64{Int64: s.DeliveryAttemptLimit, Valid: true})
	return err == nil
}

func eventIntentBudgetAdoptionMatches(s EventAcknowledgmentIntentStatus, request eventIntentBudgetAdoptionRequest) bool {
	return s.DeliveryAttempts == request.ExpectedDeliveryAttempts && s.UpdatedAt.After(request.ExpectedUpdatedAt) && s.RetryPolicy != nil && s.RetryPolicy.Version == request.PolicyVersion && s.RetryPolicy.InitialBackoffMS == request.InitialBackoffMS && s.RetryPolicy.MaxBackoffMS == request.MaxBackoffMS && s.RetryPolicy.MaxDeliveryAttempts == request.MaxDeliveryAttempts && s.DeliveryAttemptLimit == request.MaxDeliveryAttempts
}

func eventIntentBudgetAdoptionReceipt(row eventIntentScanner) (EventAcknowledgmentIntentBudgetAdoptionReceipt, []byte, error) {
	var out EventAcknowledgmentIntentBudgetAdoptionReceipt
	var body sql.NullString
	var digest []byte
	if err := row.Scan(&out.CommandID, &out.InvocationID, &out.ReplayGeneration, &out.Attempt, &out.Outcome, &out.RecordedAt, &body, &digest); err != nil {
		return out, nil, err
	}
	if !operationUUID(out.CommandID) || !operationUUID(out.InvocationID) || out.ReplayGeneration < 0 || out.Attempt < 1 || int64(out.Attempt) > 2147483647 || len(digest) != 32 || !eventIntentBudgetAdoptionTime(out.RecordedAt) || errors.Is(eventIntentAdoptionOutcomeError(out.Outcome), ErrEventAcknowledgmentIntentStore) || (out.Outcome == "adopted") != body.Valid {
		return out, nil, ErrEventAcknowledgmentIntentStore
	}
	if body.Valid {
		var status EventAcknowledgmentIntentStatus
		if len(body.String) == 0 || len(body.String) > 8192 || json.Unmarshal([]byte(body.String), &status) != nil || !validEventIntentBudgetAdoptionStatus(status, out) {
			return out, nil, ErrEventAcknowledgmentIntentStore
		}
		out.Intent = &status
	}
	return out, digest, nil
}

// AdoptEventAcknowledgmentIntentRetryBudget upgrades one unbound/v1 active intent
// to v2 with exact timestamp/count/policy guards, no live claim and no newer attempt.
// V1 timing, callback data, state, count, due time and expired claim metadata remain
// unchanged. Only policy/grant columns and UpdatedAt change. The initial total is
// also the current grant and must exceed consumed claims; this never parks a row.
// Exact command retries return historical evidence before current row checks, with
// no new COMMIT. Rejected commands retain their outcome and sentinel. Changed input
// under a retained owner/command UUID conflicts without overwriting the receipt.
// Unknown COMMIT returns no receipt: retry the original inputs on a fresh connection.
// No HTTP, business work, renewal, recovery claim or job acknowledgment occurs.
func AdoptEventAcknowledgmentIntentRetryBudget(ctx context.Context, db *sql.DB, accountID, appID, invocationID string, generation int64, attempt int, options EventAcknowledgmentIntentAdoptRetryBudgetOptions) (EventAcknowledgmentIntentBudgetAdoptionReceipt, error) {
	var zero EventAcknowledgmentIntentBudgetAdoptionReceipt
	commandID := strings.ToLower(options.CommandID)
	if ctx == nil || db == nil || !operationUUID(commandID) {
		return zero, ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return zero, err
	}
	request, digest, err := eventIntentBudgetAdoptionRequestFor(invocationID, generation, attempt, options)
	if err != nil {
		return zero, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return zero, eventIntentStoreError(ctx)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "gregale.event-ack-intent-budget-adoption.v1:"+account+":"+app+":"+commandID); err != nil {
		return zero, eventIntentStoreError(ctx)
	}
	saved, savedDigest, err := eventIntentBudgetAdoptionReceipt(tx.QueryRowContext(ctx, `SELECT `+eventIntentBudgetAdoptionColumns+` FROM public.gregale_event_ack_intent_budget_adoptions WHERE account_id=$1::uuid AND app_id=$2::uuid AND command_id=$3::uuid`, account, app, commandID))
	if err == nil {
		if saved.CommandID != commandID || saved.InvocationID != request.InvocationID || saved.ReplayGeneration != request.ReplayGeneration || saved.Attempt != request.Attempt || !bytes.Equal(savedDigest, digest[:]) {
			return zero, ErrEventAcknowledgmentIntentConflict
		}
		if saved.Intent != nil && !eventIntentBudgetAdoptionMatches(*saved.Intent, request) {
			return zero, eventIntentStoreError(ctx)
		}
		return saved, eventIntentAdoptionOutcomeError(saved.Outcome)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return zero, eventIntentStoreError(ctx)
	}
	return commitEventIntentBudgetAdoption(ctx, tx, account, app, commandID, request, digest)
}

func commitEventIntentBudgetAdoption(ctx context.Context, tx *sql.Tx, account, app, commandID string, request eventIntentBudgetAdoptionRequest, digest [32]byte) (EventAcknowledgmentIntentBudgetAdoptionReceipt, error) {
	var zero EventAcknowledgmentIntentBudgetAdoptionReceipt
	status, actionErr := adoptEventIntentBudget(ctx, tx, account, app, request)
	out := EventAcknowledgmentIntentBudgetAdoptionReceipt{CommandID: commandID, InvocationID: request.InvocationID, ReplayGeneration: request.ReplayGeneration, Attempt: request.Attempt}
	var body any
	switch {
	case actionErr == nil:
		out.Outcome, out.Intent = "adopted", &status
		if !validEventIntentBudgetAdoptionStatus(status, out) || !eventIntentBudgetAdoptionMatches(status, request) {
			return zero, eventIntentStoreError(ctx)
		}
		encoded, err := json.Marshal(status)
		if err != nil || len(encoded) > 8192 {
			return zero, eventIntentStoreError(ctx)
		}
		body = string(encoded)
	case errors.Is(actionErr, ErrEventAcknowledgmentIntentConflict):
		out.Outcome = "conflict"
	case errors.Is(actionErr, ErrNotFound):
		out.Outcome = "not_found"
	default:
		return zero, actionErr
	}
	if err := tx.QueryRowContext(ctx, `INSERT INTO public.gregale_event_ack_intent_budget_adoptions(account_id,app_id,command_id,invocation_id,replay_generation,attempt,request_digest,outcome,intent_status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9) RETURNING recorded_at`, account, app, commandID, request.InvocationID, request.ReplayGeneration, request.Attempt, digest[:], out.Outcome, body).Scan(&out.RecordedAt); err != nil {
		return zero, eventIntentStoreError(ctx)
	}
	if !eventIntentBudgetAdoptionTime(out.RecordedAt) {
		return zero, eventIntentStoreError(ctx)
	}
	if err := tx.Commit(); err != nil {
		return zero, eventIntentCommitError(ctx)
	}
	return out, actionErr
}

func adoptEventIntentBudget(ctx context.Context, tx *sql.Tx, account, app string, request eventIntentBudgetAdoptionRequest) (EventAcknowledgmentIntentStatus, error) {
	var zero EventAcknowledgmentIntentStatus
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "gregale.event-ack-intent.v1:"+account+":"+app+":"+request.InvocationID); err != nil {
		return zero, eventIntentStoreError(ctx)
	}
	_, err := eventIntentStatus(tx.QueryRowContext(ctx, `SELECT `+eventIntentStatusColumns+` FROM public.gregale_event_acknowledgment_intents WHERE account_id=$1::uuid AND app_id=$2::uuid AND invocation_id=$3::uuid AND replay_generation=$4 AND attempt=$5 FOR UPDATE`, account, app, request.InvocationID, request.ReplayGeneration, request.Attempt))
	if errors.Is(err, sql.ErrNoRows) {
		return zero, ErrNotFound
	}
	if err != nil {
		return zero, eventIntentStoreError(ctx)
	}
	status, err := eventIntentStatus(tx.QueryRowContext(ctx, `UPDATE public.gregale_event_acknowledgment_intents AS target SET retry_policy_version=2,retry_initial_backoff_ms=$11::bigint,retry_max_backoff_ms=$12::bigint,retry_max_delivery_attempts=$13::bigint,delivery_attempt_limit=$13::bigint,updated_at=greatest(clock_timestamp(),target.updated_at+interval '1 microsecond') WHERE target.account_id=$1::uuid AND target.app_id=$2::uuid AND target.invocation_id=$3::uuid AND target.replay_generation=$4 AND target.attempt=$5 AND target.updated_at=$6::timestamptz AND target.delivery_attempts=$7::bigint AND target.state IN ('pending','unconfirmed') AND target.retry_max_delivery_attempts IS NULL AND target.delivery_attempt_limit IS NULL AND $13::bigint>target.delivery_attempts AND (target.claim_expires_at IS NULL OR target.claim_expires_at<=clock_timestamp()) AND (($8::integer=0 AND target.retry_policy_version IS NULL AND target.retry_initial_backoff_ms IS NULL AND target.retry_max_backoff_ms IS NULL) OR ($8::integer=1 AND target.retry_policy_version=1 AND target.retry_initial_backoff_ms=$9::bigint AND target.retry_max_backoff_ms=$10::bigint AND $11::bigint=target.retry_initial_backoff_ms AND $12::bigint=target.retry_max_backoff_ms)) AND NOT EXISTS(SELECT 1 FROM public.gregale_event_acknowledgment_intents newer WHERE newer.account_id=target.account_id AND newer.app_id=target.app_id AND newer.invocation_id=target.invocation_id AND (newer.replay_generation,newer.attempt)>(target.replay_generation,target.attempt)) RETURNING `+eventIntentBudgetAdoptionStatusColumns, account, app, request.InvocationID, request.ReplayGeneration, request.Attempt, request.ExpectedUpdatedAt, request.ExpectedDeliveryAttempts, request.ExpectedPolicyVersion, request.ExpectedInitialBackoffMS, request.ExpectedMaxBackoffMS, request.InitialBackoffMS, request.MaxBackoffMS, request.MaxDeliveryAttempts))
	if errors.Is(err, sql.ErrNoRows) {
		return zero, ErrEventAcknowledgmentIntentConflict
	}
	if err != nil {
		return zero, eventIntentStoreError(ctx)
	}
	return status, nil
}
