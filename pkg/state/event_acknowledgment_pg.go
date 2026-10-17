package state

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) EventAcknowledgmentPolicy(ctx context.Context, account, app, sub string, seconds *int) (api.EventAcknowledgmentPolicy, error) {
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return api.EventAcknowledgmentPolicy{}, err
	}
	q := sqlc.New()
	if seconds != nil {
		if *seconds < 0 || *seconds > api.EventAcknowledgmentLeaseMaxSeconds {
			return api.EventAcknowledgmentPolicy{}, ErrInvalidArgument
		}
		n, err := q.EventAckPolicySet(ctx, s.pool, sqlc.EventAckPolicySetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), LeaseSeconds: int32(*seconds)})
		if err != nil {
			return api.EventAcknowledgmentPolicy{}, err
		}
		if n != 1 {
			return api.EventAcknowledgmentPolicy{}, ErrNotFound
		}
		return api.EventAcknowledgmentPolicy{SubscriptionID: canonicalMemUUID(sub), LeaseSeconds: *seconds}, nil
	}
	secondsNow, err := q.EventAckPolicyGet(ctx, s.pool, sqlc.EventAckPolicyGetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)})
	return api.EventAcknowledgmentPolicy{SubscriptionID: canonicalMemUUID(sub), LeaseSeconds: int(secondsNow)}, mapErr(err)
}
func acknowledgmentInvocation(row sqlc.EventAckInvocationLockRow) Invocation {
	return Invocation{ID: uuidString(row.ID), AccountID: uuidString(row.AccountID), AppID: uuidString(row.AppID), Source: InvocationSource(row.Source), State: InvocationState(row.State), Attempts: int(row.Attempts), ReplayGeneration: row.ReplayGeneration, Headers: row.Headers, OperationID: uuidString(row.OperationID), OnSuccessDestinationID: uuidString(row.OnSuccessDestinationID), OnFailureDestinationID: uuidString(row.OnFailureDestinationID), LeaseExpiresAt: timestamptzToTimePtr(row.LeaseExpiresAt), Result: row.Result, QuotaReserved: row.QuotaReserved}
}
func acknowledgmentFromSQL(row sqlc.EventDeliveryAcknowledgment) eventAcknowledgment {
	var digest [32]byte
	copy(digest[:], row.TokenDigest)
	return eventAcknowledgment{rejectionStatus: row.RejectionStatus, completionDigest: row.CompletionDigest, claim: InvocationClaim{Attempt: int(row.Attempt), ReplayGeneration: row.ReplayGeneration}, digest: digest, phase: row.Phase, seconds: int(row.LeaseSeconds), deadline: timeFromPgtype(row.DeadlineAt), maxAttempts: int(row.MaxAttempts), retryDelay: time.Duration(row.RetryDelayMs) * time.Millisecond}
}
func ackLockInvocation(ctx context.Context, tx pgx.Tx, account, app, id string) (Invocation, error) {
	row, err := sqlc.New().EventAckInvocationLock(ctx, tx, sqlc.EventAckInvocationLockParams{InvocationID: mustPgUUID(id), AccountID: mustPgUUID(account), AppID: mustPgUUID(app)})
	if err != nil {
		return Invocation{}, mapErr(err)
	}
	return acknowledgmentInvocation(row), nil
}
func (s *PgStore) OfferEventAcknowledgment(ctx context.Context, inv Invocation, budget int, delay time.Duration) (string, error) {
	if err := validSubscriptionControlIDs(inv.AccountID, inv.AppID, inv.ID); err != nil {
		return "", err
	}
	if budget <= 0 || budget > api.DurableRetryMaxAttempts || delay.Milliseconds() <= 0 {
		return "", ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := ackLockInvocation(ctx, tx, inv.AccountID, inv.AppID, inv.ID)
	if err != nil {
		return "", err
	}
	claim := InvocationClaim{Attempt: inv.Attempts, ReplayGeneration: inv.ReplayGeneration}
	if err := lockInvocationAttemptClaim(ctx, tx, inv.ID, claim); err != nil {
		return "", err
	}
	seconds, err := EventAcknowledgmentSeconds(current)
	if err != nil || seconds == 0 {
		return "", ErrInvalidArgument
	}
	q := sqlc.New()
	old, err := q.EventAckGet(ctx, tx, mustPgUUID(inv.ID))
	if err == nil && int(old.Attempt) == claim.Attempt && old.ReplayGeneration == claim.ReplayGeneration {
		return "", ErrEventAcknowledgmentStale
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	token, digest, err := acknowledgmentToken()
	if err != nil {
		return "", err
	}
	err = q.EventAckOffer(ctx, tx, sqlc.EventAckOfferParams{InvocationID: mustPgUUID(inv.ID), Attempt: int32(claim.Attempt), ReplayGeneration: claim.ReplayGeneration, TokenDigest: digest[:], LeaseSeconds: int32(seconds), MaxAttempts: int32(budget), RetryDelayMs: delay.Milliseconds(), LifetimeSeconds: int32(api.EventAcknowledgmentLifetime / time.Second)})
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return token, nil
}
func (s *PgStore) AcceptEventAcknowledgment(ctx context.Context, inv Invocation, token string) (api.EventAcknowledgmentStatus, error) {
	return s.EventAcknowledgment(ctx, inv.AccountID, inv.AppID, inv.ID, token, "accept", nil)
}
func (s *PgStore) EventAcknowledgment(ctx context.Context, account, app, id, token, action string, result json.RawMessage) (api.EventAcknowledgmentStatus, error) {
	if err := validSubscriptionControlIDs(account, app, id); err != nil {
		return api.EventAcknowledgmentStatus{}, err
	}
	if err := validateAcknowledgmentFailure(action, result); err != nil {
		return api.EventAcknowledgmentStatus{}, err
	}
	if !validAcknowledgmentResult(result) {
		return api.EventAcknowledgmentStatus{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.EventAcknowledgmentStatus{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	inv, err := ackLockInvocation(ctx, tx, account, app, id)
	if err != nil {
		return api.EventAcknowledgmentStatus{}, err
	}
	q := sqlc.New()
	row, err := q.EventAckGet(ctx, tx, mustPgUUID(id))
	if errors.Is(err, pgx.ErrNoRows) && action == "get" {
		seconds, policyErr := EventAcknowledgmentSeconds(inv)
		if policyErr == nil && seconds > 0 {
			return acknowledgmentStatus(inv, eventAcknowledgment{}, time.Now()), nil
		}
	}
	if err != nil {
		return api.EventAcknowledgmentStatus{}, mapErr(err)
	}
	ack := acknowledgmentFromSQL(row)
	clock, err := q.EventAckNow(ctx, tx)
	if err != nil {
		return api.EventAcknowledgmentStatus{}, err
	}
	now := timeFromPgtype(clock)
	if action == "get" {
		return acknowledgmentStatus(inv, ack, now), nil
	}
	digest, err := acknowledgmentDigest(token)
	if err != nil {
		return api.EventAcknowledgmentStatus{}, err
	}
	if subtle.ConstantTimeCompare(digest[:], ack.digest[:]) != 1 || inv.Attempts != ack.claim.Attempt || inv.ReplayGeneration != ack.claim.ReplayGeneration {
		return api.EventAcknowledgmentStatus{}, ErrEventAcknowledgmentStale
	}
	if ack.phase == "rejected" {
		if action == "renew" {
			return rejectAcknowledgmentRenewal(ctx, tx, id, action)
		}
		return replayAcknowledgmentFailure(inv, ack, action, result)
	}
	if ack.phase == "completed" && inv.State == InvocationCompleted {
		if action == "accept" || (action == "complete" && bytes.Equal(acknowledgmentCompletionDigest(result), ack.completionDigest)) {
			return acknowledgmentStatus(inv, ack, now), nil
		}
		return rejectAcknowledgmentRenewal(ctx, tx, id, action)
	}
	if inv.State != InvocationDispatching || inv.LeaseExpiresAt == nil || !inv.LeaseExpiresAt.After(now) {
		return rejectAcknowledgmentRenewal(ctx, tx, id, action)
	}
	if !ack.deadline.After(now) {
		return rejectAcknowledgmentRenewal(ctx, tx, id, action)
	}
	switch action {
	case "retry", "dead-letter":
		return s.rejectEventAcknowledgmentTx(ctx, tx, inv, ack, action, result, now)
	case "accept", "renew":
		if action == "renew" && ack.phase != "awaiting" {
			return rejectAcknowledgmentRenewal(ctx, tx, id, action)
		}
		if action == "accept" && ack.phase == "awaiting" {
			return acknowledgmentStatus(inv, ack, now), nil
		}
		expiry, err := q.EventAckExtend(ctx, tx, mustPgUUID(id))
		if err != nil {
			return api.EventAcknowledgmentStatus{}, err
		}
		inv.LeaseExpiresAt = timestamptzToTimePtr(expiry)
		ack.phase = "awaiting"
	case "complete":
		inv, err = s.completeInvocationTx(ctx, tx, id, ack.claim.Attempt, normalizedAcknowledgmentResult(result), true)
		if err != nil {
			return api.EventAcknowledgmentStatus{}, err
		}
		ack.phase = "completed"
		ack.completionDigest = acknowledgmentCompletionDigest(result)
	default:
		return api.EventAcknowledgmentStatus{}, ErrInvalidArgument
	}
	if err := q.EventAckPhase(ctx, tx, sqlc.EventAckPhaseParams{InvocationID: mustPgUUID(id), Phase: ack.phase, CompletionDigest: ack.completionDigest}); err != nil {
		return api.EventAcknowledgmentStatus{}, err
	}
	if err := recordAcknowledgmentOutcome(ctx, tx, id, acknowledgmentOutcomeKind(action)); err != nil {
		return api.EventAcknowledgmentStatus{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.EventAcknowledgmentStatus{}, err
	}
	return acknowledgmentStatus(inv, ack, now), nil
}
func (s *PgStore) ExpireEventAcknowledgments(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > api.EventAcknowledgmentSweepBatch {
		limit = api.EventAcknowledgmentSweepBatch
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.EventAckOutcomePrune(ctx, tx, int32(limit)); err != nil {
		return 0, err
	}
	if err := q.EventAckMetricPrune(ctx, tx, int32(limit)); err != nil {
		return 0, err
	}
	rows, err := q.EventAckExpired(ctx, tx, int32(limit))
	if err != nil {
		return 0, err
	}
	// Preserve selection by lease expiry, but acquire shared metric/quota rows in scope order.
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.AccountID != b.AccountID {
			return uuidString(a.AccountID) < uuidString(b.AccountID)
		}
		if a.AppID != b.AppID {
			return uuidString(a.AppID) < uuidString(b.AppID)
		}
		asub, bsub := canonicalMemUUID(a.SubscriptionID), canonicalMemUUID(b.SubscriptionID)
		if asub != bsub {
			return asub < bsub
		}
		return uuidString(a.ID) < uuidString(b.ID)
	})
	previousAccount := ""
	for _, row := range rows {
		account := uuidString(row.AccountID)
		if account != previousAccount {
			// Lock even for unreserved attempts before touching this account's counters.
			if err := q.EventAckMetricQuotaLock(ctx, tx, row.AccountID); err != nil {
				return 0, err
			}
			previousAccount = account
		}
		if err := q.EventAckExpire(ctx, tx, sqlc.EventAckExpireParams{InvocationID: row.ID, MaxAttempts: row.MaxAttempts, RetryDelayMs: row.RetryDelayMs}); err != nil {
			return 0, err
		}
		// Account quota precedes shared metric counters to avoid cross-app lock inversion.
		if row.QuotaReserved {
			if err := decrementAccountAsyncInflightTx(ctx, tx, uuidString(row.AccountID)); err != nil {
				return 0, err
			}
		}
		if err := recordAcknowledgmentOutcome(ctx, tx, uuidString(row.ID), "expired"); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(rows), nil
}
