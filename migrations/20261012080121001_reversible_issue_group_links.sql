-- +goose Up
-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS app_issues_scope_identity_idx ON app_issues(app_id,environment,id);
CREATE TABLE IF NOT EXISTS issue_group_links (
 issue_id uuid PRIMARY KEY,
 canonical_issue_id uuid NOT NULL,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 environment text NOT NULL CHECK (environment ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
 linked_at timestamptz NOT NULL DEFAULT now(),
 CHECK (issue_id <> canonical_issue_id),
 FOREIGN KEY (app_id,environment,issue_id) REFERENCES app_issues(app_id,environment,id) ON DELETE CASCADE,
 FOREIGN KEY (app_id,environment,canonical_issue_id) REFERENCES app_issues(app_id,environment,id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS issue_group_links_canonical_idx ON issue_group_links(canonical_issue_id,issue_id);

CREATE OR REPLACE FUNCTION guard_issue_group_link() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM id FROM apps WHERE id=NEW.app_id FOR UPDATE;
 IF EXISTS (SELECT 1 FROM issue_group_links WHERE issue_id=NEW.canonical_issue_id)
 OR EXISTS (SELECT 1 FROM issue_group_links WHERE canonical_issue_id=NEW.issue_id) THEN
  RAISE EXCEPTION 'issue links must reference a canonical root' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS issue_group_link_guard ON issue_group_links;
CREATE TRIGGER issue_group_link_guard BEFORE INSERT OR UPDATE ON issue_group_links
FOR EACH ROW EXECUTE FUNCTION guard_issue_group_link();

CREATE OR REPLACE VIEW issue_group_assignments AS
SELECT i.id AS original_issue_id,i.id AS canonical_issue_id FROM app_issues i
WHERE NOT EXISTS (SELECT 1 FROM issue_group_links l WHERE l.issue_id=i.id)
UNION ALL SELECT issue_id,canonical_issue_id FROM issue_group_links;

-- Preserve the additive routing column when this migration is replayed after ADR-1116.
DO $guard$
BEGIN
 IF EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='app_issues' AND column_name='split_source_issue_id') THEN
  EXECUTE $view$CREATE OR REPLACE VIEW issue_group_rollups AS
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
WHERE NOT EXISTS (SELECT 1 FROM issue_group_links l WHERE l.issue_id=i.id);$view$;
 ELSE
  EXECUTE $view$CREATE OR REPLACE VIEW issue_group_rollups AS
SELECT i.id,i.account_id,i.app_id,i.environment,i.fingerprint,i.grouping_version,i.title,i.state,i.assignee_account_id,
 totals.first_seen_at,totals.last_seen_at,totals.event_count,totals.regression_count,
 i.resolved_at,i.fixed_deployment_id,i.fixed_deployment_created_at,i.ignored_until
FROM app_issues i
CROSS JOIN LATERAL (
 SELECT min(m.first_seen_at) AS first_seen_at,max(m.last_seen_at) AS last_seen_at,
 sum(m.event_count)::bigint AS event_count,sum(m.regression_count)::bigint AS regression_count
 FROM app_issues m
 WHERE m.id IN (SELECT i.id UNION ALL SELECT issue_id FROM issue_group_links WHERE canonical_issue_id=i.id)
) totals
WHERE NOT EXISTS (SELECT 1 FROM issue_group_links l WHERE l.issue_id=i.id);$view$;
 END IF;
END;
$guard$;

ALTER TABLE issue_activity DROP CONSTRAINT IF EXISTS issue_activity_action_check;
DO $guard$
BEGIN
 IF to_regclass('issue_split_rules') IS NOT NULL THEN
  ALTER TABLE issue_activity ADD CONSTRAINT issue_activity_action_check
   CHECK(action IN ('created','assigned','resolved','reopened','ignored','regressed','impact_threshold_reached','merged','split','split_rule_created','split_rule_updated'));
 ELSE
  ALTER TABLE issue_activity ADD CONSTRAINT issue_activity_action_check
   CHECK(action IN ('created','assigned','resolved','reopened','ignored','regressed','impact_threshold_reached','merged','split'));
 END IF;
END;
$guard$;
-- +goose StatementEnd

-- +goose Down
-- Preserve links and audit history on rollback, following ADR-380 and ADR-1114.
SELECT 1;
