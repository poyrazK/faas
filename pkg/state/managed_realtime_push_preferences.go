package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

type ManagedRealtimePushPreferencesStore interface {
	GetManagedRealtimeNotificationPreferences(context.Context, string, string) (api.RealtimeNotificationPreferences, error)
	PutManagedRealtimeNotificationPreferences(context.Context, string, string, api.RealtimeNotificationPreferences) error
	PrepareManagedRealtimePush(context.Context, string, string) (bool, error)
}

func preferencesKey(ep, principal string) (managedRealtimeHistoryKey, error) {
	pk, err := managedRealtimeInboxKey(principal)
	if err != nil {
		return managedRealtimeHistoryKey{}, err
	}
	if validateManagedRealtimeHistoryRequest(ep, pk) != nil {
		return managedRealtimeHistoryKey{}, ErrManagedRealtimeHistoryInvalid
	}
	return managedRealtimeHistoryKey{endpointID: ep, channel: pk}, nil
}
func copyPreferences(p api.RealtimeNotificationPreferences) api.RealtimeNotificationPreferences {
	data, _ := json.Marshal(p)
	var out api.RealtimeNotificationPreferences
	_ = json.Unmarshal(data, &out)
	return out
}
func (m *MemStore) GetManagedRealtimeNotificationPreferences(ctx context.Context, ep, principal string) (api.RealtimeNotificationPreferences, error) {
	key, err := preferencesKey(ep, principal)
	if err != nil {
		return api.RealtimeNotificationPreferences{}, err
	}
	if err = ctx.Err(); err != nil {
		return api.RealtimeNotificationPreferences{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.managedRealtimeEndpoints[ep]; !ok {
		return api.RealtimeNotificationPreferences{}, ErrNotFound
	}
	p, exists := m.managedRealtimePushPreferences[key]
	if !exists {
		p = api.DefaultRealtimeNotificationPreferences()
	}
	return copyPreferences(p), nil
}
func (m *MemStore) PutManagedRealtimeNotificationPreferences(ctx context.Context, ep, principal string, p api.RealtimeNotificationPreferences) error {
	key, err := preferencesKey(ep, principal)
	if err != nil {
		return err
	}
	if p.Validate() != nil {
		return ErrManagedRealtimeHistoryInvalid
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.managedRealtimeEndpoints[ep]; !ok {
		return ErrNotFound
	}
	if _, exists := m.managedRealtimePushPreferences[key]; !exists {
		count := 0
		for k := range m.managedRealtimePushPreferences {
			if k.endpointID == ep {
				count++
			}
		}
		if count >= 256 {
			return ErrManagedRealtimeDurableCursorLimit
		}
	}
	m.managedRealtimePushPreferences[key] = copyPreferences(p)
	now := time.Now().UTC()
	for id, j := range m.managedRealtimePushDeliveries {
		if j.EndpointID == ep && j.Principal == key.channel && j.Status == "pending" && (j.Code == "quiet_hours" || j.Code == "digest_wait" || j.Code == "rate_limited" || j.Code == "scheduled") {
			j.NextAttempt = now
			if j.DigestID == "" {
				j.DigestAt = time.Time{}
			}
			m.setPushDeliveryLocked(id, j)
		}
	}
	return nil
}
func pushPreferenceDecision(j ManagedRealtimePushDelivery, p api.RealtimeNotificationPreferences, now time.Time) (ManagedRealtimePushDelivery, bool) {
	if !j.ExpiresAt.After(now) {
		j.Status = "cancelled"
		j.Code = "expired"
		j.Lease = ""
		j.LeaseUntil = time.Time{}
		j.UpdatedAt = now
		return j, false
	}
	p = pushPriorityPreferences(p, j)
	next, allowed := p.NextPushTime(now, j.Category, j.Device)
	ready, _ := digestSchedule(p, j)
	reason := "quiet_hours"
	if j.NotBefore.After(next) {
		next = j.NotBefore
		reason = "scheduled"
	}
	if ready.After(next) {
		next = ready
		reason = "digest_wait"
		if j.NotBefore.After(now) && ready.Equal(j.NotBefore) {
			reason = "scheduled"
		}
	}
	if allowed {
		if end, ok := p.NextPushTime(next, j.Category, j.Device); ok && end.After(next) {
			next = end
			reason = "quiet_hours"
		}
	}
	if j.DigestID == "" {
		j.DigestAt = ready
	}
	if allowed && !next.After(now) {
		return j, true
	}
	j.Attempts--
	j.Lease = ""
	j.LeaseUntil = time.Time{}
	j.UpdatedAt = now
	if !allowed {
		j.Status = "cancelled"
		j.Code = "preferences_muted"
		return j, false
	}
	limit := j.CreatedAt.Add(72 * time.Hour)
	if !next.Before(limit) {
		j.Status = "cancelled"
		j.Code = "quiet_hours_expired"
		return j, false
	}
	j.Status = "pending"
	j.Code = reason
	j.NextAttempt = next
	expiry := next.Add(time.Hour)
	if expiry.After(limit) {
		expiry = limit
	}
	if !j.HardExpiresAt.IsZero() && !next.Before(j.HardExpiresAt) {
		j.Status = "cancelled"
		j.Code = "expired"
		return j, false
	}
	if j.HardExpiresAt.IsZero() && expiry.After(j.ExpiresAt) {
		j.ExpiresAt = expiry
	}
	return j, false
}
func (m *MemStore) PrepareManagedRealtimePush(ctx context.Context, id, lease string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.managedRealtimePushDeliveries[id]
	if j.Status != "sending" || j.Lease != lease || !j.LeaseUntil.After(time.Now()) {
		return false, nil
	}
	p, exists := m.managedRealtimePushPreferences[managedRealtimeHistoryKey{endpointID: j.EndpointID, channel: j.Principal}]
	if !exists {
		p = api.DefaultRealtimeNotificationPreferences()
	}
	updated, send := pushPreferenceDecision(j, p, time.Now().UTC())
	if !send {
		m.setPushDeliveryLocked(id, updated)
	}
	return send, nil
}
func (s *PgStore) GetManagedRealtimeNotificationPreferences(ctx context.Context, ep, principal string) (api.RealtimeNotificationPreferences, error) {
	key, err := preferencesKey(ep, principal)
	if err != nil {
		return api.RealtimeNotificationPreferences{}, err
	}
	p := api.DefaultRealtimeNotificationPreferences()
	var raw []byte
	err = s.pool.QueryRow(ctx, `select coalesce((select preferences from managed_realtime_push_preferences where endpoint_id=$1 and principal=$2),'null'::jsonb) from managed_realtime_endpoints where id=$1`, ep, key.channel).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	if string(raw) != "null" {
		err = json.Unmarshal(raw, &p)
	}
	return p, err
}
func (s *PgStore) PutManagedRealtimeNotificationPreferences(ctx context.Context, ep, principal string, p api.RealtimeNotificationPreferences) error {
	key, err := preferencesKey(ep, principal)
	if err != nil {
		return err
	}
	if p.Validate() != nil {
		return ErrManagedRealtimeHistoryInvalid
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked string
	err = tx.QueryRow(ctx, `select id from managed_realtime_endpoints where id=$1 for update`, ep).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var exists bool
	var count int
	err = tx.QueryRow(ctx, `select exists(select 1 from managed_realtime_push_preferences where endpoint_id=$1 and principal=$2),(select count(*) from managed_realtime_push_preferences where endpoint_id=$1)`, ep, key.channel).Scan(&exists, &count)
	if err != nil {
		return err
	}
	if !exists && count >= 256 {
		return ErrManagedRealtimeDurableCursorLimit
	}
	_, err = tx.Exec(ctx, `insert into managed_realtime_push_preferences(endpoint_id,principal,preferences) values($1,$2,$3) on conflict(endpoint_id,principal) do update set preferences=excluded.preferences,updated_at=clock_timestamp()`, ep, key.channel, raw)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `update managed_realtime_push_deliveries set next_attempt=clock_timestamp(),digest_at=case when digest_id is null then 'epoch'::timestamptz else digest_at end where endpoint_id=$1 and principal=$2 and status='pending' and code in ('quiet_hours','digest_wait','rate_limited','scheduled')`, ep, key.channel)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *PgStore) PrepareManagedRealtimePush(ctx context.Context, id, lease string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	j, err := scanPush(tx.QueryRow(ctx, `select `+pushColumns+` from managed_realtime_push_deliveries where id=$1 and lease=$2 and status='sending' and lease_until>clock_timestamp() for update`, id, lease))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	p := api.DefaultRealtimeNotificationPreferences()
	var raw []byte
	err = tx.QueryRow(ctx, `select preferences from managed_realtime_push_preferences where endpoint_id=$1 and principal=$2`, j.EndpointID, j.Principal).Scan(&raw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if err == nil {
		if err = json.Unmarshal(raw, &p); err != nil {
			return false, err
		}
	}
	updated, send := pushPreferenceDecision(j, p, time.Now().UTC())
	if !send {
		_, err = tx.Exec(ctx, `update managed_realtime_push_deliveries set status=$3,code=$4,next_attempt=$5,attempts=$6,lease=null,lease_until=null,updated_at=$7,expires_at=$8,digest_at=$9 where id=$1 and lease=$2 and status='sending'`, id, lease, updated.Status, updated.Code, updated.NextAttempt, updated.Attempts, updated.UpdatedAt, updated.ExpiresAt, updated.DigestAt)
		if err != nil {
			return false, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return send, nil
}

var _ ManagedRealtimePushPreferencesStore = (*MemStore)(nil)
var _ ManagedRealtimePushPreferencesStore = (*PgStore)(nil)
