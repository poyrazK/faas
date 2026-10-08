package sched

// The probe → breaker feed (ADR-201 §3).
//
// meterd writes one data_upstream_probes row per (host, region) every 30s.
// schedd reads the opted-in upstreams, folds distinct recent probe outcomes for
// each into the breaker, and the breaker pushes the resulting circuit set to
// vmmd.
//
// A POLL, not a LISTEN. The ADR originally said the breaker would subscribe
// to an existing notify, which was wrong: `data_upstreams_changed` fires on
// the CAPTURE table, not on probe inserts, and pkg/sched/upstream_affinity.go
// records that schedd deliberately does not listen on it. Adding a notify per
// probe row would also mean one notify per upstream per 30s per app across
// the fleet, on the same pg_notify pipe the wake path uses. A poll at the
// probe's own cadence carries the same information, costs one indexed query
// per tick, and rebuilds breaker state after a schedd restart for free.

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"
)

// EgressCircuitCandidate is one opted-in upstream plus the recent probe
// verdict for it. The reader is responsible for the join; this package stays
// free of SQL.
type EgressCircuitCandidate struct {
	Upstream EgressUpstream
	// OK is one actual probe outcome. Sampled is its timestamp.
	OK      bool
	Sampled time.Time
}

// EgressCircuitCandidateReader returns every opted-in upstream with its
// recent probe verdicts in timestamp order. Returning candidates with no probe at all is fine —
// the loop skips them; an upstream nothing has measured must not be broken.
type EgressCircuitCandidateReader func(ctx context.Context) ([]EgressCircuitCandidate, error)

// EgressCircuitLoop folds probe verdicts into the breaker on a timer.
type EgressCircuitLoop struct {
	breaker  *EgressCircuitBreaker
	read     EgressCircuitCandidateReader
	interval time.Duration
	// maxAge bounds how stale a probe may be and still count. Past it the
	// upstream is treated as unmeasured and skipped, so a stalled meterd
	// cannot freeze a circuit open on evidence nobody is refreshing.
	maxAge time.Duration
	log    *slog.Logger
	now    func() time.Time
	// seen is the last sampled_at folded per breaker key. A probe row that
	// has not advanced is NOT re-observed: the breaker's window counts
	// observations, so re-folding the same row every tick would manufacture
	// evidence and trip a circuit on a single real sample.
	seen    map[string]time.Time
	tracked map[string]EgressUpstream
	apps    func(context.Context) ([]string, error)
}

// NewEgressCircuitLoop wires the loop. A zero interval uses 30s, matching the
// probe cadence; a zero maxAge uses four intervals, so a couple of missed
// probes do not immediately invalidate a verdict.
func NewEgressCircuitLoop(breaker *EgressCircuitBreaker, read EgressCircuitCandidateReader, interval, maxAge time.Duration, log *slog.Logger) *EgressCircuitLoop {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if maxAge <= 0 {
		maxAge = 4 * interval
	}
	if log == nil {
		log = slog.Default()
	}
	return &EgressCircuitLoop{
		breaker:  breaker,
		read:     read,
		interval: interval,
		maxAge:   maxAge,
		log:      log,
		now:      time.Now,
		seen:     make(map[string]time.Time),
		tracked:  make(map[string]EgressUpstream),
	}
}

// WithClock overrides the loop's clock. Tests only.
func (l *EgressCircuitLoop) WithClock(now func() time.Time) *EgressCircuitLoop {
	l.now = now
	return l
}

// Interval reports the configured tick interval, for the daemon's loop
// registration and its liveness beat.
func (l *EgressCircuitLoop) Interval() time.Duration { return l.interval }

// Run ticks until ctx is done. Never returns an error: a failed read or a
// failed push is logged and retried on the next tick, because a transient
// Postgres or vmmd blip must not tear down the scheduler.
func (l *EgressCircuitLoop) Run(ctx context.Context) {
	if l == nil || l.breaker == nil || l.read == nil {
		return
	}
	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := l.Tick(ctx); err != nil {
				l.log.Warn("sched: egress circuit loop tick failed", "err", err)
			}
		}
	}
}

// Tick performs one reconcile pass. Exported so the daemon can run it once at
// startup (rebuilding breaker state before the first interval elapses) and so
// tests do not need a timer.
func (l *EgressCircuitLoop) Tick(ctx context.Context) error {
	candidates, err := l.read(ctx)
	if err != nil {
		return err
	}
	live, freshest, apps, err := l.indexCandidates(ctx, candidates)
	if err != nil {
		return err
	}
	l.foldCandidates(candidates, live)
	l.selectFreshUpstreams(live, freshest, apps)
	var failures []error
	for appID, upstreams := range apps {
		if err := l.breaker.RefreshApp(ctx, appID, upstreams); err != nil {
			failures = append(failures, err)
			l.log.Warn("sched: egress circuit reconcile failed", "app", appID)
		}
	}
	for key := range l.seen {
		if _, ok := live[key]; !ok {
			delete(l.seen, key)
		}
	}
	l.tracked = live
	return errors.Join(failures...)
}

func (l *EgressCircuitLoop) indexCandidates(ctx context.Context, candidates []EgressCircuitCandidate) (map[string]EgressUpstream, map[string]time.Time, map[string][]EgressUpstream, error) {
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Sampled.Before(candidates[j].Sampled) })
	live := make(map[string]EgressUpstream)
	freshest := make(map[string]time.Time)
	apps := make(map[string][]EgressUpstream)
	for _, c := range candidates {
		if c.Upstream.AppID == "" || c.Upstream.Hash == "" {
			continue
		}
		key := c.Upstream.key()
		live[key], freshest[key] = c.Upstream, c.Sampled
		apps[c.Upstream.AppID] = nil
	}
	for key, up := range l.tracked {
		apps[up.AppID] = nil
		if current, ok := live[key]; !ok || egressUpstreamConfig(current) != egressUpstreamConfig(up) {
			delete(l.seen, key)
			l.breaker.forgetProbeState(up)
		}
	}
	if l.apps != nil {
		ids, err := l.apps(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, id := range ids {
			apps[id] = nil
		}
	}
	return live, freshest, apps, nil
}

func (l *EgressCircuitLoop) foldCandidates(candidates []EgressCircuitCandidate, live map[string]EgressUpstream) {
	now := l.now()
	for _, c := range candidates {
		key := c.Upstream.key()
		if _, ok := live[key]; !ok || c.Sampled.IsZero() || now.Sub(c.Sampled) > l.maxAge || c.Sampled.After(now) {
			continue
		}
		if last, ok := l.seen[key]; ok && !c.Sampled.After(last) {
			continue
		}
		l.breaker.observeProbe(c.Upstream, c.OK, c.Sampled)
		l.seen[key] = c.Sampled
	}
}

func (l *EgressCircuitLoop) selectFreshUpstreams(live map[string]EgressUpstream, freshest map[string]time.Time, apps map[string][]EgressUpstream) {
	now := l.now()
	for key, up := range live {
		latest := freshest[key]
		if latest.IsZero() || now.Sub(latest) > l.maxAge || latest.After(now) {
			// Release expired rejection and discard counts. Fresh evidence
			// starts a new window after a stalled probe feed recovers.
			l.breaker.forgetProbeState(up)
			delete(l.seen, key)
			continue
		}
		apps[up.AppID] = append(apps[up.AppID], up)
	}
}

// WithAppLister includes durable policies whose final upstream was removed
// while schedd was down, allowing an empty set to clear missed closes.
func (l *EgressCircuitLoop) WithAppLister(list func(context.Context) ([]string, error)) *EgressCircuitLoop {
	l.apps = list
	return l
}
