package faas

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// EventAcknowledgmentIntentSchema is opt-in, application-owned DDL. No helper
// installs or prunes it. Pending rows contain protected callback capabilities.
//
//go:embed event_acknowledgment_intent_schema.sql
var EventAcknowledgmentIntentSchema string

var (
	ErrEventAcknowledgmentIntentConflict   = errors.New("immutable acknowledgment intent conflicts")
	ErrEventAcknowledgmentIntentSuperseded = errors.New("acknowledgment intent has a newer claim")
	ErrEventAcknowledgmentIntentStore      = errors.New("acknowledgment intent storage operation failed")
	ErrEventAcknowledgmentIntentEmpty      = errors.New("no acknowledgment intent is due")
	ErrEventAcknowledgmentIntentClaimLost  = errors.New("acknowledgment intent recovery lease expired or replaced")
)

// Status contains no token, result, original event body or claim capability.
type EventAcknowledgmentIntentStatus struct {
	InvocationID     string     `json:"invocation_id"`
	ReplayGeneration int64      `json:"replay_generation"`
	Attempt          int        `json:"attempt"`
	Action           string     `json:"action"`
	Reason           string     `json:"reason,omitempty"`
	State            string     `json:"state"`
	DeliveryAttempts int64      `json:"delivery_attempts"`
	NextAttemptAt    time.Time  `json:"next_attempt_at"`
	ClaimExpiresAt   *time.Time `json:"claim_expires_at,omitempty"`
	LastCode         string     `json:"last_code,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}
type EventAcknowledgmentIntentResult struct {
	Receipt          EventConsumerTransactionResult
	ReceiptConfirmed bool
	Intent           EventAcknowledgmentIntentStatus
}

const eventIntentStatusColumns = `invocation_id::text,replay_generation,attempt,action,reason,state,delivery_attempts,next_attempt_at,claim_expires_at,last_code,created_at,updated_at`

type eventIntentScanner interface{ Scan(...any) error }

func eventIntentStatus(row eventIntentScanner) (EventAcknowledgmentIntentStatus, error) {
	var s EventAcknowledgmentIntentStatus
	var expiry sql.NullTime
	err := row.Scan(&s.InvocationID, &s.ReplayGeneration, &s.Attempt, &s.Action, &s.Reason, &s.State, &s.DeliveryAttempts, &s.NextAttemptAt, &expiry, &s.LastCode, &s.CreatedAt, &s.UpdatedAt)
	if expiry.Valid {
		s.ClaimExpiresAt = &expiry.Time
	}
	return s, err
}
func eventIntentStoreError(ctx context.Context) error {
	// Driver diagnostics may include a rejected row's secret token/result.
	return errors.Join(ErrEventAcknowledgmentIntentStore, ctx.Err())
}
func eventIntentCommitError(ctx context.Context) error {
	return errors.Join(ErrEventConsumerCommitUnknown, eventIntentStoreError(ctx))
}
func eventIntentScope(account, app string) (string, string, error) {
	account, app = strings.ToLower(account), strings.ToLower(app)
	if !operationUUID(account) || !operationUUID(app) {
		return "", "", ErrInvalidEventConsumerRequest
	}
	return account, app, nil
}
func (s *eventAcknowledgmentWorker) intentLock(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "gregale.event-ack-intent.v1:"+s.input.identity.AccountID+":"+s.input.identity.AppID+":"+s.delivery.InvocationID)
	if err != nil {
		return eventIntentStoreError(ctx)
	}
	return nil
}
func (s *eventAcknowledgmentWorker) saveIntent(ctx context.Context, tx *sql.Tx, action, reason string, body json.RawMessage) (EventAcknowledgmentIntentStatus, error) {
	var zero EventAcknowledgmentIntentStatus
	if len(s.app) > 256 {
		return zero, ErrInvalidEventConsumerRequest
	}
	if err := s.intentLock(ctx, tx); err != nil {
		return zero, err
	}
	identity, request := eventConsumerDigests(s.input)
	capability := sha256.Sum256([]byte(s.token))
	payload := sha256.Sum256(append([]byte(action+"\n"+reason+"\n"), body...))
	id := s.input.identity
	var generation int64
	var attempt int
	var oldIdentity, oldRequest []byte
	err := tx.QueryRowContext(ctx, `SELECT replay_generation,attempt,identity_digest,request_digest FROM public.gregale_event_acknowledgment_intents WHERE account_id=$1::uuid AND app_id=$2::uuid AND invocation_id=$3::uuid ORDER BY replay_generation DESC,attempt DESC LIMIT 1`, id.AccountID, id.AppID, s.delivery.InvocationID).Scan(&generation, &attempt, &oldIdentity, &oldRequest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return zero, eventIntentStoreError(ctx)
	}
	if err == nil {
		if !bytes.Equal(oldIdentity, identity[:]) || !bytes.Equal(oldRequest, request[:]) {
			return zero, ErrEventAcknowledgmentIntentConflict
		}
		if generation > s.delivery.ReplayGeneration || generation == s.delivery.ReplayGeneration && attempt > s.delivery.Attempt {
			return zero, ErrEventAcknowledgmentIntentSuperseded
		}
		if generation == s.delivery.ReplayGeneration && attempt == s.delivery.Attempt {
			var matches bool
			if err := tx.QueryRowContext(ctx, `SELECT capability_digest=$6 AND action_digest=$7 AND app_slug=$8 AND lease_seconds=$9 FROM public.gregale_event_acknowledgment_intents WHERE account_id=$1::uuid AND app_id=$2::uuid AND invocation_id=$3::uuid AND replay_generation=$4 AND attempt=$5`, id.AccountID, id.AppID, s.delivery.InvocationID, generation, attempt, capability[:], payload[:], s.app, s.seconds).Scan(&matches); err != nil {
				return zero, eventIntentStoreError(ctx)
			}
			if !matches {
				return zero, ErrEventAcknowledgmentIntentConflict
			}
			status, err := eventIntentStatus(tx.QueryRowContext(ctx, `SELECT `+eventIntentStatusColumns+` FROM public.gregale_event_acknowledgment_intents WHERE account_id=$1::uuid AND app_id=$2::uuid AND invocation_id=$3::uuid AND replay_generation=$4 AND attempt=$5`, id.AccountID, id.AppID, s.delivery.InvocationID, generation, attempt))
			if err != nil {
				return zero, eventIntentStoreError(ctx)
			}
			if status.State == "superseded" {
				return zero, ErrEventAcknowledgmentIntentSuperseded
			}
			return status, nil
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE public.gregale_event_acknowledgment_intents SET state='superseded',token=NULL,result_body=NULL,claim_token=NULL,claim_expires_at=NULL,last_code='claim_replaced',updated_at=clock_timestamp() WHERE account_id=$1::uuid AND app_id=$2::uuid AND invocation_id=$3::uuid AND state IN ('pending','unconfirmed')`, id.AccountID, id.AppID, s.delivery.InvocationID); err != nil {
		return zero, eventIntentStoreError(ctx)
	}
	var result any
	if action == "complete" {
		result = string(body)
	}
	status, err := eventIntentStatus(tx.QueryRowContext(ctx, `INSERT INTO public.gregale_event_acknowledgment_intents(account_id,app_id,invocation_id,replay_generation,attempt,identity_digest,request_digest,capability_digest,action_digest,app_slug,lease_seconds,action,reason,token,result_body,state) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,'pending') RETURNING `+eventIntentStatusColumns, id.AccountID, id.AppID, s.delivery.InvocationID, s.delivery.ReplayGeneration, s.delivery.Attempt, identity[:], request[:], capability[:], payload[:], s.app, s.seconds, action, reason, s.token, result))
	if err != nil {
		return zero, eventIntentStoreError(ctx)
	}
	return status, nil
}
func (s *eventAcknowledgmentWorker) intentHook(status *EventAcknowledgmentIntentStatus) func(context.Context, *sql.Tx, EventConsumerTransactionResult) error {
	return func(ctx context.Context, tx *sql.Tx, receipt EventConsumerTransactionResult) error {
		saved, err := s.saveIntent(ctx, tx, "complete", "", receipt.Body)
		if err == nil {
			*status = saved
		}
		return err
	}
}

