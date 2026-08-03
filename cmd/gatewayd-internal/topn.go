// Sampler goroutine for the gateway_top_tenant_rps gauge (issue #300).
//
// Layering (gateway side):
//
//   Handler.observe → Metrics.ObserveTopTenantRPS(appID) — per-request
//                                              bump of the rolling count.
//   topNSampler.run                         — once-per-5s tick that
//                                              computes rps from the diff
//                                              and calls TopTenantRPSEmit.
//   Metrics.TopTenantRPSEmit                — drives the gauge from the
//                                              sorted (id, rps) slice,
//                                              bounded at cap+1 series.
//
// Why a separate topAccountSet in this package:
//
//   pkg/gateway can't import pkg/wire (cmd/gatewayd → pkg/gateway → pkg/wire
//   → cmd/gatewayd would cycle through the package import graph; pkg/gateway
//   predates pkg/wire and is intentionally narrow). The primitive here is a
//   private mirror — same shape, same cap, same resetWindow cadence. If
//   pkg/gateway ever grows a wire dependency, the two primitives should be
//   collapsed into one (tracked as follow-up; not in scope for #300).
//
// Why 5s + 24h:
//
//   Matches the apid-side sampler so the two gauges share the same
//   cadence; the §12 dashboard joins them on account_id for a single
//   "noisy tenant" panel. Faster (1s) burns CPU on the sort for no
//   panel fidelity gain; slower (30s) lets a noisy customer slip
//   between samples.
//
// Lifecycle:
//
//   Started by cmd/gatewayd/main.go after the gateway Handler is
//   constructed; runs until ctx is cancelled. Stops cleanly because
//   the only mutable state is the topAccountSet (the gauge rows
//   persist across ticks — Prometheus gauges don't have rows to
//   delete; emitting 0 simply updates the existing series).

package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
)

// topAccountSetCap mirrors pkg/wire/topn.go's topAccountSetCap. The
// two constants are intentionally identical (1000) — a future
// refactor that unifies the two primitives would have a single
// source of truth here. Kept as a separate const so this package
// doesn't import pkg/wire for a single int.
const topAccountSetCap = 1_000

// topAccountOtherLabel mirrors pkg/wire/topn.go's topAccountOtherLabel.
// Same rationale: distinct from the underlying counter's
// "__other__" overflow so a Grafana panel can filter one without
// filtering the other.
const topAccountOtherLabel = "other"

// topAccountWindow is the rolling 24h window. Matches pkg/wire/topn.go
// so the two gauges reset on the same cadence and the §12 dashboard's
// top-N view is internally consistent.
const topAccountWindow = 24 * time.Hour

// topNSamplerInterval is the 5s gauge-emission cadence.
const topNSamplerInterval = 5 * time.Second

// topAccountSet is a private mirror of pkg/wire/topAccountSet.
// Kept identical in shape so a future refactor can replace both
// with a single shared primitive. The two are NOT kept in sync
// by tests (no shared test surface); the integration is pinned
// at the dashboard/alert level (FaasTenantAbuse uses both gauges
// and the synthetic-fixture test exercises the contract).
type topAccountSet struct {
	mu        sync.Mutex
	counts    map[string]uint64
	cap       int
	lastReset time.Time
	now       func() time.Time
}

func newTopAccountSet(capacity int) *topAccountSet {
	if capacity <= 0 {
		panic("gatewayd: topAccountSet capacity must be positive")
	}
	return &topAccountSet{
		counts:    make(map[string]uint64, capacity),
		cap:       capacity,
		lastReset: time.Now(),
		now:       time.Now,
	}
}

// sample bumps the rolling-window count for the given accountID.
// Cheap path: mutex + map incr.
func (s *topAccountSet) sample(accountID string) {
	s.mu.Lock()
	s.counts[accountID]++
	s.mu.Unlock()
}

// topNSnapshot returns the current top-N sorted descending by count,
// ties broken by id (lex) for deterministic ordering. The returned
// slice is a copy.
func (s *topAccountSet) topNSnapshot() []topNEntry {
	s.mu.Lock()
	raw := make([]topNEntry, 0, len(s.counts))
	for id, count := range s.counts {
		if count == 0 {
			continue
		}
		raw = append(raw, topNEntry{ID: id, Count: count})
	}
	s.mu.Unlock()
	sortEntries(raw)
	if len(raw) > s.cap {
		raw = raw[:s.cap]
	}
	return raw
}

