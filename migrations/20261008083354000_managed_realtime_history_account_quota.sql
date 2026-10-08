-- filename: 20261008083354000_managed_realtime_history_account_quota.sql

-- +goose Up
ALTER TABLE managed_realtime_channel_messages
    ADD COLUMN account_id uuid;

UPDATE managed_realtime_channel_messages m
SET account_id = e.account_id
FROM managed_realtime_endpoints e
WHERE e.id = m.endpoint_id;

ALTER TABLE managed_realtime_channel_messages
    ALTER COLUMN account_id SET NOT NULL;

CREATE TABLE managed_realtime_history_account_usage (
    account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    payload_bytes bigint NOT NULL DEFAULT 0 CHECK (payload_bytes >= 0)
);

INSERT INTO managed_realtime_history_account_usage (account_id, payload_bytes)
SELECT account_id, sum(octet_length(data))::bigint
FROM managed_realtime_channel_messages
GROUP BY account_id;

-- The account id is denormalized onto each retained row so deletion accounting
-- remains correct when endpoint or channel cascades remove message rows.
-- +goose StatementBegin
CREATE FUNCTION managed_realtime_history_set_message_account() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    SELECT account_id INTO NEW.account_id
    FROM managed_realtime_endpoints
    WHERE id = NEW.endpoint_id;
    IF NEW.account_id IS NULL THEN
        RAISE EXCEPTION 'managed realtime endpoint does not exist'
            USING ERRCODE = '23503';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION managed_realtime_history_account_usage_delta() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO managed_realtime_history_account_usage (account_id, payload_bytes)
        VALUES (NEW.account_id, octet_length(NEW.data))
        ON CONFLICT (account_id) DO UPDATE
        SET payload_bytes = managed_realtime_history_account_usage.payload_bytes + EXCLUDED.payload_bytes;
        RETURN NEW;
    END IF;

    UPDATE managed_realtime_history_account_usage
    SET payload_bytes = payload_bytes - octet_length(OLD.data)
    WHERE account_id = OLD.account_id;
    RETURN OLD;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER managed_realtime_history_set_message_account_trg
BEFORE INSERT ON managed_realtime_channel_messages
FOR EACH ROW EXECUTE FUNCTION managed_realtime_history_set_message_account();

CREATE TRIGGER managed_realtime_history_account_usage_delta_trg
AFTER INSERT OR DELETE ON managed_realtime_channel_messages
FOR EACH ROW EXECUTE FUNCTION managed_realtime_history_account_usage_delta();

-- +goose Down
DROP TRIGGER IF EXISTS managed_realtime_history_account_usage_delta_trg ON managed_realtime_channel_messages;
DROP TRIGGER IF EXISTS managed_realtime_history_set_message_account_trg ON managed_realtime_channel_messages;
DROP FUNCTION IF EXISTS managed_realtime_history_account_usage_delta();
DROP FUNCTION IF EXISTS managed_realtime_history_set_message_account();
DROP TABLE IF EXISTS managed_realtime_history_account_usage;
ALTER TABLE managed_realtime_channel_messages DROP COLUMN IF EXISTS account_id;
