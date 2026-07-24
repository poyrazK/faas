package main

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// lastSeenFlushInterval is how often the gateway drains buffered touches to
// schedd (spec §4.1: flush last_request_at every 15 s, not per request).
const lastSeenFlushInterval = 15 * time.Second

// activityReporter flushes a batch of last_request_at touches. scheddgrpc.Client
// satisfies it (schedd is the sole writer to `instances`, so the gateway hands it
// the batch over gRPC rather than writing the table — CLAUDE.md ownership).
type activityReporter interface {
	ReportActivity(ctx context.Context, touches []state.InstanceTouch) (int, error)
}

// schedFlushSink is gatewayd's production LastSeenSink. The handler Touches it
// by the instance_id it proxied to on every 2xx (issue #168 — per-instance
// attribution survives the multi-instance fan-out where multiple instances
// share a single compute_node). Flush reports the batch directly to schedd.
// Touches whose instance is no longer cached are kept in the batch anyway:
// schedd's ReportActivity drops unknown instance ids silently and returns
// the count it actually applied, so we don't need a resolver hop on the
// gateway side anymore.
type schedFlushSink struct {
	reporter activityReporter
	log      logger

	mu   sync.Mutex
	seen map[string]time.Time // instance_id -> most-recent request time
}

// logger is the tiny slice of *slog.Logger this file needs (Warn only), kept as
// an interface so the sink stays trivially testable.
type logger interface {
	Warn(msg string, args ...any)
}

func newSchedFlushSink(reporter activityReporter, log logger) *schedFlushSink {
	return &schedFlushSink{
		reporter: reporter,
		log:      log,
		seen:     map[string]time.Time{},
	}
}

// Touch records the latest request time for instanceID. Keeps only the newest
// per instance id so a burst collapses to one row per instance per flush.
func (s *schedFlushSink) Touch(instanceID string, t time.Time) {
	s.mu.Lock()
	if prev, ok := s.seen[instanceID]; !ok || t.After(prev) {
		s.seen[instanceID] = t
	}
	s.mu.Unlock()
}

// Get returns the buffered time for instanceID (mainly for tests / symmetry).
func (s *schedFlushSink) Get(instanceID string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.seen[instanceID]
	return t, ok
}

// Forget drops instanceID from the buffer (e.g. when the cached target is
// evicted via instance_changed; the next touch will repopulate or the
// entry will simply age out on Flush).
func (s *schedFlushSink) Forget(instanceID string) {
	s.mu.Lock()
	delete(s.seen, instanceID)
	s.mu.Unlock()
}

// Flush drains the buffer and reports the batch to schedd. The buffer is
// cleared up front so a slow/failed report never wedges the hot path;
// unknown instance ids are dropped on schedd's side, so no resolver hop
// is needed here.
func (s *schedFlushSink) Flush(ctx context.Context) error {
	s.mu.Lock()
	if len(s.seen) == 0 {
		s.mu.Unlock()
		return nil
	}
	batch := s.seen
	s.seen = map[string]time.Time{}
	s.mu.Unlock()

	touches := make([]state.InstanceTouch, 0, len(batch))
	for id, t := range batch {
		touches = append(touches, state.InstanceTouch{InstanceID: id, LastRequest: t})
	}
	if _, err := s.reporter.ReportActivity(ctx, touches); err != nil {
		s.log.Warn("gatewayd: report activity", "touches", len(touches), "err", err)
		return err
	}
	return nil
}
