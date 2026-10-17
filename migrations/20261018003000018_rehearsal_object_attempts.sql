-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
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
-- +goose StatementEnd
CREATE TRIGGER scenario_object_attempt_immutable BEFORE UPDATE ON scenario_object_fixtures
 FOR EACH ROW EXECUTE FUNCTION scenario_object_attempt_immutable();
-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER scenario_object_attempt_immutable ON scenario_object_fixtures;
DROP FUNCTION scenario_object_attempt_immutable();
