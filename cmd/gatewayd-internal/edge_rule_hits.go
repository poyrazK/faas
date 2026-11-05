package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	edgeRuleHitFlushInterval = time.Minute
	edgeRuleHitPruneInterval = 6 * time.Hour
	// edgeRuleHitMaxRules bounds the pending map. Rule IDs come from loaded
	// rules, so this is only reached under extreme churn; past it new rules
	// are not counted until the next flush (counts are telemetry, ADR-960).
	edgeRuleHitMaxRules = 50_000
	// ADR-964 sampling: the first matches of each rule per flush interval
	// are kept as full events, bounded per gateway.
	edgeRuleEventsPerRule  = 10
	edgeRuleEventMaxQueued = 2_000
	// ADR-831 amendment 5: kind=waf detections get their own per-rule
	// budget, so ordinary matches never use it up.
	edgeRuleDetectionEventsPerRule = 10
	// ADR-20261022105846 amendment 2: penalty box events get a per-rule
	// budget of their own. A box is rare outside a flood, so the budget
	// keeps them all then and bounds them in one.
	edgeRulePenaltyEventsPerRule = 10
)

type edgeRuleHitCount struct {
	appID   string
	matched atomic.Int64
	logged  atomic.Int64
	sampled atomic.Int64 // events kept this interval (ADR-964)
}

// edgeRuleHitCounter implements gateway.EdgeRuleHitRecorder: per-rule
// counts accumulate in memory (one atomic add per hit) and a background loop
// adds them to Postgres in one batch per minute, off the request path.
type edgeRuleHitCounter struct {
	mu      sync.RWMutex
	pending map[string]*edgeRuleHitCount
	now     func() time.Time
	dropped atomic.Int64

	// skipped counts rows dropped at flush because an id is not a UUID.
	skipped *prometheus.CounterVec

	eventsMu      sync.Mutex
	events        []gateway.EdgeRuleEvent
	droppedEvents int64          // guarded by eventsMu
	detections    map[string]int // detection events sampled per rule this interval; guarded by eventsMu
	penalties     map[string]int // penalty box events sampled per rule this interval; guarded by eventsMu
}

func newEdgeRuleHitCounter() *edgeRuleHitCounter {
	skipped := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gatewayd_edge_rule_telemetry_skipped_total",
		Help: "ADR-960/964 edge-rule hit-count rows and events dropped at flush because their rule or app id is not a UUID, by kind (hit, event).",
	}, []string{"kind"})
	for _, kind := range []string{"hit", "event"} {
		skipped.WithLabelValues(kind)
	}
	return &edgeRuleHitCounter{pending: map[string]*edgeRuleHitCount{}, now: time.Now, skipped: skipped}
}

// register adds the skipped-row counter to reg. A second daemon run in one
// process (tests, ADR-1736 hand-off) reuses the registered counter.
func (c *edgeRuleHitCounter) register(reg prometheus.Registerer) {
	if reg == nil {
		return
	}
	var already prometheus.AlreadyRegisteredError
	if err := reg.Register(c.skipped); errors.As(err, &already) {
		if existing, ok := already.ExistingCollector.(*prometheus.CounterVec); ok {
			c.skipped = existing
		}
	}
}

// edgeRuleTelemetryIDsValid reports whether a row can be written. rule_id
// and app_id are NOT NULL uuid columns and a batch is one insert, so a single
// row with an empty id failed every hit and event flush on rc.296.
func edgeRuleTelemetryIDsValid(ruleID, appID string) bool {
	return isAppID(ruleID) && isAppID(appID)
}

// skipInvalid drops rows whose ids (rule, app) cannot be written, counts them
// under kind and logs one example, so one bad row cannot fail the batch.
func skipInvalid[T any](c *edgeRuleHitCounter, rows []T, kind string, ids func(T) (string, string), log *slog.Logger) []T {
	valid := rows[:0]
	var skipped int
	var ruleID, appID string
	for _, row := range rows {
		rule, app := ids(row)
		if edgeRuleTelemetryIDsValid(rule, app) {
			valid = append(valid, row)
			continue
		}
		skipped++
		ruleID, appID = rule, app
	}
	if skipped > 0 {
		c.skipped.WithLabelValues(kind).Add(float64(skipped))
		if log != nil {
			log.Warn("gatewayd: edge-rule telemetry rows without a valid rule or app id skipped", "kind", kind, "rows", skipped, "rule", ruleID, "app", appID)
		}
	}
	return valid
}

