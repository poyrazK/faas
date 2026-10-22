-- +goose Up
CREATE SEQUENCE IF NOT EXISTS issue_tracker_source_revision_seq AS bigint;

ALTER TABLE issue_tracker_inbound_guards ADD COLUMN IF NOT EXISTS source_revision bigint NOT NULL DEFAULT nextval('issue_tracker_source_revision_seq') CHECK(source_revision>0);

CREATE TABLE IF NOT EXISTS issue_tracker_source_guards(
 issue_id uuid PRIMARY KEY REFERENCES app_issues(id) ON DELETE CASCADE,
 revision bigint NOT NULL DEFAULT nextval('issue_tracker_source_revision_seq') CHECK(revision>0)
);

INSERT INTO issue_tracker_source_guards(issue_id) SELECT id FROM app_issues ON CONFLICT DO NOTHING;

ALTER TABLE issue_tracker_auto_sync ADD COLUMN IF NOT EXISTS direction text NOT NULL DEFAULT 'outbound' CHECK(direction IN ('outbound','inbound','both'));
ALTER TABLE issue_tracker_auto_sync ADD COLUMN IF NOT EXISTS source_revision bigint NOT NULL DEFAULT 0 CHECK(source_revision>=0);
ALTER TABLE issue_tracker_auto_sync ADD COLUMN IF NOT EXISTS pending_source_revision bigint NOT NULL DEFAULT 0 CHECK(pending_source_revision>=0);
ALTER TABLE issue_tracker_auto_sync ADD COLUMN IF NOT EXISTS observed_state text NOT NULL DEFAULT '' CHECK(observed_state IN ('','open','resolved'));
-- +goose StatementBegin
DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_tracker_auto_sync'::regclass AND conname='issue_tracker_inbound_revision_required') THEN
  ALTER TABLE issue_tracker_auto_sync ADD CONSTRAINT issue_tracker_inbound_revision_required CHECK(direction='outbound' OR source_revision>0);
 END IF;
END $issue_replay$;
-- +goose StatementEnd
-- +goose StatementBegin

CREATE OR REPLACE FUNCTION issue_tracker_source_bump(target uuid) RETURNS void LANGUAGE sql AS $$
 INSERT INTO issue_tracker_source_guards(issue_id) SELECT id FROM app_issues WHERE id=target
 ON CONFLICT(issue_id) DO UPDATE SET revision=nextval('issue_tracker_source_revision_seq');
$$;
-- +goose StatementEnd
-- +goose StatementBegin

CREATE OR REPLACE FUNCTION issue_tracker_inbound_bump() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target uuid;
 source_change boolean;
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
 source_change := TG_TABLE_NAME IN ('apps','issue_tracker_linear_connections');
 INSERT INTO issue_tracker_inbound_guards(app_id) SELECT target WHERE EXISTS(SELECT 1 FROM apps WHERE id=target)
 ON CONFLICT(app_id) DO UPDATE SET revision=issue_tracker_inbound_guards.revision+1,
 source_revision=CASE WHEN source_change THEN nextval('issue_tracker_source_revision_seq') ELSE issue_tracker_inbound_guards.source_revision END;
 IF TG_TABLE_NAME='app_issues' THEN
  IF TG_OP<>'DELETE' THEN PERFORM issue_tracker_source_bump(NEW.id); END IF;
 ELSIF TG_TABLE_NAME='issue_group_links' THEN
  IF TG_OP<>'INSERT' THEN
   PERFORM issue_tracker_source_bump(OLD.issue_id);
   PERFORM issue_tracker_source_bump(OLD.canonical_issue_id);
  END IF;
  IF TG_OP<>'DELETE' THEN
   PERFORM issue_tracker_source_bump(NEW.issue_id);
   PERFORM issue_tracker_source_bump(NEW.canonical_issue_id);
  END IF;
 ELSIF TG_TABLE_NAME='issue_split_rules' THEN
  IF TG_OP<>'INSERT' THEN
   PERFORM issue_tracker_source_bump(OLD.source_issue_id);
   PERFORM issue_tracker_source_bump(OLD.target_issue_id);
  END IF;
  IF TG_OP<>'DELETE' THEN
   PERFORM issue_tracker_source_bump(NEW.source_issue_id);
   PERFORM issue_tracker_source_bump(NEW.target_issue_id);
  END IF;
 ELSIF TG_TABLE_NAME='issue_tracker_links' THEN
  IF TG_OP<>'INSERT' THEN PERFORM issue_tracker_source_bump(OLD.issue_id); END IF;
  IF TG_OP<>'DELETE' THEN PERFORM issue_tracker_source_bump(NEW.issue_id); END IF;
 END IF;
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
 ON CONFLICT(app_id) DO UPDATE SET revision=issue_tracker_inbound_guards.revision+1,source_revision=nextval('issue_tracker_source_revision_seq');
 RETURN NULL;
END $$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS issue_inbound_policy_update ON issue_tracker_auto_sync;

CREATE OR REPLACE TRIGGER issue_inbound_policy_update AFTER UPDATE ON issue_tracker_auto_sync FOR EACH ROW
 WHEN (ROW(OLD.revision,OLD.enabled,OLD.source_issue_id,OLD.binding_fingerprint,OLD.baseline,OLD.source_state,OLD.pending_state,OLD.outcome,OLD.reason,OLD.claim_id,OLD.direction,OLD.source_revision,OLD.pending_source_revision,OLD.observed_state)
 IS DISTINCT FROM ROW(NEW.revision,NEW.enabled,NEW.source_issue_id,NEW.binding_fingerprint,NEW.baseline,NEW.source_state,NEW.pending_state,NEW.outcome,NEW.reason,NEW.claim_id,NEW.direction,NEW.source_revision,NEW.pending_source_revision,NEW.observed_state))
 EXECUTE FUNCTION issue_tracker_inbound_bump();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'Directional sync and source intent history must be preserved; rollback is not supported';
END $$;
-- +goose StatementEnd
