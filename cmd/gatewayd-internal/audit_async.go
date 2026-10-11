package main

import (
	"context"
	"log/slog"
	"slices"
	"sync/atomic"
	"time"
)

const (
	// asyncAuditQueueCapacity bounds the request-path audit backlog. Beyond
	// it events are dropped (ADR-035: gateway audit is best-effort).
	asyncAuditQueueCapacity = 4096
	// asyncAuditWriteTimeout bounds one row write so a slow database cannot
	// wedge the writer behind a single insert.
	asyncAuditWriteTimeout = 2 * time.Second
	// asyncAuditDropLogInterval rate-limits the dropped-events warning.
	asyncAuditDropLogInterval = 30 * time.Second
)

type asyncAuditRecord struct {
	actor, kind string
	subject     *string
	data        []byte
}

// asyncAuditStore moves request-path audit rows off the request goroutine.
// Edge-rule and authn gates emit one row per JWT success, IP/geo/throttle
// denial, or forged-XFF hit; writing each synchronously turned attack
// traffic against a deny rule into Postgres write load and added an insert's
// latency to every gated request. AppendEvent now only enqueues; one writer
// drains the queue, so database load from audit is bounded to a single
// connection's throughput regardless of request rate, and a full queue drops
// the event (counted and periodically logged) instead of blocking traffic.
type asyncAuditStore struct {
	next     auditStore
	queue    chan asyncAuditRecord
	log      *slog.Logger
	dropped  atomic.Int64
	reported atomic.Int64
}

// newAsyncAuditStore starts the writer; it drains until ctx is done.
func newAsyncAuditStore(ctx context.Context, next auditStore, capacity int, log *slog.Logger) *asyncAuditStore {
	if capacity < 1 {
		capacity = 1
	}
	s := &asyncAuditStore{next: next, queue: make(chan asyncAuditRecord, capacity), log: log}
	go s.run(ctx)
	return s
}

// AppendEvent enqueues without blocking. The caller's context is not
// retained: the request has usually finished by the time the row is written.
func (s *asyncAuditStore) AppendEvent(_ context.Context, actor, kind string, subject *string, data []byte) error {
	var subjectCopy *string
	if subject != nil {
		value := *subject
		subjectCopy = &value
	}
	select {
	case s.queue <- asyncAuditRecord{actor: actor, kind: kind, subject: subjectCopy, data: slices.Clone(data)}:
	default:
		s.dropped.Add(1)
	}
	return nil
}

func (s *asyncAuditStore) run(ctx context.Context) {
	ticker := time.NewTicker(asyncAuditDropLogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.reportDrops()
		case rec := <-s.queue:
			s.write(ctx, rec)
		}
	}
}

func (s *asyncAuditStore) write(ctx context.Context, rec asyncAuditRecord) {
	writeCtx, cancel := context.WithTimeout(ctx, asyncAuditWriteTimeout)
	defer cancel()
	if err := s.next.AppendEvent(writeCtx, rec.actor, rec.kind, rec.subject, rec.data); err != nil && s.log != nil {
		s.log.Warn("gatewayd audit emit failed", "kind", rec.kind, "err", err)
	}
}

func (s *asyncAuditStore) reportDrops() {
	total := s.dropped.Load()
	previous := s.reported.Swap(total)
	if total > previous && s.log != nil {
		s.log.Warn("gatewayd audit queue full; events dropped", "dropped", total-previous, "dropped_total", total)
	}
}
