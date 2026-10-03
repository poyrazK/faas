package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

const (
	orgActivityOutboxConsumer      = "apid"
	orgActivityOutboxLease         = 30 * time.Second
	orgActivityOutboxPollInterval  = 2 * time.Second
	orgActivityOutboxBatchSize     = 32
	orgActivityOutboxPruneInterval = 24 * time.Hour
	orgActivityOutboxRetention     = 90 * 24 * time.Hour
)

func runOrgActivityOutbox(ctx context.Context, store state.Store, log *slog.Logger) error {
	outbox, ok := store.(state.OrgActivityOutboxStore)
	if !ok {
		return nil
	}
	ticker := time.NewTicker(orgActivityOutboxPollInterval)
	defer ticker.Stop()
	lastPrune := time.Time{}
	for {
		if _, err := drainOrgActivityOutboxOnce(ctx, outbox, log); err != nil && ctx.Err() == nil {
			log.Warn("activity: durable outbox drain failed", "err", err)
		}
		now := time.Now().UTC()
		if lastPrune.IsZero() || now.Sub(lastPrune) >= orgActivityOutboxPruneInterval {
			if _, err := outbox.PruneOrgActivityOutbox(ctx, now.Add(-orgActivityOutboxRetention)); err != nil && ctx.Err() == nil {
				log.Warn("activity: durable outbox prune failed", "err", err)
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

func drainOrgActivityOutboxOnce(ctx context.Context, outbox state.OrgActivityOutboxStore, log *slog.Logger) (int, error) {
	deliveredCount := 0
	for i := 0; i < orgActivityOutboxBatchSize; i++ {
		item, err := outbox.ClaimOrgActivityOutbox(ctx, orgActivityOutboxConsumer, orgActivityOutboxLease)
		if errors.Is(err, state.ErrNotFound) {
			return deliveredCount, nil
		}
		if err != nil {
			return deliveredCount, fmt.Errorf("claim: %w", err)
		}
		delivered, err := outbox.DeliverOrgActivityOutbox(ctx, item.ID)
		if err != nil {
			if failErr := outbox.FailOrgActivityOutbox(ctx, item.ID, err); failErr != nil && !errors.Is(failErr, state.ErrNotFound) {
				log.Warn("activity: durable outbox failure state update failed", "id", item.ID, "err", failErr)
			}
			log.Warn("activity: durable outbox delivery failed", "id", item.ID, "err", err)
			continue
		}
		if delivered {
			deliveredCount++
		}
	}
	return deliveredCount, nil
}

func (s *server) deliverOrgActivityOutbox(ctx context.Context, id int64) {
	outbox, ok := s.store.(state.OrgActivityOutboxStore)
	if !ok {
		return
	}
	if _, err := outbox.DeliverOrgActivityOutbox(ctx, id); err != nil {
		if failErr := outbox.FailOrgActivityOutbox(ctx, id, err); failErr != nil && !errors.Is(failErr, state.ErrNotFound) && s.log != nil {
			s.log.Warn("activity: durable outbox failure state update failed", "id", id, "err", failErr)
		}
		if s.log != nil {
			s.log.Warn("activity: durable outbox immediate delivery failed", "id", id, "err", err)
		}
	}
}
