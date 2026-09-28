package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const managedRealtimeChannelRouteTargetChangeChannel = "managed_realtime_channel_route_targets_changed"

// WatchManagedRealtimeChannelRouteTargetChanges listens for committed route
// directory changes. It marks the listener unavailable on disconnect so
// callers can stop serving cached target sets until LISTEN is re-established.
func (s *PgStore) WatchManagedRealtimeChannelRouteTargetChanges(ctx context.Context) <-chan ManagedRealtimeChannelRouteTargetCacheEvent {
	events := make(chan ManagedRealtimeChannelRouteTargetCacheEvent, 32)
	emit := func(event ManagedRealtimeChannelRouteTargetCacheEvent) {
		select {
		case events <- event:
			return
		default:
		}
		// Scoped events may not be dropped: if the consumer falls behind,
		// collapse the backlog into one full invalidation instead.
		for {
			select {
			case <-events:
			default:
				event.EndpointID = ""
				event.Channel = ""
				event.InvalidateAll = true
				select {
				case events <- event:
				case <-ctx.Done():
				}
				return
			}
		}
	}
	go func() {
		defer close(events)
		if s == nil || s.pool == nil {
			emit(ManagedRealtimeChannelRouteTargetCacheEvent{Err: errors.New("state: pgstore has nil pool")})
			return
		}

		retryDelay := time.Second
		for ctx.Err() == nil {
			conn, err := s.pool.Acquire(ctx)
			if err == nil {
				_, err = conn.Exec(ctx, "listen "+managedRealtimeChannelRouteTargetChangeChannel)
			}
			if err != nil {
				if conn != nil {
					conn.Release()
				}
				emit(ManagedRealtimeChannelRouteTargetCacheEvent{Err: err})
				if !waitManagedRealtimeChannelRouteListenerRetry(ctx, retryDelay) {
					return
				}
				retryDelay = min(retryDelay*2, 30*time.Second)
				continue
			}

			retryDelay = time.Second
			emit(ManagedRealtimeChannelRouteTargetCacheEvent{Listening: true, InvalidateAll: true})
			for ctx.Err() == nil {
				notification, waitErr := conn.Conn().WaitForNotification(ctx)
				if waitErr != nil {
					err = waitErr
					break
				}
				if notification != nil && notification.Channel == managedRealtimeChannelRouteTargetChangeChannel {
					emit(managedRealtimeChannelRouteTargetCacheEvent(notification.Payload))
				}
			}
			conn.Release()
			if ctx.Err() != nil {
				return
			}
			emit(ManagedRealtimeChannelRouteTargetCacheEvent{InvalidateAll: true, Err: err})
			if !waitManagedRealtimeChannelRouteListenerRetry(ctx, retryDelay) {
				return
			}
			retryDelay = min(retryDelay*2, 30*time.Second)
		}
	}()
	return events
}

func managedRealtimeChannelRouteTargetCacheEvent(payload string) ManagedRealtimeChannelRouteTargetCacheEvent {
	event := ManagedRealtimeChannelRouteTargetCacheEvent{Listening: true}
	var scope struct {
		EndpointID string `json:"endpoint_id"`
		Channel    string `json:"channel"`
	}
	if err := json.Unmarshal([]byte(payload), &scope); err != nil || scope.EndpointID == "" {
		event.InvalidateAll = true
		return event
	}
	event.EndpointID = scope.EndpointID
	event.Channel = scope.Channel
	return event
}

func waitManagedRealtimeChannelRouteListenerRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
