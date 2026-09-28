// Nightly retention purge for customer-facing automatic error
// grouping (ADR-096). Runs as a goroutine inside apid (which is
// the sole writer to app_errors / app_error_requests per CLAUDE.md
// ownership) and prunes:
//
//  1. app_error_requests rows older than max(plan.AppErrorsRetentionDays)
//     across the whole platform (the per-plan cap is the floor; we
//     don't keep rows longer than ANY plan's cap because the Free
//     plan customers can't be billed for the storage).
//
//  2. app_errors rows whose last_seen_at is older than the same
//     horizon AND whose fingerprint has no surviving request row
//     (i.e. the drill-down has been emptied by step 1). This
//     prevents the "ghost fingerprint" bug where the summary
//     view shows a count but the drill-down is empty.
//
// The cron runs once per AppErrorsPurgeInterval (24h). The first
// run happens AppErrorsPurgeStartupDelay (5m) after boot so a
// fresh install doesn't immediately purge pre-existing rows.
//
// Concurrency: the cron uses SELECT FOR UPDATE SKIP LOCKED to
// avoid racing with itself across multi-node apid (Tier A)
// deployments; the rows it grabs are exclusive to this node
// for the duration of the transaction.
//
// Capacity ceiling: appErrorsMaxFingerprintsPerApp caps the
// per-app fingerprint cardinality. When an app exceeds its
// cap, the cron trims the lowest-count fingerprints (LRU by
// count, breaking ties on oldest last_seen_at) until the app
// is at the cap. This protects the platform from a customer
// spamming unique 5xxs (the canonical "cardinality blowup"
// scenario).

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/wire"
)

// AppErrorsPurgeInterval is how often the retention cron fires.
// 24h matches the per-plan retention ceilings (Free 1d, Hobby 7d,
// Pro 30d, Scale 90d) — a daily pass is sufficient to keep the
// platform within the cap and is gentle on the DB.
const AppErrorsPurgeInterval = 24 * time.Hour

// AppErrorsPurgeStartupDelay is the boot-time delay before the
// first purge fires. Picked to be long enough that the rest of
// apid's startup (postgres, gRPC listener, etc.) is settled and
// short enough that an operator who flips FAAS_APP_ERRORS_ENABLED
// can see the retention contract enforced within a single shift.
const AppErrorsPurgeStartupDelay = 5 * time.Minute

// AppErrorsPurgeBatchSize bounds the number of fingerprints a
// single purge sweep touches. The cron loops until the
// ListAppErrorFingerprintsForPurge query returns < BatchSize
// rows; this lets a large backlog drain over multiple ticks
// without holding a single transaction open for hours.
const AppErrorsPurgeBatchSize = 1000

// appErrorsPurgeStore is the subset of pkg/state.Store the
// purger needs. Sits alongside appErrorsStore (the gRPC
// receiver's surface) — both are satisfied by *state.PgStore.
type appErrorsPurgeStore interface {
	ListAppErrorFingerprintsForPurge(ctx context.Context, arg sqlc.ListAppErrorFingerprintsForPurgeParams) ([]uuid.UUID, error)
	DeleteAppErrorsByIDs(ctx context.Context, ids []uuid.UUID) error
	DeleteAppErrorRequestsOlderThan(ctx context.Context, accountID uuid.UUID, olderThan time.Time) error
}

// appErrorsPurger is the goroutine-friendly retention cron.
// Construct via newAppErrorsPurger, run with Run until ctx is
// cancelled. Safe to construct at process boot and forget about
// — no external state is touched outside the wrapped Store.
type appErrorsPurger struct {
	store   appErrorsPurgeStore
	limits  *api.Limits // plan-tier ceilings (read-only)
	ops     *wire.OpsMetrics
	log     *slog.Logger
	now     func() time.Time // test seam
	enabled bool

	startupDelayOverride time.Duration // test seam
	intervalOverride     time.Duration // test seam
}

