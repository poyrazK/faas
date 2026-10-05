-- +goose Up
-- +goose StatementBegin
ALTER TABLE triggers ADD COLUMN IF NOT EXISTS queue_binding_id uuid;
DO $replay$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='queue_bindings'::regclass AND conname='queue_bindings_consumer_identity_unique') THEN
    ALTER TABLE queue_bindings ADD CONSTRAINT queue_bindings_consumer_identity_unique
  UNIQUE (id, app_id, account_id);
  END IF;
END $replay$;

-- Adopt only a single historical projection carrying the binding's exact
-- marker and tenant identity. Ambiguous markers remain unowned for review;
-- do not choose a winner or delete delivery evidence during migration.
DO $adopt$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='triggers'::regclass
    AND tgname='queue_consumer_binding_identity_guard') THEN
WITH candidates AS (
  SELECT t.id, b.id AS binding_id, t.config->>'mode' AS mode,
    count(*) OVER (PARTITION BY b.id) AS copies
  FROM triggers t JOIN queue_bindings b
    ON t.config->>'queue_binding_id' = b.id::text
    AND t.app_id = b.app_id AND t.account_id = b.account_id
  WHERE t.kind = 'queue' AND t.source = 'queue'
)
UPDATE triggers t SET queue_binding_id = c.binding_id
FROM candidates c WHERE t.id = c.id AND c.copies = 1 AND c.mode = 'queue';

  END IF;
END $adopt$;

DO $replay$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='triggers'::regclass AND conname='triggers_queue_binding_projection_check') THEN
    ALTER TABLE triggers ADD CONSTRAINT triggers_queue_binding_projection_check
  CHECK (queue_binding_id IS NULL OR (kind = 'queue'
    AND source IS NOT DISTINCT FROM 'queue'
    AND config->>'mode' IS NOT DISTINCT FROM 'queue'
    AND config->>'queue_binding_id' IS NOT DISTINCT FROM queue_binding_id::text));
  END IF;
END $replay$;
DO $replay$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='triggers'::regclass AND conname='triggers_queue_binding_identity_fk') THEN
    ALTER TABLE triggers ADD CONSTRAINT triggers_queue_binding_identity_fk
  FOREIGN KEY (queue_binding_id, app_id, account_id)
  REFERENCES queue_bindings (id, app_id, account_id)
  DEFERRABLE INITIALLY DEFERRED;
  END IF;
END $replay$;
CREATE UNIQUE INDEX IF NOT EXISTS triggers_queue_binding_identity_unique
  ON triggers (queue_binding_id) WHERE queue_binding_id IS NOT NULL;

DO $replay$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
    WHERE p.proname='guard_queue_consumer_binding_identity' AND n.nspname=current_schema()) THEN
    EXECUTE $function$CREATE FUNCTION guard_queue_consumer_binding_identity() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.queue_binding_id IS NOT NULL
    AND (NEW.queue_binding_id IS DISTINCT FROM OLD.queue_binding_id
      OR NEW.app_id IS DISTINCT FROM OLD.app_id
      OR NEW.account_id IS DISTINCT FROM OLD.account_id) THEN
    RAISE EXCEPTION 'queue consumer binding identity is immutable'
      USING ERRCODE = '23514', CONSTRAINT = 'queue_consumer_binding_identity';
  END IF;
  RETURN NEW;
END;
$$;$function$;
  END IF;
END $replay$;
DROP TRIGGER IF EXISTS queue_consumer_binding_identity_guard ON triggers;
CREATE TRIGGER queue_consumer_binding_identity_guard BEFORE UPDATE ON triggers
  FOR EACH ROW EXECUTE FUNCTION guard_queue_consumer_binding_identity();
-- +goose StatementEnd

-- +goose Down
-- Preserve durable ownership, accepted work and runtime evidence on rollback.
SELECT 1;
