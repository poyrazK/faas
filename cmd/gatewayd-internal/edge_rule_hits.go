package main

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

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

	eventsMu      sync.Mutex
	events        []gateway.EdgeRuleEvent
	droppedEvents int64 // guarded by eventsMu
}

func newEdgeRuleHitCounter() *edgeRuleHitCounter {
	return &edgeRuleHitCounter{pending: map[string]*edgeRuleHitCount{}, now: time.Now}
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
	c.events, c.droppedEvents = nil, 0
	c.eventsMu.Unlock()
	out := make([]state.EdgeRuleEvent, 0, len(queued))
	for _, ev := range queued {
		outcome := state.EdgeRuleHitMatched
		if ev.Logged {
			outcome = state.EdgeRuleHitLogged
		}
		out = append(out, state.EdgeRuleEvent{
			RuleID: ev.RuleID, AppID: ev.AppID, OccurredAt: ev.At, Outcome: outcome,
			RequestID: ev.RequestID, Method: ev.Method, Host: ev.Host, Path: ev.Path,
			ClientIP: ev.ClientIP, Country: ev.Country, UserAgent: ev.UserAgent,
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
	hits := c.drain()
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
