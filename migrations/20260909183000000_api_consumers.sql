-- filename: 20260909183000000_api_consumers.sql
-- +goose Up
-- +goose StatementBegin

-- Stable identities for an application's customers. Credentials are kept in
-- consumer_keys and point at this row, so rotating a credential does not
-- change the identity used for throttling, usage attribution, or billing.
CREATE TABLE IF NOT EXISTS api_consumers (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid NOT NULL,
    app_id uuid NOT NULL,
    external_ref text NOT NULL,
    name text NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    revoked_at timestamp with time zone,
    CONSTRAINT api_consumers_pkey PRIMARY KEY (id),
    CONSTRAINT api_consumers_external_ref_len_chk CHECK ((char_length(external_ref) >= 1) AND (char_length(external_ref) <= 256)),
    CONSTRAINT api_consumers_name_len_chk CHECK ((char_length(name) >= 1) AND (char_length(name) <= 128)),
    CONSTRAINT api_consumers_status_chk CHECK (status = ANY (ARRAY['active'::text, 'revoked'::text])),
    CONSTRAINT api_consumers_revoked_state_chk CHECK ((revoked_at IS NULL) OR (revoked_at >= created_at)),
    CONSTRAINT api_consumers_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT api_consumers_app_id_fkey FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS api_consumers_app_external_ref_uniq
    ON api_consumers (app_id, external_ref);
CREATE INDEX IF NOT EXISTS api_consumers_account_app_idx
    ON api_consumers (account_id, app_id);

CREATE OR REPLACE FUNCTION api_consumers_set_updated_at() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger
         WHERE tgname = 'api_consumers_set_updated_at_trg'
           AND tgrelid = 'api_consumers'::regclass
    ) THEN
        CREATE TRIGGER api_consumers_set_updated_at_trg
            BEFORE UPDATE ON api_consumers
            FOR EACH ROW EXECUTE FUNCTION api_consumers_set_updated_at();
    END IF;
END;
$$;

-- Keep the column nullable during the compatibility window: existing callers
-- of CreateConsumerKey predate stable consumer identities. New credentials
-- should use CreateConsumerKeyForConsumer; a later control-plane migration
-- can enforce NOT NULL after those callers have been cut over.
ALTER TABLE consumer_keys ADD COLUMN IF NOT EXISTS consumer_id uuid;

-- Give pre-existing credentials a deterministic identity without changing
-- their authentication behavior. These rows are marked by a legacy-prefixed
-- external_ref; the CTE only links consumers inserted by this migration, so
-- an exceptionally colliding customer reference can never hijack a key.
WITH inserted_legacy AS (
    INSERT INTO api_consumers (account_id, app_id, external_ref, name)
    SELECT ck.account_id,
           ck.app_id,
           'legacy:' || ck.id::text,
           'legacy-' || left(ck.id::text, 32)
      FROM consumer_keys ck
     WHERE ck.consumer_id IS NULL
    ON CONFLICT (app_id, external_ref) DO NOTHING
    RETURNING id, app_id, external_ref
)
UPDATE consumer_keys ck
   SET consumer_id = il.id
  FROM inserted_legacy il
 WHERE ck.consumer_id IS NULL
   AND il.app_id = ck.app_id
   AND il.external_ref = 'legacy:' || ck.id::text;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'consumer_keys_consumer_id_fkey'
           AND conrelid = 'consumer_keys'::regclass
    ) THEN
        ALTER TABLE consumer_keys
            ADD CONSTRAINT consumer_keys_consumer_id_fkey
            FOREIGN KEY (consumer_id) REFERENCES api_consumers(id) ON DELETE CASCADE;
    END IF;
END;
$$;

CREATE INDEX IF NOT EXISTS consumer_keys_consumer_id_idx
    ON consumer_keys (consumer_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE consumer_keys DROP CONSTRAINT IF EXISTS consumer_keys_consumer_id_fkey;
DROP INDEX IF EXISTS consumer_keys_consumer_id_idx;
ALTER TABLE consumer_keys DROP COLUMN IF EXISTS consumer_id;
DROP TABLE IF EXISTS api_consumers;
DROP FUNCTION IF EXISTS api_consumers_set_updated_at();
-- +goose StatementEnd
