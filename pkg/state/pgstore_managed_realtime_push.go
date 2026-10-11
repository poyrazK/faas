package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *PgStore) PutManagedRealtimePushProvider(ctx context.Context, p ManagedRealtimePushProvider) error {
	if !validPushProvider(p.Provider) || len(p.Sealed) == 0 || len(p.Sealed) > 32768 {
		return ErrManagedRealtimeHistoryInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `insert into managed_realtime_push_providers(endpoint_id,provider,enabled,sealed) select id,$2,$3,$4 from managed_realtime_endpoints where id=$1 on conflict(endpoint_id,provider) do update set enabled=excluded.enabled,sealed=excluded.sealed,updated_at=clock_timestamp()`, p.EndpointID, p.Provider, p.Enabled, p.Sealed)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if !p.Enabled {
		_, err = tx.Exec(ctx, `update managed_realtime_push_deliveries set status='cancelled',code='provider_disabled',lease=null,lease_until=null,updated_at=clock_timestamp() where endpoint_id=$1 and provider=$2 and status in ('pending','sending')`, p.EndpointID, p.Provider)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s *PgStore) ListManagedRealtimePushProviders(ctx context.Context, ep string) ([]ManagedRealtimePushProvider, error) {
	rows, err := s.pool.Query(ctx, `select provider,enabled,sealed,updated_at from managed_realtime_push_providers where endpoint_id=$1 order by provider`, ep)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ManagedRealtimePushProvider{}
	for rows.Next() {
		p := ManagedRealtimePushProvider{EndpointID: ep}
		if err = rows.Scan(&p.Provider, &p.Enabled, &p.Sealed, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *PgStore) PutManagedRealtimePushDevice(ctx context.Context, d ManagedRealtimePushDevice) error {
	key, err := pushDeviceKey(d.EndpointID, d.Principal, d.Device)
	if err != nil {
		return err
	}
	if !validPushProvider(d.Provider) || len(d.Sealed) == 0 || len(d.Sealed) > 16384 || !validPushFingerprint(d.Fingerprint) {
		return ErrManagedRealtimeHistoryInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var ep string
	err = tx.QueryRow(ctx, `select id from managed_realtime_endpoints where id=$1 and enabled for update`, d.EndpointID).Scan(&ep)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var available, exists bool
	var principalCount, total int
	err = tx.QueryRow(ctx, `select exists(select 1 from managed_realtime_push_providers where endpoint_id=$1 and provider=$3 and enabled),exists(select 1 from managed_realtime_push_devices where endpoint_id=$1 and principal=$2 and device=$4),(select count(*) from managed_realtime_push_devices where endpoint_id=$1 and principal=$2),(select count(*) from managed_realtime_push_devices where endpoint_id=$1)`, d.EndpointID, key.principal, d.Provider, d.Device).Scan(&available, &exists, &principalCount, &total)
	if err != nil {
		return err
	}
	if !available {
		return ErrManagedRealtimeFallbackSubscription
	}
	if !exists && (principalCount >= 16 || total >= 1024) {
		return ErrManagedRealtimeDurableCursorLimit
	}
	_, err = tx.Exec(ctx, `insert into managed_realtime_push_devices(endpoint_id,principal,device,provider,enabled,sealed,fingerprint) values($1,$2,$3,$4,true,$5,$6) on conflict(endpoint_id,principal,device) do update set provider=excluded.provider,enabled=true,sealed=excluded.sealed,version=case when managed_realtime_push_devices.enabled and managed_realtime_push_devices.provider=excluded.provider and managed_realtime_push_devices.fingerprint=excluded.fingerprint then managed_realtime_push_devices.version else nextval('managed_realtime_push_device_version') end,fingerprint=excluded.fingerprint,updated_at=clock_timestamp()`, d.EndpointID, key.principal, d.Device, d.Provider, d.Sealed, d.Fingerprint)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `update managed_realtime_push_deliveries set status='cancelled',code='device_rotated',lease=null,lease_until=null,updated_at=clock_timestamp() where endpoint_id=$1 and principal=$2 and device=$3 and version<>(select version from managed_realtime_push_devices where endpoint_id=$1 and principal=$2 and device=$3) and status in ('pending','sending')`, d.EndpointID, key.principal, d.Device)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *PgStore) ListManagedRealtimePushDevices(ctx context.Context, ep, principal string) ([]ManagedRealtimePushDevice, error) {
	pk, err := managedRealtimeInboxKey(principal)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `select device,provider,enabled,version,updated_at from managed_realtime_push_devices where endpoint_id=$1 and principal=$2 order by device`, ep, pk)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ManagedRealtimePushDevice{}
	for rows.Next() {
		d := ManagedRealtimePushDevice{EndpointID: ep, Principal: pk}
		if err = rows.Scan(&d.Device, &d.Provider, &d.Enabled, &d.Version, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *PgStore) DeleteManagedRealtimePushDevice(ctx context.Context, ep, principal, device string) error {
	key, err := pushDeviceKey(ep, principal, device)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `delete from managed_realtime_push_devices where endpoint_id=$1 and principal=$2 and device=$3`, ep, key.principal, device)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `update managed_realtime_push_deliveries set status='cancelled',code='device_removed',lease=null,lease_until=null,updated_at=clock_timestamp() where endpoint_id=$1 and principal=$2 and device=$3 and status in ('pending','sending')`, ep, key.principal, device)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

const pushColumns = `id,endpoint_id,principal,device,provider,version,message_id,sequence,status,attempts,status_code,code,created_at,updated_at,next_attempt,coalesce(lease::text,''),coalesce(lease_until,'epoch'::timestamptz),category,expires_at,group_key,group_label,coalesce(digest_id::text,''),digest_count,digest_at,priority,coalesce(hard_expires_at,'epoch'::timestamptz),collapse_key,not_before`

func scanPush(row pgx.Row) (ManagedRealtimePushDelivery, error) {
	var j ManagedRealtimePushDelivery
	err := row.Scan(&j.ID, &j.EndpointID, &j.Principal, &j.Device, &j.Provider, &j.Version, &j.MessageID, &j.Sequence, &j.Status, &j.Attempts, &j.StatusCode, &j.Code, &j.CreatedAt, &j.UpdatedAt, &j.NextAttempt, &j.Lease, &j.LeaseUntil, &j.Category, &j.ExpiresAt, &j.GroupKey, &j.GroupLabel, &j.DigestID, &j.DigestCount, &j.DigestAt, &j.Priority, &j.HardExpiresAt, &j.CollapseKey, &j.NotBefore)
	if j.HardExpiresAt.Equal(time.Unix(0, 0)) {
		j.HardExpiresAt = time.Time{}
	}
	return j, err
}
func (s *PgStore) ListManagedRealtimePushDeliveries(ctx context.Context, ep, principal string) ([]ManagedRealtimePushDelivery, error) {
	pk, err := managedRealtimeInboxKey(principal)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `select `+pushColumns+` from managed_realtime_push_deliveries where endpoint_id=$1 and principal=$2 order by created_at desc,id limit 100`, ep, pk)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ManagedRealtimePushDelivery{}
	for rows.Next() {
		j, e := scanPush(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
func (s *PgStore) ClaimManagedRealtimePush(ctx context.Context, batch int) ([]ManagedRealtimePushDelivery, error) {
	if batch < 1 || batch > 128 {
		return nil, ErrManagedRealtimeHistoryInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `delete from managed_realtime_notification_timeline where occurred_at<clock_timestamp()-interval '7 days'`)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `delete from managed_realtime_push_rate_reservations where reserved_at<clock_timestamp()-interval '2 hours'`)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `delete from managed_realtime_push_deliveries where status in ('sent','failed','cancelled') and updated_at<clock_timestamp()-interval '7 days'`)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `update managed_realtime_push_deliveries j set status='cancelled',code=case when expires_at<=clock_timestamp() then 'expired' else 'disabled' end,lease=null,lease_until=null,updated_at=clock_timestamp() where status in ('pending','sending') and (expires_at<=clock_timestamp() or not exists(select 1 from managed_realtime_push_devices d join managed_realtime_push_providers p using(endpoint_id,provider) join managed_realtime_endpoints e on e.id=d.endpoint_id where d.endpoint_id=j.endpoint_id and d.principal=j.principal and d.device=j.device and d.version=j.version and d.provider=j.provider and d.enabled and p.enabled and e.enabled))`)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `update managed_realtime_push_deliveries set status='failed',code='attempts_exhausted',lease=null,lease_until=null,updated_at=clock_timestamp() where attempts>=7 and (status='pending' or (status='sending' and lease_until<=clock_timestamp()))`)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `with due as (select j.id from managed_realtime_push_deliveries j where ((j.status='pending' and j.next_attempt<=clock_timestamp()) or (j.status='sending' and j.lease_until<=clock_timestamp())) and (j.digest_id is null or not exists(select 1 from managed_realtime_push_deliveries peer where peer.digest_id=j.digest_id and peer.status in ('pending','sending') and peer.id<j.id)) order by case j.priority when 'urgent' then 0 when 'low' then 2 else 1 end,j.next_attempt,j.id limit $1 for update skip locked) update managed_realtime_push_deliveries j set status='sending',attempts=attempts+1,lease=gen_random_uuid(),lease_until=clock_timestamp()+interval '60 seconds',updated_at=clock_timestamp() from due where j.id=due.id returning `+prefixedPushColumns("j"), batch)
	if err != nil {
		return nil, err
	}
	out := []ManagedRealtimePushDelivery{}
	for rows.Next() {
		j, e := scanPush(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		out = append(out, j)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		j := &out[i]
		err = tx.QueryRow(ctx, `select p.sealed,d.sealed from managed_realtime_push_devices d join managed_realtime_push_providers p using(endpoint_id,provider) where d.endpoint_id=$1 and d.principal=$2 and d.device=$3 and d.version=$4 and d.provider=$5 and d.enabled and p.enabled`, j.EndpointID, j.Principal, j.Device, j.Version, j.Provider).Scan(&j.Config, &j.Target)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
func prefixedPushColumns(p string) string {
	return p + `.id,` + p + `.endpoint_id,` + p + `.principal,` + p + `.device,` + p + `.provider,` + p + `.version,` + p + `.message_id,` + p + `.sequence,` + p + `.status,` + p + `.attempts,` + p + `.status_code,` + p + `.code,` + p + `.created_at,` + p + `.updated_at,` + p + `.next_attempt,coalesce(` + p + `.lease::text,''),coalesce(` + p + `.lease_until,'epoch'::timestamptz),` + p + `.category,` + p + `.expires_at,` + p + `.group_key,` + p + `.group_label,coalesce(` + p + `.digest_id::text,''),` + p + `.digest_count,` + p + `.digest_at,` + p + `.priority,coalesce(` + p + `.hard_expires_at,'epoch'::timestamptz),` + p + `.collapse_key,` + p + `.not_before`
}
func (s *PgStore) ManagedRealtimePushLeaseActive(ctx context.Context, id, lease string) (bool, error) {
	var active bool
	err := s.pool.QueryRow(ctx, `select exists(select 1 from managed_realtime_push_deliveries j join managed_realtime_push_devices d using(endpoint_id,principal,device) join managed_realtime_push_providers p on p.endpoint_id=j.endpoint_id and p.provider=j.provider join managed_realtime_endpoints e on e.id=j.endpoint_id where j.id=$1 and j.lease=$2 and j.status='sending' and j.lease_until>clock_timestamp() and j.version=d.version and d.provider=j.provider and d.enabled and p.enabled and e.enabled)`, id, lease).Scan(&active)
	return active, err
}
func (s *PgStore) CompleteManagedRealtimePush(ctx context.Context, id, lease string, statusCode int, code string, retry, invalid bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Device first, then delivery: registration and invalidation use the same order.
	if invalid {
		_, err = tx.Exec(ctx, `update managed_realtime_push_devices d set enabled=false,sealed='\x'::bytea,updated_at=clock_timestamp() from managed_realtime_push_deliveries j where j.id=$1 and j.lease=$2 and j.status='sending' and j.endpoint_id=d.endpoint_id and j.principal=d.principal and j.device=d.device and j.version=d.version and j.provider=d.provider`, id, lease)
		if err != nil {
			return err
		}
	}
	j, err := scanPush(tx.QueryRow(ctx, `select `+pushColumns+` from managed_realtime_push_deliveries where id=$1 and lease=$2 and status='sending' for update`, id, lease))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	status := "failed"
	next := time.Now().UTC()
	if statusCode >= 200 && statusCode < 300 {
		status = "sent"
	} else if retry && !invalid && j.Attempts <= len(pushRetryDelays) && next.Add(pushRetryDelays[j.Attempts-1]).Before(j.ExpiresAt) {
		status = "pending"
		next = next.Add(pushRetryDelays[j.Attempts-1])
	}
	_, err = tx.Exec(ctx, `update managed_realtime_push_deliveries set status=$3,status_code=$4,code=$5,next_attempt=$6,lease=null,lease_until=null,updated_at=clock_timestamp() where id=$1 and lease=$2 and status='sending'`, id, lease, status, statusCode, code, next)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

var _ ManagedRealtimePushStore = (*PgStore)(nil)
