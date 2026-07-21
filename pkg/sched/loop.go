// Package sched — daemon glue that translates pg_notify events into ledger
// updates and instance state writes. schedd is the sole writer to the
// instances table (spec §Component ownership); this file owns the loop that
// reacts to apid's notifications and drives the reaper tick. All instance
// mutation (create, transition, snapshot, destroy) goes through the Engine —
// the Loop is pure glue that decides *when* to act, not *how*.
package sched

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// Loop subscribes to the pg_notify channels schedd cares about and reacts. It
// runs the idle reaper on a 10 s tick and cron on a 60 s tick (spec §4.3). The
// Engine holds the store, ledger, and vmmd client; the Loop only orchestrates.
type Loop struct {
	pool       *pgxpool.Pool
	engine     *Engine
	log        *slog.Logger
	gateway    GatewaySynth
	now        func() time.Time
	flowCounts FlowCounter
	watchdog   *Watchdog  // §6.1 watchdog; nil means "no watchdog" (tests can opt out)
	retention  *Retention // §17 retention sweep; nil means "no retention" (tests can opt out)
}

func NewLoop(pool *pgxpool.Pool, engine *Engine, log *slog.Logger) *Loop {
	return &Loop{
		pool: pool, engine: engine, log: log,
		now:        time.Now,
		flowCounts: noopFlowCounter{},
	}
}

// WithWatchdog attaches the §6.1 watchdog (commit 3). Tests can skip
// it by not calling this; the watchdog field stays nil and Run's
// 4th ticker simply never fires a case. Production cmd/schedd wires
// the real Watchdog from the existing engine deps so the watchdog
// shares the same store / engine / clock as the rest of the loop.
func (l *Loop) WithWatchdog(w *Watchdog) *Loop {
	l.watchdog = w
	return l
}

// WithRetention attaches the §17 retention sweep (PR #74). Same opt-out
// shape as WithWatchdog: nil means no ticker fires the retention case.
// Production wires NewRetention(store, log); the default retention
// window + interval live in pkg/api/limits.
func (l *Loop) WithRetention(r *Retention) *Loop {
	l.retention = r
	return l
}

// WithGatewaySynth wires the gateway-internal RPC client the cron
// dispatch loop uses. Production calls this from cmd/schedd after
// dialing the gateway socket; tests inject a recording stub.
func (l *Loop) WithGatewaySynth(g GatewaySynth) *Loop {
	l.gateway = g
	return l
}

// WithClock swaps the time source. Tests use it to advance through cron
// boundaries deterministically; production leaves the default.
func (l *Loop) WithClock(now func() time.Time) *Loop {
	if now != nil {
		l.now = now
	}
	return l
}

// FlowCounter is the slice of "open TCP flow count by instance" the
// reaper uses to gate idle parking (spec §17 G7). Production injects a
// conntrack reader in PR-B; the default noopFlowCounter returns 0 for
// every instance, preserving the prior LastRequest-only behaviour.
type FlowCounter interface {
	Open(ctx context.Context, instanceID string) (int64, error)
}

// noopFlowCounter is the default FlowCounter. Used until PR-B wires a
// real reader; keeps ReapIdle's G7 rule inert.
type noopFlowCounter struct{}

func (noopFlowCounter) Open(_ context.Context, _ string) (int64, error) { return 0, nil }

// WithFlowCounter wires the conntrack-derived "open flows per
// instance" source (spec §17 G7). Tests inject a fake to drive table
// cases for the reaper's skip-when-busy rule; production wires a
// real conntrack reader once that lands. Nil/inert callers leave
// the noop default in place.
func (l *Loop) WithFlowCounter(fc FlowCounter) *Loop {
	if fc != nil {
		l.flowCounts = fc
	}
	return l
}

