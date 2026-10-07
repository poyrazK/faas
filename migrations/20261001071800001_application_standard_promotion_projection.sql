-- +goose Up
-- PostgreSQL arithmetic operators bind before ->. Group the parent binding
-- before subtracting ignored grant fields; keep the original applied migration.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_native_promotion_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE locked jsonb; parent jsonb; b jsonb; r jsonb; now_nano bigint;
BEGIN
 IF TG_OP='DELETE' THEN
  IF NOT EXISTS(SELECT 1 FROM instances WHERE id=OLD.instance_id) THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'native promotion history is immutable' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
 END IF;
 IF TG_OP='UPDATE' THEN
  IF NEW IS NOT DISTINCT FROM OLD THEN RETURN NEW; END IF;
  IF NEW.token IS DISTINCT FROM OLD.token OR NEW.instance_id IS DISTINCT FROM OLD.instance_id
   OR NEW.parent_token IS DISTINCT FROM OLD.parent_token OR NEW.binding IS DISTINCT FROM OLD.binding
   OR NEW.created_at IS DISTINCT FROM OLD.created_at OR OLD.receipt IS NOT NULL OR NEW.receipt IS NULL THEN
   RAISE EXCEPTION 'native promotion history is immutable' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
  END IF;
 ELSIF NEW.receipt IS NOT NULL THEN
  RAISE EXCEPTION 'promotion receipt requires a saved grant' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
 END IF;
 locked:=application_standard_lock_native_promotion(NEW.instance_id,false);
 parent:=locked->'parent'; b:=NEW.binding; now_nano:=(locked->>'clock_unix_nano')::bigint;
 IF parent->'binding'->>'token' IS DISTINCT FROM NEW.parent_token::text OR NEW.token=NEW.parent_token
  OR b->>'token' IS DISTINCT FROM NEW.token::text
  OR (b-ARRAY['token','payload_hash','issued_at_unix_nano','expires_at_unix_nano']) IS DISTINCT FROM
     ((parent->'binding')-ARRAY['token','payload_hash','issued_at_unix_nano','expires_at_unix_nano'])
  OR coalesce(b->>'payload_hash','') !~ '^[0-9a-f]{64}$'
  OR coalesce((b->>'issued_at_unix_nano')::bigint,0)<=0
  OR coalesce((b->>'expires_at_unix_nano')::bigint,0)<=now_nano
  OR (b->>'expires_at_unix_nano')::bigint <= (b->>'issued_at_unix_nano')::bigint
  OR (b->>'expires_at_unix_nano')::numeric-(b->>'issued_at_unix_nano')::numeric >600000000000
  OR (b->>'issued_at_unix_nano')::bigint>now_nano+5000000000
  OR (TG_OP='INSERT' AND (b->>'issued_at_unix_nano')::bigint<now_nano-5000000000) THEN
  RAISE EXCEPTION 'native promotion grant is stale or invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF TG_OP='UPDATE' THEN
  r:=NEW.receipt;
  IF r->'binding' IS DISTINCT FROM b OR (r->>'paused')::boolean IS DISTINCT FROM false
   OR (r-ARRAY['binding','paused','completed_at_unix_nano']) IS DISTINCT FROM (parent-ARRAY['binding','paused','completed_at_unix_nano'])
   OR coalesce((r->>'completed_at_unix_nano')::bigint,0)<(b->>'issued_at_unix_nano')::bigint-5000000000
   OR (r->>'completed_at_unix_nano')::bigint<(parent->>'completed_at_unix_nano')::bigint
   OR (r->>'completed_at_unix_nano')::bigint >= (b->>'expires_at_unix_nano')::bigint
   OR (r->>'completed_at_unix_nano')::bigint>now_nano+5000000000 OR NEW.received_at IS NULL THEN
   RAISE EXCEPTION 'native promotion receipt is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- Retain fail-closed native promotion validation.
