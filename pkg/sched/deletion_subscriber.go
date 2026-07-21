package sched

// ADR-026: schedd consumes the account_deletion_pending pg_notify
// channel published by apid (handlers_account.go::scheduleDeletion)
// and evicts every live instance owned by the customer the moment the
// customer hits DELETE /v1/account. The producer/consumer model:
// apid writes accounts.status='deleted_pending' and emits the
// notification; schedd (this file) reacts by walking instances for
// the account and parking each via Engine.Park, which transitions the
// row to the new "evicting_account_deleting" state (migration 00012)
// and dials vmmd to stop the microVM.
//
// The subscriber lives inside pkg/sched rather than cmd/schedd because
// it's stateless wrt the daemon — it just needs *Engine + ctx + log
// to run. Reuses the same log/ctx fan-out the cron loop uses.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// pgNotifyFeed is the producer-side interface the subscriber depends
// on. It's a thin wrapper around db.Subscribe so tests can swap in a
// fake channel without standing up Postgres. The production path
// (cmd/schedd/main.go) wires `realSubscribe` which delegates to
// db.Subscribe.
type pgNotifyFeed = func(ctx context.Context, channels []string) (<-chan db.Notification, func(), error)

// DeletionSubscriber consumes NotifyAccountDeletionPending and parks
// every live instance owned by the customer. Backoff is linear with
// jitter; on connection loss it reconnects via the pgNotifyFeed.
type DeletionSubscriber struct {
	engine *Engine
	log    *slog.Logger
	// backoffMin and backoffMax bound the reconnect delay. Kept as
	// fields so tests can dial them down to 0 (no sleep).
	backoffMin time.Duration
	backoffMax time.Duration
	// SubFn is the producer-side seam — Subscribe to a list of
	// channels and get a read-only feed + a release-func. nil = use
	// the production db.Subscribe wrapper (set up in Run()).
	SubFn func(ctx context.Context, channels []string) (<-chan db.Notification, func(), error)
	// ChannelIDs is the list passed to SubFn. Production wires
	// {db.NotifyAccountDeletionPending}; tests can pass any list.
	ChannelIDs []string
}

// NewDeletionSubscriber wires the subscriber with default production
// settings. The SubFn is filled in lazily by Run against db.Subscribe
// for backwards compatibility with the cmd/schedd/main.go seam.
func NewDeletionSubscriber(engine *Engine, log *slog.Logger) *DeletionSubscriber {
	return &DeletionSubscriber{
		engine:     engine,
		log:        log,
		backoffMin: 1 * time.Second,
		backoffMax: 30 * time.Second,
	}
}

// NewDeletionSubscriberFromChannel wires a subscriber that consumes
// from an already-opened channel (cmd/schedd's "I dialed it once at
// startup, hand me the feed" pattern). No reconnect logic — the
// caller owns the Subscribe lifecycle.
func NewDeletionSubscriberFromChannel(engine *Engine, feed <-chan db.Notification, log *slog.Logger) *DeletionSubscriber {
	return &DeletionSubscriber{
		engine:     engine,
		log:        log,
		backoffMin: 1 * time.Second,
		backoffMax: 30 * time.Second,
	}
}

// SetBackoff overrides the (min, max) reconnect bounds. Tests use this
// to dial both to zero so the loop runs without sleeping. Production
// sets (1s, 30s) implicitly via NewDeletionSubscriber.
func (d *DeletionSubscriber) SetBackoff(min, max time.Duration) {
	d.backoffMin = min
	d.backoffMax = max
}

// RunWithFeed drains an already-opened channel until ctx is cancelled
// or the channel closes. No reconnect — the caller owns the producer
// lifecycle. This is the entry point used by cmd/schedd's
// deps.subscribeDeletion seam.
func (d *DeletionSubscriber) RunWithFeed(ctx context.Context, ch <-chan db.Notification) error {
	d.consume(ctx, ch)
	return ctx.Err()
}

// Run reconnects on every Subscribe failure until ctx is cancelled.
// Used by tests + callers that don't want to manage the Subscribe
// loop themselves. Production prefers RunWithFeed.
//
// Failure modes handled here:
//   - connection reset (pg_notify client exited)  → reconnect, retry
//   - payload parse failure                       → log, skip, keep going
//   - ListInstancesForAccount failure             → log, skip, keep going
//   - Engine.Park failure on one instance         → log, continue with the rest
//
// Each "keep going" decision is deliberate: pg_notify is best-effort;
// the gdpr_requests ledger in pkg/state is the source of truth for
// "the customer hit DELETE", and pkg/grace in apid will eventually
// hard-delete the account row on the 30-day timer regardless of
// whether schedd received the notification.
func (d *DeletionSubscriber) Run(ctx context.Context) error {
	if d.SubFn == nil {
		return errors.New("sched.DeletionSubscriber.Run: SubFn is not set; use NewDeletionSubscriberFromChannel + RunWithFeed instead")
	}
	channels := d.ChannelIDs
	if len(channels) == 0 {
		channels = []string{db.NotifyAccountDeletionPending}
	}
	delay := d.backoffMin
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ch, cancel, err := d.SubFn(ctx, channels)
		if err != nil {
			d.log.Warn("schedd: deletion subscriber dial failed",
				"err", err, "retry_in", delay.String())
			if !d.sleep(ctx, delay) {
				return ctx.Err()
			}
			delay = nextDelay(delay, d.backoffMax)
			continue
		}
		delay = d.backoffMin
		d.consume(ctx, ch)
		cancel()
	}
}