// Run blocks until ctx is cancelled. It owns three event sources: the LISTEN
// subscriber, the reaper tick, and the cron tick.
func (l *Loop) Run(ctx context.Context) error {
	// F-11: SubscribeWithReconnect wraps Subscribe with exponential backoff
	// (100ms → 5s cap) and re-acquires the LISTEN connection across pg
	// restarts. The outer channel never closes on conn drop — only ctx
	// cancel can stop this loop. Prior `notif, ok := <-` would have
	// exited cleanly the instant the LISTEN conn died, leaving the daemon
	// alive (systemd Restart=on-failure doesn't catch clean exits) but
	// inert. schedd is now a long-running aware subscriber.
	notif, err := db.SubscribeWithReconnect(ctx, l.pool, []string{
		db.NotifyAppChanged,
		db.NotifyDeploymentChanged,
		db.NotifySnapshotPrime,
	}, l.log)
	if err != nil {
		return err
	}
	// SubscribeWithReconnect owns its own cancel via the deferred
	// goroutine inside the wrapper; we close by ending ctx.

	reaperT := time.NewTicker(10 * time.Second)
	defer reaperT.Stop()
	cronT := time.NewTicker(60 * time.Second)
	defer cronT.Stop()
	// Watchdog ticker (commit 3, spec §6.1). 1s cadence matches the
	// spec's "per-second" granularity for catching stuck rows before
	// they pin a ledger reservation for the full 30s cold-boot
	// budget. nil watchdog skips this ticker entirely so the test
	// surface stays green without a watchdog dependency.
	var watchdogT *time.Ticker
	if l.watchdog != nil {
		watchdogT = time.NewTicker(DefaultWatchdogInterval)
		defer watchdogT.Stop()
	}
	// Retention ticker (PR #74, spec §17 follow-up). Default cadence
	// is hourly (pkg/api.DefaultRetentionInterval) — the sweep itself
	// reads now-30d, so hourly granularity means a row that crossed
	// the threshold gets DELETED within the next hour. nil retention
	// skips this ticker entirely.
	//
	// First-fire is intentionally DEFERRED one minute after startup
	// (retentionFirstFireDelay). A bare time.NewTicker fires once
	// immediately, which on a fresh deploy would race the §6.1
	// watchdog's first sweep and delete any rows the backfill
	// (migration 00017) anchored to a now()-based terminal_at before
	// the watchdog has had a chance to stamp its first batch.
	var retentionT *time.Ticker
	var retentionFirst <-chan time.Time
	if l.retention != nil {
		t := time.NewTicker(api.DefaultRetentionInterval)
		defer t.Stop()
		retentionT = t
		delay := time.NewTimer(retentionFirstFireDelay)
		defer delay.Stop()
		retentionFirst = delay.C
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case n, ok := <-notif:
			if !ok {
				// Defensive — wrapper guarantees open until ctx done.
				return nil
			}
			l.handleNotification(ctx, n)
		case <-reaperT.C:
			l.runReaper(ctx)
		case <-cronT.C:
			l.runCronTick(ctx)
		case <-watchdogTick(watchdogT):
			l.runWatchdog(ctx)
		case <-retentionFirst:
			// One-shot first fire (see retentionFirstFireDelay). After
			// this the channel is set to nil so subsequent ticks
			// exclusively come from retentionT (the 1h ticker).
			l.runRetention(ctx)
			retentionFirst = nil
		case <-retentionTick(retentionT):
			l.runRetention(ctx)
		}
	}
}

