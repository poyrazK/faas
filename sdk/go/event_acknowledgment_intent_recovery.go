package faas

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"sync"
	"time"
)

// Claim is a process-local recovery capability. Copies share an operation guard.
// Formatting/JSON redact both the claim and platform token; never persist the claim.
type EventAcknowledgmentIntentClaim struct {
	state *eventAcknowledgmentIntentClaim
}
type eventAcknowledgmentIntentClaim struct {
	identity                    []byte
	mu                          sync.Mutex
	account, app, invocation    string
	generation                  int64
	attempt                     int
	deliveryAttempts            int64
	deliveryAttemptLimit        int64
	claim                       [32]byte
	token, slug, action, reason string
	body                        json.RawMessage
	seconds                     int
	policy                      *EventAcknowledgmentIntentRetryPolicy
}

func (c EventAcknowledgmentIntentClaim) String() string {
	return "EventAcknowledgmentIntentClaim{redacted}"
}
func (c EventAcknowledgmentIntentClaim) GoString() string { return c.String() }
func (c EventAcknowledgmentIntentClaim) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.String())
}

// ClaimEventAcknowledgmentIntent claims one due row with SKIP LOCKED. Duration must
// be whole milliseconds between one second and five minutes. A failed commit does
// not return a capability: wait for the possibly committed lease to expire.
// An exhausted due v2 row is parked instead: a known commit returns a
// *EventAcknowledgmentIntentParkedError with metadata and no capability.
func ClaimEventAcknowledgmentIntent(ctx context.Context, db *sql.DB, accountID, appID string, duration time.Duration) (EventAcknowledgmentIntentClaim, error) {
	var zero EventAcknowledgmentIntentClaim
	if ctx == nil || db == nil || duration < time.Second || duration > 5*time.Minute || duration%time.Millisecond != 0 {
		return zero, ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return zero, err
	}
	s := &eventAcknowledgmentIntentClaim{account: account, app: app}
	if _, err := rand.Read(s.claim[:]); err != nil {
		return zero, ErrEventAcknowledgmentIntentStore
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return zero, eventIntentStoreError(ctx)
	}
	defer func() { _ = tx.Rollback() }()
	var body sql.NullString
	var actionDigest, capabilityDigest []byte
	var version, initial, maximum, budget, limit sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT invocation_id::text,replay_generation,attempt,token,app_slug,lease_seconds,action,reason,result_body,identity_digest,action_digest,capability_digest,retry_policy_version,retry_initial_backoff_ms,retry_max_backoff_ms,retry_max_delivery_attempts,delivery_attempt_limit,delivery_attempts FROM public.gregale_event_acknowledgment_intents WHERE account_id=$1::uuid AND app_id=$2::uuid AND state IN ('pending','unconfirmed') AND next_attempt_at<=clock_timestamp() AND (claim_expires_at IS NULL OR claim_expires_at<=clock_timestamp()) ORDER BY next_attempt_at,created_at,invocation_id,replay_generation,attempt LIMIT 1 FOR UPDATE SKIP LOCKED`, account, app).Scan(&s.invocation, &s.generation, &s.attempt, &s.token, &s.slug, &s.seconds, &s.action, &s.reason, &body, &s.identity, &actionDigest, &capabilityDigest, &version, &initial, &maximum, &budget, &limit, &s.deliveryAttempts)
	if errors.Is(err, sql.ErrNoRows) {
		return zero, ErrEventAcknowledgmentIntentEmpty
	}
	if err != nil {
		return zero, eventIntentStoreError(ctx)
	}
	stored, err := eventIntentStoredBudgetPolicy(version, initial, maximum, budget, limit)
	if err != nil || s.deliveryAttempts < 0 || s.deliveryAttempts > 2147483647 {
		return zero, eventIntentStoreError(ctx)
	}
	if stored != nil {
		policy := stored.retryPolicy()
		s.policy = &policy
	}
	s.deliveryAttemptLimit = limit.Int64
	if body.Valid {
		s.body = json.RawMessage(body.String)
	}
	capability := sha256.Sum256([]byte(s.token))
	payload := sha256.Sum256(append([]byte(s.action+"\n"+s.reason+"\n"), s.body...))
	if !bytes.Equal(capability[:], capabilityDigest) || !bytes.Equal(payload[:], actionDigest) || len(s.identity) != 32 {
		return zero, ErrEventAcknowledgmentIntentStore
	}
	if s.deliveryAttemptLimit > 0 && s.deliveryAttempts >= s.deliveryAttemptLimit {
		return parkEventIntent(ctx, tx, s)
	}
	err = tx.QueryRowContext(ctx, `UPDATE public.gregale_event_acknowledgment_intents SET claim_token=$6,claim_expires_at=clock_timestamp()+$7::bigint*interval '1 millisecond',delivery_attempts=least(delivery_attempts+1,2147483647),updated_at=clock_timestamp() WHERE account_id=$1::uuid AND app_id=$2::uuid AND invocation_id=$3::uuid AND replay_generation=$4 AND attempt=$5 RETURNING delivery_attempts`, account, app, s.invocation, s.generation, s.attempt, s.claim[:], duration.Milliseconds()).Scan(&s.deliveryAttempts)
	if err != nil || s.deliveryAttempts < 1 || s.deliveryAttempts > 2147483647 {
		return zero, eventIntentStoreError(ctx)
	}
	if err := tx.Commit(); err != nil {
		return zero, eventIntentCommitError(ctx)
	}
	return EventAcknowledgmentIntentClaim{state: s}, nil
}

// Deliver sends only the saved callback; it never executes business code. The
// database lease bounds the HTTP context, and settlement rechecks its token/expiry.
// backoff sets the next due time after uncertainty (1 ms..1 hour), using DB time.
// A captured stored policy takes precedence over that fallback delay.
func (c *EventAcknowledgmentIntentClaim) Deliver(ctx context.Context, db *sql.DB, client *Client, options EventAcknowledgmentWorkerOptions, backoff time.Duration) (EventAcknowledgmentIntentStatus, error) {
	if backoff < time.Millisecond || backoff > time.Hour || backoff%time.Millisecond != 0 {
		return EventAcknowledgmentIntentStatus{}, ErrInvalidEventConsumerRequest
	}
	return c.deliver(ctx, db, client, options, backoff, nil)
}

func (c *EventAcknowledgmentIntentClaim) deliver(ctx context.Context, db *sql.DB, client *Client, options EventAcknowledgmentWorkerOptions, backoff time.Duration, policy *EventAcknowledgmentIntentRetryPolicy) (EventAcknowledgmentIntentStatus, error) {
	var zero EventAcknowledgmentIntentStatus
	if c == nil || c.state == nil || ctx == nil || db == nil || client == nil || client.Client == nil {
		return zero, ErrInvalidEventConsumerRequest
	}
	s := c.state
	if !s.mu.TryLock() {
		return zero, ErrEventAcknowledgmentWorkerBusy
	}
	defer s.mu.Unlock()
	if s.policy != nil {
		policy = s.policy
	}
	if policy != nil {
		backoff = s.retryDelay(*policy)
	}
	options, err := eventWorkerOptions(options, s.seconds)
	if err != nil {
		return zero, err
	}
	sent := time.Now()
	var remaining float64
	err = db.QueryRowContext(ctx, `SELECT extract(epoch FROM claim_expires_at-clock_timestamp())::double precision FROM public.gregale_event_acknowledgment_intents WHERE account_id=$1::uuid AND app_id=$2::uuid AND invocation_id=$3::uuid AND replay_generation=$4 AND attempt=$5 AND state IN ('pending','unconfirmed') AND claim_token=$6 AND claim_expires_at>clock_timestamp()`, s.account, s.app, s.invocation, s.generation, s.attempt, s.claim[:]).Scan(&remaining)
	if errors.Is(err, sql.ErrNoRows) {
		return zero, ErrEventAcknowledgmentIntentClaimLost
	}
	if err != nil {
		return zero, eventIntentStoreError(ctx)
	}
	// Claim durations are capped; reject invalid stored evidence before conversion.
	if math.IsNaN(remaining) || math.IsInf(remaining, 0) || remaining <= 0 || remaining > 300 {
		return zero, ErrEventAcknowledgmentIntentClaimLost
	}
	budget := time.Duration(remaining*float64(time.Second)) - time.Since(sent)
	if budget <= 0 {
		return zero, ErrEventAcknowledgmentIntentClaimLost
	}
	callCtx, cancel := context.WithTimeout(ctx, budget)
	worker := &eventAcknowledgmentWorker{client: client, app: s.slug, token: s.token, seconds: s.seconds, options: options, delivery: EventConsumerDeliveryContext{InvocationID: s.invocation, Attempt: s.attempt, ReplayGeneration: s.generation}}
	var callbackErr error
	if s.action != "complete" {
		var committed bool
		readErr := db.QueryRowContext(callCtx, `SELECT EXISTS(SELECT 1 FROM public.gregale_event_consumer_inbox WHERE identity_digest=$1 AND account_id=$2::uuid AND app_id=$3::uuid)`, s.identity, s.account, s.app).Scan(&committed)
		if readErr != nil {
			cancel()
			return zero, eventIntentStoreError(ctx)
		}
		if committed {
			callbackErr = ErrEventConsumerReceiptCommitted
		}
	}
	if callbackErr == nil {
		_, callbackErr = worker.terminal(callCtx, s.action, s.reason, s.body)
	}
	cancel()
	state, code := "confirmed", "callback_confirmed"
	if callbackErr != nil {
		state, code = "unconfirmed", "unconfirmed"
		if errors.Is(callbackErr, ErrEventConsumerReceiptCommitted) {
			state, code = "superseded", "receipt_committed"
		} else if errors.Is(callbackErr, ErrEventAcknowledgmentLeaseLost) {
			state, code = "superseded", "lease_lost"
		} else if s.deliveryAttemptLimit > 0 && s.deliveryAttempts >= s.deliveryAttemptLimit {
			state, code = "parked", "attempts_exhausted"
		}
	}
	status, err := eventIntentStatus(db.QueryRowContext(ctx, `UPDATE public.gregale_event_acknowledgment_intents SET state=$7,last_code=$8,token=CASE WHEN $7 IN ('confirmed','superseded') THEN NULL ELSE token END,result_body=CASE WHEN $7 IN ('confirmed','superseded') THEN NULL ELSE result_body END,claim_token=NULL,claim_expires_at=NULL,next_attempt_at=CASE WHEN $7='unconfirmed' THEN clock_timestamp()+$9::bigint*interval '1 millisecond' ELSE next_attempt_at END,updated_at=clock_timestamp() WHERE account_id=$1::uuid AND app_id=$2::uuid AND invocation_id=$3::uuid AND replay_generation=$4 AND attempt=$5 AND state IN ('pending','unconfirmed') AND claim_token=$6 AND claim_expires_at>clock_timestamp() RETURNING `+eventIntentStatusColumns, s.account, s.app, s.invocation, s.generation, s.attempt, s.claim[:], state, code, backoff.Milliseconds()))
	if errors.Is(err, sql.ErrNoRows) {
		return zero, errors.Join(ErrEventAcknowledgmentIntentClaimLost, callbackErr)
	}
	if err != nil {
		return zero, errors.Join(eventIntentStoreError(ctx), callbackErr)
	}
	if status.State == "parked" {
		return status, errors.Join(ErrEventAcknowledgmentIntentParked, callbackErr)
	}
	return status, callbackErr
}

// RecoverNextEventAcknowledgmentIntent is one bounded recovery iteration. The host
// owns scheduling/backoff and process lifetime; an empty queue returns IntentEmpty.
func RecoverNextEventAcknowledgmentIntent(ctx context.Context, db *sql.DB, client *Client, accountID, appID string, duration time.Duration, options EventAcknowledgmentWorkerOptions, backoff time.Duration) (EventAcknowledgmentIntentStatus, error) {
	var zero EventAcknowledgmentIntentStatus
	if client == nil || client.Client == nil || backoff < time.Millisecond || backoff > time.Hour || backoff%time.Millisecond != 0 {
		return zero, ErrInvalidEventConsumerRequest
	}
	claim, err := ClaimEventAcknowledgmentIntent(ctx, db, accountID, appID, duration)
	if err != nil {
		return eventIntentParkedStatus(err), err
	}
	return claim.Deliver(ctx, db, client, options, backoff)
}