// SaveCompletionIntent reads a committed receipt and atomically stages its callback.
// It never invokes business code, and is safe after an uncertain transaction commit.
func (w *EventAcknowledgmentWorker) SaveCompletionIntent(ctx context.Context, db *sql.DB) (EventAcknowledgmentIntentResult, error) {
	var out EventAcknowledgmentIntentResult
	s, err := w.lock(ctx, db)
	if err != nil {
		return out, err
	}
	defer s.mu.Unlock()
	receipt, err := withEventConsumerTransaction(ctx, db, s.input, func(EventConsumerSQLTransaction) (json.RawMessage, error) { return nil, ErrEventConsumerReceiptMissing }, nil, s.intentHook(&out.Intent))
	if err != nil {
		if errors.Is(err, ErrEventConsumerCommitUnknown) {
			return EventAcknowledgmentIntentResult{}, eventIntentCommitError(ctx)
		}
		return EventAcknowledgmentIntentResult{}, err
	}
	out.Receipt, out.ReceiptConfirmed = receipt, true
	return out, nil
}

// RunDurable commits a completion intent with business writes and the receipt.
// It sends no terminal HTTP callback; a recovery host must claim and deliver intents.
func (w *EventAcknowledgmentWorker) RunDurable(ctx context.Context, db *sql.DB, handler func(context.Context, EventConsumerSQLTransaction) (json.RawMessage, error)) (EventAcknowledgmentIntentResult, error) {
	return w.runDurable(ctx, db, nil, handler)
}

