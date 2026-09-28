package state

import (
	"context"
	"errors"
	"time"
)

const managedRealtimeChannelRouteTargetChangeChannel = "managed_realtime_channel_route_targets_changed"

// WatchManagedRealtimeChannelRouteTargetChanges listens for committed route
// directory changes. It marks the listener unavailable on disconnect so
// callers can stop serving cached target sets until LISTEN is re-established.
func (s *PgStore) WatchManagedRealtimeChannelRouteTargetChanges(ctx context.Context) <-chan ManagedRealtimeChannelRouteTargetCacheEvent {
	events := make(chan ManagedRealtimeChannelRouteTargetCacheEvent, 1)
	emit := func(event ManagedRealtimeChannelRouteTargetCacheEvent) {
		select {
		case events <- event:
		default:
			select {
			case <-events:
			default:
			}
			select {
			case events <- event:
			case <-ctx.Done():
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
			emit(ManagedRealtimeChannelRouteTargetCacheEvent{Listening: true})
			for ctx.Err() == nil {
				notification, waitErr := conn.Conn().WaitForNotification(ctx)
				if waitErr != nil {
					err = waitErr
					break
				}
				if notification != nil && notification.Channel == managedRealtimeChannelRouteTargetChangeChannel {
					emit(ManagedRealtimeChannelRouteTargetCacheEvent{Listening: true})
				}
			}
			conn.Release()
			if ctx.Err() != nil {
				return
			}
			emit(ManagedRealtimeChannelRouteTargetCacheEvent{Err: err})
			if !waitManagedRealtimeChannelRouteListenerRetry(ctx, retryDelay) {
				return
			}
			retryDelay = min(retryDelay*2, 30*time.Second)
		}
	}()
	return events
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
