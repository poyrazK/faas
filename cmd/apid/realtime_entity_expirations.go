package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) runManagedRealtimeEntityExpirations(ctx context.Context) {
	store, ok := s.store.(state.ManagedRealtimeEntityExpirationStore)
	if !ok || !s.realtimeHistoryPreviewEnabled {
		return
	}
	log := s.log
	if log == nil {
		log = slog.Default()
	}
	run := func() {
		passCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		candidates, err := store.ListManagedRealtimeEntityExpirations(passCtx, 128)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Warn("managed realtime entity expiration scan failed", "err", err)
			}
			return
		}
		for _, candidate := range candidates {
			if passCtx.Err() != nil {
				return
			}
			message, err := store.ExpireManagedRealtimeEntity(passCtx, candidate)
			if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
				continue
			}
			if err != nil {
				log.Warn("managed realtime entity expiration failed", "endpoint_id", candidate.EndpointID, "channel", candidate.Channel, "err", err)
				continue
			}
			// The retained commit is authoritative. Live fanout is best effort; replay
			// recovers a missed event after a crash or unavailable node.
			if s.realtimeOwner == nil {
				continue
			}
			payload := realtime.Message{Data: message.Data, Metadata: message.Metadata}
			if publisher, ok := s.realtimeOwner.(realtimeRetainedPublishStatus); ok {
				result, publishErr := publisher.PublishRetainedWithStatus(passCtx, message.EndpointID, message.Channel, payload, message.Sequence)
				if publishErr != nil || result.Partial || result.NodesUnavailable > 0 || result.QueueFull > 0 || result.Failed > 0 {
					log.Warn("realtime entity expiration committed with incomplete live fanout", "endpoint_id", message.EndpointID, "channel", message.Channel, "sequence", message.Sequence, "err", publishErr)
				}
			} else if _, err := s.realtimeOwner.Publish(passCtx, message.EndpointID, message.Channel, payload); err != nil {
				log.Warn("realtime entity expiration live fanout failed", "endpoint_id", message.EndpointID, "channel", message.Channel, "sequence", message.Sequence, "err", err)
			}
		}
	}
	run()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
