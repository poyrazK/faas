package state

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"strconv"
	"time"
)

func readRealtimeGroupAlert(ctx context.Context, tx pgx.Tx, id string) (RealtimeConsumerGroupAlert, error) {
	var a RealtimeConsumerGroupAlert
	err := tx.QueryRow(ctx, `select enabled,unassigned_threshold::text,oldest_pending_seconds,dead_letter_threshold,cooldown_seconds,
 breached,observed_backlog::text,observed_age,observed_dead,observed_at,last_event_at from managed_realtime_consumer_group_alerts where group_id=$1`, id).Scan(
		&a.Enabled, &a.UnassignedThreshold, &a.OldestPendingSeconds, &a.DeadLetterThreshold, &a.CooldownSeconds, &a.Breached, &a.Unassigned, &a.OldestPendingAgeSeconds, &a.DeadLetters, &a.ObservedAt, &a.LastEventAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RealtimeConsumerGroupAlert{RealtimeConsumerGroupAlertConfig: RealtimeConsumerGroupAlertConfig{UnassignedThreshold: "0", CooldownSeconds: api.RealtimeConsumerGroupAlertCooldownDefaultSeconds}}, nil
	}
	return a, err
}
func (s *PgStore) ReadRealtimeConsumerGroupAlert(ctx context.Context, ep, ch, name string) (RealtimeConsumerGroupAlert, error) {
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return RealtimeConsumerGroupAlert{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	a, err := readRealtimeGroupAlert(ctx, tx, g.id)
	if err != nil {
		return a, err
	}
	return a, tx.Commit(ctx)
}
func (s *PgStore) SetRealtimeConsumerGroupAlert(ctx context.Context, ep, ch, name string, c RealtimeConsumerGroupAlertConfig) (RealtimeConsumerGroupAlert, error) {
	if _, ok := realtimeWorkerFromContext(ctx); ok {
		return RealtimeConsumerGroupAlert{}, ErrRealtimeWorkerCredentialDenied
	}
	if c.UnassignedThreshold == "" {
		c.UnassignedThreshold = "0"
	}
	backlog, err := realtimeGroupSequence(c.UnassignedThreshold)
	if c.CooldownSeconds == 0 {
		c.CooldownSeconds = api.RealtimeConsumerGroupAlertCooldownDefaultSeconds
	}
	if err != nil || backlog > api.RealtimeConsumerGroupAlertBacklogMax || c.OldestPendingSeconds < 0 || c.OldestPendingSeconds > api.RealtimeConsumerGroupAlertAgeMaxSeconds || c.DeadLetterThreshold < 0 || c.DeadLetterThreshold > api.RealtimeConsumerGroupOutstandingMax || c.CooldownSeconds < api.RealtimeConsumerGroupAlertCooldownMinSeconds || c.CooldownSeconds > api.RealtimeConsumerGroupAlertCooldownMaxSeconds || (c.Enabled && backlog == 0 && c.OldestPendingSeconds == 0 && c.DeadLetterThreshold == 0) {
		return RealtimeConsumerGroupAlert{}, ErrRealtimeConsumerGroupInvalid
	}
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return RealtimeConsumerGroupAlert{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `insert into managed_realtime_consumer_group_alerts(group_id,enabled,unassigned_threshold,oldest_pending_seconds,dead_letter_threshold,cooldown_seconds)
 values($1,$2,$3,$4,$5,$6) on conflict(group_id) do update set enabled=excluded.enabled,unassigned_threshold=excluded.unassigned_threshold,
 oldest_pending_seconds=excluded.oldest_pending_seconds,dead_letter_threshold=excluded.dead_letter_threshold,cooldown_seconds=excluded.cooldown_seconds,
 breached=false,observed_backlog=null,observed_age=null,observed_dead=null,observed_at=null,last_event_at=null,next_check_at=clock_timestamp()
 where (managed_realtime_consumer_group_alerts.enabled,managed_realtime_consumer_group_alerts.unassigned_threshold,managed_realtime_consumer_group_alerts.oldest_pending_seconds,managed_realtime_consumer_group_alerts.dead_letter_threshold,managed_realtime_consumer_group_alerts.cooldown_seconds)
 is distinct from (excluded.enabled,excluded.unassigned_threshold,excluded.oldest_pending_seconds,excluded.dead_letter_threshold,excluded.cooldown_seconds)`, g.id, c.Enabled, backlog, c.OldestPendingSeconds, c.DeadLetterThreshold, c.CooldownSeconds)
	if err != nil {
		return RealtimeConsumerGroupAlert{}, err
	}
	a, err := readRealtimeGroupAlert(ctx, tx, g.id)
	if err != nil {
		return a, err
	}
	return a, tx.Commit(ctx)
}
func (s *PgStore) EvaluateRealtimeConsumerGroupAlerts(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `select f.group_id from managed_realtime_consumer_group_alerts f join managed_realtime_consumer_groups g on g.id=f.group_id
 join managed_realtime_endpoints e on e.id=g.endpoint_id join accounts a on a.id=e.account_id join apps p on p.id=e.app_id
 where f.enabled and e.enabled and a.status in ('active','past_due') and p.status<>'deleted' and f.next_check_at<=clock_timestamp() order by f.next_check_at,f.group_id limit $1`, api.RealtimeConsumerGroupAlertPassMax)
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
		done, err := s.evaluateRealtimeGroupAlert(ctx, id)
		if err != nil {
			return count, err
		}
		if done {
			count++
		}
	}
	return count, nil
}
func (s *PgStore) evaluateRealtimeGroupAlert(ctx context.Context, id string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var ep, ch, name string
	var assigned int64
	var maxAttempts int
	var paused, draining bool
	var plan api.Plan
	err = tx.QueryRow(ctx, `select g.endpoint_id,g.channel,g.name,g.assigned_sequence,g.max_attempts,g.paused_at is not null,g.draining,a.plan
 from managed_realtime_consumer_groups g join managed_realtime_endpoints e on e.id=g.endpoint_id
 join accounts a on a.id=e.account_id join apps p on p.id=e.app_id
 where g.id=$1 and e.enabled and a.status in ('active','past_due') and p.status<>'deleted'
 for update of g skip locked`, id).Scan(&ep, &ch, &name, &assigned, &maxAttempts, &paused, &draining, &plan)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var locked string
	err = tx.QueryRow(ctx, `select group_id from managed_realtime_consumer_group_alerts where group_id=$1 and enabled and next_check_at<=clock_timestamp() for update skip locked`, id).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if limits, ok := api.RealtimeLimitsFor(plan); !ok || limits.EndpointsPerApp == 0 {
		_, err = tx.Exec(ctx, `update managed_realtime_consumer_group_alerts set next_check_at=clock_timestamp()+make_interval(secs=>$2::double precision) where group_id=$1`, id, api.RealtimeConsumerGroupAlertScanSeconds)
		if err != nil {
			return false, err
		}
		return false, tx.Commit(ctx)
	}
	a, err := readRealtimeGroupAlert(ctx, tx, id)
	if err != nil {
		return false, err
	}
	var backlog int64
	var age, dead int
	err = tx.QueryRow(ctx, `select greatest(0,coalesce((select next_sequence-1 from managed_realtime_channel_heads where endpoint_id=$1 and channel=$2),0)-$4),
 coalesce(greatest(0,floor(extract(epoch from clock_timestamp()-least(
 (select min((message->>'created_at')::timestamptz) from managed_realtime_consumer_group_messages where group_id=$3 and status in ('ready','leased')),
 (select created_at from managed_realtime_channel_messages where endpoint_id=$1 and channel=$2 and sequence>$4 and created_at>=clock_timestamp()-make_interval(secs=>$6::double precision) order by sequence limit 1))))),0)::bigint,
 (select count(*) from managed_realtime_consumer_group_messages where group_id=$3 and (status='dead' or (status='leased' and lease_until<=clock_timestamp() and attempts>=$5)))`, ep, ch, id, assigned, maxAttempts, ManagedRealtimeHistoryRetention.Seconds()).Scan(&backlog, &age, &dead)
	if err != nil {
		return false, err
	}
	threshold, _ := strconv.ParseInt(a.UnassignedThreshold, 10, 64)
	breached := (threshold > 0 && backlog >= threshold) || (a.OldestPendingSeconds > 0 && age >= a.OldestPendingSeconds) || (a.DeadLetterThreshold > 0 && dead >= a.DeadLetterThreshold)
	var now time.Time
	err = tx.QueryRow(ctx, `select clock_timestamp()`).Scan(&now)
	if err != nil {
		return false, err
	}
	event := ""
	if breached && (!a.Breached || a.LastEventAt == nil || now.Sub(*a.LastEventAt) >= time.Duration(a.CooldownSeconds)*time.Second) {
		event = string(AppWebhookEventRealtimeConsumerGroupLagExceeded)
	}
	if !breached && a.Breached {
		event = string(AppWebhookEventRealtimeConsumerGroupLagRecovered)
	}
	if event != "" {
		if err = enqueueRealtimeGroupLagAlert(ctx, tx, id, ep, ch, name, event, backlog, age, dead, paused, draining, now); err != nil {
			return false, err
		}
	}
	_, err = tx.Exec(ctx, `update managed_realtime_consumer_group_alerts set breached=$2,observed_backlog=$3,observed_age=$4,observed_dead=$5,observed_at=$6,
 last_event_at=case when $7 then $6 else last_event_at end,next_check_at=$6+make_interval(secs=>$8::double precision) where group_id=$1`, id, breached, backlog, age, dead, now, event != "", api.RealtimeConsumerGroupAlertScanSeconds)
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
func enqueueRealtimeGroupLagAlert(ctx context.Context, tx pgx.Tx, id, ep, ch, name, event string, backlog int64, age, dead int, paused, draining bool, now time.Time) error {
	_, err := tx.Exec(ctx, `with recipients as (
 select e.account_id,e.app_id,array_agg(h.id order by h.id) ids from managed_realtime_endpoints e
 join app_webhooks h on h.account_id=e.account_id and h.app_id=e.app_id
 where e.id=$2 and h.enabled and h.scope='app' and $5=any(h.event_filter) group by e.account_id,e.app_id),
 identity as (select gen_random_uuid() event_id)
 insert into app_webhook_event_outbox(id,account_id,app_id,event,source_id,payload,recipient_webhook_ids)
 select i.event_id,r.account_id,r.app_id,$5,i.event_id,jsonb_build_object('event_id',i.event_id,'app_id',r.app_id,
 'endpoint_id',$2::text,'group_id',$1::text,'channel',$3::text,'group',$4::text,'unassigned',$6::bigint::text,
 'oldest_pending_age_seconds',$7::bigint,'dead_letters',$8::integer,'paused',$9::boolean,'draining',$10::boolean,'occurred_at',$11::timestamptz,'thresholds',(select jsonb_build_object('unassigned_threshold',unassigned_threshold::text,'oldest_pending_seconds',oldest_pending_seconds,'dead_letter_threshold',dead_letter_threshold,'cooldown_seconds',cooldown_seconds) from managed_realtime_consumer_group_alerts where group_id=$1::uuid)),r.ids
 from recipients r cross join identity i`, id, ep, ch, name, event, backlog, age, dead, paused, draining, now)
	return err
}