// RunDurableWithProcessingLease additionally fences the existing local preparation
// lease; like RunWithProcessingLease, it does not renew that locked local lease.
func (w *EventAcknowledgmentWorker) RunDurableWithProcessingLease(ctx context.Context, db *sql.DB, lease EventConsumerProcessingLease, handler func(context.Context, EventConsumerSQLTransaction) (json.RawMessage, error)) (EventAcknowledgmentIntentResult, error) {
	return w.runDurable(ctx, db, &lease, handler)
}
func (w *EventAcknowledgmentWorker) runDurable(ctx context.Context, db *sql.DB, lease *EventConsumerProcessingLease, handler func(context.Context, EventConsumerSQLTransaction) (json.RawMessage, error)) (EventAcknowledgmentIntentResult, error) {
	var out EventAcknowledgmentIntentResult
	s, err := w.lock(ctx, db)
	if err != nil {
		return out, err
	}
	defer s.mu.Unlock()
	if handler == nil {
		return out, ErrInvalidEventConsumerRequest
	}
	if lease != nil {
		a, b := eventConsumerDigests(s.input)
		c, d := eventConsumerDigests(lease.input)
		if !lease.valid || a != c || b != d {
			return out, ErrInvalidEventConsumerRequest
		}
	}
	noop := func(EventConsumerSQLTransaction) (json.RawMessage, error) { return nil, ErrEventConsumerReceiptMissing }
	receipt, err := withEventConsumerTransaction(ctx, db, s.input, noop, nil, s.intentHook(&out.Intent))
	if err == nil {
		out.Receipt, out.ReceiptConfirmed = receipt, true
		return out, nil
	}
	if !errors.Is(err, ErrEventConsumerReceiptMissing) {
		return EventAcknowledgmentIntentResult{}, sanitizeIntentCommit(ctx, err)
	}
	if err := s.startLease(ctx); err != nil {
		return out, err
	}
	workCtx, cancelWork := context.WithCancelCause(ctx)
	defer cancelWork(context.Canceled)
	stop := s.monitor(workCtx, cancelWork)
	defer stop()
	callback := func(tx EventConsumerSQLTransaction) (json.RawMessage, error) { return handler(workCtx, tx) }
	receipt, err = withEventConsumerTransaction(workCtx, db, s.input, callback, lease, s.intentHook(&out.Intent))
	stop()
	if err != nil {
		return EventAcknowledgmentIntentResult{}, errors.Join(sanitizeIntentCommit(ctx, err), context.Cause(workCtx))
	}
	out.Receipt, out.ReceiptConfirmed = receipt, true
	return out, nil
}
func sanitizeIntentCommit(ctx context.Context, err error) error {
	if errors.Is(err, ErrEventConsumerCommitUnknown) {
		return eventIntentCommitError(ctx)
	}
	return err
}

