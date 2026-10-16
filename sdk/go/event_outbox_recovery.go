package faas

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"time"
)

// EventOutboxRecoveryOptions bounds one recovery iteration. Backoff is applied
// after uncertainty, never past the immutable retry deadline. A single recovery
// call runs no background goroutine and never retries blocked publications.
type EventOutboxRecoveryOptions struct {
	ClaimDuration  time.Duration
	RequestTimeout time.Duration
	// Backoff is the legacy fixed delay; cannot be combined with RetryPolicy.
	Backoff     time.Duration
	RetryPolicy *EventOutboxRetryPolicy
}

func eventOutboxRecoveryOptions(o EventOutboxRecoveryOptions) (EventOutboxRecoveryOptions, error) {
	if o.ClaimDuration == 0 {
		o.ClaimDuration = 30 * time.Second
	}
	if o.RequestTimeout == 0 {
		o.RequestTimeout = 10 * time.Second
	}
	if o.ClaimDuration < time.Second || o.ClaimDuration > 5*time.Minute || o.ClaimDuration%time.Millisecond != 0 || o.RequestTimeout < time.Millisecond || o.RequestTimeout >= o.ClaimDuration {
		return o, ErrInvalidEventConsumerRequest
	}
	if o.RetryPolicy == nil {
		if o.Backoff == 0 {
			o.Backoff = 5 * time.Second
		}
		if o.Backoff < time.Millisecond || o.Backoff > time.Hour || o.Backoff%time.Millisecond != 0 {
			return o, ErrInvalidEventConsumerRequest
		}
	} else {
		if o.Backoff != 0 {
			return o, ErrInvalidEventConsumerRequest
		}
		policy, err := eventOutboxRetryPolicy(*o.RetryPolicy)
		if err != nil {
			return o, err
		}
		o.RetryPolicy = &policy
	}
	return o, nil
}

type eventOutboxClaim struct {
	account, app, key, slug string
	body                    string
	token                   [32]byte
	attempts                int64
	request                 AppPublishEventRequest
	policy                  *EventOutboxRetryPolicy
}

// RecoverNextEventOutbox claims and publishes at most one retained due intent.
// Concurrent workers skip locked rows. HTTP occurs after the claim transaction
// commits and is bounded by both the lease and original retry deadline. Settlement
// requires the same live claim. Errors do not imply that publication was rejected.
// Use against a server supporting expected_app_id; never target an older server.
func RecoverNextEventOutbox(ctx context.Context, db *sql.DB, client *Client, accountID, appID string, options EventOutboxRecoveryOptions) (EventOutboxStatus, error) {
	var zero EventOutboxStatus
	if ctx == nil || db == nil || client == nil || client.Client == nil {
		return zero, ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return zero, err
	}
	options, err = eventOutboxRecoveryOptions(options)
	if err != nil {
		return zero, err
	}
	claim, status, err := claimNextEventOutbox(ctx, db, account, app, options)
	if err != nil {
		return status, err
	}
	return claim.publish(ctx, db, client, claim.request, options)
}

