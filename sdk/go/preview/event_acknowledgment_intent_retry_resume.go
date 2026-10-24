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

// EventAcknowledgmentIntentRetryResumeSchema creates optional application-owned
// command receipts. Install explicitly before resume, independently of intent
// retention. Staging/recovery never requires this additional table or executes DDL.
//
//go:embed event_acknowledgment_intent_retry_resume_schema.sql
var EventAcknowledgmentIntentRetryResumeSchema string

type EventAcknowledgmentIntentResumeRetryBudgetOptions struct {
	CommandID                    string
	ExpectedUpdatedAt            time.Time
	ExpectedDeliveryAttempts     int64
	ExpectedDeliveryAttemptLimit int64
	NewDeliveryAttemptLimit      int64
}

// EventAcknowledgmentIntentRetryResumeReceipt is a historical command outcome.
// Rejections have no snapshot. Success grants no claim or remote confirmation.
type EventAcknowledgmentIntentRetryResumeReceipt struct {
	CommandID        string                           `json:"command_id"`
	InvocationID     string                           `json:"invocation_id"`
	ReplayGeneration int64                            `json:"replay_generation"`
	Attempt          int                              `json:"attempt"`
	Outcome          string                           `json:"outcome"`
	RecordedAt       time.Time                        `json:"recorded_at"`
	Intent           *EventAcknowledgmentIntentStatus `json:"intent,omitempty"`
}

type eventIntentRetryResumeRequest struct {
	Version                      int       `json:"version"`
	Action                       string    `json:"action"`
	InvocationID                 string    `json:"invocation_id"`
	ReplayGeneration             int64     `json:"replay_generation"`
	Attempt                      int       `json:"attempt"`
	ExpectedUpdatedAt            time.Time `json:"expected_updated_at"`
	ExpectedDeliveryAttempts     int64     `json:"expected_delivery_attempts"`
	ExpectedDeliveryAttemptLimit int64     `json:"expected_delivery_attempt_limit"`
	NewDeliveryAttemptLimit      int64     `json:"new_delivery_attempt_limit"`
}

func eventIntentRetryResumeRequestFor(invocationID string, generation int64, attempt int, options EventAcknowledgmentIntentResumeRetryBudgetOptions) (eventIntentRetryResumeRequest, [32]byte, error) {
	var zero eventIntentRetryResumeRequest
	var noDigest [32]byte
	invocationID = strings.ToLower(invocationID)
	if !operationUUID(invocationID) || generation < 0 || attempt < 1 || int64(attempt) > 2147483647 || options.ExpectedUpdatedAt.IsZero() || options.ExpectedUpdatedAt.Year() < 1 || options.ExpectedUpdatedAt.Year() > 9999 || options.ExpectedUpdatedAt.Nanosecond()%1000 != 0 || options.ExpectedDeliveryAttempts < 0 || options.ExpectedDeliveryAttempts > 2147483647 || options.ExpectedDeliveryAttemptLimit < 1 || options.ExpectedDeliveryAttemptLimit > 2147483647 || options.NewDeliveryAttemptLimit <= options.ExpectedDeliveryAttemptLimit || options.NewDeliveryAttemptLimit <= options.ExpectedDeliveryAttempts || options.NewDeliveryAttemptLimit > 2147483647 {
		return zero, noDigest, ErrInvalidEventConsumerRequest
	}
	request := eventIntentRetryResumeRequest{1, "resume_retry_budget", invocationID, generation, attempt, options.ExpectedUpdatedAt.UTC(), options.ExpectedDeliveryAttempts, options.ExpectedDeliveryAttemptLimit, options.NewDeliveryAttemptLimit}
	body, err := json.Marshal(request)
	if err != nil {
		return zero, noDigest, ErrInvalidEventConsumerRequest
	}
	return request, sha256.Sum256(append([]byte("gregale-event-ack-intent-retry-resume-v1\n"), body...)), nil
}

func eventIntentRetryResumeOutcomeError(outcome string) error {
	switch outcome {
	case "resumed":
		return nil
	case "conflict":
		return ErrEventAcknowledgmentIntentConflict
	case "not_found":
		return faas.ErrNotFound
	default:
		return ErrEventAcknowledgmentIntentStore
	}
}

