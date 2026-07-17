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

// instanceResolver maps a proxied addr back to the instance schedd woke it as.
// gateway.PGBackend satisfies it (from the wake response).
type instanceResolver interface {
	InstanceIDForAddr(addr string) (string, bool)
}

// activityReporter flushes a batch of last_request_at touches. scheddgrpc.Client
// satisfies it (schedd is the sole writer to `instances`, so the gateway hands it
// the batch over gRPC rather than writing the table — CLAUDE.md ownership).
type activityReporter interface {
	ReportActivity(ctx context.Context, touches []state.InstanceTouch) (int, error)
}

// schedFlushSink is gatewayd's production LastSeenSink. The handler Touches it by
// the addr it proxied to on every 2xx; Flush resolves each addr to its instance
// id and reports the batch to schedd (ADR-018). Touches whose addr no longer
// resolves (the instance parked) are dropped — schedd would reject them anyway.
type schedFlushSink struct {
	resolve  instanceResolver
	reporter activityReporter
	log      logger

	mu   sync.Mutex
	seen map[string]time.Time // addr -> most-recent request time
}

// logger is the tiny slice of *slog.Logger this file needs (Warn only), kept as
// an interface so the sink stays trivially testable.
type logger interface {
	Warn(msg string, args ...any)
}

func newSchedFlushSink(resolve instanceResolver, reporter activityReporter, log logger) *schedFlushSink {
	return &schedFlushSink{
		resolve:  resolve,
		reporter: reporter,
		log:      log,
		seen:     map[string]time.Time{},
	}
}

// Touch records the latest request time for addr. Keeps only the newest per addr
// so a burst collapses to one row per instance per flush.
func (s *schedFlushSink) Touch(addr string, t time.Time) {
	s.mu.Lock()
	if prev, ok := s.seen[addr]; !ok || t.After(prev) {
		s.seen[addr] = t
	}
	s.mu.Unlock()
}

// Get returns the buffered time for addr (mainly for tests / symmetry).
func (s *schedFlushSink) Get(addr string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.seen[addr]
	return t, ok
}

// Forget drops addr from the buffer.
func (s *schedFlushSink) Forget(addr string) {
	s.mu.Lock()
	delete(s.seen, addr)
	s.mu.Unlock()
}

// Flush drains the buffer, resolves each addr to an instance id, and reports the
// batch to schedd. The buffer is cleared up front so a slow/failed report never
// wedges the hot path; unresolved addrs are simply dropped.
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
	for addr, t := range batch {
		id, ok := s.resolve.InstanceIDForAddr(addr)
		if !ok {
			continue
		}
		touches = append(touches, state.InstanceTouch{InstanceID: id, LastRequest: t})
	}
	if len(touches) == 0 {
		return nil
	}
	if _, err := s.reporter.ReportActivity(ctx, touches); err != nil {
		s.log.Warn("gatewayd: report activity", "touches", len(touches), "err", err)
		return err
	}
	return nil
}
