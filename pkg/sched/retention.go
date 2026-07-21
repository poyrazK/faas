// Package sched — retention.go is the §17 daily sweep that DELETEs
// instances rows in {STOPPED, FAILED} older than the configured
// retention window (PR #74, spec §17 follow-up). The PR-73 watchdog
// moves stuck rows to STOPPED/FAILED; without this sweep those rows
// accumulate forever.
//
// Pattern mirrors pkg/grace (the G6 30-day deletion grace timer):
//   - injected Now() for test-time freezing,
//   - per-row errors logged + continued (one stuck row never stalls
//     the rest),
//   - ErrNotFound swallowed for redelivery safety.
//
// SweepOnce is exported so tests drive the sweep deterministically
// without spinning up a real ticker.
package sched

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Retention owns the DELETE sweep for terminal instance rows. The
// Engine field is read-only here — we don't touch the ledger; the
// watchdog already released the reservation before transitioning the
// row to STOPPED/FAILED, so a terminal row carries no live resources.
type Retention struct {
	store     state.Store
	now       func() time.Time
	retention time.Duration
	log       *slog.Logger
}

// NewRetention returns a Retention ready for the Loop ticker. Defaults:
// retention api.DefaultInstanceRetention (30d), interval owner of the
// Loop ticker (api.DefaultRetentionInterval = 1h), Now time.Now, Log
// slog.Default.
func NewRetention(store state.Store, log *slog.Logger) *Retention {
	if store == nil {
		panic("sched: Retention.store is required")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Retention{
		store:     store,
		now:       time.Now,
		retention: api.DefaultInstanceRetention,
		log:       log,
	}
}

// WithClock injects a frozen time source for tests. Same shape as
// Loop.WithClock and grace.Grace.Params.Now.
func (r *Retention) WithClock(now func() time.Time) *Retention {
	if now != nil {
		r.now = now
	}
	return r
}

// WithRetention overrides the sweep window. 0 or negative reverts to
// the api.DefaultInstanceRetention default.
func (r *Retention) WithRetention(d time.Duration) *Retention {
	if d <= 0 {
		d = api.DefaultInstanceRetention
	}
	r.retention = d
	return r
}

// SweepOnce lists STOPPED/FAILED rows older than r.retention and
// DELETEs them. Idempotent — a redelivered tick finds zero rows.
//
// Errors:
//   - listing failure: returned (caller logs).
//   - per-row delete failure other than ErrNotFound: logged, counted,
//     and the loop continues.
//
// Returns the count of rows deleted (0 is a normal outcome — every
// tick after the first sweep is a no-op).
func (r *Retention) SweepOnce(ctx context.Context) (int, error) {
	cutoff := r.now().Add(-r.retention)
	rows, err := r.store.ListInstancesInTerminalStatesOlderThan(ctx,
		[]state.State{state.StateStopped, state.StateFailed}, cutoff)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, row := range rows {
		if err := r.store.DeleteInstance(ctx, row.ID); err != nil {
			if errors.Is(err, state.ErrNotFound) {
				continue
			}
			r.log.Warn("retention: delete failed", "instance", row.ID, "state", row.State, "err", err)
			continue
		}
		deleted++
	}
	if deleted > 0 {
		r.log.Info("retention sweep", "deleted", deleted, "retention", r.retention, "cutoff", cutoff.Format(time.RFC3339))
	}
	return deleted, nil
}