package sched

// The probe → breaker feed (ADR-201 §3).
//
// meterd writes one data_upstream_probes row per (host, region) every 30s.
// schedd reads the opted-in upstreams, folds the newest probe outcome for
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
	"log/slog"
	"time"
)

// EgressCircuitCandidate is one opted-in upstream plus the newest probe
// verdict for it. The reader is responsible for the join; this package stays
// free of SQL.
type EgressCircuitCandidate struct {
	Upstream EgressUpstream
	// OK is the newest probe outcome. Sampled is its timestamp.
	OK      bool
	Sampled time.Time
}

// EgressCircuitCandidateReader returns every opted-in upstream with its
// newest probe verdict. Returning candidates with no probe at all is fine —
// the loop skips them; an upstream nothing has measured must not be broken.
type EgressCircuitCandidateReader func(ctx context.Context) ([]EgressCircuitCandidate, error)

// EgressCircuitLoop folds probe verdicts into the breaker on a timer.
type EgressCircuitLoop struct {
	breaker  *EgressCircuitBreaker
	read     EgressCircuitCandidateReader
	interval time.Duration
	// maxAge bounds how stale a probe may be and still count. Past it the
	// upstream is treated as unmeasured and skipped, so a stalled meterd
	// cannot freeze a circuit open (or closed) on evidence nobody is
	// refreshing.
	maxAge time.Duration
	log    *slog.Logger
	now    func() time.Time
	// seen is the last sampled_at folded per breaker key. A probe row that
	// has not advanced is NOT re-observed: the breaker's window counts
	// observations, so re-folding the same row every tick would manufacture
	// evidence and trip a circuit on a single real sample.
	seen map[string]time.Time
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
	now := l.now()
	for _, c := range candidates {
		if c.Upstream.AppID == "" || c.Upstream.Hash == "" {
			continue
		}
		// No probe, or one too old to trust. Skipping is the safe direction:
		// an unmeasured upstream must never be broken, and a circuit already
		// open stays open until fresh evidence closes it.
		if c.Sampled.IsZero() || now.Sub(c.Sampled) > l.maxAge {
			continue
		}
		key := c.Upstream.key()
		if last, ok := l.seen[key]; ok && !c.Sampled.After(last) {
			continue
		}
		l.seen[key] = c.Sampled
		if err := l.breaker.Observe(ctx, c.Upstream, c.OK); err != nil {
			// Keep going: one app's vmmd being unreachable must not stop the
			// rest of the fleet from converging.
			l.log.Warn("sched: egress circuit observe failed",
				"app", c.Upstream.AppID, "upstream", c.Upstream.Hash, "err", err)
		}
	}
	l.forgetRetired(candidates)
	return nil
}

// forgetRetired drops per-key dedupe state for upstreams that are no longer
// candidates, so the map cannot outlive the rows it describes. The breaker's
// own Forget is NOT called here: an upstream can drop out of the candidate set
// because the customer opted out, and that has to close the circuit rather
// than merely stop tracking it — which the opt-out path does explicitly.
func (l *EgressCircuitLoop) forgetRetired(candidates []EgressCircuitCandidate) {
	live := make(map[string]struct{}, len(candidates))
	for _, c := range candidates {
		live[c.Upstream.key()] = struct{}{}
	}
	for key := range l.seen {
		if _, ok := live[key]; !ok {
			delete(l.seen, key)
		}
	}
}
