package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// managedRealtimeAccessChangeChannel carries one notification per committed
// realtime access change: a principal's access write or a ticket revocation.
// apid relays them to realtime nodes, which cache access decisions instead of
// reading them for every connection every second (ADR-1113, amendment of
// 2026-11-02).
const managedRealtimeAccessChangeChannel = "managed_realtime_access_changed"

// ManagedRealtimeAccessChange names the principal of an endpoint whose access
// or tickets changed.
type ManagedRealtimeAccessChange struct {
	EndpointID string `json:"endpoint_id"`
	Principal  string `json:"principal"`
}

// ManagedRealtimeAccessChanges is one page of apid's access change feed.
// Epoch changes whenever apid may have missed a change (its listener
// reconnected or fell behind); a reader that sees a new epoch, or Reset,
// drops every cached decision. Seq is the position to ask after next.
type ManagedRealtimeAccessChanges struct {
	Epoch   string                        `json:"epoch"`
	Seq     int64                         `json:"seq"`
	Reset   bool                          `json:"reset,omitempty"`
	More    bool                          `json:"more,omitempty"`
	Changes []ManagedRealtimeAccessChange `json:"changes,omitempty"`
}

// ManagedRealtimeAccessChangeEvent is one listener event: readiness, a change
// to apply, or an instruction to drop everything.
type ManagedRealtimeAccessChangeEvent struct {
	Listening     bool
	InvalidateAll bool
	Change        ManagedRealtimeAccessChange
	Err           error
}

// ManagedRealtimeAccessChangeListener streams committed access changes.
type ManagedRealtimeAccessChangeListener interface {
	WatchManagedRealtimeAccessChanges(context.Context) <-chan ManagedRealtimeAccessChangeEvent
}

// ValidRealtimeTicketToken reports whether token has the shape of an issued
// subscription ticket, so a realtime node can refuse a malformed one without
// asking the control plane.
func ValidRealtimeTicketToken(token string) bool {
	_, err := ticketHash(token)
	return err == nil
}

// notifyRealtimeAccessChangedPG queues the change notification inside tx;
// PostgreSQL delivers it only if tx commits.
func notifyRealtimeAccessChangedPG(ctx context.Context, tx pgx.Tx, endpointID, principal string) error {
	payload, err := json.Marshal(ManagedRealtimeAccessChange{EndpointID: endpointID, Principal: principal})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `SELECT pg_notify($1, $2)`, managedRealtimeAccessChangeChannel, string(payload))
	return err
}

// WatchManagedRealtimeAccessChanges listens for committed access changes. A
// (re)established LISTEN and a consumer that fell behind both emit
// InvalidateAll, because changes may have been missed.
func (s *PgStore) WatchManagedRealtimeAccessChanges(ctx context.Context) <-chan ManagedRealtimeAccessChangeEvent {
	events := make(chan ManagedRealtimeAccessChangeEvent, 256)
	emit := func(event ManagedRealtimeAccessChangeEvent) {
		select {
		case events <- event:
			return
		default:
		}
		for {
			select {
			case <-events:
			default:
				event.Change = ManagedRealtimeAccessChange{}
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
			emit(ManagedRealtimeAccessChangeEvent{Err: errors.New("state: pgstore has nil pool")})
			return
		}
		retryDelay := time.Second
		for ctx.Err() == nil {
			conn, err := s.pool.Acquire(ctx)
			if err == nil {
				_, err = conn.Exec(ctx, "listen "+managedRealtimeAccessChangeChannel)
			}
			if err == nil {
				retryDelay = time.Second
				emit(ManagedRealtimeAccessChangeEvent{Listening: true, InvalidateAll: true})
				for ctx.Err() == nil {
					notification, waitErr := conn.Conn().WaitForNotification(ctx)
					if waitErr != nil {
						err = waitErr
						break
					}
					if notification == nil || notification.Channel != managedRealtimeAccessChangeChannel {
						continue
					}
					var change ManagedRealtimeAccessChange
					if json.Unmarshal([]byte(notification.Payload), &change) != nil || change.EndpointID == "" || change.Principal == "" {
						emit(ManagedRealtimeAccessChangeEvent{Listening: true, InvalidateAll: true})
						continue
					}
					emit(ManagedRealtimeAccessChangeEvent{Listening: true, Change: change})
				}
			}
			if conn != nil {
				conn.Release()
			}
			if ctx.Err() != nil {
				return
			}
			emit(ManagedRealtimeAccessChangeEvent{InvalidateAll: true, Err: err})
			if !waitManagedRealtimeChannelRouteListenerRetry(ctx, retryDelay) {
				return
			}
			retryDelay = min(retryDelay*2, 30*time.Second)
		}
	}()
	return events
}