const eventIntentRetryResumeColumns = `command_id::text,invocation_id::text,replay_generation,attempt,outcome,recorded_at,intent_status,request_digest`
const eventIntentRetryResumeStatusColumns = `target.invocation_id::text,target.replay_generation,target.attempt,target.action,target.reason,target.state,target.delivery_attempts,target.next_attempt_at,target.claim_expires_at,target.last_code,target.created_at,target.updated_at,target.retry_policy_version,target.retry_initial_backoff_ms,target.retry_max_backoff_ms,target.retry_max_delivery_attempts,target.delivery_attempt_limit`

func validEventIntentRetryResumeStatus(s EventAcknowledgmentIntentStatus, r EventAcknowledgmentIntentRetryResumeReceipt) bool {
	if s.InvocationID != r.InvocationID || s.ReplayGeneration != r.ReplayGeneration || s.Attempt != r.Attempt || s.State != "unconfirmed" || s.LastCode != "unconfirmed" || s.DeliveryAttempts < 1 || s.DeliveryAttempts > 2147483647 || s.ClaimExpiresAt != nil || s.NextAttemptAt.IsZero() || s.CreatedAt.IsZero() || s.UpdatedAt.IsZero() || s.RetryPolicy == nil || s.RetryPolicy.Version != 2 || !eventIntentBudgetStatus(s) {
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
	return err == nil && s.DeliveryAttempts < s.DeliveryAttemptLimit
}

func eventIntentRetryResumeReceipt(row eventIntentScanner) (EventAcknowledgmentIntentRetryResumeReceipt, []byte, error) {
	var out EventAcknowledgmentIntentRetryResumeReceipt
	var body sql.NullString
	var digest []byte
	err := row.Scan(&out.CommandID, &out.InvocationID, &out.ReplayGeneration, &out.Attempt, &out.Outcome, &out.RecordedAt, &body, &digest)
	if err != nil {
		return out, nil, err
	}
	if !operationUUID(out.CommandID) || !operationUUID(out.InvocationID) || out.ReplayGeneration < 0 || out.Attempt < 1 || len(digest) != 32 || out.RecordedAt.IsZero() || errors.Is(eventIntentRetryResumeOutcomeError(out.Outcome), ErrEventAcknowledgmentIntentStore) || (out.Outcome == "resumed") != body.Valid {
		return out, nil, ErrEventAcknowledgmentIntentStore
	}
	if body.Valid {
		var status EventAcknowledgmentIntentStatus
		if len(body.String) == 0 || len(body.String) > 8192 || json.Unmarshal([]byte(body.String), &status) != nil || !validEventIntentRetryResumeStatus(status, out) {
			return out, nil, ErrEventAcknowledgmentIntentStore
		}
		out.Intent = &status
	}
	return out, digest, nil
}

// ResumeEventAcknowledgmentIntentRetryBudget raises the total claim limit and
// resumes one parked v2 intent at its existing due time. Guards require the exact
// timestamp/count/current limit, no claim and no newer generation/attempt. Counts,
// initial policy, action and protected callback inputs are preserved. Exact command
// retries return historical success/rejection before checking current row state.
// Unknown COMMIT returns no receipt; retry the same original inputs on recovery.
// No HTTP, business work, platform renewal or automatic scheduling occurs here.
func ResumeEventAcknowledgmentIntentRetryBudget(ctx context.Context, db *sql.DB, accountID, appID, invocationID string, generation int64, attempt int, options EventAcknowledgmentIntentResumeRetryBudgetOptions) (EventAcknowledgmentIntentRetryResumeReceipt, error) {
	var zero EventAcknowledgmentIntentRetryResumeReceipt
	commandID := strings.ToLower(options.CommandID)
	if ctx == nil || db == nil || !operationUUID(commandID) {
		return zero, ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return zero, err
	}
	request, digest, err := eventIntentRetryResumeRequestFor(invocationID, generation, attempt, options)
	if err != nil {
		return zero, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return zero, eventIntentStoreError(ctx)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "gregale.event-ack-intent-retry-resume.v1:"+account+":"+app+":"+commandID); err != nil {
		return zero, eventIntentStoreError(ctx)
	}
	saved, savedDigest, err := eventIntentRetryResumeReceipt(tx.QueryRowContext(ctx, `SELECT `+eventIntentRetryResumeColumns+` FROM public.gregale_event_ack_intent_retry_resumes WHERE account_id=$1::uuid AND app_id=$2::uuid AND command_id=$3::uuid`, account, app, commandID))
	if err == nil {
		if saved.InvocationID != request.InvocationID || saved.ReplayGeneration != request.ReplayGeneration || saved.Attempt != request.Attempt || !bytes.Equal(savedDigest, digest[:]) {
			return zero, ErrEventAcknowledgmentIntentConflict
		}
		if saved.Intent != nil && (saved.Intent.DeliveryAttempts != request.ExpectedDeliveryAttempts || saved.Intent.DeliveryAttemptLimit != request.NewDeliveryAttemptLimit || saved.Intent.RetryPolicy.MaxDeliveryAttempts > request.ExpectedDeliveryAttemptLimit || !saved.Intent.UpdatedAt.After(request.ExpectedUpdatedAt)) {
			return zero, eventIntentStoreError(ctx)
		}
		return saved, eventIntentRetryResumeOutcomeError(saved.Outcome)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return zero, eventIntentStoreError(ctx)
	}
	return commitEventIntentRetryResume(ctx, tx, account, app, commandID, request, digest)
}

func commitEventIntentRetryResume(ctx context.Context, tx *sql.Tx, account, app, commandID string, request eventIntentRetryResumeRequest, digest [32]byte) (EventAcknowledgmentIntentRetryResumeReceipt, error) {
	var zero EventAcknowledgmentIntentRetryResumeReceipt
	status, actionErr := resumeEventIntentRetryBudget(ctx, tx, account, app, request)
	out := EventAcknowledgmentIntentRetryResumeReceipt{CommandID: commandID, InvocationID: request.InvocationID, ReplayGeneration: request.ReplayGeneration, Attempt: request.Attempt}
	var body any
	switch {
	case actionErr == nil:
		out.Outcome, out.Intent = "resumed", &status
		if !validEventIntentRetryResumeStatus(status, out) {
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
	err := tx.QueryRowContext(ctx, `INSERT INTO public.gregale_event_ack_intent_retry_resumes(account_id,app_id,command_id,invocation_id,replay_generation,attempt,request_digest,outcome,intent_status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9) RETURNING recorded_at`, account, app, commandID, request.InvocationID, request.ReplayGeneration, request.Attempt, digest[:], out.Outcome, body).Scan(&out.RecordedAt)
	if err != nil {
		return zero, eventIntentStoreError(ctx)
	}
	if err := tx.Commit(); err != nil {
		return zero, eventIntentCommitError(ctx)
	}
	return out, actionErr
}

func resumeEventIntentRetryBudget(ctx context.Context, tx *sql.Tx, account, app string, request eventIntentRetryResumeRequest) (EventAcknowledgmentIntentStatus, error) {
	var zero EventAcknowledgmentIntentStatus
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
	status, err := eventIntentStatus(tx.QueryRowContext(ctx, `UPDATE public.gregale_event_acknowledgment_intents AS target SET state='unconfirmed',last_code='unconfirmed',delivery_attempt_limit=$9::bigint,updated_at=greatest(clock_timestamp(),target.updated_at+interval '1 microsecond') WHERE target.account_id=$1::uuid AND target.app_id=$2::uuid AND target.invocation_id=$3::uuid AND target.replay_generation=$4 AND target.attempt=$5 AND target.updated_at=$6::timestamptz AND target.delivery_attempts=$7::bigint AND target.delivery_attempt_limit=$8::bigint AND target.state='parked' AND target.retry_policy_version=2 AND target.delivery_attempts>=target.delivery_attempt_limit AND $9::bigint>target.delivery_attempt_limit AND $9::bigint>target.delivery_attempts AND target.claim_token IS NULL AND target.claim_expires_at IS NULL AND NOT EXISTS(SELECT 1 FROM public.gregale_event_acknowledgment_intents newer WHERE newer.account_id=target.account_id AND newer.app_id=target.app_id AND newer.invocation_id=target.invocation_id AND (newer.replay_generation,newer.attempt)>(target.replay_generation,target.attempt)) RETURNING `+eventIntentRetryResumeStatusColumns, account, app, request.InvocationID, request.ReplayGeneration, request.Attempt, request.ExpectedUpdatedAt, request.ExpectedDeliveryAttempts, request.ExpectedDeliveryAttemptLimit, request.NewDeliveryAttemptLimit))
	if errors.Is(err, sql.ErrNoRows) {
		return zero, ErrEventAcknowledgmentIntentConflict
	}
	if err != nil {
		return zero, eventIntentStoreError(ctx)
	}
	return status, nil
}
