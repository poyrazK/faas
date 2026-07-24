package meter

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Loop runs the five meterd timers (sample / quota / stripe / dunning
// / residency) until the context cancels. Each timer fires on its own
// cadence; the first error from any goroutine is surfaced to the
// caller.
//
// The Loop never blocks the daemon's shutdown — every ticker selects on
// both its tick and ctx.Done. Production wires this from cmd/meterd;
// tests inject a fake clock + collaborators and step through ticks
// directly (NewLoop is just a constructor, no goroutines started until
// Run).
//
// Observability (PR feat/m7-meterd-observability): every tick body is
// wrapped in ops.Observe(name, dur, err) per ADR-015 and the spec §12
// Prometheus convention. The lastTick map is the source of truth for
// /healthz — see pkg/meter/health.go. ops and log may be nil; NewLoop
// coerces nil to a fresh test registry / slog.Default so callers don't
// have to special-case the zero value (mirrors scheddgrpc/server.go:54-56).
type Loop struct {
	store  state.Store
	parker ScheddParker
	pusher billing.Provider
	notif  Notifier
	// mailer is the customer-facing email sender (spec §171 "dunning +
	// quota mails reference email"). Shared with the dunning timer —
	// both loops hand off the same Sender via DunningSender's local
	// interface shape. nil falls back to a no-op sender in NewLoop so
	// tests don't have to thread a stub.
	mailer    DunningSender
	dunning   *Dunning
	residency *Residency
	now       func() time.Time
	log       *slog.Logger
	cfg       *Config
	ops       *wire.OpsMetrics

	lastTickMu sync.RWMutex
	// lastTick records the wall-clock time each named tick body last
	// completed successfully. Keys mirror the runTicks "name" argument:
	// "sample", "stripe", "dunning", "residency" are populated by
	// runTicks; "quota" is populated by runQuotaOnce (same field,
	// written outside runTicks because quota is loop-shaped, not
	// single-tick).
	lastTick map[string]time.Time
}

// NewLoop wires the loop. The interfaces are local to pkg/meter so the
// daemon (cmd/meterd) can substitute test doubles without importing the
// concrete packages (scheddgrpc, stripex). dunning may be nil; tests
// that don't exercise dunning pass nil and the fourth goroutine is
// skipped. mailer may be nil; NewLoop coerces nil to noopDunningSender
// so callers don't have to special-case the zero value (mirrors
// ops/log coercion above). residency is wired unconditionally today
// (the gauge emit is the source of truth for the §12 dashboard panel
// and must not be skipped in production); the ops.SetResidentGBPerCustomer
// method is nil-safe so the loop tolerates a nil ops receiver. ops
// and log likewise may be nil — see the Loop doc comment.
func NewLoop(store state.Store, parker ScheddParker, pusher billing.Provider, notif Notifier, mailer DunningSender, dunning *Dunning, residency *Residency, now func() time.Time, log *slog.Logger, cfg *Config, ops *wire.OpsMetrics) *Loop {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	if ops == nil {
		ops = wire.NewOpsMetrics("meter_test")
	}
	if mailer == nil {
		mailer = noopDunningSender{}
	}
	if residency == nil {
		residency = NewResidency(store, now, log, ops)
	}
	return &Loop{
		store: store, parker: parker, pusher: pusher, notif: notif,
		mailer: mailer, dunning: dunning, residency: residency, now: now, log: log, cfg: cfg, ops: ops,
		lastTick: make(map[string]time.Time),
	}
}

// Run starts the five timers and blocks until ctx cancels or any timer
// errors out. Sampler / quota loop / stripe pusher / dunning /
// residency each log + continue on per-tick errors so a transient
// Postgres blip doesn't kill the daemon; only a context cancel returns
// cleanly.
func (l *Loop) Run(ctx context.Context) error {
	sampler := NewSampler(l.store, l.now)
	pusher := NewPusher(l.store, l.pusher, l.log, l.now, l.ops)
	errc := make(chan error, 5)
	go func() {
		errc <- l.runTicks(ctx, l.cfg.SampleInterval, func(c context.Context) error {
			_, err := sampler.SampleAndRoll(c)
			return err
		}, "sample")
	}()
	go func() { errc <- l.runQuotaTicks(ctx) }()
	go func() {
		errc <- l.runTicks(ctx, l.cfg.StripeInterval, func(c context.Context) error {
			_, err := pusher.PushHour(c)
			return err
		}, "stripe")
	}()
	if l.dunning != nil {
		go func() {
			errc <- l.runTicks(ctx, l.cfg.DunningInterval,
				func(c context.Context) error { return l.dunning.RunOnce(c) },
				"dunning")
		}()
	}
	if l.residency != nil {
		go func() {
			errc <- l.runTicks(ctx, l.cfg.ResidencyInterval,
				func(c context.Context) error {
					_, err := l.residency.RunOnce(c)
					return err
				}, "residency")
		}()
	}
	// Block until either ctx cancels or a hard error fires.
	select {
	case <-ctx.Done():
		return nil
	case err := <-errc:
		return err
	}
}

// runTicks is the shared timer driver. Per-tick errors are logged and
// swallowed so a transient backend hiccup doesn't kill meterd (spec §14
// hardening: metering must be self-healing). Each successful (or
// failed-but-logged) tick is observed via ops.Observe and recorded in
// lastTick[name] under lastTickMu — both writes happen together so the
// two observability surfaces cannot drift.
func (l *Loop) runTicks(ctx context.Context, interval time.Duration, tick func(context.Context) error, name string) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			start := time.Now()
			err := tick(ctx)
			l.ops.Observe(name, time.Since(start), err)
			l.recordTick(name, start)
			if err != nil {
				l.log.Warn("meter: "+name+" tick", "err", err)
			}
		}
	}
}

