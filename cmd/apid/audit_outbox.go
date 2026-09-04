package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	auditOutboxConsumer      = "apid"
	auditOutboxLease         = 30 * time.Second
	auditOutboxPollInterval  = 2 * time.Second
	auditOutboxBatchSize     = 32
	auditOutboxPruneInterval = 24 * time.Hour
	auditOutboxRetention     = 90 * 24 * time.Hour
)

// runAuditOutbox continuously replays durable imaged→apid audit handoffs.
// The optional capability check keeps MemStore-backed development and narrow
// integration stores source-compatible; production PgStore always implements
// it after migration 00590.
func runAuditOutbox(ctx context.Context, store state.Store, log *slog.Logger, evts *events.Platform) error {
	outbox, ok := store.(state.AuditEventOutboxStore)
	if !ok {
		return nil
	}

	ticker := time.NewTicker(auditOutboxPollInterval)
	defer ticker.Stop()
	lastPrune := time.Time{}
	for {
		if _, err := drainAuditOutboxOnce(ctx, outbox, log, evts); err != nil && ctx.Err() == nil {
			log.Warn("audit: durable outbox drain failed", "err", err)
		}
		now := time.Now().UTC()
		if lastPrune.IsZero() || now.Sub(lastPrune) >= auditOutboxPruneInterval {
			if _, err := outbox.PruneAuditEventOutbox(ctx, now.Add(-auditOutboxRetention)); err != nil && ctx.Err() == nil {
				log.Warn("audit: durable outbox prune failed", "err", err)
			}
			lastPrune = now
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// drainAuditOutboxOnce is split from the ticker loop so tests can exercise a
// complete replay deterministically without waiting for the production tick.
func drainAuditOutboxOnce(ctx context.Context, outbox state.AuditEventOutboxStore, log *slog.Logger, evts *events.Platform) (int, error) {
	deliveredCount := 0
	for i := 0; i < auditOutboxBatchSize; i++ {
		item, err := outbox.ClaimAuditEvent(ctx, auditOutboxConsumer, auditOutboxLease)
		if errors.Is(err, state.ErrNotFound) {
			return deliveredCount, nil
		}
		if err != nil {
			return deliveredCount, fmt.Errorf("claim: %w", err)
		}
		delivered, err := outbox.DeliverAuditEvent(ctx, item.ID)
		if err != nil {
			if failErr := outbox.FailAuditEvent(ctx, item.ID, err); failErr != nil && !errors.Is(failErr, state.ErrNotFound) {
				log.Warn("audit: durable outbox failure state update failed", "id", item.ID, "err", failErr)
			}
			log.Warn("audit: durable outbox delivery failed", "id", item.ID, "err", err)
			continue
		}
		if !delivered {
			continue
		}
		deliveredCount++

		var p auditEventPayload
		if err := json.Unmarshal(item.Data, &p); err != nil {
			// The audit row is already durable. Keep the queue
			// terminal rather than retrying a malformed payload
			// forever, but make the projection defect visible.
			log.Error("audit: durable outbox payload decode failed", "id", item.ID, "err", err)
			continue
		}
		p.OutboxID = item.ID
		p.Kind = item.Kind
		emitSignatureAuditProjection(ctx, evts, p)
	}
	return deliveredCount, nil
}