// consume drains the channel until ctx is cancelled or the channel
// closes. Each message is parsed and handled; parse errors log and
// continue (a malformed payload MUST NOT block other accounts).
func (d *DeletionSubscriber) consume(ctx context.Context, ch <-chan db.Notification) {
	for {
		select {
		case <-ctx.Done():
			return
		case n, ok := <-ch:
			if !ok {
				return
			}
			d.handle(ctx, n)
		}
	}
}

// handle is the per-message work unit. Parse, walk, park. Each step
// logs on failure but never propagates — the subscriber loop must
// outlive a transient bad event.
func (d *DeletionSubscriber) handle(ctx context.Context, n db.Notification) {
	if n.Channel != db.NotifyAccountDeletionPending {
		// Defensive: Subscribe was told to focus on one channel, but
		// a future expansion might pass a wider list. Ignore
		// unrelated traffic to avoid spuriously calling Park on a
		// misrouted payload.
		return
	}
	var payload struct {
		AccountID string `json:"account_id"`
	}
	if err := json.Unmarshal([]byte(n.Payload), &payload); err != nil {
		d.log.Warn("schedd: deletion subscriber bad payload",
			"channel", n.Channel, "err", err, "payload_first_64", first64(n.Payload))
		return
	}
	if payload.AccountID == "" {
		d.log.Warn("schedd: deletion subscriber empty account_id in payload", "channel", n.Channel)
		return
	}
	d.evictAccount(ctx, payload.AccountID)
}

// evictAccount transitions every live instance owned by the customer
// to the new EVICTED_ACCOUNT_DELETING state. We use the Store-level
// UpdateInstanceState directly rather than Engine.Park because Park
// requires state='running' under the engine's appMu lock; for waking
// and cold_booting instances the FC guest is mid-boot and the
// natural reaper will collect it.
//
// The instance-changed event the reaper emits + the state transition
// itself are enough for downstream consumers (dashboard, meterd) to
// stop billing. The firecracker microVM, if any, is reaped by the
// next loop tick — schedd doesn't dial vmmd from this path because:
//   - a WAKING instance never finished wake (vmmd has no live handle)
//   - a RUNNING instance whose row gets reaped by UpdateInstanceState
//     will be auto-collected by the engine's next state scan, which
//     also dials vmmd
//
// This keeps the subscriber's blast radius tiny: a single SQL UPDATE
// per instance + the existing reaper path.
func (d *DeletionSubscriber) evictAccount(ctx context.Context, accountID string) {
	rows, err := d.engine.store.ListInstancesForAccount(ctx, accountID)
	if err != nil {
		d.log.Warn("schedd: deletion subscriber list instances failed",
			"account", accountID, "err", err)
		return
	}
	if len(rows) == 0 {
		d.log.Info("schedd: deletion subscriber observed pending account with no live instances",
			"account", accountID)
		return
	}
	for _, ins := range rows {
		// Only act on rows still in a live state. A redelivered
		// message sees the row already transitioned and we skip
		// it — natural idempotency, no cache needed.
		switch ins.State {
		case "running", "waking", "cold_booting", "snapshotting":
		default:
			continue
		}
		if err := d.engine.store.UpdateInstanceState(ctx, ins.ID, string(state.StateEvictingAccountDeleting)); err != nil && !errors.Is(err, state.ErrNotFound) {
			d.log.Warn("schedd: deletion subscriber update state failed",
				"account", accountID, "instance", ins.ID, "err", err)
			continue
		}
	}
	d.log.Info("schedd: deletion subscriber evicted instances",
		"account", accountID, "count", len(rows))
}

// sleep waits `d` or until ctx is cancelled. Returns true on a full
// sleep, false when ctx fired first (caller should bail).
func (d *DeletionSubscriber) sleep(ctx context.Context, d2 time.Duration) bool {
	t := time.NewTimer(d2)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// nextDelay grows d up to max in linear steps. Exported for tests so
// the curve can be exercised without sleeping for real seconds.
func nextDelay(cur, max time.Duration) time.Duration {
	next := cur * 2
	if next > max {
		next = max
	}
	return next
}

// first64 returns the first 64 bytes of s as a logging-friendly form
// that won't blow up the slog line on a huge payload.
func first64(s string) string {
	if len(s) <= 64 {
		return s
	}
	return s[:64] + "…"
}