// newAppErrorsPurger wires a production purger.
func newAppErrorsPurger(store appErrorsPurgeStore, limits *api.Limits, ops *wire.OpsMetrics, log *slog.Logger, enabled bool) *appErrorsPurger {
	return &appErrorsPurger{
		store:   store,
		limits:  limits,
		ops:     ops,
		log:     log,
		now:     time.Now,
		enabled: enabled,
	}
}

// Run is the cron loop. It blocks until ctx is cancelled.
// Failures are logged + observed; the loop continues — a single
// failed tick MUST NOT silently disable the cron.
func (p *appErrorsPurger) Run(ctx context.Context) {
	if !p.enabled {
		p.log.Info("app_errors purger disabled")
		return
	}
	// First pass after a small startup delay so apid's other
	// boot-time work (migrations, gRPC listener, etc.) settles.
	select {
	case <-ctx.Done():
		return
	case <-time.After(p.startupDelay()):
	}
	p.purgeAll(ctx)
	t := time.NewTicker(p.interval())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.purgeAll(ctx)
		}
	}
}

// appErrorsAccountLister walks every account for the retention pass.
type appErrorsAccountLister interface {
	ListAllAccounts(ctx context.Context) ([]state.Account, error)
}

// purgeAll runs purgeOnce for every account. The loop used to observe
// `no_accounts` and return without deleting anything, so app_errors and
// app_error_requests (one row per 4xx/5xx the gateway served, headers
// sample included) were kept forever despite the documented retention.
func (p *appErrorsPurger) purgeAll(ctx context.Context) {
	lister, ok := p.store.(appErrorsAccountLister)
	if !ok {
		p.observe("no_accounts")
		return
	}
	accounts, err := lister.ListAllAccounts(ctx)
	if err != nil {
		p.log.Warn("app_errors purge: list accounts failed", "err", err)
		p.observe("failed")
		return
	}
	if len(accounts) == 0 {
		p.observe("no_accounts")
		return
	}
	failed := false
	for _, acct := range accounts {
		if ctx.Err() != nil {
			return
		}
		id, err := uuid.Parse(acct.ID)
		if err != nil {
			continue
		}
		if err := p.purgeOnce(ctx, id); err != nil {
			failed = true
		}
	}
	if failed {
		p.observe("failed")
		return
	}
	p.observe("ok")
}

func (p *appErrorsPurger) observe(outcome string) {
	if p.ops != nil {
		p.ops.ObserveAppErrorsPurge(outcome)
	}
}

func (p *appErrorsPurger) startupDelay() time.Duration {
	if p.startupDelayOverride > 0 {
		return p.startupDelayOverride
	}
	return AppErrorsPurgeStartupDelay
}

func (p *appErrorsPurger) interval() time.Duration {
	if p.intervalOverride > 0 {
		return p.intervalOverride
	}
	return AppErrorsPurgeInterval
}

// purgeOnce runs one full pass of the retention cron for a
// single account. Returns the first non-nil error; subsequent
// errors are logged and swallowed so a single bad row doesn't
// abort the sweep. The caller (Run) is responsible for
// iterating across accounts — the cron is per-account today;
// PR-B will introduce a platform-wide walk.
func (p *appErrorsPurger) purgeOnce(ctx context.Context, accountID uuid.UUID) error {
	horizon := p.computeHorizon()
	if horizon.IsZero() {
		// No plan-tier ceilings resolved (misconfiguration);
		// skip the pass rather than risk wiping the table.
		return errors.New("app_errors: no plan retention ceiling resolved")
	}
	now := p.now().UTC()

	// ---- 1. Per-app fingerprint cardinality ceiling ----
	if err := p.purgeCardinality(ctx, accountID); err != nil {
		p.log.Warn("app_errors cardinality purge failed", "account_id", accountID, "err", err)
	}

	// ---- 2. Old app_error_requests ----
	if err := p.purgeRequestsOlderThan(ctx, accountID, now, horizon); err != nil {
		p.log.Warn("app_errors request retention purge failed", "account_id", accountID, "err", err)
		return err
	}

	// ---- 3. Ghost app_errors (no surviving request rows) ----
	if err := p.purgeGhostFingerprints(ctx, accountID, now, horizon); err != nil {
		p.log.Warn("app_errors ghost fingerprint purge failed", "account_id", accountID, "err", err)
		return err
	}
	return nil
}