// watchdogTick is a helper that turns a nil-ticker's channel into a
// never-firing channel. It keeps the main select above free of
// per-iteration nil checks.
func watchdogTick(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// retentionTick is the same nil-safe pattern as watchdogTick, kept
// separate so each ticker type's name shows up in stack traces if
// a future regression corrupts the channel wiring.
func retentionTick(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// runWatchdog dispatches one sweep of the §6.1 watchdog. Exported as a
// method so tests can drive a single tick without spinning up Run's
// goroutine.
func (l *Loop) runWatchdog(ctx context.Context) {
	l.watchdog.sweepRuns(ctx)
}

// runRetention dispatches one sweep of the §17 retention sweep. Same
// shape as runWatchdog — exported as a method so tests drive a single
// tick without spinning up Run. Errors from SweepOnce are logged +
// swallowed (the sweep itself is idempotent + redelivery-safe; an
// error means a transient store outage, not a permanent fault).
func (l *Loop) runRetention(ctx context.Context) {
	deleted, err := l.retention.SweepOnce(ctx)
	if err != nil {
		l.log.Warn("retention: sweep failed", "err", err)
		return
	}
	if deleted > 0 {
		l.log.Info("retention: swept", "deleted", deleted)
	}
}

// handleNotification decodes the JSON payload and applies the policy.
//
//   - app_changed / deployment_changed: informational. Wake materialises an
//     instance on demand (first request), so no eager instance creation here.
//   - snapshot_prime: imaged finished building a deployment's layer; boot it
//     once, snapshot it, and park it (spec §5 step 6, ADR-018).
func (l *Loop) handleNotification(ctx context.Context, n db.Notification) {
	switch n.Channel {
	case db.NotifyAppChanged:
		l.log.Debug("app_changed", "payload", n.Payload)
	case db.NotifyDeploymentChanged:
		l.log.Debug("deployment_changed", "payload", n.Payload)
	case db.NotifySnapshotPrime:
		var p struct {
			AppID        string `json:"app_id"`
			DeploymentID string `json:"deployment_id"`
		}
		if err := json.Unmarshal([]byte(n.Payload), &p); err != nil {
			l.log.Warn("sched: bad snapshot_prime payload", "err", err)
			return
		}
		if p.AppID == "" || p.DeploymentID == "" {
			l.log.Warn("sched: snapshot_prime missing ids", "payload", n.Payload)
			return
		}
		if err := l.engine.Prime(ctx, p.AppID, p.DeploymentID); err != nil {
			l.log.Warn("sched: prime failed", "app", p.AppID, "deployment", p.DeploymentID, "err", err)
		}
	}
}

// runReaper builds a read-only snapshot of every instance and applies the idle /
// RAM-pressure selectors, delegating each action to the Engine:
//   - ReapIdle → Engine.Park (snapshot + park; snapshot reused on next wake).
//   - SelectEvictions → Engine.Evict (destroy; next wake cold-boots, ADR-005).
func (l *Loop) runReaper(ctx context.Context) {
	store := l.engine.Store()
	apps, err := store.ListAllApps(ctx)
	if err != nil {
		l.log.Warn("reaper: list apps", "err", err)
		return
	}
	// G7 conntrack warm (spec §17): if the FlowCounter is also a Warm-able
	// reader (the production flowcount.Reader is), feed it every live
	// instance up front so Open calls below are cheap map lookups. The
	// type assertion keeps the FlowCounter interface narrow — test mocks
	// that don't implement Warm are simply skipped, preserving the
	// existing test surface. Either failure falls through to
	// LastRequest-only reaping per the fail-open contract pinned by
	// TestRunReaperFlowCounterErrorFailsOpen.
	if warmer, ok := l.flowCounts.(interface {
		Warm(context.Context, []state.Instance) error
	}); ok {
		all, err := store.ListAllInstances(ctx)
		if err != nil {
			l.log.Warn("reaper: list all instances for warm", "err", err)
		} else if warmErr := warmer.Warm(ctx, all); warmErr != nil {
			l.log.Warn("reaper: warm flow reader", "err", warmErr)
		}
	}
	now := time.Now()
	var snapshot []InstanceInfo
	for _, a := range apps {
		plan := api.Plan("")
		if acct, err := store.AccountByID(ctx, a.AccountID); err == nil {
			plan = acct.Plan
		}
		instances, err := store.ListInstancesForApp(ctx, a.ID)
		if err != nil {
			continue
		}
		for _, ins := range instances {
			// G7 flow count (spec §17): the conntrack reader is the
			// production source; nil/error falls back to 0 so a flow-source
			// glitch fails open (LastRequest-only path; safe default).
			var open int64
			if l.flowCounts != nil {
				if v, err := l.flowCounts.Open(ctx, ins.ID); err == nil {
					open = v
				} else {
					l.log.Warn("reaper: flow count", "instance", ins.ID, "err", err)
				}
			}
			snapshot = append(snapshot, InstanceInfo{
				Instance:     ins.ID,
				AppID:        ins.AppID,
				Plan:         plan,
				State:        state.State(ins.State),
				RAMMB:        ins.RAMMB,
				LastRequest:  ins.LastRequestAt,
				Started:      ins.StartedAt,
				IdleTimeoutS: a.IdleTimeoutS,
				// ux_spec §6.5: per-app floor the reaper honors
				// when parking idle instances. Plan-tier-gated
				// upstream (apid updateApp handler), so the
				// value is always >= 0 here.
				MinInstances: a.MinInstances,
				OpenConns:    open,
			})
		}
	}
	resident := l.engine.Ledger().ResidentRAM()
	for _, id := range ReapIdle(now, snapshot) {
		if err := l.engine.Park(ctx, id); err != nil {
			l.log.Warn("reaper: idle park", "instance", id, "err", err)
		}
	}
	for _, id := range SelectEvictions(resident, now, snapshot) {
		if err := l.engine.Evict(ctx, id); err != nil {
			l.log.Warn("reaper: eviction", "instance", id, "err", err)
		}
	}
}

// GatewaySynth is the slice of the gateway-internal RPC the cron loop
// uses to fire a synthetic request through gatewayd (so metering +
// rate-limit apply identically to user traffic). Defined as an interface
// here so the cron loop can be tested without a live gateway socket.
type GatewaySynth interface {
	SynthesizeRequest(ctx context.Context, appID, method, path string) error
}

// httpGatewaySynth is the production GatewaySynth: a unix-socket HTTP
// client pointed at gatewayd's /v1/synthesize endpoint.
type httpGatewaySynth struct {
	client  *http.Client
	baseURL string
	log     *slog.Logger
}

// DialGatewaySynth opens an HTTP unix-socket client targeting
// gatewayd's internal listener. The client is stateless — the unix
// socket is opened per request by the transport — so dial failures
// surface on the first SynthesizeRequest call.
func DialGatewaySynth(socketPath string, log *slog.Logger) (GatewaySynth, error) {
	if socketPath == "" {
		return nil, errors.New("sched: gateway synth socket path is empty")
	}
	if log == nil {
		log = slog.Default()
	}
	tr := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", socketPath)
		},
	}
	c := &http.Client{Transport: tr, Timeout: 30 * time.Second}
	return &httpGatewaySynth{
		client:  c,
		baseURL: "http://unix/v1/synthesize",
		log:     log,
	}, nil
}

