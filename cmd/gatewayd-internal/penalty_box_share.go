package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261022105846 amendment 1: penalty boxes shared between gateways.
const (
	// penaltyBoxFlushInterval batches the boxes this gateway made; a box
	// holds here at once and on other gateways after the next flush.
	penaltyBoxFlushInterval = 250 * time.Millisecond
	// penaltyBoxRepairInterval re-reads new boxes when a notification
	// was missed.
	penaltyBoxRepairInterval = 15 * time.Second
	penaltyBoxPruneInterval  = 10 * time.Minute
	// penaltyBoxLookback re-reads boxes created this long before the last
	// read caught up, so a box whose insert committed late is not missed.
	// Re-reading a box is harmless: holding it again keeps the later end.
	penaltyBoxLookback = 30 * time.Second
	penaltyBoxPage     = 1000
	// penaltyBoxMaxQueued bounds boxes waiting for a flush; past it a box
	// still holds here but is not shared.
	penaltyBoxMaxQueued = 10_000
)

// penaltyBoxSharer writes the boxes this gateway makes to Postgres and
// holds the boxes every gateway made, so a box applies on every node and
// survives a restart. Counting toward a box stays on each gateway.
type penaltyBoxSharer struct {
	box     *gateway.PenaltyBox
	store   state.EdgePenaltyBoxStore
	metrics *gateway.Metrics
	log     *slog.Logger
	page    int
	now     func() time.Time

	mu       sync.Mutex
	pending  []state.EdgePenaltyBox
	queuedAt []time.Time
	dropped  int64

	// after is where the next read starts and seen the newest box read so
	// far; loaded reports the first read is done. Only run's goroutine
	// uses them.
	after  state.EdgePenaltyBoxCursor
	seen   state.EdgePenaltyBoxCursor
	loaded bool
}

func newPenaltyBoxSharer(box *gateway.PenaltyBox, store state.EdgePenaltyBoxStore, metrics *gateway.Metrics, log *slog.Logger) *penaltyBoxSharer {
	if log == nil {
		log = slog.Default()
	}
	return &penaltyBoxSharer{box: box, store: store, metrics: metrics, log: log, page: penaltyBoxPage, now: time.Now}
}

// queue is the PenaltyBox's OnBox hook. It never blocks the request path.
func (s *penaltyBoxSharer) queue(rec gateway.PenaltyBoxRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) >= penaltyBoxMaxQueued {
		s.dropped++
		return
	}
	s.pending = append(s.pending, state.EdgePenaltyBox{AppID: rec.AppID, RuleID: rec.RuleID, Client: rec.Client, Until: rec.Until})
	s.queuedAt = append(s.queuedAt, s.now())
}

// flush writes queued boxes. A failed write is not retried: the boxes
// still hold on this gateway, and a retry could arrive after they end.
func (s *penaltyBoxSharer) flush(ctx context.Context) {
	s.mu.Lock()
	boxes, queuedAt, dropped := s.pending, s.queuedAt, s.dropped
	s.pending, s.queuedAt, s.dropped = nil, nil, 0
	s.mu.Unlock()
	if dropped > 0 {
		s.metrics.AddPenaltyShareDropped(dropped)
		s.log.Warn("gatewayd: penalty boxes not shared at the queue bound", "boxes", dropped)
	}
	if len(boxes) == 0 {
		return
	}
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := s.store.RecordEdgePenaltyBoxes(writeCtx, boxes, db.NotifyEdgePenaltyBox)
	s.metrics.ObservePenaltyShare("flush", err)
	if err != nil {
		s.log.Warn("gatewayd: penalty boxes not shared; they hold on this node only", "boxes", len(boxes), "err", err)
		return
	}
	written := s.now()
	for _, at := range queuedAt {
		s.metrics.ObservePenaltyShareDelay(gateway.PenaltyShareQueued, written.Sub(at))
	}
}

