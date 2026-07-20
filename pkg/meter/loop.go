package meter

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Loop runs the four meterd timers (sample / quota / stripe / dunning)
// until the context cancels. Each timer fires on its own cadence; the
// first error from any goroutine is surfaced to the caller.
//
// The Loop never blocks the daemon's shutdown — every ticker selects on
// both its tick and ctx.Done. Production wires this from cmd/meterd;
// tests inject a fake clock + collaborators and step through ticks
// directly (NewLoop is just a constructor, no goroutines started until
// Run).
type Loop struct {
	store   state.Store
	parker  ScheddParker
	stripe  StripePusher
	notif   Notifier
	dunning *Dunning
	now     func() time.Time
	log     *slog.Logger
	cfg     *Config
}

// NewLoop wires the loop. The interfaces are local to pkg/meter so the
// daemon (cmd/meterd) can substitute test doubles without importing the
// concrete packages (scheddgrpc, stripex). dunning may be nil; tests
// that don't exercise dunning pass nil and the fourth goroutine is
// skipped.
func NewLoop(store state.Store, parker ScheddParker, stripe StripePusher, notif Notifier, dunning *Dunning, now func() time.Time, log *slog.Logger, cfg *Config) *Loop {
	if now == nil {
		now = time.Now
	}
	return &Loop{store: store, parker: parker, stripe: stripe, notif: notif, dunning: dunning, now: now, log: log, cfg: cfg}
}

// Run starts the four timers and blocks until ctx cancels or any timer
// errors out. Sampler / quota loop / stripe pusher / dunning each log +
// continue on per-tick errors so a transient Postgres blip doesn't kill
// the daemon; only a context cancel returns cleanly.
func (l *Loop) Run(ctx context.Context) error {
	sampler := NewSampler(l.store, l.now)
	pusher := NewPusher(l.store, l.stripe, l.log, l.now)
	errc := make(chan error, 4)
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
// hardening: metering must be self-healing).
func (l *Loop) runTicks(ctx context.Context, interval time.Duration, tick func(context.Context) error, name string) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := tick(ctx); err != nil {
				l.log.Warn("meter: "+name+" tick", "err", err)
			}
		}
	}
}

// runQuotaTicks walks every account once per quota interval and applies
// the per-plan ladder. The first per-account error is logged + skipped
// (one bad account shouldn't stop the rest).
func (l *Loop) runQuotaTicks(ctx context.Context) error {
	t := time.NewTicker(l.cfg.QuotaInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			l.runQuotaOnce(ctx)
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
		if _, err := EnforceQuota(ctx, l.store, l.notif, l.parker, l.log, acct, used, now); err != nil {
			// EnforceQuota already logged + skipped parked-instance failures;
			// only status/structural errors reach here.
			if errors.Is(err, state.ErrNotFound) {
				continue
			}
			l.log.Warn("meter: enforce_quota", "account", acct.ID, "err", err)
		}
	}
}

// HasPlan is a tiny convenience exposed so cmd/meterd's wire-up can
// gate paid-only behavior on the plan literal without re-importing api.
// Tests use it to assert "Free plan was treated as Free" without
// inspecting the full account struct.
//nolint:unused // exposed for downstream callers; not used inside pkg/meter
// (helper functions intentionally removed — see commit history if needed)