// SynthesizeRequest posts {app_id, method, path} to gatewayd's internal
// /v1/synthesize endpoint over the unix socket. The HTTP transport
// (DialContext) handles the dial; this method just shapes the request.
func (h *httpGatewaySynth) SynthesizeRequest(ctx context.Context, appID, method, path string) error {
	body, err := json.Marshal(map[string]string{
		"app_id": appID, "method": method, "path": path,
	})
	if err != nil {
		return fmt.Errorf("sched: synth marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("sched: synth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("sched: synth do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sched: synth: gateway returned %d", resp.StatusCode)
	}
	return nil
}

// runCronTick walks every enabled cron and dispatches any whose
// next-fire boundary has passed. It does NOT compute next-fire from
// robfig itself — the customer's cron.Schedule lives on the crons row
// (Schedule field) and we parse it per-tick. The dispatch path:
//
//  1. Resolve the cron + app, ensure the account isn't suspended.
//  2. Parse the schedule with robfig/cron; if NextFireAt(lastFiredAt) is
//     not in the past, skip.
//  3. Wake the app via the engine (idempotent — already-running apps
//     return their current instance).
//  4. SynthesizeRequest through gatewayd so metering + rate limits apply.
//  5. MarkCronFired + emit NotifyCronFired for the dashboard.
//
// Step 3+4 are the load-bearing spec bits (M7); they route the
// synthetic request through the gateway's full path so the metering +
// quota pipeline can't tell cron traffic from user traffic apart.
func (l *Loop) runCronTick(ctx context.Context) {
	crons, err := l.engine.Store().ListEnabledCrons(ctx)
	if err != nil {
		l.log.Warn("cron: list", "err", err)
		return
	}
	now := l.now()
	for _, c := range crons {
		l.dispatchOneCron(ctx, c, now)
	}
}

// dispatchOneCron is the per-cron decision tree. Factored out so the
// test surface can drive one cron with a fake clock.
func (l *Loop) dispatchOneCron(ctx context.Context, c state.Cron, now time.Time) {
	sched, err := ParseSchedule(c.Schedule)
	if err != nil {
		l.log.Warn("cron: bad schedule", "cron_id", c.ID, "err", err)
		return
	}
	// Boundary guard: fire iff we've crossed the next-fire boundary
	// since LastFiredAt. robfig's NextFireAt(from) is exclusive — call
	// it with LastFiredAt to get the upcoming boundary; if that boundary
	// is in the future, we already fired in this window. If LastFiredAt
	// is zero, the CreatedAt-based boundary is the first-fire guard so
	// we don't double-fire a cron enabled mid-minute.
	var boundary time.Time
	if c.LastFiredAt.IsZero() {
		boundary = c.CreatedAt
	} else {
		boundary = c.LastFiredAt
	}
	if sched.NextFireAt(boundary).After(now) {
		// Already fired in the current window.
		return
	}
	app, err := l.engine.Store().AppByID(ctx, c.AppID)
	if err != nil {
		l.log.Warn("cron: app", "cron_id", c.ID, "err", err)
		return
	}
	acct, err := l.engine.Store().AccountByID(ctx, app.AccountID)
	if err != nil {
		l.log.Warn("cron: account", "cron_id", c.ID, "err", err)
		return
	}
	if !acct.Active() {
		// Suspended accounts don't get cron traffic (spec §11 abuse
		// guard). The meter hard-stop will park the live instance; we
		// just skip the synthetic request here.
		return
	}
	if _, err := l.engine.Wake(ctx, c.AppID); err != nil {
		l.log.Warn("cron: wake", "cron_id", c.ID, "err", err)
		return
	}
	if l.gateway != nil {
		if err := l.gateway.SynthesizeRequest(ctx, c.AppID, "POST", c.Path); err != nil {
			l.log.Warn("cron: synthesize", "cron_id", c.ID, "err", err)
			return
		}
	}
	if err := l.engine.Store().MarkCronFired(ctx, c.ID, now); err != nil {
		l.log.Warn("cron: mark fired", "cron_id", c.ID, "err", err)
	}
	payload, _ := json.Marshal(map[string]any{
		"cron_id": c.ID, "app_id": c.AppID, "at": now.UTC().Format(time.RFC3339Nano),
	})
	if err := l.engine.Notifier().Notify(ctx, db.NotifyCronFired, string(payload)); err != nil {
		l.log.Warn("cron: notify cron_fired", "err", err)
	}
}
