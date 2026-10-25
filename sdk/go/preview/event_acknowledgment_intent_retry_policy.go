package preview

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"time"
)

// EventAcknowledgmentIntentRetryPolicy opts saved-callback recovery into capped
// exponential full jitter. Zero fields select one second initially and a
// five-minute cap. Both durations must be whole milliseconds from 1 ms through
// one hour, with MaxBackoff >= InitialBackoff. This schedules future recovery;
// it does not change callback retries within a claim or impose an attempt limit.
type EventAcknowledgmentIntentRetryPolicy struct {
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

func eventIntentRetryPolicy(p EventAcknowledgmentIntentRetryPolicy) (EventAcknowledgmentIntentRetryPolicy, error) {
	if p.InitialBackoff == 0 {
		p.InitialBackoff = time.Second
	}
	if p.MaxBackoff == 0 {
		p.MaxBackoff = 5 * time.Minute
	}
	if p.InitialBackoff < time.Millisecond || p.InitialBackoff > time.Hour || p.MaxBackoff < p.InitialBackoff || p.MaxBackoff > time.Hour || p.InitialBackoff%time.Millisecond != 0 || p.MaxBackoff%time.Millisecond != 0 {
		return p, ErrInvalidEventConsumerRequest
	}
	return p, nil
}

func (s *eventAcknowledgmentIntentClaim) retryDelay(p EventAcknowledgmentIntentRetryPolicy) time.Duration {
	capDelay := p.InitialBackoff
	for n := int64(1); n < s.deliveryAttempts && capDelay < p.MaxBackoff; n++ {
		capDelay = min(p.MaxBackoff, 2*capDelay)
	}
	// Claim acquisition generated this random token before any HTTP. Derive
	// separate jitter entropy without a random-source failure after uncertainty
	// or exposing raw claim-token bits through the persisted delay.
	entropy := sha256.Sum256(append([]byte("gregale-event-acknowledgment-intent-jitter-v1\n"), s.claim[:]...))
	milliseconds := binary.BigEndian.Uint64(entropy[:8])%uint64(capDelay.Milliseconds()) + 1
	return time.Duration(milliseconds) * time.Millisecond
}

// DeliverWithRetryPolicy sends the saved callback using the same protocol as
// Deliver. Only an unconfirmed settlement schedules jittered recovery, based on
// the durable count captured by this claim. The due time uses the database clock.
// Policy is copied/validated before I/O. A captured stored policy takes precedence.
// Upgrade the intent schema before using this version of Go recovery.
func (c *EventAcknowledgmentIntentClaim) DeliverWithRetryPolicy(ctx context.Context, db *sql.DB, client *Client, options EventAcknowledgmentWorkerOptions, policy EventAcknowledgmentIntentRetryPolicy) (EventAcknowledgmentIntentStatus, error) {
	policy, err := eventIntentRetryPolicy(policy)
	if err != nil {
		return EventAcknowledgmentIntentStatus{}, err
	}
	return c.deliver(ctx, db, client, options, 0, &policy)
}

// RecoverNextEventAcknowledgmentIntentWithRetryPolicy performs one saved-callback
// recovery with capped exponential jitter. Policy validation precedes claim I/O;
// the host still owns polling and lifetime. Empty, uncertainty, receipt checks
// and claim/platform fences retain the single-iteration recovery contract.
func RecoverNextEventAcknowledgmentIntentWithRetryPolicy(ctx context.Context, db *sql.DB, client *Client, accountID, appID string, duration time.Duration, options EventAcknowledgmentWorkerOptions, policy EventAcknowledgmentIntentRetryPolicy) (EventAcknowledgmentIntentStatus, error) {
	var zero EventAcknowledgmentIntentStatus
	policy, err := eventIntentRetryPolicy(policy)
	if err != nil {
		return zero, err
	}
	if client == nil || client.Client == nil {
		return zero, ErrInvalidEventConsumerRequest
	}
	claim, err := ClaimEventAcknowledgmentIntent(ctx, db, accountID, appID, duration)
	if err != nil {
		return eventIntentParkedStatus(err), err
	}
	return claim.deliver(ctx, db, client, options, 0, &policy)
}
