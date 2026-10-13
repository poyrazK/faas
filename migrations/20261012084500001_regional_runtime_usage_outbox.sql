-- +goose Up
-- +goose StatementBegin
CREATE TABLE regional_runtime_usage_outbox (
 id bigserial PRIMARY KEY,
 app_id uuid NOT NULL CHECK(app_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 allocation_id uuid NOT NULL CHECK(allocation_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 cell_id uuid NOT NULL CHECK(cell_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 minute timestamptz NOT NULL CHECK(minute=date_trunc('minute',minute)),
 usage_claims jsonb NOT NULL CHECK(jsonb_typeof(usage_claims)='object'),
 envelope jsonb NOT NULL CHECK(jsonb_typeof(envelope)='object'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 ack_claims jsonb CHECK(jsonb_typeof(ack_claims)='object'),
 ack_envelope jsonb CHECK(jsonb_typeof(ack_envelope)='object'),
 CHECK((ack_claims IS NULL)=(ack_envelope IS NULL)),
 UNIQUE(app_id,allocation_id,minute)
);
CREATE INDEX regional_runtime_usage_pending_idx ON regional_runtime_usage_outbox(id) WHERE ack_claims IS NULL;
CREATE FUNCTION guard_regional_runtime_usage_outbox()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE original jsonb;
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'regional usage outbox is retained' USING ERRCODE='23514'; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('gregale-regional-runtime-quota:'||NEW.app_id::text,0));
 IF TG_OP='INSERT' THEN
  SELECT grant_claims INTO original FROM regional_runtime_quota_receipts
   WHERE app_id=NEW.app_id AND allocation_id=NEW.allocation_id AND grant_envelope IS NOT NULL;
  IF original IS NULL OR original IS DISTINCT FROM NEW.usage_claims->'Grant'
   OR NEW.usage_claims->'Grant'->'Binding'->>'CellID' IS DISTINCT FROM NEW.cell_id::text
   OR (NEW.usage_claims->>'MinuteMicros')::bigint IS DISTINCT FROM (extract(epoch FROM NEW.minute)*1000000)::bigint
   OR NEW.ack_claims IS NOT NULL OR NEW.ack_envelope IS NOT NULL THEN
   RAISE EXCEPTION 'regional usage original issuance conflicts' USING ERRCODE='23514';
  END IF;
 ELSE
  IF (NEW.id,NEW.app_id,NEW.allocation_id,NEW.cell_id,NEW.minute,NEW.usage_claims,NEW.envelope,NEW.created_at)
   IS DISTINCT FROM (OLD.id,OLD.app_id,OLD.allocation_id,OLD.cell_id,OLD.minute,OLD.usage_claims,OLD.envelope,OLD.created_at)
   OR OLD.ack_claims IS NOT NULL OR NEW.ack_claims IS NULL OR NEW.ack_envelope IS NULL
   OR NEW.ack_claims->'Usage' IS DISTINCT FROM NEW.usage_claims THEN
   RAISE EXCEPTION 'regional usage outbox cannot be rewritten' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END
$function$;
CREATE TRIGGER regional_runtime_usage_outbox_guard BEFORE INSERT OR UPDATE OR DELETE ON regional_runtime_usage_outbox
 FOR EACH ROW EXECUTE FUNCTION guard_regional_runtime_usage_outbox();
CREATE FUNCTION refuse_regional_runtime_usage_outbox_truncate()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN RAISE EXCEPTION 'regional usage outbox is retained' USING ERRCODE='23514'; END
$function$;
CREATE TRIGGER regional_runtime_usage_outbox_truncate_guard BEFORE TRUNCATE ON regional_runtime_usage_outbox
 FOR EACH STATEMENT EXECUTE FUNCTION refuse_regional_runtime_usage_outbox_truncate();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
LOCK TABLE regional_runtime_usage_outbox IN ACCESS EXCLUSIVE MODE;
DO $function$
BEGIN
 IF EXISTS(SELECT 1 FROM regional_runtime_usage_outbox) THEN
  RAISE EXCEPTION 'cannot discard retained regional usage outbox' USING ERRCODE='23514';
 END IF;
END
$function$;
DROP TABLE regional_runtime_usage_outbox;
DROP FUNCTION guard_regional_runtime_usage_outbox();
DROP FUNCTION refuse_regional_runtime_usage_outbox_truncate();
-- +goose StatementEnd
