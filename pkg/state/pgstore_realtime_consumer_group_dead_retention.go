package state

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

func retentionPolicy(g realtimeGroupRow) RealtimeConsumerGroupRetentionPolicy {
	return RealtimeConsumerGroupRetentionPolicy{RetentionSeconds: g.retentionSeconds, WarningSeconds: api.RealtimeConsumerGroupDeadWarningSeconds, ExpiredDeadLetters: g.expiredDead}
}
func (s *PgStore) ReadRealtimeConsumerGroupRetention(ctx context.Context, ep, ch, name string) (RealtimeConsumerGroupRetentionPolicy, error) {
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return RealtimeConsumerGroupRetentionPolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return retentionPolicy(g), tx.Commit(ctx)
}
func (s *PgStore) SetRealtimeConsumerGroupRetention(ctx context.Context, ep, ch, name string, seconds int) (RealtimeConsumerGroupRetentionPolicy, error) {
	if _, ok := realtimeWorkerFromContext(ctx); ok {
		return RealtimeConsumerGroupRetentionPolicy{}, ErrRealtimeWorkerCredentialDenied
	}
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return RealtimeConsumerGroupRetentionPolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = validateRealtimeGroupRetention(seconds, g.orderingField, g.deadPolicy); err != nil {
		return RealtimeConsumerGroupRetentionPolicy{}, err
	}
	if seconds != g.retentionSeconds {
		_, err = tx.Exec(ctx, `update managed_realtime_consumer_groups set dead_letter_retention_seconds=$2,next_retention_check_at=clock_timestamp() where id=$1`, g.id, seconds)
		if err != nil {
			return RealtimeConsumerGroupRetentionPolicy{}, err
		}
		_, err = tx.Exec(ctx, `update managed_realtime_consumer_group_messages set expiry_warned_at=null where group_id=$1 and status='dead'`, g.id)
		if err != nil {
			return RealtimeConsumerGroupRetentionPolicy{}, err
		}
	}
	g.retentionSeconds = seconds
	return retentionPolicy(g), tx.Commit(ctx)
}
func (s *PgStore) ExpireRealtimeConsumerGroupDeadLetters(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `select g.id from managed_realtime_consumer_groups g
 join managed_realtime_endpoints e on e.id=g.endpoint_id join accounts a on a.id=e.account_id join apps p on p.id=e.app_id
 where g.dead_letter_retention_seconds>0 and (g.ordering_metadata_key='' or g.dead_letter_policy='skip')
 and e.enabled and a.status in ('active','past_due') and p.status<>'deleted' and g.next_retention_check_at<=clock_timestamp()
 and exists(select 1 from managed_realtime_consumer_group_messages m where m.group_id=g.id and m.status='dead')
 order by g.next_retention_check_at,g.id limit $1`, api.RealtimeConsumerGroupDeadRetentionPassMax)
	if err != nil {
		return 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, id := range ids {
		n, err := s.expireRealtimeGroupDeadLetters(ctx, id)
		if err != nil {
			return count, err
		}
		count += n
	}
	return count, nil
}
func (s *PgStore) expireRealtimeGroupDeadLetters(ctx context.Context, id string) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var ep, ch, name string
	var seconds int
	var plan api.Plan
	err = tx.QueryRow(ctx, `select g.endpoint_id,g.channel,g.name,g.dead_letter_retention_seconds,a.plan
 from managed_realtime_consumer_groups g join managed_realtime_endpoints e on e.id=g.endpoint_id join accounts a on a.id=e.account_id join apps p on p.id=e.app_id
 where g.id=$1 and g.dead_letter_retention_seconds>0 and (g.ordering_metadata_key='' or g.dead_letter_policy='skip')
 and e.enabled and a.status in ('active','past_due') and p.status<>'deleted' and g.next_retention_check_at<=clock_timestamp()
 for update of g skip locked`, id).Scan(&ep, &ch, &name, &seconds, &plan)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if limits, ok := api.RealtimeLimitsFor(plan); !ok || limits.EndpointsPerApp == 0 {
		_, err = tx.Exec(ctx, `update managed_realtime_consumer_groups set next_retention_check_at=clock_timestamp()+make_interval(secs=>$2::double precision) where id=$1`, id, api.RealtimeConsumerGroupDeadRetentionScanSeconds)
		if err != nil {
			return 0, err
		}
		return 0, tx.Commit(ctx)
	}
	if err = warnRealtimeGroupDeadLetters(ctx, tx, id, ep, ch, name, seconds); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `delete from managed_realtime_consumer_group_messages where group_id=$1 and status='dead'
 and expiry_warned_at is not null and dead_since+make_interval(secs=>$2::double precision)<=clock_timestamp()
 and expiry_warned_at+make_interval(secs=>$3::double precision)<=clock_timestamp()`, id, seconds, api.RealtimeConsumerGroupDeadWarningSeconds)
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(ctx, `update managed_realtime_consumer_groups set expired_dead_letters=expired_dead_letters+$2,
 next_retention_check_at=clock_timestamp()+make_interval(secs=>$3::double precision) where id=$1`, id, tag.RowsAffected(), api.RealtimeConsumerGroupDeadRetentionScanSeconds)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}
