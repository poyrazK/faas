-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $do$
DECLARE definition text;
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_webhooks'::regclass AND conname = 'app_webhooks_run_scope_chk') THEN RETURN; END IF;
 SELECT pg_get_constraintdef(oid) INTO STRICT definition FROM pg_constraint WHERE conrelid='app_webhooks'::regclass AND conname='app_webhooks_scope_chk';
 IF position('execution.finished' IN definition) > 0 THEN RETURN; END IF;
 definition := replace(definition, '''agent_run.finished''::text', '''agent_run.finished''::text, ''execution.finished''::text');
 definition := replace(definition, 'cardinality(event_filter) <= 9', 'cardinality(event_filter) <= 10');
 IF position('execution.finished' IN definition)=0 THEN RAISE EXCEPTION 'unexpected account receiver constraint'; END IF;
 EXECUTE 'ALTER TABLE app_webhooks ADD CONSTRAINT app_webhooks_run_scope_chk ' || definition || ' NOT VALID';
END;
$do$;
-- +goose StatementEnd
-- +goose Down
SELECT 1;
