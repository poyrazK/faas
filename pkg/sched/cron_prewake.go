package sched

// Cron pre-wake (ADR-20261107122928 §4, amendment 3). On each minute's cron
// tick schedd looks one minute ahead: an app with no resident instance whose
// HTTP cron fires within api.CronPrewakeLeadSeconds gets an ADR-160 prewarm
// intent (trigger cron, count 1, wake_at the fire time). The prewarm drain's
// lead window is as long, so it claims the intent at once and wakes the app
// through the normal admission path; the intent's floor keeps the instance
// until api.CronPrewakeHoldSeconds after the fire. Meanwhile the idle ladder
// quiet-pauses it, so the run resumes it in about a millisecond instead of
// waiting on a snapshot restore.
//
// The early seconds are unbilled only through the quiet pause ledger, so an
// app is pre-woken only when its account is one the quiet_pause_unbilled
// platform flag releases: the decision meterd and the idle-out make.

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

// cronPrewakeIntentCreator files the prewarm intents (state.PrewarmStore).
type cronPrewakeIntentCreator interface {
	CreatePrewarmIntent(ctx context.Context, appID, accountID string, count int, wakeAt, expiresAt time.Time, trigger string) (state.PrewarmIntent, error)
}

type cronPrewake struct {
	released func(platformflags.Subject) bool
	// filed holds the fire time each app's intent was filed for, so the
	// minute's later passes and a resumed sweep do not file it twice.
	filed   map[string]time.Time
	outcome *prometheus.CounterVec
}

// cronPrewakeFire is the earliest upcoming HTTP cron fire of one app.
type cronPrewakeFire struct {
	cron state.Cron
	at   time.Time
}

// WithCronPrewake turns on cron pre-wake (schedd cron_prewake, off by
// default). released decides the quiet_pause_unbilled platform flag for an
// app's subject; nil leaves the feature off. Intents fire only while the
// prewarm drain runs (WithPrewarm). reg may be nil.
func (l *Loop) WithCronPrewake(released func(platformflags.Subject) bool, reg prometheus.Registerer) *Loop {
	if released == nil {
		l.cronPrewake = nil
		return l
	}
	p := &cronPrewake{released: released, filed: map[string]time.Time{}}
	p.outcome = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "schedd_cron_prewake_total",
		Help: "Cron pre-wake decisions for apps with an HTTP cron firing within the lead (ADR-20261107122928 amendment 3), by outcome.",
	}, []string{"outcome"})
	if reg != nil {
		var already prometheus.AlreadyRegisteredError
		if err := reg.Register(p.outcome); errors.As(err, &already) {
			if existing, ok := already.ExistingCollector.(*prometheus.CounterVec); ok {
				p.outcome = existing
			}
		}
	}
	l.cronPrewake = p
	return l
}

func (p *cronPrewake) observe(outcome string) {
	if p != nil && p.outcome != nil {
		p.outcome.WithLabelValues(outcome).Inc()
	}
}

// runCronPrewake files the minute's pre-wake intents. It runs after the
// minute's first sweep pass. An app with a cron due now, or one that fired
// within the lead, is left alone: that fire's own wake is still coming up,
// and an intent beside it could admit a second instance.
func (l *Loop) runCronPrewake(ctx context.Context) {
	p := l.cronPrewake
	if p == nil {
		return
	}
	store := l.engine.Store()
	creator, ok := store.(cronPrewakeIntentCreator)
	if !ok {
		return
	}
	var crons []state.Cron
	var err error
	if owner := l.engine.OwnerNodeID(); owner != "" {
		crons, err = store.ListOwnedCronsByNodeID(ctx, owner)
	} else {
		crons, err = store.ListEnabledCrons(ctx)
	}
	if err != nil {
		l.log.Warn("cron prewake: list", "err", err)
		return
	}
	now := l.now()
	for app, at := range p.filed {
		if !at.After(now) {
			delete(p.filed, app)
		}
	}
	fires, busy := cronPrewakeFires(crons, now, api.CronPrewakeLeadSeconds*time.Second)
	apps := make([]string, 0, len(fires))
	for app := range fires {
		apps = append(apps, app)
	}
	sort.Strings(apps)
	plans := newCronPlanGate(store)
	for _, appID := range apps {
		fire := fires[appID]
		if busy[appID] {
			p.observe("firing")
			continue
		}
		if at, ok := p.filed[appID]; ok && at.Equal(fire.at) {
			continue
		}
		outcome := l.prewakeApp(ctx, store, creator, plans, fire)
		if outcome == "filed" {
			p.filed[appID] = fire.at
		}
		p.observe(outcome)
	}
}

