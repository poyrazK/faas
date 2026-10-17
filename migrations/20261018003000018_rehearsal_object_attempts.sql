-- +goose Up
SET LOCAL lock_timeout = '5s';
-- A missing ledger row may leave this migration's complete schema behind.
-- Preserve later fences only when every owned object and a known function
-- definition are present; refuse partial or unrecognized schema.
-- +goose StatementBegin
DO $rehearsal_replay$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_object_fixtures') AND tgname='scenario_object_attempt_immutable' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_object_attempt_immutable()') AND md5(prosrc)=ANY(ARRAY['b6d987283a7efbb0401b8700b579ab56'])) THEN
    RETURN;
  END IF;
  IF EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_object_fixtures') AND tgname='scenario_object_attempt_immutable' AND NOT tgisinternal)
    OR EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_object_attempt_immutable()') AND md5(prosrc)=ANY(ARRAY['b6d987283a7efbb0401b8700b579ab56'])) THEN
    RAISE EXCEPTION 'partial or unrecognized schema for rehearsal migration 18; inspect recovery before continuing';
  END IF;

CREATE FUNCTION scenario_object_attempt_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE old_attempt jsonb; new_attempt jsonb; ordinal bigint;
BEGIN
 IF jsonb_array_length(NEW.document->'Attempts') < jsonb_array_length(OLD.document->'Attempts')
 OR jsonb_array_length(NEW.document->'Attempts') > 256
 OR (OLD.status<>'ready' AND jsonb_array_length(NEW.document->'Attempts')<>jsonb_array_length(OLD.document->'Attempts')) THEN
  RAISE EXCEPTION 'object admission inventory is immutable' USING ERRCODE='23514';
 END IF;
 FOR old_attempt,ordinal IN SELECT value,ordinality FROM jsonb_array_elements(OLD.document->'Attempts') WITH ORDINALITY LOOP
  new_attempt := NEW.document->'Attempts'->(ordinal::integer-1);
  IF (old_attempt - ARRAY['Pending','Mutation']) IS DISTINCT FROM (new_attempt - ARRAY['Pending','Mutation'])
  OR (NOT (old_attempt->>'Pending')::boolean AND old_attempt IS DISTINCT FROM new_attempt)
  OR ((new_attempt->>'Pending')::boolean AND old_attempt IS DISTINCT FROM new_attempt) THEN
   RAISE EXCEPTION 'object completion receipt is immutable' USING ERRCODE='23514';
  END IF;
 END LOOP;
 RETURN NEW;
END $$;
CREATE TRIGGER scenario_object_attempt_immutable BEFORE UPDATE ON scenario_object_fixtures
 FOR EACH ROW EXECUTE FUNCTION scenario_object_attempt_immutable();
END
$rehearsal_replay$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER scenario_object_attempt_immutable ON scenario_object_fixtures;
DROP FUNCTION scenario_object_attempt_immutable();
