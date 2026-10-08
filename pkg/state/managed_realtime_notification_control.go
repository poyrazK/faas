package state

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

type ManagedRealtimeNotificationControlResult struct {
	Fallbacks  int `json:"fallbacks"`
	Deliveries int `json:"deliveries"`
}
type ManagedRealtimeNotificationControlStore interface {
	ControlManagedRealtimeNotification(context.Context, string, string, string, *time.Time) (ManagedRealtimeNotificationControlResult, error)
}

// A nil schedule cancels. Only active work is changed; terminal rows never revive.
func reschedulePush(j ManagedRealtimePushDelivery, at, now time.Time) (ManagedRealtimePushDelivery, error) {
	if !at.Before(j.CreatedAt.Add(72*time.Hour)) || !j.HardExpiresAt.IsZero() && !at.Before(j.HardExpiresAt) {
		return j, ErrManagedRealtimeHistoryInvalid
	}
	j.NotBefore = at
	j.NextAttempt = now
	j.Code = "rescheduled"
	if at.After(now) {
		j.NextAttempt = at
		j.Code = "scheduled"
	}
	j.Status = "pending"
	j.Lease = ""
	j.LeaseUntil = time.Time{}
	j.DigestID = ""
	j.DigestCount = 0
	j.DigestAt = time.Time{}
	j.UpdatedAt = now
	if j.HardExpiresAt.IsZero() {
		end := at.Add(time.Hour)
		limit := j.CreatedAt.Add(72 * time.Hour)
		if end.After(limit) {
			end = limit
		}
		if end.After(j.ExpiresAt) {
			j.ExpiresAt = end
		}
	}
	return j, nil
}
func (m *MemStore) ControlManagedRealtimeNotification(ctx context.Context, ep, principal, messageID string, at *time.Time) (ManagedRealtimeNotificationControlResult, error) {
	var result ManagedRealtimeNotificationControlResult
	if at != nil {
		if _, e := api.ParseRealtimeNotificationNotBefore(at.Format(time.RFC3339Nano), time.Now().UTC()); e != nil {
			return result, ErrManagedRealtimeHistoryInvalid
		}
	}
	pk, err := notificationKey(principal, messageID, 1)
	if err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.managedRealtimeEndpoints[ep]; !ok {
		return result, ErrNotFound
	}
	now := time.Now().UTC()
	if at != nil {
		if _, e := api.ParseRealtimeNotificationNotBefore(at.Format(time.RFC3339Nano), now); e != nil {
			return result, ErrManagedRealtimeHistoryInvalid
		}
	}
	updates := map[string]ManagedRealtimePushDelivery{}
	for id, j := range m.managedRealtimePushDeliveries {
		if j.EndpointID != ep || j.Principal != pk || j.MessageID != messageID || (j.Status != "pending" && j.Status != "sending") || at != nil && !j.ExpiresAt.After(now) {
			continue
		}
		if at != nil {
			updated, e := reschedulePush(j, *at, now)
			if e != nil {
				return result, e
			}
			updates[id] = updated
		}
	}
	for key, f := range m.managedRealtimeInboxFallbacks {
		if key.endpointID != ep || key.principal != pk || f.messageID != messageID {
			continue
		}
		if at != nil && f.ttlSeconds > 0 && (!f.expiresAt.After(now) || !at.Before(f.expiresAt)) {
			return result, ErrManagedRealtimeHistoryInvalid
		}
	}
	for key, f := range m.managedRealtimeInboxFallbacks {
		if key.endpointID != ep || key.principal != pk || f.messageID != messageID {
			continue
		}
		if at == nil {
			m.deleteNotificationFallbackLocked(key)
		} else {
			f.notBefore = *at
			m.setNotificationFallbackLocked(key, f)
		}
		result.Fallbacks++
	}
	for id, j := range m.managedRealtimePushDeliveries {
		if j.EndpointID != ep || j.Principal != pk || j.MessageID != messageID || (j.Status != "pending" && j.Status != "sending") || at != nil && !j.ExpiresAt.After(now) {
			continue
		}
		if at == nil {
			m.cancelPushLocked(id, j, "cancelled_by_publisher")
		} else {
			m.setPushDeliveryLocked(id, updates[id])
		}
		result.Deliveries++
	}
	return result, nil
}
func (s *PgStore) ControlManagedRealtimeNotification(ctx context.Context, ep, principal, messageID string, at *time.Time) (ManagedRealtimeNotificationControlResult, error) {
	var result ManagedRealtimeNotificationControlResult
	if at != nil {
		if _, e := api.ParseRealtimeNotificationNotBefore(at.Format(time.RFC3339Nano), time.Now().UTC()); e != nil {
			return result, ErrManagedRealtimeHistoryInvalid
		}
	}
	pk, err := notificationKey(principal, messageID, 1)
	if err != nil {
		return result, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Endpoint -> inbox -> fallback -> jobs matches append/ACK ordering.
	var exists bool
	if err = tx.QueryRow(ctx, `select enabled from managed_realtime_endpoints where id=$1 for update`, ep).Scan(&exists); err != nil {
		if err == pgx.ErrNoRows {
			err = ErrNotFound
		}
		return result, err
	}
	_, err = tx.Exec(ctx, `select sequence from managed_realtime_inbox_messages where endpoint_id=$1 and channel=$2 and idempotency_key=$3 for update`, ep, pk, messageID)
	if err != nil {
		return result, err
	}
	var deadline *time.Time
	err = tx.QueryRow(ctx, `select expires_at from managed_realtime_inbox_fallbacks where endpoint_id=$1 and principal=$2 and message_id=$3 for update`, ep, pk, messageID).Scan(&deadline)
	if err != nil && err != pgx.ErrNoRows {
		return result, err
	}
	if at != nil && deadline != nil && (!deadline.After(time.Now().UTC()) || !at.Before(*deadline)) {
		return result, ErrManagedRealtimeHistoryInvalid
	}
	members, err := readPushRowsPG(ctx, tx, `select `+pushColumns+` from managed_realtime_push_deliveries where endpoint_id=$1 and principal=$2 and message_id=$3 and status in ('pending','sending') and ($4::boolean or expires_at>clock_timestamp()) order by id for update`, ep, pk, messageID, at == nil)
	if err != nil {
		return result, err
	}
	now := time.Now().UTC()
	for _, j := range members {
		if at == nil {
			_, err = tx.Exec(ctx, `update managed_realtime_push_deliveries set status='cancelled',code='cancelled_by_publisher',lease=null,lease_until=null,updated_at=$2 where id=$1`, j.ID, now)
		} else {
			updated, e := reschedulePush(j, *at, now)
			if e != nil {
				return result, e
			}
			_, err = tx.Exec(ctx, `update managed_realtime_push_deliveries set status='pending',code=$2,not_before=$3,next_attempt=$4,expires_at=$5,lease=null,lease_until=null,digest_id=null,digest_count=0,digest_at='epoch',updated_at=$6 where id=$1`, j.ID, updated.Code, updated.NotBefore, updated.NextAttempt, updated.ExpiresAt, now)
		}
		if err != nil {
			return result, err
		}
		result.Deliveries++
	}
	if at == nil {
		tag, e := tx.Exec(ctx, `delete from managed_realtime_inbox_fallbacks where endpoint_id=$1 and principal=$2 and message_id=$3`, ep, pk, messageID)
		err = e
		result.Fallbacks = int(tag.RowsAffected())
	} else {
		tag, e := tx.Exec(ctx, `update managed_realtime_inbox_fallbacks set not_before=$4 where endpoint_id=$1 and principal=$2 and message_id=$3`, ep, pk, messageID, *at)
		err = e
		result.Fallbacks = int(tag.RowsAffected())
	}
	if err != nil {
		return result, err
	}
	err = tx.Commit(ctx)
	return result, err
}
