package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ QueueLeaseStore = (*PgStore)(nil)

// LeaseQueueMessages claims up to req.MaxMessages ready production messages
// for the app's enabled pull binding (ADR-1140). The binding row lock
// serializes concurrent leasers so unexpired leases never exceed
// max_concurrency.
func (s *PgStore) LeaseQueueMessages(ctx context.Context, req QueueLeaseRequest) ([]QueueLease, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	account, err := readEnvironmentQueueDeliveryAccount(ctx, tx, req.AccountID)
	if err != nil {
		return nil, err
	}
	messages, visibility, err := queueLeaseLimits(account, req)
	if err != nil {
		return nil, err
	}
	limits := api.MustLimitsFor(account.Plan)
	q := sqlc.New()
	app := mustPgUUID(req.AppID)
	binding, err := q.QueueLeaseLockBinding(ctx, tx, sqlc.QueueLeaseLockBindingParams{
		AccountID: mustPgUUID(req.AccountID), AppID: app, QueueName: req.QueueName})
	if err != nil {
		return nil, mapErr(err)
	}
	active, err := q.QueueLeaseActiveCount(ctx, tx, sqlc.QueueLeaseActiveCountParams{AppID: app, BindingID: binding.ID, QueueName: binding.QueueName})
	if err != nil {
		return nil, err
	}
	messages = min(messages, int(binding.MaxConcurrency)-int(active))
	if messages <= 0 {
		return []QueueLease{}, tx.Commit(ctx)
	}
	ids, err := q.QueueLeaseCandidates(ctx, tx, sqlc.QueueLeaseCandidatesParams{
		AppID: app, BindingID: binding.ID, QueueName: binding.QueueName, CandidateLimit: int32(messages * 4)})
	if err != nil {
		return nil, err
	}
	instance := pgtype.Text{String: req.InstanceID, Valid: req.InstanceID != ""}
	leases := make([]QueueLease, 0, messages)
	usedBytes := 0
	for _, id := range ids {
		if len(leases) == messages {
			break
		}
		fits := func(inv Invocation) bool { return queueLeaseFits(len(leases), usedBytes, inv) }
		lease, ok, err := leaseQueueCandidateTx(ctx, tx, q, app, id, visibility, instance, limits, fits)
		if errors.Is(err, errQueueLeaseFull) {
			break
		}
		if err != nil {
			return nil, err
		}
		if ok {
			leases = append(leases, lease)
			usedBytes += queueLeaseBytes(lease.Invocation)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("state: lease queue messages: %w", err)
	}
	return leases, nil
}

// errQueueLeaseFull stops a lease whose next message would pass the response
// budget; the message stays pending for the next call.
var errQueueLeaseFull = errors.New("state: queue lease response budget reached")

// leaseQueueCandidateTx leases one candidate, or dead-letters it when earlier
// leases already spent its attempt budget. A row another caller changed is
// skipped.
func leaseQueueCandidateTx(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, app, id pgtype.UUID, visibility int, instance pgtype.Text,
	limits api.Limits, fits func(Invocation) bool) (QueueLease, bool, error) {
	row, err := q.LockQueueLeaseInvocation(ctx, tx, id)
	if err != nil {
		return QueueLease{}, false, mapErr(err)
	}
	inv, err := invocationFromSQLC(row)
	if err != nil {
		return QueueLease{}, false, err
	}
	if inv.State != InvocationPending {
		return QueueLease{}, false, nil
	}
	if environmentQueueDeliveryAttemptsExhausted(inv, limits) {
		_, err := q.QueueLeaseExhaustInvocation(ctx, tx, sqlc.QueueLeaseExhaustInvocationParams{ID: id, AppID: app, Attempt: row.Attempts})
		return QueueLease{}, false, err
	}
	if !fits(inv) {
		return QueueLease{}, false, errQueueLeaseFull
	}
	row, err = q.QueueLeaseClaimInvocation(ctx, tx, sqlc.QueueLeaseClaimInvocationParams{
		LeaseSeconds: int32(visibility), InstanceID: instance, ID: id, AppID: app})
	if errors.Is(err, pgx.ErrNoRows) {
		return QueueLease{}, false, nil
	}
	if err != nil {
		return QueueLease{}, false, err
	}
	inv, err = invocationFromSQLC(row)
	if err != nil {
		return QueueLease{}, false, err
	}
	receipt, digest, err := newQueueLeaseReceipt(inv.ID)
	if err != nil {
		return QueueLease{}, false, err
	}
	if err := q.UpsertQueueLeaseReceipt(ctx, tx, sqlc.UpsertQueueLeaseReceiptParams{InvocationID: id, Attempt: row.Attempts,
		ReplayGeneration: row.ReplayGeneration, TokenHash: digest, LeaseExpiresAt: row.LeaseExpiresAt}); err != nil {
		return QueueLease{}, false, err
	}
	return QueueLease{Invocation: inv, Receipt: receipt}, true, nil
}

func (s *PgStore) AckQueueLeases(ctx context.Context, accountID, appID, queueName string, receipts []string) (QueueLeaseOutcome, error) {
	return s.settleQueueLeases(ctx, accountID, appID, queueName, receipts, func(ctx context.Context, tx pgx.Tx, inv Invocation, _ time.Time) (bool, error) {
		if _, err := s.completeInvocationTx(ctx, tx, inv.ID, inv.Attempts, nil, true); err != nil {
			if errors.Is(err, ErrNotFound) {
				return false, nil
			}
			return false, err
		}
		return true, sqlc.New().DeleteQueueLeaseReceipt(ctx, tx, mustPgUUID(inv.ID))
	})
}

func (s *PgStore) NackQueueLeases(ctx context.Context, accountID, appID, queueName string, receipts []string, delay time.Duration, lastError string) (QueueLeaseOutcome, error) {
	return s.settleQueueLeases(ctx, accountID, appID, queueName, receipts, func(ctx context.Context, tx pgx.Tx, inv Invocation, now time.Time) (bool, error) {
		account, err := readEnvironmentQueueDeliveryAccount(ctx, tx, accountID)
		if err != nil {
			return false, err
		}
		next, err := queueLeaseRetry(inv, account.Plan, now, delay, lastError)
		if err != nil {
			return false, err
		}
		args := sqlc.RetryQueueLeaseInvocationParams{State: string(next.State), LastError: next.LastError,
			DueAt: pgtype.Timestamptz{Time: next.DueAt, Valid: true}, ID: mustPgUUID(inv.ID),
			Attempt: int32(inv.Attempts), ReplayGeneration: inv.ReplayGeneration}
		if next.Outcome != nil {
			args.Outcome = pgtype.Text{String: string(*next.Outcome), Valid: true}
		}
		if next.CompletedAt != nil {
			args.CompletedAt = pgtype.Timestamptz{Time: *next.CompletedAt, Valid: true}
		}
		q := sqlc.New()
		count, err := q.RetryQueueLeaseInvocation(ctx, tx, args)
		if err != nil || count != 1 {
			return false, err
		}
		return true, q.DeleteQueueLeaseReceipt(ctx, tx, mustPgUUID(inv.ID))
	})
}

func (s *PgStore) ExtendQueueLeases(ctx context.Context, accountID, appID, queueName string, receipts []string, visibilitySeconds int) (QueueLeaseOutcome, error) {
	account, err := s.AccountByID(ctx, accountID)
	if err != nil {
		return QueueLeaseOutcome{}, err
	}
	if _, _, err := queueLeaseLimits(account, QueueLeaseRequest{QueueName: queueName, MaxMessages: 1, VisibilitySeconds: visibilitySeconds}); err != nil {
		return QueueLeaseOutcome{}, err
	}
	if visibilitySeconds == 0 {
		visibilitySeconds = api.QueueLeaseDefaultVisibilitySeconds
	}
	return s.settleQueueLeases(ctx, accountID, appID, queueName, receipts, func(ctx context.Context, tx pgx.Tx, inv Invocation, _ time.Time) (bool, error) {
		q := sqlc.New()
		until, err := q.ExtendQueueLeaseInvocation(ctx, tx, sqlc.ExtendQueueLeaseInvocationParams{LeaseSeconds: int32(visibilitySeconds),
			ID: mustPgUUID(inv.ID), Attempt: int32(inv.Attempts), ReplayGeneration: inv.ReplayGeneration})
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return true, q.ExtendQueueLeaseReceipt(ctx, tx, sqlc.ExtendQueueLeaseReceiptParams{LeaseExpiresAt: until, InvocationID: mustPgUUID(inv.ID)})
	})
}

// settleQueueLeases applies one receipt at a time, each in its own
// transaction, so a stale or foreign receipt never blocks the rest.
func (s *PgStore) settleQueueLeases(ctx context.Context, accountID, appID, queueName string, receipts []string,
	apply func(context.Context, pgx.Tx, Invocation, time.Time) (bool, error)) (QueueLeaseOutcome, error) {
	if len(receipts) == 0 || len(receipts) > api.QueueLeaseMaxReceipts {
		return QueueLeaseOutcome{}, ErrInvalidArgument
	}
	out := QueueLeaseOutcome{Succeeded: []string{}, Failed: []QueueLeaseFailure{}}
	for _, receipt := range receipts {
		reason, err := s.settleQueueLease(ctx, accountID, appID, queueName, receipt, apply)
		if err != nil {
			return QueueLeaseOutcome{}, err
		}
		if reason == "" {
			out.Succeeded = append(out.Succeeded, receipt)
		} else {
			out.Failed = append(out.Failed, QueueLeaseFailure{Receipt: receipt, Reason: reason})
		}
	}
	return out, nil
}

func (s *PgStore) settleQueueLease(ctx context.Context, accountID, appID, queueName, receipt string,
	apply func(context.Context, pgx.Tx, Invocation, time.Time) (bool, error)) (string, error) {
	id, digest, ok := parseQueueLeaseReceipt(receipt)
	if !ok {
		return QueueLeaseReasonInvalid, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	row, err := q.LockQueueLeaseInvocation(ctx, tx, mustPgUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return QueueLeaseReasonInvalid, nil
	}
	if err != nil {
		return "", err
	}
	inv, err := invocationFromSQLC(row)
	if err != nil {
		return "", err
	}
	if inv.AccountID != accountID || inv.AppID != appID {
		return QueueLeaseReasonInvalid, nil
	}
	r, err := q.ReadQueueLeaseReceipt(ctx, tx, row.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return QueueLeaseReasonExpired, nil
	}
	if err != nil {
		return "", err
	}
	now, err := q.QueueLeaseClock(ctx, tx)
	if err != nil {
		return "", err
	}
	held := queueLeaseReceipt{Attempt: int(r.Attempt), ReplayGeneration: r.ReplayGeneration, TokenHash: r.TokenHash}
	if !queueLeaseOwned(inv, accountID, appID, queueName, held, digest, now.Time) {
		return QueueLeaseReasonExpired, nil
	}
	applied, err := apply(ctx, tx, inv, now.Time)
	if err != nil {
		return "", err
	}
	if !applied {
		return QueueLeaseReasonExpired, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("state: settle queue lease: %w", err)
	}
	return "", nil
}

func (s *PgStore) QueueLeasesByInstance(ctx context.Context, appID string) (map[string]int, error) {
	rows, err := sqlc.New().QueueLeasesByInstance(ctx, s.pool, mustPgUUID(appID))
	if err != nil {
		return nil, err
	}
	out := make(map[string]int, len(rows))
	for _, row := range rows {
		out[row.InstanceID] = int(row.Leases)
	}
	return out, nil
}
