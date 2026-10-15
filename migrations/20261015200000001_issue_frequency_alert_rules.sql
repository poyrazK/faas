-- +goose Up
CREATE TABLE public.app_issue_frequency_alert_policies (
 app_id uuid PRIMARY KEY REFERENCES public.apps(id) ON DELETE CASCADE,
 revision bigint NOT NULL CHECK(revision > 0),
 rules jsonb NOT NULL CHECK(jsonb_typeof(rules)='array' AND jsonb_array_length(rules)<=10)
);
CREATE TABLE public.issue_frequency_alert_receipts (
 app_id uuid NOT NULL REFERENCES public.app_issue_frequency_alert_policies(app_id) ON DELETE CASCADE,
 rule_key text NOT NULL CHECK(rule_key ~ '^[a-z0-9][a-z0-9-]{0,47}$'),
 issue_id uuid NOT NULL REFERENCES public.app_issues(id) ON DELETE CASCADE,
 rule_hash text NOT NULL CHECK(rule_hash ~ '^[a-f0-9]{64}$'),
 last_alert_at timestamptz NOT NULL,
 PRIMARY KEY(app_id,rule_key,issue_id)
);
ALTER TABLE public.issue_activity DROP CONSTRAINT issue_activity_action_check;
ALTER TABLE public.issue_activity ADD CONSTRAINT issue_activity_action_check CHECK ((action = ANY (ARRAY['created'::text, 'assigned'::text, 'resolved'::text, 'reopened'::text, 'ignored'::text, 'regressed'::text, 'impact_threshold_reached'::text, 'frequency_threshold_reached'::text, 'merged'::text, 'split'::text, 'split_rule_created'::text, 'split_rule_updated'::text, 'tracker_linked'::text, 'tracker_link_updated'::text, 'tracker_unlinked'::text])));
ALTER TABLE public.app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
ALTER TABLE public.app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK (((event = ANY (ARRAY['usage_statement.finalized'::text, 'app.parked'::text, 'app.woken'::text, 'issue.created'::text, 'issue.assigned'::text, 'issue.resolved'::text, 'issue.reopened'::text, 'issue.ignored'::text, 'issue.regressed'::text, 'issue.impact_threshold_reached'::text, 'issue.frequency_threshold_reached'::text, 'routes.requirements.violated'::text, 'routes.requirements.recovered'::text, 'routes.requirements.changed'::text, 'routes.health.blocked'::text, 'routes.health.resumed'::text, 'routes.health.aborted'::text, 'routes.monitor.violated'::text, 'routes.monitor.escalated'::text, 'routes.monitor.recovered'::text, 'workflow.finished'::text, 'app.health.changed'::text, 'event_recovery.completed'::text, 'event_recovery.cancelled'::text, 'event_recovery.expired'::text, 'event_recovery.execution_finished'::text, 'profile.route_regressed'::text, 'profile.route_recovered'::text])) OR (event = 'automation.paused'::text) OR (event = 'event_recovery.execution_attention'::text) OR (event = 'automation.recovery_review_ready'::text) OR (event = 'automation.recovery_review_finished'::text) OR (event = 'automation.recovery_policy_paused'::text) OR (event = 'automation.recovery_queue_blocked'::text) OR (event = ANY (ARRAY['automation.recovery_health_alert'::text, 'automation.recovery_health_recovered'::text])) OR (event = 'outbound.ai_budget.threshold'::text) OR (event = ANY (ARRAY['issue.tracker_sync.attention'::text, 'issue.tracker_sync.recovered'::text]))));

-- +goose Down
DELETE FROM public.app_webhook_event_outbox WHERE event='issue.frequency_threshold_reached';
DELETE FROM public.issue_activity WHERE action='frequency_threshold_reached';
DROP TABLE public.issue_frequency_alert_receipts;
DROP TABLE public.app_issue_frequency_alert_policies;
ALTER TABLE public.issue_activity DROP CONSTRAINT issue_activity_action_check;
ALTER TABLE public.issue_activity ADD CONSTRAINT issue_activity_action_check CHECK ((action = ANY (ARRAY['created'::text, 'assigned'::text, 'resolved'::text, 'reopened'::text, 'ignored'::text, 'regressed'::text, 'impact_threshold_reached'::text, 'merged'::text, 'split'::text, 'split_rule_created'::text, 'split_rule_updated'::text, 'tracker_linked'::text, 'tracker_link_updated'::text, 'tracker_unlinked'::text])));
ALTER TABLE public.app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
ALTER TABLE public.app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK (((event = ANY (ARRAY['usage_statement.finalized'::text, 'app.parked'::text, 'app.woken'::text, 'issue.created'::text, 'issue.assigned'::text, 'issue.resolved'::text, 'issue.reopened'::text, 'issue.ignored'::text, 'issue.regressed'::text, 'issue.impact_threshold_reached'::text, 'routes.requirements.violated'::text, 'routes.requirements.recovered'::text, 'routes.requirements.changed'::text, 'routes.health.blocked'::text, 'routes.health.resumed'::text, 'routes.health.aborted'::text, 'routes.monitor.violated'::text, 'routes.monitor.escalated'::text, 'routes.monitor.recovered'::text, 'workflow.finished'::text, 'app.health.changed'::text, 'event_recovery.completed'::text, 'event_recovery.cancelled'::text, 'event_recovery.expired'::text, 'event_recovery.execution_finished'::text, 'profile.route_regressed'::text, 'profile.route_recovered'::text])) OR (event = 'automation.paused'::text) OR (event = 'event_recovery.execution_attention'::text) OR (event = 'automation.recovery_review_ready'::text) OR (event = 'automation.recovery_review_finished'::text) OR (event = 'automation.recovery_policy_paused'::text) OR (event = 'automation.recovery_queue_blocked'::text) OR (event = ANY (ARRAY['automation.recovery_health_alert'::text, 'automation.recovery_health_recovered'::text])) OR (event = 'outbound.ai_budget.threshold'::text) OR (event = ANY (ARRAY['issue.tracker_sync.attention'::text, 'issue.tracker_sync.recovered'::text]))));
