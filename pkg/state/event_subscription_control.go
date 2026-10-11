package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventSubscriptionControlStore interface {
	GetEventSubscriptionDeliveryControl(context.Context, string, string, string) (api.EventSubscriptionDeliveryControl, error)
	SetEventSubscriptionDeliveryControl(context.Context, string, string, string, bool, int) (api.EventSubscriptionDeliveryControl, error)
}

var _ EventSubscriptionControlStore = (*PgStore)(nil)
var _ EventSubscriptionControlStore = (*MemStore)(nil)

func validSubscriptionControlIDs(account, app, sub string) error {
	for _, id := range []string{account, app, sub} {
		if _, err := uuid.Parse(id); err != nil {
			return ErrInvalidArgument
		}
	}
	return nil
}

// Application controls apply only to UUID subscription identities.
func isEventSubscriptionUUID(id string) bool {
	return uuid.Validate(id) == nil
}

func getSubscriptionControl(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, account, app, sub string) (api.EventSubscriptionDeliveryControl, error) {
	out := api.EventSubscriptionDeliveryControl{SubscriptionID: canonicalMemUUID(sub), AppID: canonicalMemUUID(app)}
	row, err := q.EventSubscriptionControlGet(ctx, db, sqlc.EventSubscriptionControlGetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if err == nil {
		out.Paused = row.Paused
		out.RatePerSecond = int(row.RatePerSecond)
		out.UpdatedAt = timestamptzToTimePtr(row.UpdatedAt)
	}
	breaker, err := readCircuit(ctx, q, db, account, app, sub)
	if err != nil {
		return out, err
	}
	if breaker != nil {
		status := circuitResponse(sub, breaker, out.Paused, time.Now().UTC())
		out.CircuitBreaker = &status
	}
	stats, err := q.EventSubscriptionControlStats(ctx, db, sqlc.EventSubscriptionControlStatsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: canonicalMemUUID(sub)})
	if err != nil {
		return out, err
	}
	out.PendingRecipients, out.ProcessingRecipients = stats.PendingRecipients, stats.ProcessingRecipients
	out.OldestPendingAt = timestamptzToTimePtr(stats.OldestPendingAt)
	if out.OldestPendingAt != nil {
		out.OldestAgeSeconds = max(0, time.Since(*out.OldestPendingAt).Seconds())
	}
	return out, nil
}
func (s *PgStore) GetEventSubscriptionDeliveryControl(ctx context.Context, account, app, sub string) (api.EventSubscriptionDeliveryControl, error) {
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.EventSubscriptionControlTarget(ctx, tx, sqlc.EventSubscriptionControlTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)}); errors.Is(err, pgx.ErrNoRows) {
		return api.EventSubscriptionDeliveryControl{}, ErrNotFound
	} else if err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	out, err := getSubscriptionControl(ctx, q, tx, account, app, sub)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) SetEventSubscriptionDeliveryControl(ctx context.Context, account, app, sub string, paused bool, rate int) (api.EventSubscriptionDeliveryControl, error) {
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	if rate < 0 || rate > api.EventSubscriptionDrainRateMax {
		return api.EventSubscriptionDeliveryControl{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.EventSubscriptionControlTarget(ctx, tx, sqlc.EventSubscriptionControlTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)}); errors.Is(err, pgx.ErrNoRows) {
		return api.EventSubscriptionDeliveryControl{}, ErrNotFound
	} else if err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	if err = q.EventSubscriptionControlLock(ctx, tx, canonicalMemUUID(sub)); err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	if err = q.EventSubscriptionControlSet(ctx, tx, sqlc.EventSubscriptionControlSetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), Paused: paused, RatePerSecond: int32(rate), NowAt: pgtypeFromTime(time.Now().UTC())}); err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	out, err := getSubscriptionControl(ctx, q, tx, account, app, sub)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