func (c *edgeRuleHitCounter) RecordEdgeRuleHit(ruleID, appID string, logged bool) {
	c.mu.RLock()
	n := c.pending[ruleID]
	c.mu.RUnlock()
	if n == nil {
		c.mu.Lock()
		if n = c.pending[ruleID]; n == nil {
			if len(c.pending) >= edgeRuleHitMaxRules {
				c.mu.Unlock()
				c.dropped.Add(1)
				return
			}
			n = &edgeRuleHitCount{appID: appID}
			c.pending[ruleID] = n
		}
		c.mu.Unlock()
	}
	if logged {
		n.logged.Add(1)
	} else {
		n.matched.Add(1)
	}
}

// SampleEdgeRuleEvent implements gateway.EdgeRuleEventSampler: it admits
// the first edgeRuleEventsPerRule counted matches of a rule per interval.
func (c *edgeRuleHitCounter) SampleEdgeRuleEvent(ruleID string) bool {
	c.mu.RLock()
	n := c.pending[ruleID]
	c.mu.RUnlock()
	return n != nil && n.sampled.Add(1) <= edgeRuleEventsPerRule
}

// SampleEdgeRuleDetection implements gateway.WAFEventRecorder: it admits the
// first edgeRuleDetectionEventsPerRule kind=waf detections of a rule per
// interval, independently of match sampling.
func (c *edgeRuleHitCounter) SampleEdgeRuleDetection(ruleID string) bool {
	c.eventsMu.Lock()
	defer c.eventsMu.Unlock()
	return sampleRuleBudget(&c.detections, ruleID, edgeRuleDetectionEventsPerRule)
}

// SampleEdgePenaltyBox implements gateway.PenaltyEventRecorder: it admits
// the first edgeRulePenaltyEventsPerRule penalty boxes of a rule per
// interval, independently of the other budgets.
func (c *edgeRuleHitCounter) SampleEdgePenaltyBox(ruleID string) bool {
	c.eventsMu.Lock()
	defer c.eventsMu.Unlock()
	return sampleRuleBudget(&c.penalties, ruleID, edgeRulePenaltyEventsPerRule)
}

// sampleRuleBudget counts one event against ruleID's budget of perRule in
// *counts, which it creates on first use, and reports whether it fits. Past
// edgeRuleHitMaxRules rules a new rule gets nothing. Callers hold eventsMu.
func sampleRuleBudget(counts *map[string]int, ruleID string, perRule int) bool {
	if *counts == nil {
		*counts = map[string]int{}
	}
	n, ok := (*counts)[ruleID]
	if !ok && len(*counts) >= edgeRuleHitMaxRules {
		return false
	}
	(*counts)[ruleID] = n + 1
	return n < perRule
}

// RecordEdgeRuleEvent queues a sampled event for the next flush.
func (c *edgeRuleHitCounter) RecordEdgeRuleEvent(ev gateway.EdgeRuleEvent) {
	c.eventsMu.Lock()
	defer c.eventsMu.Unlock()
	if len(c.events) >= edgeRuleEventMaxQueued {
		c.droppedEvents++
		return
	}
	c.events = append(c.events, ev)
}

// drainEvents swaps out queued events as store rows.
func (c *edgeRuleHitCounter) drainEvents() ([]state.EdgeRuleEvent, int64) {
	c.eventsMu.Lock()
	queued, dropped := c.events, c.droppedEvents
	c.events, c.droppedEvents, c.detections, c.penalties = nil, 0, nil, nil
	c.eventsMu.Unlock()
	out := make([]state.EdgeRuleEvent, 0, len(queued))
	for _, ev := range queued {
		outcome := state.EdgeRuleHitMatched
		switch {
		case ev.Outcome != "":
			outcome = ev.Outcome
		case ev.Logged:
			outcome = state.EdgeRuleHitLogged
		}
		out = append(out, state.EdgeRuleEvent{
			RuleID: ev.RuleID, AppID: ev.AppID, OccurredAt: ev.At, Outcome: outcome,
			RequestID: ev.RequestID, Method: ev.Method, Host: ev.Host, Path: ev.Path,
			ClientIP: ev.ClientIP, Country: ev.Country, UserAgent: ev.UserAgent,
			WAFRuleIDs: ev.WAFRuleIDs, WAFCategories: ev.WAFCategories, WAFScore: ev.WAFScore,
		})
	}
	return out, dropped
}