// sync holds the unexpired boxes created since the last read. The first
// read loads every unexpired box. Each box read for the first time after
// that records how long it took to arrive, on the database clock.
func (s *penaltyBoxSharer) sync(ctx context.Context) {
	readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		boxes, now, err := s.store.ListEdgePenaltyBoxes(readCtx, s.after, s.page)
		if err != nil {
			s.metrics.ObservePenaltyShare("sync", err)
			s.log.Warn("gatewayd: read shared penalty boxes failed", "err", err)
			return
		}
		for _, b := range boxes {
			s.box.Hold(gateway.PenaltyBoxRecord{AppID: b.AppID, RuleID: b.RuleID, Client: b.Client, Until: b.Until})
			s.after = state.EdgePenaltyBoxCursor{CreatedAt: b.CreatedAt, ID: b.ID}
			s.observeArrival(b, now)
		}
		if len(boxes) < s.page {
			if lookback := now.Add(-penaltyBoxLookback); lookback.Before(s.after.CreatedAt) || s.after.CreatedAt.IsZero() {
				s.after = state.EdgePenaltyBoxCursor{CreatedAt: lookback}
			}
			s.loaded = true
			s.metrics.ObservePenaltyShare("sync", nil)
			return
		}
	}
}

// observeArrival records a box's propagation delay the first time it is
// read, except during the first read, whose boxes are old by design.
// Re-reads of the lookback window are not new arrivals.
func (s *penaltyBoxSharer) observeArrival(b state.EdgePenaltyBox, now time.Time) {
	if b.CreatedAt.Before(s.seen.CreatedAt) || (b.CreatedAt.Equal(s.seen.CreatedAt) && b.ID <= s.seen.ID) {
		return
	}
	s.seen = state.EdgePenaltyBoxCursor{CreatedAt: b.CreatedAt, ID: b.ID}
	if s.loaded {
		s.metrics.ObservePenaltyShareDelay(gateway.PenaltySharePropagated, now.Sub(b.CreatedAt))
	}
}

func (s *penaltyBoxSharer) prune(ctx context.Context) {
	_, err := s.store.PruneEdgePenaltyBoxes(ctx, s.now().Add(-state.EdgePenaltyBoxRetention))
	s.metrics.ObservePenaltyShare("prune", err)
	if err != nil {
		s.log.Warn("gatewayd: prune shared penalty boxes failed", "err", err)
	}
}

// run loads the unexpired boxes, then flushes this gateway's boxes and
// reads other gateways' on each notification and repair tick until ctx
// ends. Boxes still queued at shutdown are written, so a box made just
// before a restart outlives it.
func (s *penaltyBoxSharer) run(ctx context.Context, notes <-chan db.Notification) {
	s.sync(ctx)
	flush := time.NewTicker(penaltyBoxFlushInterval)
	repair := time.NewTicker(penaltyBoxRepairInterval)
	prune := time.NewTicker(penaltyBoxPruneInterval)
	defer flush.Stop()
	defer repair.Stop()
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			s.flush(context.WithoutCancel(ctx))
			return
		case <-flush.C:
			s.flush(ctx)
		case <-repair.C:
			s.sync(ctx)
		case _, ok := <-notes:
			if !ok {
				notes = nil
				continue
			}
			drainNotifications(notes)
			s.sync(ctx)
		case <-prune.C:
			s.prune(ctx)
		}
	}
}

// drainNotifications discards notifications already waiting: one read
// covers them all.
func drainNotifications(notes <-chan db.Notification) {
	for {
		select {
		case _, ok := <-notes:
			if !ok {
				return
			}
		default:
			return
		}
	}
}

// penaltyBoxNotifications subscribes to NotifyEdgePenaltyBox. Without a
// pool or a subscription it returns nil, and the repair interval alone
// shares boxes.
func penaltyBoxNotifications(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) <-chan db.Notification {
	if pool == nil {
		return nil
	}
	notes, err := db.SubscribeWithReconnect(ctx, pool, []string{db.NotifyEdgePenaltyBox}, log)
	if err != nil {
		if log != nil {
			log.Warn("gatewayd: penalty box subscription failed; sharing every 15 s only", "err", err)
		}
		return nil
	}
	return notes
}
