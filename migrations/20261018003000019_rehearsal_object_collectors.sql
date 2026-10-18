-- +goose Up
SET LOCAL lock_timeout = '5s';
-- A missing ledger row may leave this migration's complete schema behind.
-- Preserve later fences only when every owned object and a known function
-- definition are present; refuse partial or unrecognized schema.
-- +goose StatementBegin
DO $rehearsal_replay$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_object_fixture_immutable()') AND md5(prosrc)=ANY(ARRAY['63d78f6fce7de58b03cca77053b0f510'])) THEN
    RETURN;
  END IF;
  IF NOT (EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_object_fixture_immutable()') AND md5(prosrc)=ANY(ARRAY['d20bd53aba663d5c80bc949237d9b0a0']))) THEN
    RAISE EXCEPTION 'unexpected predecessor fence for rehearsal migration 19';
  END IF;

-- Old unqualified windows have no collector acknowledgement and cannot be adopted.
UPDATE scenario_object_fixtures SET document=jsonb_set(document,'{Failed}','true') WHERE status IN ('ready','sealing');
CREATE OR REPLACE FUNCTION scenario_object_fixture_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE old_collector text; new_collector text; old_closed boolean; new_closed boolean;
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.status NOT IN ('complete','deleted') THEN
   RAISE EXCEPTION 'object fixture requires cleanup before purge' USING ERRCODE='23514';
  END IF;
  RETURN OLD;
 END IF;
 old_collector:=COALESCE(OLD.document->>'CollectorID','');new_collector:=COALESCE(NEW.document->>'CollectorID','');
 old_closed:=COALESCE((OLD.document->>'CollectorClosed')::boolean,false);new_closed:=COALESCE((NEW.document->>'CollectorClosed')::boolean,false);
 IF (NEW.id,NEW.account_id,NEW.run_id,NEW.app_id,NEW.expires_at)
   IS DISTINCT FROM (OLD.id,OLD.account_id,OLD.run_id,OLD.app_id,OLD.expires_at)
 OR (NEW.document - ARRAY['Status','Current','Attempts','Failed','Evidence','CollectorID','CollectorClosed'])
   IS DISTINCT FROM (OLD.document - ARRAY['Status','Current','Attempts','Failed','Evidence','CollectorID','CollectorClosed'])
 OR (OLD.status IN ('complete','deleted') AND NEW IS DISTINCT FROM OLD)
 OR NOT (NEW.status=OLD.status OR (OLD.status='ready' AND NEW.status='sealing')
   OR (OLD.status='sealing' AND NEW.status='complete') OR NEW.status='deleted')
 OR ((OLD.document->>'Failed')::boolean AND NOT (NEW.document->>'Failed')::boolean)
 OR (NEW.status IN ('complete','deleted') AND jsonb_array_length(NEW.document->'Current')<>0)
 OR (old_collector<>'' AND new_collector<>old_collector)
 OR (old_collector='' AND new_collector<>'' AND (OLD.status<>'ready' OR NEW.status<>'ready' OR new_collector !~ '^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$'))
 OR (old_closed AND NOT new_closed)
 OR (NOT old_closed AND new_closed AND (OLD.status<>'sealing' OR NEW.status<>'sealing' OR old_collector=''))
 OR (NEW.status='complete' AND (NOT new_closed OR new_collector='' OR (NEW.document->>'Failed')::boolean))
 THEN RAISE EXCEPTION 'object fixture journal is immutable' USING ERRCODE='23514'; END IF;
 IF NOT old_closed AND new_closed AND EXISTS(SELECT 1 FROM jsonb_array_elements(NEW.document->'Attempts') a WHERE (a->>'Pending')::boolean) THEN
  RAISE EXCEPTION 'object collector has pending requests' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
END
$rehearsal_replay$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- Collector proof is a safety boundary; dropping it requires rolling back the adapter.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM scenario_object_fixtures WHERE status IN ('ready','sealing')) THEN
  RAISE EXCEPTION 'object fixture cleanup is required before rollback';
 END IF;
END $$;
-- +goose StatementEnd