// snapshotCounts returns a copy of the rolling counts map for
// rps-diff computation. The sampler holds this across ticks
// to compute (current - prev) / interval.
func (s *topAccountSet) snapshotCounts() map[string]uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]uint64, len(s.counts))
	for id, count := range s.counts {
		out[id] = count
	}
	return out
}

func (s *topAccountSet) shouldReset() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now().Sub(s.lastReset) >= topAccountWindow
}

func (s *topAccountSet) resetWindow() {
	s.mu.Lock()
	s.counts = make(map[string]uint64, s.cap)
	s.lastReset = s.now()
	s.mu.Unlock()
}

// sortEntries sorts the slice descending by Count, ties broken
// by ID (lex). Extracted so the test surface is in one place.
func sortEntries(entries []topNEntry) {
	// Insertion sort — the slice is small (≤ cap = 1000) and
	// partially-sorted on most ticks (top-N membership changes
	// slowly), so insertion sort outperforms sort.Slice's
	// introsort on the hot path.
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0; j-- {
			if entries[j].Count > entries[j-1].Count {
				entries[j], entries[j-1] = entries[j-1], entries[j]
				continue
			}
			if entries[j].Count == entries[j-1].Count && entries[j].ID < entries[j-1].ID {
				entries[j], entries[j-1] = entries[j-1], entries[j]
				continue
			}
			break
		}
	}
}

// topNEntry is the (id, count) tuple the primitive holds in its
// top-N view. The sampler sets RPS on each entry before handing
// it to Metrics.TopTenantRPSEmit; the primitive itself only
// stores Count (the rps is a per-tick derived value, computed
// from the prev/current diff outside the primitive's hot path).
type topNEntry struct {
	ID    string
	Count uint64
	RPS   float64
}

// topNSampler drives the per-daemon top-tenant gauge from the
// rolling count. One instance per Metrics; constructed once at
// server boot, runs as a background goroutine for the daemon's
// lifetime.
type topNSampler struct {
	set  *topAccountSet
	ops  *gateway.Metrics
	log  *slog.Logger
	mu   sync.Mutex
	prev map[string]uint64
}

func newTopNSampler(ops *gateway.Metrics, log *slog.Logger) *topNSampler {
	return &topNSampler{
		set:  newTopAccountSet(topAccountSetCap),
		ops:  ops,
		log:  log,
		prev: make(map[string]uint64),
	}
}

// Sample bumps the rolling count. Called from Handler.observe
// on every request — cheap path (mutex + map incr). The
// per-request observe path doesn't touch the gauge directly;
// the once-per-tick run() loop is the sole gauge writer.
func (s *topNSampler) Sample(accountID string) {
	if s == nil || s.set == nil {
		return
	}
	s.set.sample(accountID)
}

func (s *topNSampler) run(ctx context.Context) {
	if s.ops == nil {
		s.log.Warn("topNSampler started with nil ops; exiting")
		return
	}
	t := time.NewTicker(topNSamplerInterval)
	defer t.Stop()
	s.tick()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick()
		}
	}
}

func (s *topNSampler) tick() {
	if s.set.shouldReset() {
		s.set.resetWindow()
	}
	// Compute per-id 5s rps = (current - prev) / interval.
	current := s.set.snapshotCounts()
	s.mu.Lock()
	prev := s.prev
	s.prev = current
	s.mu.Unlock()
	topN := s.set.topNSnapshot()
	// Translate (id, count) → (id, rps) on the topNEntry in
	// place. On the very first tick after boot prev is empty
	// so the diff defaults to `now` (full count / interval) —
	// acceptable, the gauge surfaces a non-zero value as soon
	// as the daemon sees its first request, then converges to
	// a true 5s delta on subsequent ticks. The "other" overflow
	// row (pre-instantiated at boot, never reaches this loop
	// because topNSnapshot only returns real ids with non-zero
	// counts) is intentionally absent here.
	emit := make([]gateway.TopNEntry, 0, len(topN))
	var otherRPS float64
	for _, e := range topN {
		now := current[e.ID]
		delta := now
		if v, ok := prev[e.ID]; ok && now >= v {
			delta = now - v
		}
		if e.ID == topAccountOtherLabel {
			otherRPS = float64(delta) / topNSamplerInterval.Seconds()
			continue
		}
		emit = append(emit, gateway.TopNEntry{
			ID:  e.ID,
			RPS: float64(delta) / topNSamplerInterval.Seconds(),
		})
	}
	emitted := s.ops.TopTenantRPSEmit(emit, otherRPS)
	s.log.Debug("gateway topNSampler tick", slog.Int("emitted_series", emitted))
}
