package state

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

func rateLimitApplies(p api.RealtimeNotificationPreferences, j ManagedRealtimePushDelivery) bool {
	return p.RateLimit != nil && !(j.Priority == "urgent" && p.RateLimit.AllowUrgentBypass)
}
func pushRateKey(j ManagedRealtimePushDelivery) string {
	raw, _ := json.Marshal([]string{j.EndpointID, j.Principal})
	return string(raw)
}
func deferPushRate(j ManagedRealtimePushDelivery, next, now time.Time) ManagedRealtimePushDelivery {
	j.Status = "pending"
	j.Code = "rate_limited"
	j.NextAttempt = next
	j.Lease = ""
	j.LeaseUntil = time.Time{}
	j.UpdatedAt = now
	if j.Attempts > 0 {
		j.Attempts--
	}
	if !j.ExpiresAt.After(next) {
		j.Status = "cancelled"
		j.Code = "expired"
	}
	return j
}

// Reservations count logical provider deliveries, once per digest per UTC window.
// Reserving before HTTP prevents concurrent workers from exceeding the quota.
func (m *MemStore) reservePushRateLocked(p api.RealtimeNotificationPreferences, j ManagedRealtimePushDelivery, now time.Time) time.Time {
	if !rateLimitApplies(p, j) {
		return now
	}
	interval := time.Duration(p.RateLimit.WindowSeconds) * time.Second
	start := now.Truncate(interval)
	if m.realtimePushRateReservations == nil {
		m.realtimePushRateReservations = map[string]map[string]time.Time{}
	}
	for key, entries := range m.realtimePushRateReservations {
		for id, at := range entries {
			if !at.After(now.Add(-2 * time.Hour)) {
				delete(entries, id)
			}
		}
		if len(entries) == 0 {
			delete(m.realtimePushRateReservations, key)
		}
	}
	key := pushRateKey(j)
	entries := m.realtimePushRateReservations[key]
	if entries == nil {
		entries = map[string]time.Time{}
		m.realtimePushRateReservations[key] = entries
	}
	if at, ok := entries[j.DigestID]; ok && !at.Before(start) {
		return now
	}
	count := 0
	for _, at := range entries {
		if !at.Before(start) {
			count++
		}
	}
	if count >= p.RateLimit.MaxNotifications {
		return start.Add(interval)
	}
	entries[j.DigestID] = now
	return now
}
func reservePushRatePG(ctx context.Context, tx pgx.Tx, p api.RealtimeNotificationPreferences, j ManagedRealtimePushDelivery, now time.Time) (time.Time, error) {
	if !rateLimitApplies(p, j) {
		return now, nil
	}
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1,0))`, "realtime-rate:"+pushRateKey(j)); err != nil {
		return time.Time{}, err
	}
	now = time.Now().UTC()
	interval := time.Duration(p.RateLimit.WindowSeconds) * time.Second
	start := now.Truncate(interval)
	if _, err := tx.Exec(ctx, `delete from managed_realtime_push_rate_reservations where endpoint_id=$1 and principal=$2 and reserved_at<$3`, j.EndpointID, j.Principal, now.Add(-2*time.Hour)); err != nil {
		return time.Time{}, err
	}
	var reserved bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from managed_realtime_push_rate_reservations where endpoint_id=$1 and principal=$2 and digest_id=$3 and reserved_at>=$4)`, j.EndpointID, j.Principal, j.DigestID, start).Scan(&reserved); err != nil {
		return time.Time{}, err
	}
	if reserved {
		return now, nil
	}
	var count int
	if err := tx.QueryRow(ctx, `select count(*) from managed_realtime_push_rate_reservations where endpoint_id=$1 and principal=$2 and reserved_at>=$3`, j.EndpointID, j.Principal, start).Scan(&count); err != nil {
		return time.Time{}, err
	}
	if count >= p.RateLimit.MaxNotifications {
		return start.Add(interval), nil
	}
	_, err := tx.Exec(ctx, `insert into managed_realtime_push_rate_reservations(endpoint_id,principal,digest_id,reserved_at) values($1,$2,$3,$4) on conflict(endpoint_id,principal,digest_id) do update set reserved_at=excluded.reserved_at`, j.EndpointID, j.Principal, j.DigestID, now)
	return now, err
}