// Claiming is separated from publication so the worker can stop claims while
// allowing committed claims to drain. Inputs are validated by the public caller.
func claimNextEventOutbox(ctx context.Context, db *sql.DB, account, app string, options EventOutboxRecoveryOptions) (eventOutboxClaim, EventOutboxStatus, error) {
	var zero EventOutboxStatus
	var err error
	claim := eventOutboxClaim{account: account, app: app}
	if _, err = rand.Read(claim.token[:]); err != nil {
		return eventOutboxClaim{}, zero, ErrEventOutboxStore
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return eventOutboxClaim{}, zero, eventOutboxStoreError(ctx)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended($1,0))`, eventOutboxPublishingLock(account, app))
	if err != nil {
		return eventOutboxClaim{}, zero, eventOutboxStoreError(ctx)
	}
	var paused bool
	err = tx.QueryRowContext(ctx, `SELECT coalesce((SELECT paused FROM public.gregale_event_outbox_publishing_controls WHERE account_id=$1::uuid AND app_id=$2::uuid),false)`, account, app).Scan(&paused)
	if err != nil {
		return eventOutboxClaim{}, zero, eventOutboxStoreError(ctx)
	}
	if paused {
		return eventOutboxClaim{}, zero, ErrEventOutboxPublishingPaused
	}
	bucket, err := claimEventOutboxRateBucket(ctx, tx, account, app)
	if err != nil {
		return eventOutboxClaim{}, zero, err
	}
	var digest []byte
	var expired bool
	err = tx.QueryRowContext(ctx, `SELECT producer_key,app_slug,request_body,request_digest,retry_until<=clock_timestamp(),attempts FROM public.gregale_event_outbox WHERE account_id=$1::uuid AND app_id=$2::uuid AND state IN ('pending','unconfirmed') AND next_attempt_at<=clock_timestamp() AND (claim_expires_at IS NULL OR claim_expires_at<=clock_timestamp()) ORDER BY next_attempt_at,producer_key LIMIT 1 FOR UPDATE SKIP LOCKED`, account, app).Scan(&claim.key, &claim.slug, &claim.body, &digest, &expired, &claim.attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return eventOutboxClaim{}, zero, ErrEventOutboxEmpty
	}
	if err != nil {
		return eventOutboxClaim{}, zero, eventOutboxStoreError(ctx)
	}
	stored, _, policyErr := eventOutboxStoredPolicy(tx.QueryRowContext(ctx, `SELECT `+eventOutboxStoredPolicyColumns+` FROM public.gregale_event_outbox_retry_policies WHERE account_id=$1::uuid AND app_id=$2::uuid AND producer_key=$3`, account, app, claim.key))
	if policyErr == nil {
		claim.policy = stored.recoveryPolicy()
		options.RetryPolicy = claim.policy
	} else if !errors.Is(policyErr, sql.ErrNoRows) {
		return eventOutboxClaim{}, zero, eventOutboxStoreError(ctx)
	}
	var request AppPublishEventRequest
	actual := sha256.Sum256([]byte(claim.body))
	code := ""
	if expired {
		code = "retry_window_expired"
	} else if options.RetryPolicy != nil && options.RetryPolicy.MaxAttempts > 0 && claim.attempts >= options.RetryPolicy.MaxAttempts {
		code = "attempts_exhausted"
	} else if !bytes.Equal(actual[:], digest) || json.Unmarshal([]byte(claim.body), &request) != nil || request.Validate() != nil || request.ExpectedAppID != app || request.Key != claim.key {
		code = "publication_rejected"
	}
	if code != "" {
		status, readErr := eventOutboxStatus(tx.QueryRowContext(ctx, `UPDATE public.gregale_event_outbox SET state='blocked',last_code=$4,claim_token=NULL,claim_expires_at=NULL,updated_at=clock_timestamp() WHERE account_id=$1::uuid AND app_id=$2::uuid AND producer_key=$3 RETURNING `+eventOutboxStatusColumns, account, app, claim.key, code))
		if readErr != nil {
			return eventOutboxClaim{}, zero, eventOutboxStoreError(ctx)
		}
		if err = tx.Commit(); err != nil {
			return eventOutboxClaim{key: claim.key}, zero, errors.Join(ErrEventOutboxCommitUnknown, eventOutboxStoreError(ctx))
		}
		return eventOutboxClaim{}, status, ErrEventOutboxBlocked
	}
	err = tx.QueryRowContext(ctx, `UPDATE public.gregale_event_outbox SET claim_token=$4,claim_expires_at=least(retry_until,clock_timestamp()+$5::bigint*interval '1 millisecond'),attempts=least(attempts+1,2147483647),updated_at=clock_timestamp() WHERE account_id=$1::uuid AND app_id=$2::uuid AND producer_key=$3 RETURNING attempts`, account, app, claim.key, claim.token[:], options.ClaimDuration.Milliseconds()).Scan(&claim.attempts)
	if err != nil {
		return eventOutboxClaim{}, zero, eventOutboxStoreError(ctx)
	}
	if err = bucket.consume(ctx, tx, account, app); err != nil {
		return eventOutboxClaim{}, zero, err
	}
	if err = tx.Commit(); err != nil {
		return eventOutboxClaim{key: claim.key}, zero, errors.Join(ErrEventOutboxCommitUnknown, eventOutboxStoreError(ctx))
	}
	claim.request = request
	return claim, EventOutboxStatus{}, nil
}

func (c eventOutboxClaim) publish(ctx context.Context, db *sql.DB, client *Client, request AppPublishEventRequest, options EventOutboxRecoveryOptions) (EventOutboxStatus, error) {
	if c.policy != nil {
		options.RetryPolicy = c.policy
		options.Backoff = 0
	}
	var zero EventOutboxStatus
	started := time.Now()
	var remaining float64
	err := db.QueryRowContext(ctx, `SELECT extract(epoch FROM least(claim_expires_at,retry_until)-clock_timestamp())::double precision FROM public.gregale_event_outbox WHERE account_id=$1::uuid AND app_id=$2::uuid AND producer_key=$3 AND state IN ('pending','unconfirmed') AND claim_token=$4 AND claim_expires_at>clock_timestamp() AND retry_until>clock_timestamp()`, c.account, c.app, c.key, c.token[:]).Scan(&remaining)
	if errors.Is(err, sql.ErrNoRows) {
		return zero, ErrEventOutboxClaimLost
	}
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	if math.IsNaN(remaining) || math.IsInf(remaining, 0) || remaining <= 0 || remaining > 300 {
		return zero, ErrEventOutboxClaimLost
	}
	budget := min(options.RequestTimeout, time.Duration(remaining*float64(time.Second))-time.Since(started))
	if budget <= 0 {
		return zero, ErrEventOutboxClaimLost
	}
	callCtx, cancel := context.WithTimeout(ctx, budget)
	if options.RetryPolicy != nil {
		callCtx = context.WithValue(callCtx, eventOutboxPolicyContextKey{}, true)
	}
	response, publishErr := client.PublishAppEvent(callCtx, c.slug, request)
	cancel()
	state, code := "unconfirmed", "unconfirmed"
	var accepted *time.Time
	resultErr := ErrEventOutboxUnconfirmed
	id := sha256.Sum256([]byte(c.key))
	if publishErr == nil && response.AppID == c.app && response.Source == "app."+c.app && response.Receipt.AccountID == c.account && response.Receipt.ID == "key."+hex.EncodeToString(id[:]) && !response.Receipt.AcceptedAt.IsZero() {
		state, code = "published", "publication_confirmed"
		accepted = &response.Receipt.AcceptedAt
		resultErr = nil
	} else if apiErr, ok := AsAPIError(publishErr); ok && apiErr.Problem.Status >= 400 && apiErr.Problem.Status < 500 && apiErr.Problem.Status != 408 && apiErr.Problem.Status != 429 {
		state, code = "blocked", "publication_rejected"
		resultErr = ErrEventOutboxBlocked
	}
	var retryAfter time.Duration
	var retryAt *time.Time
	if options.RetryPolicy != nil && state == "unconfirmed" {
		if options.RetryPolicy.MaxAttempts > 0 && c.attempts >= options.RetryPolicy.MaxAttempts {
			state, code = "blocked", "attempts_exhausted"
			resultErr = ErrEventOutboxBlocked
		} else {
			retryAfter, retryAt = eventOutboxRetryAfter(publishErr)
		}
	}
	delay := c.retryDelay(options)
	// Returning status and sanitizing errors avoids exposing server/driver payloads.
	status, err := eventOutboxStatus(db.QueryRowContext(ctx, `WITH stamp AS MATERIALIZED (SELECT clock_timestamp() AS t) UPDATE public.gregale_event_outbox SET state=$5,last_code=$6,accepted_at=$7::timestamptz,request_body=CASE WHEN $5='published' THEN NULL ELSE request_body END,claim_token=NULL,claim_expires_at=NULL,next_attempt_at=least(retry_until,greatest(stamp.t+$9::bigint*interval '1 millisecond',$10::timestamptz)+$8::bigint*interval '1 millisecond'),updated_at=stamp.t FROM stamp WHERE account_id=$1::uuid AND app_id=$2::uuid AND producer_key=$3 AND state IN ('pending','unconfirmed') AND claim_token=$4 AND claim_expires_at>stamp.t RETURNING `+eventOutboxStatusColumns, c.account, c.app, c.key, c.token[:], state, code, accepted, delay.Milliseconds(), retryAfter.Milliseconds(), retryAt))
	if errors.Is(err, sql.ErrNoRows) {
		return zero, ErrEventOutboxClaimLost
	}
	if err != nil {
		return zero, errors.Join(resultErr, eventOutboxStoreError(ctx))
	}
	return status, resultErr
}
