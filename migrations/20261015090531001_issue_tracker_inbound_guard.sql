-- +goose Up
CREATE TABLE IF NOT EXISTS issue_tracker_inbound_guards (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0)
);

INSERT INTO issue_tracker_inbound_guards(app_id) SELECT id FROM apps ON CONFLICT DO NOTHING;
-- +goose StatementBegin


-- Intent changes advance a durable app-wide fence. Traffic counters, token
-- refreshes and scheduler timestamps do not invalidate an owner's preview.
CREATE OR REPLACE FUNCTION issue_tracker_inbound_bump() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target uuid;
BEGIN
 IF TG_TABLE_NAME = 'apps' THEN
  target := NEW.id;
 ELSIF TG_TABLE_NAME = 'issue_tracker_links' THEN
  IF TG_OP = 'DELETE' THEN
   SELECT app_id INTO target FROM app_issues WHERE id=OLD.issue_id;
  ELSE
   SELECT app_id INTO target FROM app_issues WHERE id=NEW.issue_id;
  END IF;
 ELSIF TG_OP = 'DELETE' THEN
  target := OLD.app_id;
 ELSE
  target := NEW.app_id;
 END IF;
 INSERT INTO issue_tracker_inbound_guards(app_id) SELECT target WHERE EXISTS(SELECT 1 FROM apps WHERE id=target)
 ON CONFLICT(app_id) DO UPDATE SET revision=issue_tracker_inbound_guards.revision+1;
 RETURN NULL;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin

CREATE OR REPLACE FUNCTION issue_tracker_inbound_install_bump() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO issue_tracker_inbound_guards(app_id)
 SELECT id FROM apps WHERE
  (TG_OP <> 'INSERT' AND github_install_id=OLD.installation_id AND github_install_account_id=OLD.account_id) OR
  (TG_OP <> 'DELETE' AND github_install_id=NEW.installation_id AND github_install_account_id=NEW.account_id)
 ORDER BY id
 ON CONFLICT(app_id) DO UPDATE SET revision=issue_tracker_inbound_guards.revision+1;
 RETURN NULL;
END $$;
-- +goose StatementEnd

CREATE OR REPLACE TRIGGER issue_inbound_intent_insert AFTER INSERT ON app_issues FOR EACH ROW EXECUTE FUNCTION issue_tracker_inbound_bump();

CREATE OR REPLACE TRIGGER issue_inbound_intent_update AFTER UPDATE ON app_issues FOR EACH ROW
 WHEN (ROW(OLD.state,OLD.assignee_account_id,OLD.assignee_team_id,OLD.resolved_at,OLD.fixed_deployment_id,OLD.fixed_deployment_created_at,OLD.ignored_until,OLD.fingerprint,OLD.grouping_version,OLD.environment,OLD.split_source_issue_id)
 IS DISTINCT FROM ROW(NEW.state,NEW.assignee_account_id,NEW.assignee_team_id,NEW.resolved_at,NEW.fixed_deployment_id,NEW.fixed_deployment_created_at,NEW.ignored_until,NEW.fingerprint,NEW.grouping_version,NEW.environment,NEW.split_source_issue_id))
 EXECUTE FUNCTION issue_tracker_inbound_bump();

CREATE OR REPLACE TRIGGER issue_inbound_intent_delete AFTER DELETE ON app_issues FOR EACH ROW EXECUTE FUNCTION issue_tracker_inbound_bump();

CREATE OR REPLACE TRIGGER issue_inbound_group AFTER INSERT OR UPDATE OR DELETE ON issue_group_links FOR EACH ROW EXECUTE FUNCTION issue_tracker_inbound_bump();

CREATE OR REPLACE TRIGGER issue_inbound_rules AFTER INSERT OR UPDATE OR DELETE ON issue_split_rules FOR EACH ROW EXECUTE FUNCTION issue_tracker_inbound_bump();

CREATE OR REPLACE TRIGGER issue_inbound_links AFTER INSERT OR UPDATE OR DELETE ON issue_tracker_links FOR EACH ROW EXECUTE FUNCTION issue_tracker_inbound_bump();

CREATE OR REPLACE TRIGGER issue_inbound_policy_insert AFTER INSERT ON issue_tracker_auto_sync FOR EACH ROW EXECUTE FUNCTION issue_tracker_inbound_bump();

CREATE OR REPLACE TRIGGER issue_inbound_policy_update AFTER UPDATE ON issue_tracker_auto_sync FOR EACH ROW
 WHEN (ROW(OLD.revision,OLD.enabled,OLD.source_issue_id,OLD.binding_fingerprint,OLD.baseline,OLD.source_state,OLD.pending_state,OLD.outcome,OLD.reason,OLD.claim_id)
 IS DISTINCT FROM ROW(NEW.revision,NEW.enabled,NEW.source_issue_id,NEW.binding_fingerprint,NEW.baseline,NEW.source_state,NEW.pending_state,NEW.outcome,NEW.reason,NEW.claim_id))
 EXECUTE FUNCTION issue_tracker_inbound_bump();

