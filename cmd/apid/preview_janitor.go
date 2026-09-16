// preview_janitor.go — ADR-095 PR-C (issue #272) preview
// teardown janitor.
//
// Runs inside apid (the sole writer to customer-intent tables
// per CLAUDE.md line 71) and drives the preview row through the
// state machine:
//
//	open  --PR closed-->  closed  --24h grace-->  stale
//	                                              |
//	                                              v
//	                                          torn_down
//
// The 24h grace between closed and stale lets a customer reopen
// the PR without losing the deployment; torn_down is the final
// tombstone (apps.status='deleted'). The janitor is the only
// writer of 'stale' and 'torn_down' on preview_pr_state.
//
// Lives in apid, not schedd: per CLAUDE.md, schedd owns instances
// (not customer-intent tables). The tombstone triggers the same
// NotifyAppDelete that the dashboard delete button uses — schedd's
// app_delete subscriber already evicts in-flight wakes for the
// deleted app, so instance reaping comes for free. ADR-095 PR-C
// places the janitor here specifically to honour the CLAUDE.md
// invariant; the ADR's earlier text named cmd/schedd/janitor_preview.go,
// but that would have made schedd a writer to apps for the first
// time.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// PreviewJanitorInterval (ADR-095 PR-C) is the cadence the
// teardown sweep fires at. Five minutes is a compromise: short
// enough that the 24h closed→stale grace is observed within an
// acceptable tolerance (worst case: a 24h-1min grace lands on
// the 25-minute mark after the second tick), long enough that
// the sweep doesn't compete with customer traffic for DB time.
//
// The same number is exported as a const rather than a
// FAAS_PREVIEW_JANITOR_INTERVAL_SECONDS env knob because (a) the
// per-tick cost is bounded by PreviewJanitorMaxPerTick and (b)
// the closed-state column was specifically designed so that no
// adjustment is needed; the 24h grace is the only operator
// lever, and that's a dashboard / future-ADR concern.
const PreviewJanitorInterval = 5 * time.Minute

// PreviewJanitorStartupDelay is the boot-time delay before the
// first sweep fires. Picked to be long enough that apid's other
// startup (postgres, gRPC listeners) settles and short enough
// that an operator who restarts the box doesn't see a 5-minute
// "preview not torn down yet" window.
//
// Override via FAAS_PREVIEW_JANITOR_STARTUP_DELAY_SECONDS for
// tests (cmd/e2e sets this to 0 so the sweep fires immediately).
const PreviewJanitorStartupDelay = 1 * time.Minute

// previewJanitorStartupDelay reads FAAS_PREVIEW_JANITOR_STARTUP_DELAY_SECONDS.
// Zero or unset returns PreviewJanitorStartupDelay; a parse error
// or negative value falls back. The default of 1 minute matches
// the original behaviour and keeps the test surface minimal — the
// e2e harness is the only known caller that overrides.
func previewJanitorStartupDelay() time.Duration {
	v := strings.TrimSpace(os.Getenv("FAAS_PREVIEW_JANITOR_STARTUP_DELAY_SECONDS"))
	if v == "" {
		return PreviewJanitorStartupDelay
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return PreviewJanitorStartupDelay
	}
	return time.Duration(n) * time.Second
}

// previewJanitorInterval reads FAAS_PREVIEW_JANITOR_INTERVAL_SECONDS.
// Zero or unset returns PreviewJanitorInterval (5m). Negative
// values fall back. Tests use 1-2 seconds so multiple ticks fit
// inside a single e2e run.
func previewJanitorInterval() time.Duration {
	v := strings.TrimSpace(os.Getenv("FAAS_PREVIEW_JANITOR_INTERVAL_SECONDS"))
	if v == "" {
		return PreviewJanitorInterval
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return PreviewJanitorInterval
	}
	return time.Duration(n) * time.Second
}

// PreviewJanitorMaxPerTick bounds the sweep size. Without it,
// a customer who somehow provisioned 10k preview rows (the
// customer-facing limit is 100 — Scale tier) would tie up a
// transaction for minutes. 100 rows / tick * 5 min = 20k rows
// / hour, well above any legitimate backlog.
const PreviewJanitorMaxPerTick = 100

// previewJanitorStore is the slice of pkg/state.Store the
// janitor uses. Defined alongside appErrorsPurgeStore so the
// pattern stays consistent for any future apid-side cron.
//
// The store-level fan-out is intentionally minimal: reads use
// the dedicated ListPreviewsForTeardown (so the SQL is owned
// by the store, not duplicated here) and writes use the
// existing SetPreviewPrState + SoftDeleteAppCascade. Two
// surface methods, both already on pkg/state.Store.
type previewJanitorStore interface {
	ListPreviewsForTeardown(ctx context.Context, now time.Time, maxPerTick int) ([]state.App, error)
	SetPreviewPrState(ctx context.Context, appID, prState string) (state.App, error)
	SoftDeleteAppCascade(ctx context.Context, id string) (state.App, error)
}