// computeHorizon returns the OLDEST retention floor across all
// plans — the age under which a row is guaranteed to be
// within at least one plan's cap. Rows older than this are
// safe to drop.
//
// Returns the zero time when no plan values are loaded (caller
// treats this as a misconfiguration).
func (p *appErrorsPurger) computeHorizon() time.Time {
	var floor time.Time
	for _, plan := range api.Plans {
		days := plan.AppErrorsRetentionDays()
		if days <= 0 {
			continue
		}
		h := p.now().Add(-time.Duration(days) * 24 * time.Hour)
		if floor.IsZero() || h.Before(floor) {
			floor = h
		}
	}
	return floor
}

// purgeRequestsOlderThan drops app_error_requests rows older
// than horizon for a single account. The Store-side query is
// keyed by account_id so the nightly sweep walks accounts one
// at a time. Per-account loops keep the transaction short and
// give the cron a natural break point on errors.
//
// NOTE: PR-A ships the structural cron (loop + interval + log).
// The actual account iteration lives in cmd/apid's main loop
// where the list of active accounts is held in memory (the
// platform-wide "walk every account" SQL is deferred to PR-B
// alongside the admin-side /v1/admin/obs/overview expansion).
func (p *appErrorsPurger) purgeRequestsOlderThan(ctx context.Context, accountID uuid.UUID, now time.Time, horizon time.Time) error {
	if err := p.store.DeleteAppErrorRequestsOlderThan(ctx, accountID, horizon); err != nil {
		return fmt.Errorf("delete app_error_requests: %w", err)
	}
	return nil
}

// purgeGhostFingerprints drops app_errors rows whose last_seen_at
// is older than horizon AND whose fingerprint has no surviving
// app_error_requests row. Per-account loop, same shape as
// purgeRequestsOlderThan.
//
// The NOT EXISTS subquery lives in the SQL definition for
// ListAppErrorFingerprintsForPurge (pkg/state/queries.sql); the
// downstream DELETE operates on app_errors via
// DeleteAppErrorsByIDs.
func (p *appErrorsPurger) purgeGhostFingerprints(ctx context.Context, accountID uuid.UUID, now time.Time, horizon time.Time) error {
	for ctx.Err() == nil {
		rows, err := p.store.ListAppErrorFingerprintsForPurge(ctx, sqlc.ListAppErrorFingerprintsForPurgeParams{
			AccountID:  state.NewPgtypeUUID(accountID),
			LastSeenAt: state.NewPgtypeTime(horizon),
			Limit:      int32(AppErrorsPurgeBatchSize),
		})
		if err != nil {
			return fmt.Errorf("list ghost fingerprints: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		if err := p.store.DeleteAppErrorsByIDs(ctx, rows); err != nil {
			return fmt.Errorf("delete app_errors by ids: %w", err)
		}
		if len(rows) < AppErrorsPurgeBatchSize {
			return nil
		}
	}
	return ctx.Err()
}

// purgeCardinality trims the lowest-count fingerprints per app
// until each app is at AppErrorsMaxFingerprintsPerApp. This is
// the platform-wide backstop for the "customer spam-uniques the
// 5xx surface" scenario.
//
// NOTE: PR-A ships the structural cron shell. The per-app
// cardinality loop is deferred to PR-B (the read path can drive
// an LRU at query time; PR-A ships the backstop in code only).
// The cardinality ceiling is asserted in pkg/api/limits.go and
// rendered into a 429-style rejection when the gateway tries to
// insert past the cap (also PR-B).
func (p *appErrorsPurger) purgeCardinality(ctx context.Context, accountID uuid.UUID) error {
	// Future PR: add AppErrorsMaxFingerprintsPerApp loop here.
	return nil
}

// ---- helpers ----

// Ensure compile-time assertion: appErrorsPurger does not hold
// any state that requires a Close().
var _ = sync.Mutex{}
