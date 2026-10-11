-- ADR-905: preserve existing event vocabularies while admitting pause notifications.
-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE t text; n text; definition text;
BEGIN
  FOREACH t IN ARRAY ARRAY['app_webhook_event_outbox'] LOOP
    n := t || '_event_chk';
    SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
      WHERE conrelid = t::regclass AND conname = n;
    IF definition IS NULL THEN RAISE EXCEPTION 'missing webhook event constraint %', n; END IF;
    IF strpos(definition, 'automation.paused') = 0 THEN
      definition := regexp_replace(definition, ' NOT VALID$', '');
      EXECUTE format('ALTER TABLE %I DROP CONSTRAINT %I', t, n);
      EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I CHECK ((%s) OR event = %L)',
        t, n, substring(definition from 8 for length(definition)-8), 'automation.paused');
    END IF;
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Keep the expanded vocabulary so committed pause notifications remain replayable.
SELECT 1;
