-- +goose Up
-- +goose StatementBegin
ALTER TABLE app_issues ADD COLUMN IF NOT EXISTS split_source_issue_id uuid;
ALTER TABLE app_issues DROP CONSTRAINT IF EXISTS app_issues_split_source_scope_fkey;
ALTER TABLE app_issues ADD CONSTRAINT app_issues_split_source_scope_fkey
 FOREIGN KEY (app_id,environment,split_source_issue_id) REFERENCES app_issues(app_id,environment,id) ON DELETE CASCADE;
ALTER TABLE issue_events ADD COLUMN IF NOT EXISTS original_issue_id uuid REFERENCES app_issues(id) ON DELETE CASCADE;
CREATE TABLE IF NOT EXISTS issue_split_rules (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 environment text NOT NULL CHECK(environment ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
 source_issue_id uuid NOT NULL,
 target_issue_id uuid NOT NULL UNIQUE,
 priority integer NOT NULL CHECK(priority BETWEEN 1 AND 8),
 exception_type text NOT NULL DEFAULT '' CHECK(octet_length(exception_type) <= 256),
 error_code text NOT NULL DEFAULT '' CHECK(octet_length(error_code) <= 256),
 route text NOT NULL DEFAULT '' CHECK(octet_length(route) <= 256),
 source_kind text NOT NULL DEFAULT '' CHECK(source_kind IN ('','exception','http','runtime','worker')),
 enabled boolean NOT NULL DEFAULT true CHECK(enabled IN (true,false)),
 initial_moved_event_count bigint NOT NULL DEFAULT 0 CHECK(initial_moved_event_count >= 0),
 unavailable_history_event_count bigint NOT NULL DEFAULT 0 CHECK(unavailable_history_event_count >= 0),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK(source_issue_id <> target_issue_id),
 CHECK(exception_type <> '' OR error_code <> '' OR route <> '' OR source_kind <> ''),
 UNIQUE(source_issue_id,priority),
 UNIQUE(source_issue_id,exception_type,error_code,route,source_kind),
 FOREIGN KEY(app_id,environment,source_issue_id) REFERENCES app_issues(app_id,environment,id) ON DELETE CASCADE,
 FOREIGN KEY(app_id,environment,target_issue_id) REFERENCES app_issues(app_id,environment,id) ON DELETE CASCADE
);
CREATE OR REPLACE FUNCTION guard_issue_split_rule() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM id FROM apps WHERE id=NEW.app_id FOR UPDATE;
 IF EXISTS(SELECT 1 FROM app_issues WHERE id=NEW.source_issue_id AND split_source_issue_id IS NOT NULL)
 OR NOT EXISTS(SELECT 1 FROM app_issues WHERE id=NEW.target_issue_id AND split_source_issue_id=NEW.source_issue_id) THEN
  RAISE EXCEPTION 'split rules require an original source and its derived branch' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' AND (NEW.id,NEW.app_id,NEW.environment,NEW.source_issue_id,NEW.target_issue_id,NEW.priority,NEW.exception_type,NEW.error_code,NEW.route,NEW.source_kind,NEW.initial_moved_event_count,NEW.unavailable_history_event_count,NEW.created_at)
 IS DISTINCT FROM (OLD.id,OLD.app_id,OLD.environment,OLD.source_issue_id,OLD.target_issue_id,OLD.priority,OLD.exception_type,OLD.error_code,OLD.route,OLD.source_kind,OLD.initial_moved_event_count,OLD.unavailable_history_event_count,OLD.created_at) THEN
  RAISE EXCEPTION 'split rule definitions are immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS issue_split_rule_guard ON issue_split_rules;
CREATE TRIGGER issue_split_rule_guard BEFORE INSERT OR UPDATE ON issue_split_rules
FOR EACH ROW EXECUTE FUNCTION guard_issue_split_rule();
CREATE OR REPLACE VIEW issue_group_rollups AS
SELECT i.id,i.account_id,i.app_id,i.environment,i.fingerprint,i.grouping_version,i.title,i.state,i.assignee_account_id,
 totals.first_seen_at,totals.last_seen_at,totals.event_count,totals.regression_count,
 i.resolved_at,i.fixed_deployment_id,i.fixed_deployment_created_at,i.ignored_until,i.split_source_issue_id
FROM app_issues i
CROSS JOIN LATERAL (
 SELECT min(m.first_seen_at) AS first_seen_at,max(m.last_seen_at) AS last_seen_at,
 sum(m.event_count)::bigint AS event_count,sum(m.regression_count)::bigint AS regression_count
 FROM app_issues m
 WHERE m.id IN (SELECT i.id UNION ALL SELECT issue_id FROM issue_group_links WHERE canonical_issue_id=i.id)
) totals
WHERE NOT EXISTS (SELECT 1 FROM issue_group_links l WHERE l.issue_id=i.id);


ALTER TABLE issue_activity DROP CONSTRAINT IF EXISTS issue_activity_action_check;
ALTER TABLE issue_activity ADD CONSTRAINT issue_activity_action_check
 CHECK(action IN ('created','assigned','resolved','reopened','ignored','regressed','impact_threshold_reached','merged','split','split_rule_created','split_rule_updated'));
-- +goose StatementEnd

-- +goose Down
-- Preserve rule definitions, routing destinations, provenance, and audit history.
SELECT 1;
