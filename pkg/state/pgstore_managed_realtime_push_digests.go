package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

func readDigestPreferencesPG(ctx context.Context, tx pgx.Tx, j ManagedRealtimePushDelivery) (api.RealtimeNotificationPreferences, error) {
	p := api.DefaultRealtimeNotificationPreferences()
	var raw []byte
	err := tx.QueryRow(ctx, `select preferences from managed_realtime_push_preferences where endpoint_id=$1 and principal=$2`, j.EndpointID, j.Principal).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	err = json.Unmarshal(raw, &p)
	return p, err
}
func saveDigestDecisionPG(ctx context.Context, tx pgx.Tx, j ManagedRealtimePushDelivery) error {
	_, err := tx.Exec(ctx, `update managed_realtime_push_deliveries set status=$2,code=$3,next_attempt=$4,attempts=$5,lease=null,lease_until=null,updated_at=$6,expires_at=$7,digest_at=$8,status_code=$9 where id=$1`, j.ID, j.Status, j.Code, j.NextAttempt, j.Attempts, j.UpdatedAt, j.ExpiresAt, j.DigestAt, j.StatusCode)
	return err
}
func readPushRowsPG(ctx context.Context, tx pgx.Tx, query string, args ...any) ([]ManagedRealtimePushDelivery, error) {
	rows, err := tx.Query(ctx, query, args...)
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
func digestCredentialsPG(ctx context.Context, tx pgx.Tx, payload *ManagedRealtimePushDelivery) error {
	if payload == nil {
		return nil
	}
	return tx.QueryRow(ctx, `select p.sealed,d.sealed from managed_realtime_push_devices d join managed_realtime_push_providers p using(endpoint_id,provider) where d.endpoint_id=$1 and d.principal=$2 and d.device=$3 and d.version=$4 and d.provider=$5 and d.enabled and p.enabled`, payload.EndpointID, payload.Principal, payload.Device, payload.Version, payload.Provider).Scan(&payload.Config, &payload.Target)
}
func (s *PgStore) AcquireManagedRealtimePushDigest(ctx context.Context, id, lease string) (*ManagedRealtimePushDelivery, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Read without a row lock first. Advisory locks serialize group formation
	// across replicas; row locks are then acquired in a consistent ID order.
	seed, err := scanPush(tx.QueryRow(ctx, `select `+pushColumns+` from managed_realtime_push_deliveries where id=$1 and lease=$2 and status='sending' and lease_until>clock_timestamp()`, id, lease))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var locked bool
	err = tx.QueryRow(ctx, `select pg_try_advisory_xact_lock(hashtextextended($1,0))`, digestLockKey(seed)).Scan(&locked)
	if err != nil || !locked {
		return nil, err
	}
	// Confirm the seed is still ours after obtaining the group lock.
	seed, err = scanPush(tx.QueryRow(ctx, `select `+pushColumns+` from managed_realtime_push_deliveries where id=$1 and lease=$2 and status='sending' and lease_until>clock_timestamp()`, id, lease))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p, err := readDigestPreferencesPG(ctx, tx, seed)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	at, group := digestSchedule(p, seed)
	var candidates []ManagedRealtimePushDelivery
	if seed.DigestID != "" {
		candidates, err = readPushRowsPG(ctx, tx, `select `+pushColumns+` from managed_realtime_push_deliveries where digest_id=$1 and status in ('pending','sending') order by id for update`, seed.DigestID)
	} else if group {
		candidates, err = readPushRowsPG(ctx, tx, `select `+pushColumns+` from managed_realtime_push_deliveries where endpoint_id=$1 and principal=$2 and device=$3 and version=$4 and provider=$5 and category=$6 and group_key=$7 and priority=$8 and digest_id is null and status in ('pending','sending') and next_attempt<=clock_timestamp() and expires_at>clock_timestamp() order by id for update`, seed.EndpointID, seed.Principal, seed.Device, seed.Version, seed.Provider, seed.Category, seed.GroupKey, seed.Priority)
	} else {
		candidates, err = readPushRowsPG(ctx, tx, `select `+pushColumns+` from managed_realtime_push_deliveries where id=$1 order by id for update`, id)
	}
	if err != nil {
		return nil, err
	}
	found := false
	eligible := []ManagedRealtimePushDelivery{}
	for _, j := range candidates {
		if j.ID == id {
			if j.Status != "sending" || j.Lease != lease || !j.LeaseUntil.After(now) {
				return nil, nil
			}
			found = true
		}
		if !j.ExpiresAt.After(now) || j.Status == "pending" && j.Attempts >= 7 {
			continue
		}
		ready, _ := digestSchedule(p, j)
		if seed.DigestID != "" || j.ID == id || group && ready.Equal(at) {
			eligible = append(eligible, j)
		}
	}
	if !found {
		return nil, nil
	}
	// A settings change can defer or suppress the seed before the batch exists.
	updated, send := pushPreferenceDecision(seed, p, now)
	if !send && seed.DigestID == "" {
		if err = saveDigestDecisionPG(ctx, tx, updated); err != nil {
			return nil, err
		}
		return nil, tx.Commit(ctx)
	}
	sort.Slice(eligible, func(i, j int) bool {
		if eligible[i].ID == id || eligible[j].ID == id {
			return eligible[i].ID == id && eligible[j].ID != id
		}
		return eligible[i].Sequence < eligible[j].Sequence
	})
	if len(eligible) > ManagedRealtimePushDigestMaxMembers {
		eligible = eligible[:ManagedRealtimePushDigestMaxMembers]
	}
	digestID := seed.DigestID
	if digestID == "" {
		digestID = uuid.NewString()
	}
	for i := range eligible {
		j := &eligible[i]
		if j.ID != id && (j.Status == "pending" || j.DigestID != "") {
			j.Attempts++
		}
		j.DigestID = digestID
		j.DigestAt = at
		j.Status = "sending"
		j.Lease = lease
		j.LeaseUntil = now.Add(time.Minute)
		j.UpdatedAt = now
	}
	payload := digestPayload(eligible)
	if payload == nil {
		return nil, nil
	}
	for _, j := range eligible {
		_, err = tx.Exec(ctx, `update managed_realtime_push_deliveries set digest_id=$2,digest_count=$3,digest_at=$4,status='sending',attempts=$5,lease=$6,lease_until=$7,updated_at=$8 where id=$1`, j.ID, digestID, payload.DigestCount, at, j.Attempts, lease, j.LeaseUntil, now)
		if err != nil {
			return nil, err
		}
	}
	if err = digestCredentialsPG(ctx, tx, payload); errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return payload, nil
}
func (s *PgStore) PrepareManagedRealtimePushDigest(ctx context.Context, digestID, lease string) (*ManagedRealtimePushDelivery, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	members, err := readPushRowsPG(ctx, tx, `select `+pushColumns+` from managed_realtime_push_deliveries where digest_id=$1 and lease=$2 and status='sending' and lease_until>clock_timestamp() order by id for update`, digestID, lease)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, nil
	}
	p, err := readDigestPreferencesPG(ctx, tx, members[0])
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	active := []ManagedRealtimePushDelivery{}
	for _, j := range members {
		updated, send := pushPreferenceDecision(j, p, now)
		if !j.ExpiresAt.After(now) {
			send = false
			updated = j
			updated.Status = "cancelled"
			updated.Code = "expired"
			updated.UpdatedAt = now
		}
		if !send {
			if err = saveDigestDecisionPG(ctx, tx, updated); err != nil {
				return nil, err
			}
		} else {
			active = append(active, j)
		}
	}
	payload := digestPayload(active)
	if payload != nil {
		next, rateErr := reservePushRatePG(ctx, tx, p, *payload, now)
		if rateErr != nil {
			return nil, rateErr
		}
		if next.After(now) {
			for _, j := range active {
				if err = saveDigestDecisionPG(ctx, tx, deferPushRate(j, next, now)); err != nil {
					return nil, err
				}
			}
			payload = nil
		}
	}
	if payload != nil {
		_, err = tx.Exec(ctx, `update managed_realtime_push_deliveries set digest_count=$3 where digest_id=$1 and lease=$2 and status='sending'`, digestID, lease, payload.DigestCount)
		if err != nil {
			return nil, err
		}
		if err = digestCredentialsPG(ctx, tx, payload); errors.Is(err, pgx.ErrNoRows) {
			payload = nil
		} else if err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return payload, nil
}
func (s *PgStore) CompleteManagedRealtimePushDigest(ctx context.Context, digestID, lease string, statusCode int, code string, retry, invalid bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Device first, as in token registration and individual delivery completion.
	if invalid {
		_, err = tx.Exec(ctx, `update managed_realtime_push_devices d set enabled=false,sealed='\x'::bytea,updated_at=clock_timestamp() from managed_realtime_push_deliveries j where j.digest_id=$1 and j.lease=$2 and j.status='sending' and j.endpoint_id=d.endpoint_id and j.principal=d.principal and j.device=d.device and j.version=d.version and j.provider=d.provider`, digestID, lease)
		if err != nil {
			return err
		}
	}
	members, err := readPushRowsPG(ctx, tx, `select `+pushColumns+` from managed_realtime_push_deliveries where digest_id=$1 and lease=$2 and status='sending' order by id for update`, digestID, lease)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, j := range members {
		j = digestCompletion(j, statusCode, code, retry, invalid, now)
		if err = saveDigestDecisionPG(ctx, tx, j); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

var _ ManagedRealtimePushDigestStore = (*PgStore)(nil)