// cronPrewakeFires returns, per app, the earliest HTTP cron fire in
// (now, now+lead], and the apps with a cron due now or fired within lead.
func cronPrewakeFires(crons []state.Cron, now time.Time, lead time.Duration) (map[string]cronPrewakeFire, map[string]bool) {
	fires := map[string]cronPrewakeFire{}
	busy := map[string]bool{}
	horizon := now.Add(lead)
	for _, c := range crons {
		if !c.Enabled {
			continue
		}
		sched, err := ParseScheduleWithTimezone(c.Schedule, c.Timezone)
		if err != nil {
			continue
		}
		at := sched.NextFireAt(c.ScheduleCursor())
		if !at.After(now) || (!c.LastFiredAt.IsZero() && now.Sub(c.LastFiredAt) < lead) {
			busy[c.AppID] = true
			continue
		}
		// A command cron runs in its own job VM, not the app's instance.
		if len(c.Command) > 0 || at.After(horizon) {
			continue
		}
		if cur, ok := fires[c.AppID]; !ok || at.Before(cur.at) {
			fires[c.AppID] = cronPrewakeFire{cron: c, at: at}
		}
	}
	return fires, busy
}

// prewakeApp applies the cron sweep's own gates, then the pre-wake's, and
// files the intent. It returns the outcome label.
func (l *Loop) prewakeApp(ctx context.Context, store state.Store, creator cronPrewakeIntentCreator, plans *cronPlanGate, fire cronPrewakeFire) string {
	app, err := store.AppByID(ctx, fire.cron.AppID)
	if err != nil {
		return "error"
	}
	switch {
	case !l.engine.ownsApp(app):
		return "not_owned"
	case !plans.admit(ctx, app, fire.cron):
		return "plan_limit"
	case !l.engine.appCapabilityAllowed(ctx, app, "crons"):
		return "crons_off"
	case l.cronAppBudgetHeld(ctx, app):
		return "budget_held"
	}
	acct, err := state.AppResourceAccount(ctx, store, app)
	if err != nil {
		return "error"
	}
	if !l.cronPrewake.released(platformflags.Subject{AccountID: app.AccountID, Plan: string(acct.Plan), OrgID: app.OrgID}) {
		return "not_released"
	}
	resident, err := l.engine.appHasResidentInstances(ctx, app.ID)
	if err != nil {
		return "error"
	}
	if resident {
		return "resident"
	}
	hold := fire.at.Add(api.CronPrewakeHoldSeconds * time.Second)
	if _, err := creator.CreatePrewarmIntent(ctx, app.ID, app.AccountID, 1, fire.at, hold, state.PrewarmTriggerCron); err != nil {
		l.log.Warn("cron prewake: file intent", "app_id", app.ID, "cron_id", fire.cron.ID, "fire_at", fire.at, "err", err)
		return "error"
	}
	return "filed"
}

// runCronPrewakeRetention deletes the settled intents cron pre-wake filed,
// api.CronPrewakeIntentRetention after their window ended, on the hourly
// retention tick.
func (l *Loop) runCronPrewakeRetention(ctx context.Context) {
	store, ok := l.engine.store.(state.CronPrewarmRetentionStore)
	if !ok {
		return
	}
	before := l.now().Add(-api.CronPrewakeIntentRetention)
	deleted := 0
	for range api.CronHistoryPrunePasses {
		n, err := store.PruneCronPrewarmIntents(ctx, before, api.CronHistoryPruneBatch)
		deleted += n
		if err != nil {
			l.log.Warn("cron prewake retention: sweep failed", "deleted", deleted, "err", err)
			return
		}
		if n < api.CronHistoryPruneBatch {
			break
		}
	}
	if deleted > 0 {
		l.log.Info("cron prewake retention: swept", "deleted", deleted)
	}
}
