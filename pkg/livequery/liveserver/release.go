package liveserver

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/livequery"
)

// Scale to zero (ADR-20261016151947, "Parked databases"). A database that commits
// nothing cannot change any result, so after Config.ReleaseAfter without a
// transaction to apply the hub checkpoints and releases the database
// (Engine.Release): no replication or pooled connection stays open to keep
// it awake, and subscribers keep the results they hold. The hub resumes when
// Config.Awake reports the database running again, when Wake is called, or
// when a subscription needs a result the hub does not hold; a subscriber to
// a held result joins without the database.

// errReleaseConfig refuses a hub that would release without a way back.
var errReleaseConfig = errors.New("liveserver: ReleaseAfter needs Reconnect")

// Released reports whether the hub has released the database. Safe from
// any goroutine.
func (h *Hub) Released() bool { return h.released.Load() }

// Wake resumes a released hub: the database is running again (a scheduler
// woke it), so committed transactions may be waiting in the slot.
func (h *Hub) Wake(ctx context.Context) {
	h.do(ctx, func(loop context.Context) {
		if h.released.Load() {
			_ = h.resume(loop, "woken")
		}
	})
}

// idleLongEnough reports whether the hub should release the database.
func (h *Hub) idleLongEnough() bool {
	return h.cfg.ReleaseAfter > 0 && !h.released.Load() && !h.dirty &&
		h.cfg.Now().Sub(h.lastActive) >= h.cfg.ReleaseAfter
}

// release checkpoints and lets go of the database.
func (h *Hub) release() {
	if err := h.checkpoint(); err != nil {
		// Without a durable checkpoint the slot keeps what it would replay;
		// stay connected and try again later.
		h.cfg.Logger.Error("live query checkpoint before release failed", "err", err)
		h.lastActive = h.cfg.Now()
		return
	}
	if err := h.engine.Release(); err != nil {
		h.cfg.Logger.Warn("live query release: closing the slot connection", "err", err)
	}
	h.released.Store(true)
	h.cfg.Metrics.setReleased(true)
	h.cfg.Logger.Info("released the database; subscribers keep their results",
		"idle", h.cfg.ReleaseAfter, "subscriptions", h.engine.Subscriptions())
}

// resume reconnects to the slot and catches up at the next round.
func (h *Hub) resume(ctx context.Context, reason string) error {
	if !h.released.Load() {
		return nil
	}
	start := h.cfg.Now()
	capture, err := h.cfg.Reconnect(ctx)
	if err != nil {
		h.cfg.Logger.Error("live query resume failed", "reason", reason, "err", err)
		h.cfg.Metrics.resume("failed")
		return err
	}
	if err := h.engine.Resume(capture); err != nil {
		_ = capture.Close() //nolint:contextcheck // Close bounds its own shutdown
		h.cfg.Logger.Error("live query resume failed", "reason", reason, "err", err)
		h.cfg.Metrics.resume("failed")
		return err
	}
	if capture.Created {
		h.cfg.Metrics.SlotLost(h.engine.Queries())
		h.cfg.Logger.Warn("the slot was lost while released; every query starts over", "queries", h.engine.Queries())
	}
	h.released.Store(false)
	h.cfg.Metrics.setReleased(false)
	h.cfg.Metrics.resume(reason)
	h.dirty = true // catch up now
	h.lastActive = h.cfg.Now()
	h.cfg.Logger.Info("resumed the database", "reason", reason, "took", h.cfg.Now().Sub(start))
	return nil
}

// probe asks whether a released database is running again. An error or an
// unknown state changes nothing: reconnecting would itself wake a database
// that sleeps.
func (h *Hub) probe(ctx context.Context) {
	if !h.released.Load() || h.cfg.Awake == nil {
		return
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	awake, err := h.cfg.Awake(probeCtx)
	if err != nil {
		h.cfg.Logger.Warn("live query activity probe failed", "err", err)
		return
	}
	if awake {
		_ = h.resume(ctx, "database_active")
	}
}

// join serves a subscription from a held result while the database is
// released. It reports false when the subscription needs the database.
func (h *Hub) join(engineID, sql string, session *livequery.Session, args []any) bool {
	return h.released.Load() && h.engine.Join(engineID, sql, session, args...)
}