// flushEvents writes sampled events. Events are telemetry (ADR-964): a
// failed write drops them rather than growing the queue.
func (c *edgeRuleHitCounter) flushEvents(ctx context.Context, store state.EdgeRuleEventStore, log *slog.Logger) {
	events, dropped := c.drainEvents()
	if dropped > 0 && log != nil {
		log.Warn("gatewayd: edge-rule events dropped at the queue bound", "events", dropped)
	}
	events = skipInvalid(c, events, "event", func(e state.EdgeRuleEvent) (string, string) { return e.RuleID, e.AppID }, log)
	if len(events) == 0 {
		return
	}
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := store.RecordEdgeRuleEvents(writeCtx, events); err != nil && log != nil {
		log.Warn("gatewayd: edge-rule event flush failed; events dropped", "events", len(events), "err", err)
	}
}

// drain swaps out the pending counts and returns them as store rows.
func (c *edgeRuleHitCounter) drain() []state.EdgeRuleHit {
	c.mu.Lock()
	pending := c.pending
	c.pending = map[string]*edgeRuleHitCount{}
	c.mu.Unlock()
	bucket := c.now().UTC().Truncate(time.Hour)
	out := make([]state.EdgeRuleHit, 0, len(pending))
	for id, n := range pending {
		if m := n.matched.Load(); m > 0 {
			out = append(out, state.EdgeRuleHit{RuleID: id, AppID: n.appID, Bucket: bucket, Outcome: state.EdgeRuleHitMatched, Hits: m})
		}
		if l := n.logged.Load(); l > 0 {
			out = append(out, state.EdgeRuleHit{RuleID: id, AppID: n.appID, Bucket: bucket, Outcome: state.EdgeRuleHitLogged, Hits: l})
		}
	}
	return out
}

// restore re-adds counts a failed flush could not write, within the bound.
func (c *edgeRuleHitCounter) restore(hits []state.EdgeRuleHit) {
	for _, h := range hits {
		c.mu.Lock()
		n := c.pending[h.RuleID]
		if n == nil && len(c.pending) < edgeRuleHitMaxRules {
			n = &edgeRuleHitCount{appID: h.AppID}
			c.pending[h.RuleID] = n
		}
		c.mu.Unlock()
		switch {
		case n == nil:
			c.dropped.Add(h.Hits)
		case h.Outcome == state.EdgeRuleHitLogged:
			n.logged.Add(h.Hits)
		default:
			n.matched.Add(h.Hits)
		}
	}
}

func (c *edgeRuleHitCounter) flush(ctx context.Context, store state.EdgeRuleHitStore, log *slog.Logger) {
	if events, ok := store.(state.EdgeRuleEventStore); ok {
		c.flushEvents(ctx, events, log)
	}
	hits := skipInvalid(c, c.drain(), "hit", func(h state.EdgeRuleHit) (string, string) { return h.RuleID, h.AppID }, log)
	if len(hits) == 0 {
		return
	}
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := store.RecordEdgeRuleHits(writeCtx, hits); err != nil {
		c.restore(hits)
		if log != nil {
			log.Warn("gatewayd: edge-rule hit flush failed; retrying next interval", "rules", len(hits), "err", err)
		}
	}
	if d := c.dropped.Swap(0); d > 0 && log != nil {
		log.Warn("gatewayd: edge-rule hit counts dropped at the pending-rule bound", "hits", d)
	}
}

// run flushes every minute and prunes expired buckets every few hours until
// ctx ends, then flushes once more.
func (c *edgeRuleHitCounter) run(ctx context.Context, store state.EdgeRuleHitStore, log *slog.Logger) {
	flush := time.NewTicker(edgeRuleHitFlushInterval)
	prune := time.NewTicker(edgeRuleHitPruneInterval)
	defer flush.Stop()
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			c.flush(context.WithoutCancel(ctx), store, log)
			return
		case <-flush.C:
			c.flush(ctx, store, log)
		case <-prune.C:
			if _, err := store.PruneEdgeRuleHitCounts(ctx, c.now().Add(-state.EdgeRuleHitCountRetention)); err != nil && log != nil {
				log.Warn("gatewayd: prune edge-rule hit counts failed", "err", err)
			}
			if events, ok := store.(state.EdgeRuleEventStore); ok {
				if _, err := events.PruneEdgeRuleEvents(ctx, c.now().Add(-state.EdgeRuleEventRetention)); err != nil && log != nil {
					log.Warn("gatewayd: prune edge-rule events failed", "err", err)
				}
			}
		}
	}
}
