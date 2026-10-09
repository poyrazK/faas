-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
 FOREACH t IN ARRAY ARRAY['managed_realtime_channel_messages','managed_realtime_inbox_messages'] LOOP
  EXECUTE format('ALTER TABLE %I ADD COLUMN IF NOT EXISTS target_message_id text NOT NULL DEFAULT %L, ADD COLUMN IF NOT EXISTS version bigint NOT NULL DEFAULT 1 CHECK (version > 0), ADD COLUMN IF NOT EXISTS message_event text NOT NULL DEFAULT %L CHECK (message_event IN (%L,%L,%L)), ADD COLUMN IF NOT EXISTS deleted boolean NOT NULL DEFAULT false',t,'','created','created','updated','deleted');
  EXECUTE format('UPDATE %I SET target_message_id = coalesce(idempotency_key,%L) WHERE target_message_id = %L AND message_event = %L',t,'','','created');
  EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON %I(endpoint_id,channel,target_message_id,sequence DESC)',t || '_target_idx',t);
 END LOOP;
END $$;
-- +goose StatementEnd
ALTER TABLE managed_realtime_inbox_messages ALTER COLUMN idempotency_key DROP NOT NULL;

-- Keep legacy publishers compatible while initial rows gain stable identities.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION faas_realtime_message_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.message_event = 'created' THEN NEW.target_message_id := coalesce(NEW.idempotency_key,''); END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER managed_realtime_channel_message_identity BEFORE INSERT ON managed_realtime_channel_messages FOR EACH ROW EXECUTE FUNCTION faas_realtime_message_identity();
CREATE OR REPLACE TRIGGER managed_realtime_inbox_message_identity BEFORE INSERT ON managed_realtime_inbox_messages FOR EACH ROW EXECUTE FUNCTION faas_realtime_message_identity();

-- Revision rows have no publish dedup key; ACK webhooks use their stable target.
-- +goose StatementBegin
DO $$
DECLARE definition text;
BEGIN
 definition := pg_get_functiondef('faas_advance_realtime_inbox_cursor_before_fallback(uuid,text,text,bigint)'::regprocedure);
 EXECUTE replace(definition,'SELECT idempotency_key INTO mid','SELECT target_message_id INTO mid');
END $$;
-- +goose StatementEnd

-- +goose Down
-- Forward only: deleting mutation metadata could replay a redaction as content.
SELECT 1;
