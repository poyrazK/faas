-- filename: 20261021183000603_app_webhook_outbox_consumer_events_swap.sql

-- ADR-20261021183000: app webhook events about customers acting on their
-- own account go through the outbox, and so does consumer.usage_threshold,
-- which the outbox event check never allowed: in Postgres a usage alert for
-- an app with a subscribed webhook failed. The check is replaced online in
-- three steps: this one drops the old check and gives the wider one its name, which later
-- migrations widening the vocabulary look up.

-- migration-compat: contract

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT IF EXISTS app_webhook_event_outbox_event_chk;
ALTER TABLE app_webhook_event_outbox RENAME CONSTRAINT app_webhook_event_outbox_event_v2_chk TO app_webhook_event_outbox_event_chk;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE app_webhook_event_outbox RENAME CONSTRAINT app_webhook_event_outbox_event_chk TO app_webhook_event_outbox_event_v2_chk;
ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk
  CHECK (((event = ANY (ARRAY['issue.compound_threshold_reached'::text, 'usage_statement.finalized'::text, 'app.parked'::text, 'app.woken'::text, 'issue.created'::text, 'issue.assigned'::text, 'issue.resolved'::text, 'issue.reopened'::text, 'issue.ignored'::text, 'issue.regressed'::text, 'issue.impact_threshold_reached'::text, 'routes.requirements.violated'::text, 'routes.requirements.recovered'::text, 'routes.requirements.changed'::text, 'routes.health.blocked'::text, 'routes.health.resumed'::text, 'routes.health.aborted'::text, 'routes.monitor.violated'::text, 'routes.monitor.escalated'::text, 'routes.monitor.recovered'::text, 'workflow.finished'::text, 'app.health.changed'::text, 'event_recovery.completed'::text, 'event_recovery.cancelled'::text, 'event_recovery.expired'::text, 'event_recovery.execution_finished'::text, 'profile.route_regressed'::text, 'profile.route_recovered'::text])) OR (event = 'automation.paused'::text) OR (event = 'event_recovery.execution_attention'::text) OR (event = 'automation.recovery_review_ready'::text) OR (event = 'automation.recovery_review_finished'::text) OR (event = 'automation.recovery_policy_paused'::text) OR (event = 'automation.recovery_queue_blocked'::text) OR (event = ANY (ARRAY['automation.recovery_health_alert'::text, 'automation.recovery_health_recovered'::text])) OR (event = 'outbound.ai_budget.threshold'::text) OR (event = 'realtime.channel.published'::text) OR (event = 'realtime.channel.acknowledged'::text) OR (event = 'realtime.consumer_group.dead_lettered'::text) OR (event = ANY (ARRAY['realtime.consumer_group.lag_exceeded'::text, 'realtime.consumer_group.lag_recovered'::text])) OR (event = 'realtime.consumer_group.dead_letter_expiring'::text) OR (event = 'realtime.consumer_group.delivery_expired'::text) OR (event = ANY (ARRAY['realtime.consumer_group.worker_lost'::text, 'realtime.consumer_group.worker_recovered'::text])) OR (event = ANY (ARRAY['realtime.consumer_group.circuit_opened'::text, 'realtime.consumer_group.circuit_half_open'::text, 'realtime.consumer_group.circuit_closed'::text])) OR (event = ANY (ARRAY['realtime.replay_job.completed'::text, 'realtime.replay_job.cancelled'::text, 'realtime.replay_job.processing_failed'::text])) OR (event = 'automation.recovery_polling_status'::text) OR (event = ANY (ARRAY['issue.tracker_sync.attention'::text, 'issue.tracker_sync.recovered'::text])) OR (event = 'issue.frequency_threshold_reached'::text))) NOT VALID;