// previewJanitorNotifier is the slice of cmd/apid/server.Notifier
// the janitor uses. Defined locally so the sweep is testable
// against a fake (cmd/apid/server.go's noopNotifier already
// satisfies this signature).
type previewJanitorNotifier interface {
	Notify(ctx context.Context, channel, payload string) error
}

// previewJanitor is the goroutine-friendly teardown cron.
// Construct via newPreviewJanitor, run with Run until ctx is
// cancelled. Mirrors the existing app_errors purge pattern:
// per-tick failures are logged + observed; the loop continues;
// one bad row MUST NOT silently disable the cron.
//
// The closed→stale grace is NOT a janitor concern — it lives
// entirely on the row's preview_expires_at column, which the
// githubd dispatcher re-stamps at provision time
// (created_at + 7d) and on every sync / reopened event. The
// janitor treats "PreviewExpiresAt < now() AND PR state ∈
// {closed, open}" as past-grace via transition() below; no
// in-memory grace tracking is needed.
type previewJanitor struct {
	store   previewJanitorStore
	notif   previewJanitorNotifier
	cleanup func(context.Context, state.App) error
	ops     *wire.OpsMetrics
	log     *slog.Logger
	now     func() time.Time // test seam for TTL advance
	enabled bool
}

// newPreviewJanitor wires a production janitor.
func newPreviewJanitor(store previewJanitorStore, notif previewJanitorNotifier, ops *wire.OpsMetrics, log *slog.Logger, enabled bool) *previewJanitor {
	return &previewJanitor{
		store:   store,
		notif:   notif,
		ops:     ops,
		log:     log,
		now:     time.Now,
		enabled: enabled,
	}
}

// withClock injects a frozen time source. Tests use this to
// drive preview_expires_at into the past without sleeping.
// Returns the receiver so the constructor + tests compose:
//
//	j := newPreviewJanitor(...).withClock(func() time.Time { ... })
func (j *previewJanitor) withClock(now func() time.Time) *previewJanitor {
	j.now = now
	return j
}

// withResourceCleanup attaches optional resource teardown to the same
// tombstone boundary as the preview app. Keeping it optional preserves the
// janitor's small test seam while allowing developer environments to reclaim
// bound data resources with their app lease.
func (j *previewJanitor) withResourceCleanup(cleanup func(context.Context, state.App) error) *previewJanitor {
	j.cleanup = cleanup
	return j
}

// Run is the cron loop. It blocks until ctx is cancelled.
// Failures are logged + observed; the loop continues — a single
// failed tick MUST NOT silently disable the cron. Mirrors
// appErrorsPurger.Run exactly so any future refactor treats both
// crons identically.
func (j *previewJanitor) Run(ctx context.Context) {
	if !j.enabled {
		j.log.Info("preview janitor disabled")
		return
	}
	// First tick after a small startup delay so apid's other
	// boot-time work (migrations, gRPC listeners) settles.
	// Tests override via FAAS_PREVIEW_JANITOR_STARTUP_DELAY_SECONDS
	// (cmd/e2e uses 0 to fire immediately on boot).
	startDelay := previewJanitorStartupDelay()
	select {
	case <-ctx.Done():
		return
	case <-time.After(startDelay):
	}
	t := time.NewTicker(previewJanitorInterval())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := j.sweepOnce(ctx); err != nil {
				j.log.Warn("preview janitor: tick failed", "err", err)
				j.observeOutcome("failed")
				continue
			}
			j.observeOutcome("ok")
		}
	}
}