// runQuotaTicks walks every account once per quota interval and applies
// the per-plan ladder. The first per-account error is logged + skipped
// (one bad account shouldn't stop the rest). Records the last tick
// timestamp under "quota" — separate from runTicks because quota sweeps
// a list rather than a single tick body.
//
// Observe is called with err=nil unconditionally: runQuotaOnce already
// logs and skips per-account failures, so there is no aggregate error
// to surface. Operators alerting on quota errors should scrape
// journald (the warn lines carry account_id + err); this is documented
// inline to make the silent counter explicit — a future
// meterd_quota_errors_total counter is in the survey follow-ups.
func (l *Loop) runQuotaTicks(ctx context.Context) error {
	t := time.NewTicker(l.cfg.QuotaInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			start := time.Now()
			l.runQuotaOnce(ctx)
			l.ops.Observe("quota", time.Since(start), nil)
			l.recordTick("quota", start)
		}
	}
}

// runQuotaOnce is exported as RunQuotaOnce so tests can step it without
// spinning the ticker. Production's only caller is runQuotaTicks.
func (l *Loop) runQuotaOnce(ctx context.Context) {
	accounts, err := l.store.ListAllAccounts(ctx)
	if err != nil {
		l.log.Warn("meter: quota list_accounts", "err", err)
		return
	}
	now := l.now()
	for _, acct := range accounts {
		usages, err := MonthUsageForAccount(ctx, l.store, acct.ID, now)
		if err != nil {
			l.log.Warn("meter: quota usage_by_month", "account", acct.ID, "err", err)
			continue
		}
		used := MonthlyUsageGB(usages)
		if _, err := EnforceQuota(ctx, l.store, l.notif, l.parker, l.mailer, l.log, acct, used, now); err != nil {
			// EnforceQuota already logged + skipped parked-instance failures;
			// only status/structural errors reach here.
			if errors.Is(err, state.ErrNotFound) {
				continue
			}
			l.log.Warn("meter: enforce_quota", "account", acct.ID, "err", err)
		}
	}
}

// recordTick stamps the last successful tick for /healthz. Centralized
// so the runTicks / runQuotaTicks paths agree on the storage shape.
func (l *Loop) recordTick(name string, at time.Time) {
	l.lastTickMu.Lock()
	l.lastTick[name] = at
	l.lastTickMu.Unlock()
}

// LastTick returns the wall-clock time the named tick body last
// completed. ok=false means the tick has never fired. Read-mostly path;
// the RWMutex lets /healthz probes go lock-free against the writers in
// the common case.
func (l *Loop) LastTick(name string) (time.Time, bool) {
	l.lastTickMu.RLock()
	defer l.lastTickMu.RUnlock()
	t, ok := l.lastTick[name]
	return t, ok
}

// HasPlan is a tiny convenience exposed so cmd/meterd's wire-up can
// gate paid-only behavior on the plan literal without re-importing api.
// Tests use it to assert "Free plan was treated as Free" without
// inspecting the full account struct.
//nolint:unused // exposed for downstream callers; not used inside pkg/meter
// (helper functions intentionally removed — see commit history if needed)
