package main

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
	"log/slog"
	"time"
)

func realtimeScheduledFailureCode(err error) string {
	var scheduledCondition *state.ManagedRealtimeScheduleConditionFailure
	var version *state.ManagedRealtimeEntityVersionConflict
	var condition *state.ManagedRealtimeConditionConflict
	var reducer *state.ManagedRealtimeReducerError
	var schema *state.ManagedRealtimeEventSchemaError
	switch {
	case errors.As(err, &scheduledCondition):
		return "realtime_schedule_condition_failed"
	case errors.As(err, &version):
		return api.CodeRealtimeEntityVersionConflict
	case errors.As(err, &condition):
		return api.CodeRealtimeConditionConflict
	case errors.As(err, &reducer), errors.As(err, &schema), errors.Is(err, state.ErrManagedRealtimeHistoryInvalid):
		return api.CodeRealtimeInvalid
	case errors.Is(err, state.ErrManagedRealtimeHistoryLimit):
		return api.CodeCapacity
	}
	return "realtime_schedule_unavailable"
}
func (s *server) runManagedRealtimeSchedules(ctx context.Context) {
	store, ok := s.store.(state.ManagedRealtimeScheduleStore)
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
		due, err := store.ListDueManagedRealtimeSchedules(passCtx, 128)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Warn("managed realtime schedule scan failed", "err", err)
			}
			return
		}
		for _, schedule := range due {
			if passCtx.Err() != nil {
				return
			}
			msg, err := store.PublishManagedRealtimeSchedule(passCtx, schedule)
			if errors.Is(err, state.ErrManagedRealtimeScheduleSkipped) || errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
				continue
			}
			if err != nil {
				if code := realtimeScheduledFailureCode(err); passCtx.Err() == nil {
					if failureErr := store.FailManagedRealtimeSchedule(passCtx, schedule, code); failureErr != nil {
						log.Warn("realtime schedule failure recording failed", "schedule_id", schedule.ID, "err", failureErr)
					}
				}
				log.Warn("managed realtime scheduled publish failed", "endpoint_id", schedule.EndpointID, "channel", schedule.Channel, "schedule_id", schedule.ID, "err", err)
				continue
			}
			// Publication and terminal status committed together; fanout failures do not
			// republish. Replay recovers events missed during outages or a worker crash.
			if s.realtimeOwner == nil {
				continue
			}
			payload := realtime.Message{Data: msg.Data, Binary: msg.Binary, Metadata: msg.Metadata}
			if publisher, ok := s.realtimeOwner.(realtimeRetainedPublishStatus); ok {
				result, publishErr := publisher.PublishRetainedWithStatus(passCtx, msg.EndpointID, msg.Channel, payload, msg.Sequence)
				if publishErr != nil || result.Partial || result.NodesUnavailable > 0 || result.QueueFull > 0 || result.Failed > 0 {
					log.Warn("scheduled realtime publish committed with incomplete fanout", "schedule_id", schedule.ID, "sequence", msg.Sequence, "err", publishErr)
				}
			} else if _, err := s.realtimeOwner.Publish(passCtx, msg.EndpointID, msg.Channel, payload); err != nil {
				log.Warn("scheduled realtime publish live fanout failed", "schedule_id", schedule.ID, "sequence", msg.Sequence, "err", err)
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
