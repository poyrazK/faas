package main

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

// lastSeenFlushInterval is how often the gateway drains buffered touches to
// schedd. ADR-147 replaces the legacy 15 s cadence: a legal 10 s idle
// timeout otherwise expires while successful requests are still buffered.
// Keep writes batched and leave headroom for the scheduler RPC.
const lastSeenFlushInterval = 2 * time.Second

// schedFlushSink is gatewayd's production LastSeenSink. The handler Touches it
// by the instance_id it proxied to on every 2xx (issue #168 — per-instance
// attribution survives the multi-instance fan-out where multiple instances
// share a single compute_node). Flush reports the batch to the per-node
// schedd that owns each instance (Phase 2 / Gate A: the touch flow is
// partitioned by instance.node_id via the scheddRouter).
//
// Touches whose instance is no longer cached are kept in the batch anyway:
// schedd's ReportActivity drops unknown instance ids silently and returns
// the count it actually applied, so we don't need a resolver hop on the
// gateway side anymore.
type schedFlushSink struct {
	router  scheddInstanceResolver
	store   *state.PgStore
	log     logger
	nowFunc func() time.Time

	mu   sync.Mutex
	seen map[string]bufferedInstanceTouch // instance_id -> coalesced activity
}

// bufferedInstanceTouch preserves both halves of the activity contract while
// collapsing a burst into one schedd row. last_request_at is a max, whereas
// request_count is a sum; storing only the timestamp silently discarded the
// count and prevented warm-snapshot eligibility from ever being reached.
type bufferedInstanceTouch struct {
	lastRequest  time.Time
	requestDelta int64
}

// scheddInstanceResolver is the slice of scheddRouter the flush
// sink needs. Production wires the real router; tests inject a
// fake that returns a known ScheddClient per instance id. A
// nil return with err=nil signals "instance vanished from the
// store, drop silently" — same semantics as the legacy
// single-dial path where schedd dropped unknown ids server-side.
//
// The ScheddClient interface satisfies activityReporter (its
// ReportActivity method matches), so the returned value is
// directly usable as the activityReporter without an adapter.
type scheddInstanceResolver interface {
	ScheddForInstance(ctx context.Context, instanceID string) (scheddgrpc.ScheddClient, error)
}

// logger is the tiny slice of *slog.Logger this file needs (Warn only), kept as
// an interface so the sink stays trivially testable.
type logger interface {
	Warn(msg string, args ...any)
}

func newSchedFlushSink(router scheddInstanceResolver, store *state.PgStore, log logger) *schedFlushSink {
	return &schedFlushSink{
		router:  router,
		store:   store,
		log:     log,
		nowFunc: time.Now,
		seen:    map[string]bufferedInstanceTouch{},
	}
}

// Touch records one successful request for instanceID. The newest timestamp
// and the total request delta are coalesced independently so a burst still
// becomes one row without losing requests.
func (s *schedFlushSink) Touch(instanceID string, t time.Time) {
	s.mu.Lock()
	touch := s.seen[instanceID]
	if touch.lastRequest.IsZero() || t.After(touch.lastRequest) {
		touch.lastRequest = t
	}
	touch.requestDelta++
	s.seen[instanceID] = touch
	s.mu.Unlock()
}

// Get returns the buffered time for instanceID (mainly for tests / symmetry).
func (s *schedFlushSink) Get(instanceID string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	touch, ok := s.seen[instanceID]
	return touch.lastRequest, ok
}

// Forget drops instanceID from the buffer (e.g. when the cached target is
// evicted via instance_changed; the next touch will repopulate or the
// entry will simply age out on Flush).
func (s *schedFlushSink) Forget(instanceID string) {
	s.mu.Lock()
	delete(s.seen, instanceID)
	s.mu.Unlock()
}

// Flush drains the buffer and reports each touch to the schedd
// that owns the corresponding instance (Phase 2 / Gate A).
//
// Partitioning: each touch is grouped by the resolved ScheddClient
// (one per compute_nodes.id), so a single Flush issues N per-node
// ReportActivity calls rather than one fan-out call. On the
// single-box default-local fleet every touch lands in the same
// group — byte-identical to the pre-PR behaviour.
//
// Failure handling: the buffer is cleared up front so a slow /
// failed report never wedges the hot path. The first per-schedd
// error is returned to the caller so the FlushEvery loop sees the
// failure, but later groups still issue their RPCs (a single
// unreachable node should not block touches to siblings). The
// instances themselves stay in the underlying store, so a
// subsequent touch re-populates the buffer.
//
// Unknown instance ids (admin deleted, hard-deleted post-M7) are
// silently dropped — schedd's ReportActivity would drop them
// anyway, and the gateway side wants to fail quietly so a stray
// race doesn't 503 the listener.
func (s *schedFlushSink) Flush(ctx context.Context) error {
	s.mu.Lock()
	if len(s.seen) == 0 {
		s.mu.Unlock()
		return nil
	}
	batch := s.seen
	s.seen = map[string]bufferedInstanceTouch{}
	s.mu.Unlock()

	// Group touches by ScheddClient. We carry the resolved client
	// directly (rather than re-resolving per group dispatch) so the
	// hot path makes exactly one ScheddForInstance call per
	// instance per Flush, even on a multi-node fleet. The ScheddClient
	// interface satisfies activityReporter via its ReportActivity
	// method, so the map key is the ScheddClient and the dispatch is
	// via the same value.
	type group struct {
		cli     scheddgrpc.ScheddClient
		nodeID  string
		touches []state.InstanceTouch
	}
	groups := map[scheddgrpc.ScheddClient]*group{}
	for id, touch := range batch {
		cli, err := s.router.ScheddForInstance(ctx, id)
		if err != nil {
			s.log.Warn("gatewayd: report activity resolve instance", "instance", id, "err", err)
			continue
		}
		if cli == nil {
			// instance no longer exists in the store — drop silently.
			continue
		}
		if g, ok := groups[cli]; ok {
			g.touches = append(g.touches, state.InstanceTouch{InstanceID: id, LastRequest: touch.lastRequest, RequestDelta: touch.requestDelta})
			continue
		}
		// Resolve the owning node id for the log line. Cache hit
		// path: a single InstanceByID per first-seen instance per
		// Flush. Touches that share an instance land on the same
		// cli, so this lookup runs at most once per instance id
		// per Flush — and the instance count per Flush is bounded
		// by lastSeenFlushInterval × per-instance request rate,
		// which is well within reason for the batched cadence. nil
		// store (tests) skips the lookup; the log line just
		// doesn't carry the node id.
		var nodeID string
		if s.store != nil {
			if ins, err := s.store.InstanceByID(ctx, id); err == nil {
				nodeID = ins.NodeID
			}
		}
		groups[cli] = &group{cli: cli, nodeID: nodeID, touches: []state.InstanceTouch{{InstanceID: id, LastRequest: touch.lastRequest, RequestDelta: touch.requestDelta}}}
	}

	var firstErr error
	for _, g := range groups {
		if _, err := g.cli.ReportActivity(ctx, g.touches); err != nil {
			s.log.Warn("gatewayd: report activity", "node", g.nodeID, "touches", len(g.touches), "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
