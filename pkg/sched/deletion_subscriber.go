package sched

// ADR-026: schedd consumes the account_deletion_pending pg_notify
// channel published by apid (handlers_account.go::scheduleDeletion)
// and evicts every live instance owned by the customer the moment
// the customer hits DELETE /v1/account. The producer/consumer model:
// apid writes accounts.status='deleted_pending' and emits the
// notification; schedd (this file) reacts by walking instances for
// the account and transitioning each via a single
// store.UpdateInstanceState to the "evicting_account_deleting" state
// (migration 00015).
//
// The subscriber is purely a drain: it takes an already-opened
// `<-chan db.Notification` and a *Engine and runs the consumer loop
// until the channel closes or ctx is cancelled. The reconnect /
// Subscribe lifecycle is the caller's responsibility (cmd/schedd
// owns it via db.Subscribe; tests inject a fake producer). Keeping
// pg_notify plumbing out of this file lets every test simulate a
// reconnect just by closing the channel and handing over a fresh
// one — no reconnect-state bookkeeping to mock.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// DeletionSubscriber consumes NotifyAccountDeletionPending and
// transitions every live instance owned by the customer to a
// terminal state the reaper sweeps.
type DeletionSubscriber struct {
	engine *Engine
	log    *slog.Logger
}

// NewDeletionSubscriber wires a subscriber with the engine + log.
// The caller is responsible for opening the pg_notify feed (see
// db.Subscribe and the fakeSubscribe in deletion_subscriber_test.go)
// and for any reconnect logic.
func NewDeletionSubscriber(engine *Engine, log *slog.Logger) *DeletionSubscriber {
	return &DeletionSubscriber{engine: engine, log: log}
}

// Run drains an already-opened channel until ctx is cancelled or the
// channel closes. Returns ctx.Err() on cancellation; any in-flight
// handle() call is given time to finish by the channel's natural
// delivery pacing.
//
// Each "keep going" decision is deliberate: pg_notify is
// best-effort; the gdpr_requests ledger in pkg/state is the source
// of truth for "the customer hit DELETE", and pkg/grace in apid
// will eventually hard-delete the account row on the 30-day timer
// regardless of whether schedd received the notification.
func (d *DeletionSubscriber) Run(ctx context.Context, ch <-chan db.Notification) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case n, ok := <-ch:
			if !ok {
				return nil
			}
			d.handle(ctx, n)
		}
	}
}

// handle is the per-message work unit. Parse, walk, evict. Each
// step logs on failure but never propagates — the loop must outlive
// a transient bad event.
func (d *DeletionSubscriber) handle(ctx context.Context, n db.Notification) {
	if n.Channel != db.NotifyAccountDeletionPending {
		// Defensive: callers generally Subscribe to a single channel,
		// but a wider-list caller could route unrelated traffic
		// here. Ignore to avoid evicting on a misrouted payload.
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

// evictAccount transitions every live instance owned by the
// customer to StateEvictingAccountDeleting. Uses the Store-level
// UpdateInstanceState directly rather than Engine.Park because Park
// requires state='running' under the engine's appMu lock; for
// waking and cold_booting instances the FC guest is mid-boot and
// the natural reaper will collect it.
//
// The state transition + the instance-changed event the reaper
// emits are enough for downstream consumers (dashboard, meterd) to
// stop billing. The firecracker microVM, if any, is reaped by the
// next loop tick — schedd doesn't dial vmmd from this path because:
//   - a WAKING instance never finished wake (vmmd has no live handle)
//   - a RUNNING instance whose row gets reaped by
//     UpdateInstanceState is auto-collected by the engine's next
//     state scan, which also dials vmmd.
//
// This keeps the subscriber's blast radius tiny: a single SQL
// UPDATE per instance, no extra RPC, no appMu.
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
	evicted := 0
	for _, ins := range rows {
		// Only act on rows still in a live state. A redelivered
		// message sees the row already transitioned; we skip it
		// — natural idempotency, no cache needed.
		if !state.IsLive(ins.State) {
			continue
		}
		if err := d.engine.store.UpdateInstanceState(ctx, ins.ID, string(state.StateEvictingAccountDeleting)); err != nil && !errors.Is(err, state.ErrNotFound) {
			d.log.Warn("schedd: deletion subscriber update state failed",
				"account", accountID, "instance", ins.ID, "err", err)
			continue
		}
		evicted++
	}
	d.log.Info("schedd: deletion subscriber evicted instances",
		"account", accountID, "observed", len(rows), "evicted", evicted)
}

// first64 returns the first 64 bytes of s as a logging-friendly
// form that won't blow up the slog line on a huge payload.
func first64(s string) string {
	const max = 64
	if len(s) <= max {
		return s
	}
	return strings.TrimRight(s[:max], "\r\n") + "…"
}
