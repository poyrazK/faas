-- +goose Up
CREATE TABLE IF NOT EXISTS app_issue_teams (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 team_key text NOT NULL CHECK(team_key ~ '^[a-z0-9][a-z0-9-]{0,47}$'),
 name text NOT NULL CHECK(octet_length(name) BETWEEN 1 AND 64),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 updated_by_account_id uuid REFERENCES accounts(id) ON DELETE SET NULL,
 UNIQUE(app_id,team_key), UNIQUE(app_id,id)
);

CREATE TABLE IF NOT EXISTS app_issue_team_members (
 team_id uuid NOT NULL REFERENCES app_issue_teams(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 PRIMARY KEY(team_id,account_id)
);

ALTER TABLE app_issues ADD COLUMN IF NOT EXISTS assignee_team_id uuid;
-- +goose StatementBegin
DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='app_issues'::regclass AND conname='app_issues_target_check') THEN
  ALTER TABLE app_issues ADD CONSTRAINT app_issues_target_check CHECK(num_nonnulls(assignee_account_id,assignee_team_id)<=1);
 END IF;
END $issue_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='app_issues'::regclass AND conname='app_issues_team_fkey') THEN
  ALTER TABLE app_issues ADD CONSTRAINT app_issues_team_fkey FOREIGN KEY(app_id,assignee_team_id) REFERENCES app_issue_teams(app_id,id);
 END IF;
END $issue_replay$;
-- +goose StatementEnd

ALTER TABLE app_issue_ownership_rules ALTER COLUMN assignee_account_id DROP NOT NULL;
ALTER TABLE app_issue_ownership_rules ADD COLUMN IF NOT EXISTS assignee_team_id uuid;
-- +goose StatementBegin
DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='app_issue_ownership_rules'::regclass AND conname='app_issue_ownership_rules_target_check') THEN
  ALTER TABLE app_issue_ownership_rules ADD CONSTRAINT app_issue_ownership_rules_target_check CHECK(num_nonnulls(assignee_account_id,assignee_team_id)=1);
 END IF;
END $issue_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='app_issue_ownership_rules'::regclass AND conname='app_issue_ownership_rules_team_fkey') THEN
  ALTER TABLE app_issue_ownership_rules ADD CONSTRAINT app_issue_ownership_rules_team_fkey FOREIGN KEY(app_id,assignee_team_id) REFERENCES app_issue_teams(app_id,id);
 END IF;
END $issue_replay$;
-- +goose StatementEnd

CREATE INDEX IF NOT EXISTS app_issues_team_inbox_idx ON app_issues(app_id,assignee_team_id,last_seen_at DESC,id DESC) WHERE assignee_team_id IS NOT NULL;

CREATE OR REPLACE VIEW issue_group_rollups AS
 SELECT i.id,
    i.account_id,
    i.app_id,
    i.environment,
    i.fingerprint,
    i.grouping_version,
    i.title,
    i.state,
    i.assignee_account_id,
    totals.first_seen_at,
    totals.last_seen_at,
    totals.event_count,
    totals.regression_count,
    i.resolved_at,
    i.fixed_deployment_id,
    i.fixed_deployment_created_at,
    i.ignored_until,
    i.split_source_issue_id,
    i.assignee_team_id
   FROM (app_issues i
     CROSS JOIN LATERAL ( SELECT min(m.first_seen_at) AS first_seen_at,
            max(m.last_seen_at) AS last_seen_at,
            (sum(m.event_count))::bigint AS event_count,
            (sum(m.regression_count))::bigint AS regression_count
           FROM app_issues m
          WHERE (m.id IN ( SELECT i.id
                UNION ALL
                 SELECT issue_group_links.issue_id
                   FROM issue_group_links
                  WHERE (issue_group_links.canonical_issue_id = i.id)))) totals)
  WHERE (NOT (EXISTS ( SELECT 1
           FROM issue_group_links l
          WHERE (l.issue_id = i.id))));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM app_issue_saved_views WHERE coalesce(filters->>'team','')<>'') OR EXISTS(SELECT 1 FROM app_issue_teams) OR EXISTS(SELECT 1 FROM app_issues WHERE assignee_team_id IS NOT NULL) OR EXISTS(SELECT 1 FROM app_issue_ownership_rules WHERE assignee_team_id IS NOT NULL) THEN
  RAISE EXCEPTION 'issue teams are in use; remove team assignments, routing, teams and saved team filters explicitly before rollback';
 END IF;
END $$;
DROP VIEW issue_group_rollups;
ALTER TABLE app_issues DROP COLUMN assignee_team_id;
ALTER TABLE app_issue_ownership_rules DROP CONSTRAINT app_issue_ownership_rules_target_check, DROP COLUMN assignee_team_id, ALTER COLUMN assignee_account_id SET NOT NULL;
DROP TABLE app_issue_team_members;
DROP TABLE app_issue_teams;
CREATE VIEW issue_group_rollups AS
 SELECT i.id,
    i.account_id,
    i.app_id,
    i.environment,
    i.fingerprint,
    i.grouping_version,
    i.title,
    i.state,
    i.assignee_account_id,
    totals.first_seen_at,
    totals.last_seen_at,
    totals.event_count,
    totals.regression_count,
    i.resolved_at,
    i.fixed_deployment_id,
    i.fixed_deployment_created_at,
    i.ignored_until,
    i.split_source_issue_id
   FROM (app_issues i
     CROSS JOIN LATERAL ( SELECT min(m.first_seen_at) AS first_seen_at,
            max(m.last_seen_at) AS last_seen_at,
            (sum(m.event_count))::bigint AS event_count,
            (sum(m.regression_count))::bigint AS regression_count
           FROM app_issues m
          WHERE (m.id IN ( SELECT i.id
                UNION ALL
                 SELECT issue_group_links.issue_id
                   FROM issue_group_links
                  WHERE (issue_group_links.canonical_issue_id = i.id)))) totals)
  WHERE (NOT (EXISTS ( SELECT 1
           FROM issue_group_links l
          WHERE (l.issue_id = i.id))));
-- +goose StatementEnd