CREATE OR REPLACE TRIGGER issue_inbound_policy_delete AFTER DELETE ON issue_tracker_auto_sync FOR EACH ROW EXECUTE FUNCTION issue_tracker_inbound_bump();

CREATE OR REPLACE TRIGGER issue_inbound_linear_insert AFTER INSERT ON issue_tracker_linear_connections FOR EACH ROW EXECUTE FUNCTION issue_tracker_inbound_bump();

CREATE OR REPLACE TRIGGER issue_inbound_linear_update AFTER UPDATE ON issue_tracker_linear_connections FOR EACH ROW
 WHEN (ROW(OLD.revision,OLD.account_id,OLD.connected,OLD.workspace_id,OLD.team_id,OLD.viewer_id,OLD.auth_method,OLD.oauth_status IN ('reconnect_required','revoking'))
 IS DISTINCT FROM ROW(NEW.revision,NEW.account_id,NEW.connected,NEW.workspace_id,NEW.team_id,NEW.viewer_id,NEW.auth_method,NEW.oauth_status IN ('reconnect_required','revoking')))
 EXECUTE FUNCTION issue_tracker_inbound_bump();

CREATE OR REPLACE TRIGGER issue_inbound_linear_delete AFTER DELETE ON issue_tracker_linear_connections FOR EACH ROW EXECUTE FUNCTION issue_tracker_inbound_bump();

CREATE OR REPLACE TRIGGER issue_inbound_app AFTER UPDATE ON apps FOR EACH ROW
 WHEN (ROW(OLD.account_id,OLD.status,OLD.deleted_at,OLD.github_install_id,OLD.github_repo_full_name,OLD.github_owner_id,OLD.github_repo_id,OLD.github_install_binding_id,OLD.github_install_account_id)
 IS DISTINCT FROM ROW(NEW.account_id,NEW.status,NEW.deleted_at,NEW.github_install_id,NEW.github_repo_full_name,NEW.github_owner_id,NEW.github_repo_id,NEW.github_install_binding_id,NEW.github_install_account_id))
 EXECUTE FUNCTION issue_tracker_inbound_bump();

CREATE OR REPLACE TRIGGER issue_inbound_install_insert AFTER INSERT ON github_installations FOR EACH ROW EXECUTE FUNCTION issue_tracker_inbound_install_bump();

CREATE OR REPLACE TRIGGER issue_inbound_install_delete AFTER DELETE ON github_installations FOR EACH ROW EXECUTE FUNCTION issue_tracker_inbound_install_bump();

CREATE OR REPLACE TRIGGER issue_inbound_install_update AFTER UPDATE ON github_installations FOR EACH ROW
 WHEN (ROW(OLD.account_id,OLD.installation_id,OLD.org_id) IS DISTINCT FROM ROW(NEW.account_id,NEW.installation_id,NEW.org_id))
 EXECUTE FUNCTION issue_tracker_inbound_install_bump();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM issue_tracker_inbound_guards) THEN
  RAISE EXCEPTION 'Inbound intent revisions must be preserved; rollback requires an empty guard table';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER issue_inbound_install_update ON github_installations;
DROP TRIGGER issue_inbound_install_delete ON github_installations;
DROP TRIGGER issue_inbound_install_insert ON github_installations;
DROP TRIGGER issue_inbound_app ON apps;
DROP TRIGGER issue_inbound_linear_delete ON issue_tracker_linear_connections;
DROP TRIGGER issue_inbound_linear_update ON issue_tracker_linear_connections;
DROP TRIGGER issue_inbound_linear_insert ON issue_tracker_linear_connections;
DROP TRIGGER issue_inbound_policy_delete ON issue_tracker_auto_sync;
DROP TRIGGER issue_inbound_policy_update ON issue_tracker_auto_sync;
DROP TRIGGER issue_inbound_policy_insert ON issue_tracker_auto_sync;
DROP TRIGGER issue_inbound_links ON issue_tracker_links;
DROP TRIGGER issue_inbound_rules ON issue_split_rules;
DROP TRIGGER issue_inbound_group ON issue_group_links;
DROP TRIGGER issue_inbound_intent_delete ON app_issues;
DROP TRIGGER issue_inbound_intent_update ON app_issues;
DROP TRIGGER issue_inbound_intent_insert ON app_issues;
DROP FUNCTION issue_tracker_inbound_install_bump();
DROP FUNCTION issue_tracker_inbound_bump();
DROP TABLE issue_tracker_inbound_guards;