// SaveRetryIntent requires known rollback and exclusive, stopped job ownership.
func (w *EventAcknowledgmentWorker) SaveRetryIntent(ctx context.Context, db *sql.DB, reason string) (EventAcknowledgmentIntentStatus, error) {
	return w.saveNegativeIntent(ctx, db, "retry", reason)
}

// SaveDeadLetterIntent has the same ownership and rollback requirements.
func (w *EventAcknowledgmentWorker) SaveDeadLetterIntent(ctx context.Context, db *sql.DB, reason string) (EventAcknowledgmentIntentStatus, error) {
	return w.saveNegativeIntent(ctx, db, "dead-letter", reason)
}
func (w *EventAcknowledgmentWorker) saveNegativeIntent(ctx context.Context, db *sql.DB, action, reason string) (EventAcknowledgmentIntentStatus, error) {
	var zero EventAcknowledgmentIntentStatus
	s, err := w.lock(ctx, db)
	if err != nil {
		return zero, err
	}
	defer s.mu.Unlock()
	if !eventWorkerReason(reason) {
		return zero, ErrInvalidEventConsumerRequest
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return zero, eventIntentStoreError(ctx)
	}
	defer func() { _ = tx.Rollback() }()
	identity, request := eventConsumerDigests(s.input)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "gregale.event-consumer-inbox.v1:"+hex.EncodeToString(identity[:])); err != nil {
		return zero, eventIntentStoreError(ctx)
	}
	var saved []byte
	err = tx.QueryRowContext(ctx, `SELECT request_digest FROM public.gregale_event_consumer_inbox WHERE identity_digest=$1`, identity[:]).Scan(&saved)
	if err == nil {
		if !bytes.Equal(saved, request[:]) {
			return zero, ErrEventConsumerReceiptConflict
		}
		return zero, ErrEventConsumerReceiptCommitted
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return zero, eventIntentStoreError(ctx)
	}
	status, err := s.saveIntent(ctx, tx, action, reason, nil)
	if err != nil {
		return zero, err
	}
	if err := tx.Commit(); err != nil {
		return zero, eventIntentCommitError(ctx)
	}
	return status, nil
}

// ListEventAcknowledgmentIntents returns at most 100 payload-free diagnostics.
// Empty invocationID lists all retained intents in this owner scope.
func ListEventAcknowledgmentIntents(ctx context.Context, db *sql.DB, accountID, appID, invocationID string, limit int) ([]EventAcknowledgmentIntentStatus, error) {
	if ctx == nil || db == nil || limit < 1 || limit > 100 {
		return nil, ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return nil, err
	}
	invocationID = strings.ToLower(invocationID)
	if invocationID != "" && !operationUUID(invocationID) {
		return nil, ErrInvalidEventConsumerRequest
	}
	rows, err := db.QueryContext(ctx, `SELECT `+eventIntentStatusColumns+` FROM public.gregale_event_acknowledgment_intents WHERE account_id=$1::uuid AND app_id=$2::uuid AND ($3='' OR invocation_id::text=$3) ORDER BY updated_at DESC,invocation_id,replay_generation DESC,attempt DESC LIMIT $4`, account, app, invocationID, limit)
	if err != nil {
		return nil, eventIntentStoreError(ctx)
	}
	defer func() { _ = rows.Close() }()
	out := make([]EventAcknowledgmentIntentStatus, 0)
	for rows.Next() {
		s, err := eventIntentStatus(rows)
		if err != nil {
			return nil, eventIntentStoreError(ctx)
		}
		out = append(out, s)
	}
	if rows.Err() != nil {
		return nil, eventIntentStoreError(ctx)
	}
	return out, nil
}