// Called under the consumer advisory lock, held through admission commit. A
// pause response therefore follows any earlier admission of this consumer.
func eventSubscriptionGateTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, p eventAdmissionPlan) (string, time.Time, error) {
	if !p.matched || p.prior {
		return "", time.Time{}, nil
	}
	// Non-UUID workflow/object recipients do not use application subscription controls.
	if !isEventSubscriptionUUID(p.recipient.ID) {
		return "", time.Time{}, nil
	}
	if err := q.EventSubscriptionControlLock(ctx, tx, canonicalMemUUID(p.recipient.ID)); err != nil {
		return "", time.Time{}, err
	}
	r, err := readCircuit(ctx, q, tx, p.recipient.AccountID, p.recipient.AppID, p.recipient.ID)
	if err != nil {
		return "", time.Time{}, err
	}
	row, err := q.EventSubscriptionControlGet(ctx, tx, sqlc.EventSubscriptionControlGetParams{AccountID: mustPgUUID(p.recipient.AccountID), AppID: mustPgUUID(p.recipient.AppID), SubscriptionID: mustPgUUID(p.recipient.ID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, err
	}
	now := time.Now().UTC()
	until := timeFromPgtype(row.WindowStartedAt).Add(time.Second)
	if row.Paused {
		return "subscription_paused", now.Add(api.EventSubscriptionControlRetryDelay), nil
	}
	if reason, next := circuitAdmissionReason(r, p.claim, time.Now().UTC()); reason != "" {
		return reason, next, nil
	}
	if row.RatePerSecond > 0 {
		if row.WindowCount >= row.RatePerSecond && until.After(now) {
			return "subscription_rate_limited", until, nil
		}
		if err = q.EventSubscriptionControlUsePermit(ctx, tx, sqlc.EventSubscriptionControlUsePermitParams{SubscriptionID: row.SubscriptionID, NowAt: pgtypeFromTime(now)}); err != nil {
			return "", time.Time{}, err
		}
	}
	return "", time.Time{}, nil
}
func eventSubscriptionControlProgress(previous PublishedEventRecipientProgress, attempts int, reason string, next time.Time) PublishedEventRecipientProgress {
	return PublishedEventRecipientProgress{DeliveryAgeOverride: previous.DeliveryAgeOverride, State: PublishedEventRecipientPending, Attempts: max(0, attempts-1), CapacityDeferrals: previous.CapacityDeferrals, RetrySpentMS: previous.RetrySpentMS, RetryGeneration: previous.RetryGeneration, DeliveryControlReason: reason, NextAttemptAt: &next, UpdatedAt: time.Now().UTC()}
}

type memEventSubscriptionControl struct {
	AccountID   string
	AppID       string
	Paused      bool
	Rate        int
	WindowAt    time.Time
	WindowCount int
	PausedAt    time.Time
	UpdatedAt   time.Time
}

func (m *MemStore) eventSubscriptionControlTargetLocked(account, app, sub string) bool {
	target, ok := m.eventSubscriptionAppLocked(app)
	if !ok || !sameMemUUID(target.AccountID, account) || target.Status == AppDeleted {
		return false
	}
	for _, subscription := range m.eventSubscriptions {
		if sameMemUUID(subscription.ID, sub) && sameMemUUID(subscription.AppID, app) && sameMemUUID(subscription.AccountID, account) {
			return true
		}
	}
	c := m.eventSubscriptionControls[canonicalMemUUID(sub)]
	return c != nil && sameMemUUID(c.AccountID, account) && sameMemUUID(c.AppID, app)
}
func (m *MemStore) eventSubscriptionControlResponseLocked(account, app, sub string) api.EventSubscriptionDeliveryControl {
	out := api.EventSubscriptionDeliveryControl{SubscriptionID: canonicalMemUUID(sub), AppID: canonicalMemUUID(app)}
	if c := m.eventSubscriptionControls[out.SubscriptionID]; c != nil && sameMemUUID(c.AccountID, account) && sameMemUUID(c.AppID, app) {
		out.Paused = c.Paused
		out.RatePerSecond = c.Rate
		t := c.UpdatedAt
		out.UpdatedAt = &t
	}
	if breaker := m.eventCircuitBreakers[out.SubscriptionID]; breaker != nil {
		status := circuitResponse(sub, breaker, out.Paused, time.Now().UTC())
		out.CircuitBreaker = &status
	}
	for _, receipt := range m.eventFanout {
		if receipt.Delivered || !sameMemUUID(eventRoutingAccount(receipt), account) {
			continue
		}
		for _, recipient := range receipt.RecipientSnapshot {
			if !sameMemUUID(recipient.AppID, app) || !sameMemUUID(recipient.ID, sub) {
				continue
			}
			state := eventRecipientRoutingStateLocked(receipt, recipient.ID)
			if state == "processing" {
				out.ProcessingRecipients++
			}
			if state == "pending" {
				out.PendingRecipients++
				if out.OldestPendingAt == nil || receipt.CreatedAt.Before(*out.OldestPendingAt) {
					t := receipt.CreatedAt
					out.OldestPendingAt = &t
				}
			}
		}
	}
	if out.OldestPendingAt != nil {
		out.OldestAgeSeconds = max(0, time.Since(*out.OldestPendingAt).Seconds())
	}
	return out
}
func (m *MemStore) GetEventSubscriptionDeliveryControl(ctx context.Context, account, app, sub string) (api.EventSubscriptionDeliveryControl, error) {
	if err := ctx.Err(); err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return api.EventSubscriptionDeliveryControl{}, ErrNotFound
	}
	return m.eventSubscriptionControlResponseLocked(account, app, sub), nil
}
func (m *MemStore) SetEventSubscriptionDeliveryControl(ctx context.Context, account, app, sub string, paused bool, rate int) (api.EventSubscriptionDeliveryControl, error) {
	if err := ctx.Err(); err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	if rate < 0 || rate > api.EventSubscriptionDrainRateMax {
		return api.EventSubscriptionDeliveryControl{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return api.EventSubscriptionDeliveryControl{}, ErrNotFound
	}
	if m.eventSubscriptionControls == nil {
		m.eventSubscriptionControls = map[string]*memEventSubscriptionControl{}
	}
	id := canonicalMemUUID(sub)
	c := m.eventSubscriptionControls[id]
	if c == nil {
		c = &memEventSubscriptionControl{AccountID: canonicalMemUUID(account), AppID: canonicalMemUUID(app)}
		m.eventSubscriptionControls[id] = c
	}
	if !paused && (c.Paused || c.Rate != rate) {
		c.WindowAt = time.Now().UTC()
		c.WindowCount = 0
	}
	if paused && !c.Paused {
		c.PausedAt = time.Now().UTC()
	}
	if !paused {
		c.PausedAt = time.Time{}
	}
	c.Paused = paused
	if !paused {
		c.Rate = rate
	}
	c.UpdatedAt = time.Now().UTC()
	return m.eventSubscriptionControlResponseLocked(account, app, sub), nil
}
func (m *MemStore) eventSubscriptionWaitingReasonLocked(account, app, sub string, now time.Time) string {
	target, exists := m.eventSubscriptionAppLocked(app)
	if !exists || target.Status == AppDeleted || !sameMemUUID(target.AccountID, account) {
		return ""
	}
	c := m.eventSubscriptionControls[canonicalMemUUID(sub)]
	if c == nil || !sameMemUUID(c.AccountID, account) || !sameMemUUID(c.AppID, app) {
		return ""
	}
	if c.Paused {
		return "subscription_paused"
	}
	if r := m.eventCircuitBreakers[canonicalMemUUID(sub)]; r != nil {
		if reason, _ := circuitWaitingReason(r, now); reason != "" {
			return reason
		}
	}
	if c.Rate > 0 && c.WindowCount >= c.Rate && c.WindowAt.Add(time.Second).After(now) {
		return "subscription_rate_limited"
	}
	return ""
}
func (m *MemStore) eventSubscriptionGateLocked(p eventAdmissionPlan) (string, time.Time) {
	now := time.Now().UTC()
	if !p.matched || p.prior {
		return "", time.Time{}
	}
	reason := ""
	c := m.eventSubscriptionControls[canonicalMemUUID(p.recipient.ID)]
	if c != nil && (!sameMemUUID(c.AccountID, p.recipient.AccountID) || !sameMemUUID(c.AppID, p.recipient.AppID)) {
		c = nil
	}
	if c != nil && c.Paused {
		reason = "subscription_paused"
	} else if c != nil && c.Rate > 0 && c.WindowCount >= c.Rate && c.WindowAt.Add(time.Second).After(now) {
		reason = "subscription_rate_limited"
	}
	if reason == "subscription_paused" {
		return reason, now.Add(api.EventSubscriptionControlRetryDelay)
	}
	if reason, next := circuitAdmissionReason(m.eventCircuitBreakers[canonicalMemUUID(p.recipient.ID)], p.claim, now); reason != "" {
		return reason, next
	}
	if reason == "subscription_rate_limited" {
		return reason, c.WindowAt.Add(time.Second)
	}
	if c != nil && c.Rate > 0 {
		if !c.WindowAt.Add(time.Second).After(now) {
			c.WindowAt = now
			c.WindowCount = 0
		}
		c.WindowCount++
	}
	return "", time.Time{}
}
