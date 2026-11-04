-- migration-compat: expand
-- filename: 20261104220407817_automation_live_idempotency_retention.sql

-- +goose Up
-- Compact tombstones outlive pruned workflow runs. No sample input or output
-- is retained; app deletion removes these identities.
CREATE TABLE IF NOT EXISTS workflow_automation_live_retired_keys (
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name text NOT NULL,
    key_hash bytea NOT NULL CHECK (octet_length(key_hash)=32),
    request_fingerprint bytea NOT NULL CHECK (octet_length(request_fingerprint)=32),
    PRIMARY KEY (app_id, name, key_hash)
);

-- Old API and scheduler binaries must not bypass the retained identity while
-- the application is being rolled out. Existing rows are captured on delete.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_automation_live_idempotency() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO workflow_automation_live_retired_keys(app_id,name,key_hash,request_fingerprint)
        VALUES (NEW.app_id,NEW.name,sha256(convert_to(NEW.idempotency_key,'UTF8')),NEW.request_fingerprint)
        ON CONFLICT (app_id,name,key_hash) DO NOTHING;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'live verification idempotency key is already reserved' USING ERRCODE='23505';
        END IF;
        RETURN NEW;
    END IF;
    -- A cascading app deletion must not create a new dependent row.
    IF EXISTS (SELECT 1 FROM apps WHERE id=OLD.app_id AND status<>'deleted') THEN
        INSERT INTO workflow_automation_live_retired_keys(app_id,name,key_hash,request_fingerprint)
        VALUES (OLD.app_id,OLD.name,sha256(convert_to(OLD.idempotency_key,'UTF8')),OLD.request_fingerprint)
        ON CONFLICT (app_id,name,key_hash) DO NOTHING;
    END IF;
    RETURN OLD;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS automation_live_idempotency_guard ON workflow_automation_live_verifications;
CREATE TRIGGER automation_live_idempotency_guard BEFORE INSERT OR DELETE ON workflow_automation_live_verifications
    FOR EACH ROW EXECUTE FUNCTION preserve_automation_live_idempotency();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM workflow_automation_live_retired_keys) THEN
        RAISE EXCEPTION 'cannot remove live-verification idempotency while retired keys exist';
    END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS automation_live_idempotency_guard ON workflow_automation_live_verifications;
DROP FUNCTION IF EXISTS preserve_automation_live_idempotency();
DROP TABLE IF EXISTS workflow_automation_live_retired_keys;