// sweepOnce runs one full pass. Returns the first non-nil
// error; subsequent per-row failures are logged and swallowed
// so a single bad row doesn't abort the sweep.
//
// State transitions (ADR-095 PR-C):
//
//  1. Read every eligible preview row via
//     store.ListPreviewsForTeardown(now, maxPerTick). The store's
//     predicate already excludes torn_down rows so the sweep is
//     idempotent.
//
//  2. For each row: determine the transition.
//
//     - preview_pr_state='open' AND preview_expires_at<now:
//     promote to 'stale' (TTL elapsed with no PR close —
//     reap without grace). Then tombstone.
//     - preview_pr_state='open': leave alone (live preview).
//     - preview_pr_state='closed' AND
//     (preview_expires_at is null OR preview_expires_at<now):
//     promote to 'stale' (PR closed AND grace elapsed).
//     Then tombstone.
//     - preview_pr_state='closed': still in grace; leave alone.
//     - preview_pr_state='stale': already promoted; tombstone.
//
//  3. Tombstone = SetPreviewPrState='torn_down' followed by
//     SoftDeleteAppCascade. The two writes are NOT in a single
//     transaction by design: a crash between them is recoverable
//     on the next tick (ListPreviewsForTeardown observes
//     status='deleted' rows and the SetPreviewPrState is the
//     second idempotent write).
//
//  4. Emit db.NotifyAppDelete on every tombstoned row so schedd
//     reaps in-flight instances via its existing app_delete
//     subscriber (pkg/sched/app_delete_subscriber.go).
func (j *previewJanitor) sweepOnce(ctx context.Context) error {
	now := j.now().UTC()
	rows, err := j.store.ListPreviewsForTeardown(ctx, now, PreviewJanitorMaxPerTick)
	if err != nil {
		return fmt.Errorf("preview janitor: list: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	var stale, tombstoned int
	for _, row := range rows {
		next, action := j.transition(row, now)
		if next != "" {
			if _, err := j.store.SetPreviewPrState(ctx, row.ID, next); err != nil {
				if !errors.Is(err, state.ErrNotFound) {
					j.log.Warn("preview janitor: set state failed",
						"app_id", row.ID, "slug", row.Slug,
						"to", next, "err", err)
				}
				continue
			}
			stale++
			row.PreviewPrState = next
		}
		if action == actionTombstone {
			if err := j.tombstone(ctx, row); err != nil {
				j.log.Warn("preview janitor: tombstone failed",
					"app_id", row.ID, "slug", row.Slug, "err", err)
				continue
			}
			tombstoned++
		}
	}
	if stale > 0 || tombstoned > 0 {
		j.log.Info("preview janitor: sweep",
			"now", now.Format(time.RFC3339),
			"scanned", len(rows),
			"promoted", stale,
			"tombstoned", tombstoned)
	}
	return nil
}

// transitionAction enumerates what the sweeper should do to a
// single row. Defined as a typed int (rather than a richer
// struct) because the three states map cleanly to
// {promote-only, promote-and-tombstone, tombstone-only}.
type transitionAction int

const (
	actionNone transitionAction = iota
	actionTombstone
)

// transition returns the next preview_pr_state value and the
// tombstone action. Pulled into its own method so the test
// table can exercise every combination without exercising the
// store.
//
// The grace decision is encoded entirely on the row's
// PreviewExpiresAt column (set by the githubd dispatcher at
// provision time as created_at + 7d, refreshed on every sync /
// reopened event). The janitor reads "expired" once and
// routes through the state machine — no in-memory grace
// bookkeeping is needed.
func (j *previewJanitor) transition(row state.App, now time.Time) (string, transitionAction) {
	expired := row.PreviewExpiresAt != nil && row.PreviewExpiresAt.Before(now)
	switch row.PreviewPrState {
	case state.PreviewPrStateStale:
		return "", actionTombstone
	case state.PreviewPrStateClosed:
		if expired {
			return state.PreviewPrStateStale, actionTombstone
		}
		return "", actionNone
	case state.PreviewPrStateOpen:
		if expired {
			return state.PreviewPrStateStale, actionTombstone
		}
		return "", actionNone
	}
	return "", actionNone
}

// tombstone flips preview_pr_state='torn_down' then
// status='deleted'. Each write is idempotent: SetPreviewPrState
// refuses to relabel a closed-state row out of order (it would
// surface ErrInvalidPreviewPrState on a corrupt value, but
// 'torn_down' is always in the closed set). SoftDeleteAppCascade
// is also idempotent (returns the freshly-tombstoned row or
// ErrNotFound).
//
// On success, emits db.NotifyAppDelete so schedd reaps in-flight
// instances for the deleted app — same channel the dashboard
// delete button uses. The payload is a JSON object matching
// the existing app-delete contract (pkg/db/notify.go).
func (j *previewJanitor) tombstone(ctx context.Context, row state.App) error {
	if j.cleanup != nil {
		if err := j.cleanup(ctx, row); err != nil {
			return fmt.Errorf("cleanup preview resources: %w", err)
		}
	}
	if _, err := j.store.SetPreviewPrState(ctx, row.ID, state.PreviewPrStateTornDown); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("set torn_down: %w", err)
	}
	if _, err := j.store.SoftDeleteAppCascade(ctx, row.ID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("soft delete: %w", err)
	}
	// Emit the notify AFTER both writes succeed — schedd's
	// subscriber will see a row that's already status='deleted',
	// which avoids any chance of a wake racing the tombstone.
	if j.notif != nil {
		payload := fmt.Sprintf(`{"app_id":"%s","slug":"%s","kind":"preview_teardown"}`, row.ID, row.Slug)
		if err := j.notif.Notify(ctx, db.NotifyAppDelete, payload); err != nil {
			j.log.Warn("preview janitor: notify app_delete failed",
				"app_id", row.ID, "err", err)
		}
	}
	j.observeOutcome("torn_down")
	return nil
}

// observeOutcome emits a counter sample via the shared OpsMetrics
// registry. nil-safe — the janitor runs before srv.ops is wired
// in tests, and even in production a missing metrics registry
// is non-fatal. The label set is closed (ok, failed,
// torn_down) so cardinality stays bounded per
// pkg/wire/metrics.go::ObserveAppErrorsPurge convention.
func (j *previewJanitor) observeOutcome(outcome string) {
	if j.ops == nil {
		return
	}
	j.ops.ObservePreviewJanitor(outcome)
}

// --- compile-time interface assertions ---

// Pin both interfaces to the production types. ADR-095 PR-C.
// The var assignments are read-only at init time; golangci-lint's
// unused checker is happy with them.
var (
	_ previewJanitorStore    = (*state.PgStore)(nil)
	_ previewJanitorStore    = (*state.MemStore)(nil)
	_ previewJanitorNotifier = (Notifier)(nil) //nolint:unused // resolves to cmd/apid/server.go's Notifier interface
)