func warnRealtimeGroupDeadLetters(ctx context.Context, tx pgx.Tx, id, ep, ch, name string, seconds int) error {
	rows, err := tx.Query(ctx, `select sequence,attempts,failure_code,dead_since+make_interval(secs=>$2::double precision)
 from managed_realtime_consumer_group_messages where group_id=$1 and status='dead' and expiry_warned_at is null
 and dead_since+make_interval(secs=>$2::double precision)<=clock_timestamp()+make_interval(secs=>$3::double precision)
 order by sequence limit $4`, id, seconds, api.RealtimeConsumerGroupDeadWarningSeconds, api.RealtimeConsumerGroupOutstandingMax)
	if err != nil {
		return err
	}
	type warning struct {
		sequence int64
		attempts int
		code     string
		expires  time.Time
	}
	warnings := []warning{}
	for rows.Next() {
		var w warning
		if err = rows.Scan(&w.sequence, &w.attempts, &w.code, &w.expires); err != nil {
			rows.Close()
			return err
		}
		warnings = append(warnings, w)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, w := range warnings {
		var now time.Time
		if err = tx.QueryRow(ctx, `select clock_timestamp()`).Scan(&now); err != nil {
			return err
		}
		cleanup := w.expires
		if grace := now.Add(time.Duration(api.RealtimeConsumerGroupDeadWarningSeconds) * time.Second); grace.After(cleanup) {
			cleanup = grace
		}
		if err = enqueueRealtimeDeadExpiry(ctx, tx, id, ep, ch, name, w.sequence, w.attempts, w.code, w.expires, cleanup, now); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `update managed_realtime_consumer_group_messages set expiry_warned_at=$3 where group_id=$1 and sequence=$2 and status='dead'`, id, w.sequence, now)
		if err != nil {
			return err
		}
	}
	return nil
}
func enqueueRealtimeDeadExpiry(ctx context.Context, tx pgx.Tx, id, ep, ch, name string, sequence int64, attempts int, code string, expires, cleanup, now time.Time) error {
	_, err := tx.Exec(ctx, `with recipients as (
 select e.account_id,e.app_id,array_agg(h.id order by h.id) ids from managed_realtime_endpoints e
 join app_webhooks h on h.account_id=e.account_id and h.app_id=e.app_id where e.id=$2 and h.enabled and h.scope='app'
 and 'realtime.consumer_group.dead_letter_expiring'=any(h.event_filter) group by e.account_id,e.app_id),
 identity as (select gen_random_uuid() event_id)
 insert into app_webhook_event_outbox(id,account_id,app_id,event,source_id,payload,recipient_webhook_ids)
 select i.event_id,r.account_id,r.app_id,'realtime.consumer_group.dead_letter_expiring',i.event_id,
 jsonb_build_object('event_id',i.event_id,'app_id',r.app_id,'endpoint_id',$2::text,'group_id',$1::text,'channel',$3::text,'group',$4::text,
 'sequence',$5::bigint::text,'attempts',$6::integer,'failure_code',$7::text,'expires_at',$8::timestamptz,'cleanup_after',$9::timestamptz,'occurred_at',$10::timestamptz),r.ids
 from recipients r cross join identity i`, id, ep, ch, name, sequence, attempts, code, expires, cleanup, now)
	return err
}
