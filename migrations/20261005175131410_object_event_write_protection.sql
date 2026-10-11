-- filename: 20261005175131410_object_event_write_protection.sql
-- +goose Up
-- +goose StatementBegin
-- Independent validators keep event snapshots valid if the fixed-write
-- migration is replayed. Historical triggers still enforce immutable ownership,
-- aware dispatch and exact-version verified settlement.
CREATE OR REPLACE FUNCTION valid_object_event_write_protection(p jsonb) RETURNS boolean LANGUAGE plpgsql IMMUTABLE STRICT AS $$
DECLARE r jsonb; d jsonb; t timestamptz; minimum timestamptz; period jsonb; n integer;
BEGIN
 IF p='{}' THEN RETURN true; END IF;
 IF jsonb_typeof(p) IS DISTINCT FROM 'object' OR octet_length(p::text)>16384 OR p-ARRAY['enabled','revision','captured_at','default_retention','requested']<>'{}' OR p->'enabled' IS DISTINCT FROM 'true'::jsonb OR
  p ? 'revision' AND (jsonb_typeof(p->'revision') IS DISTINCT FROM 'number' OR p->>'revision' !~ '^[0-9]+$' OR (p->>'revision')::numeric NOT BETWEEN 0 AND 9007199254740991) OR jsonb_typeof(p->'captured_at') IS DISTINCT FROM 'string' THEN RETURN false; END IF;
 t:=(p->>'captured_at')::timestamptz;
 IF NOT isfinite(t) OR extract(year from t) NOT BETWEEN 1 AND 9999 THEN RETURN false; END IF;
 IF p ? 'default_retention' AND NOT coalesce(valid_object_lock_configuration(jsonb_build_object('enabled',true,'default_retention',p->'default_retention')),false) THEN RETURN false; END IF;
 r:=coalesce(p->'requested','{}'::jsonb);
 IF jsonb_typeof(r) IS DISTINCT FROM 'object' OR r-ARRAY['retention','legal_hold']<>'{}' THEN RETURN false; END IF;
 IF r ? 'retention' THEN
  d:=r->'retention';
  IF d='{}' OR NOT object_event_hold_retention_valid(d,true) OR NOT (d->>'event_hold'='ON' OR d ? 'retain_until_date') THEN RETURN false; END IF;
  IF d ? 'retain_until_date' AND date_trunc('milliseconds',(d->>'retain_until_date')::timestamptz)<>(d->>'retain_until_date')::timestamptz THEN RETURN false; END IF;
  IF d->>'event_hold'='ON' THEN period:=d->'event_hold_duration'; END IF;
 ELSE
  d:=p->'default_retention';
  period:=d->'default_event_hold';
  IF d ? 'days' THEN minimum:=t+(d->>'days')::integer*interval '1 day'; END IF;
  IF d ? 'years' THEN minimum:=t+(d->>'years')::integer*interval '1 year'; END IF;
  IF minimum IS NOT NULL AND extract(year FROM minimum) NOT BETWEEN 1 AND 9999 THEN RETURN false; END IF;
 END IF;
 IF period IS NOT NULL THEN
  n:=CASE WHEN period ? 'days' THEN (period->>'days')::integer ELSE (period->>'years')::integer*365 END;
  minimum:=t+n*interval '1 day';
  IF extract(year FROM minimum) NOT BETWEEN 1 AND 9999 THEN RETURN false; END IF;
 END IF;
 IF r ? 'legal_hold' AND (jsonb_typeof(r->'legal_hold') IS DISTINCT FROM 'object' OR (r->'legal_hold')-ARRAY['status']<>'{}' OR coalesce(r->'legal_hold'->>'status','') NOT IN ('ON','OFF')) THEN RETURN false; END IF;
 RETURN true;
EXCEPTION WHEN OTHERS THEN RETURN false;
END $$;

CREATE OR REPLACE FUNCTION valid_object_event_protected_url_request(r jsonb) RETURNS boolean LANGUAGE plpgsql IMMUTABLE STRICT AS $$
BEGIN
 IF NOT coalesce(valid_object_protected_url_request(r-'protection'),false) THEN RETURN false; END IF;
 IF NOT r ? 'protection' THEN RETURN true; END IF;
 RETURN coalesce(r->>'method'='PUT' AND NOT r ? 'multipart' AND r->'protection'<>'{}' AND
  valid_object_event_write_protection(jsonb_build_object('enabled',true,'captured_at','2026-01-01T00:00:00Z','requested',r->'protection')),false);
EXCEPTION WHEN OTHERS THEN RETURN false;
END $$;

DO $$ DECLARE c record; BEGIN
 FOR c IN SELECT conrelid::regclass AS tbl,conname FROM pg_constraint WHERE conrelid IN ('object_upload_completions'::regclass,'object_storage_multipart_uploads'::regclass)
  AND contype='c' AND pg_get_constraintdef(oid) LIKE '%valid_object_write_protection(%' LOOP
  EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I',c.tbl,c.conname);
 END LOOP;
END $$;
ALTER TABLE object_upload_completions DROP CONSTRAINT IF EXISTS object_upload_event_protection_snapshot;
ALTER TABLE object_upload_completions ADD CONSTRAINT object_upload_event_protection_snapshot CHECK(valid_object_event_write_protection(protection_snapshot));
ALTER TABLE object_storage_multipart_uploads DROP CONSTRAINT IF EXISTS object_multipart_event_protection_snapshot;
ALTER TABLE object_storage_multipart_uploads ADD CONSTRAINT object_multipart_event_protection_snapshot CHECK(valid_object_event_write_protection(protection_snapshot));
ALTER TABLE object_storage_s3_credentials DROP CONSTRAINT IF EXISTS object_storage_s3_credentials_url_request_check;
ALTER TABLE object_storage_s3_credentials DROP CONSTRAINT IF EXISTS object_s3_event_protected_url_request;
ALTER TABLE object_storage_s3_credentials ADD CONSTRAINT object_s3_event_protected_url_request CHECK(url_request IS NULL OR valid_object_event_protected_url_request(url_request));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_upload_completions WHERE protection_snapshot->'default_retention' ? 'default_event_hold' OR protection_snapshot->'requested'->'retention' ? 'event_hold') OR
  EXISTS(SELECT 1 FROM object_storage_multipart_uploads WHERE protection_snapshot->'default_retention' ? 'default_event_hold' OR protection_snapshot->'requested'->'retention' ? 'event_hold') OR
  EXISTS(SELECT 1 FROM object_storage_s3_credentials WHERE url_request->'protection'->'retention' ? 'event_hold') THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Preserve event write protection history before rollback';
 END IF;
END $$;
ALTER TABLE object_upload_completions DROP CONSTRAINT object_upload_event_protection_snapshot;
ALTER TABLE object_upload_completions ADD CONSTRAINT object_upload_completions_protection_snapshot_check CHECK(valid_object_write_protection(protection_snapshot));
ALTER TABLE object_storage_multipart_uploads DROP CONSTRAINT object_multipart_event_protection_snapshot;
ALTER TABLE object_storage_multipart_uploads ADD CONSTRAINT object_storage_multipart_uploads_protection_snapshot_check CHECK(valid_object_write_protection(protection_snapshot));
ALTER TABLE object_storage_s3_credentials DROP CONSTRAINT object_s3_event_protected_url_request;
ALTER TABLE object_storage_s3_credentials ADD CONSTRAINT object_storage_s3_credentials_url_request_check CHECK(url_request IS NULL OR valid_object_protected_url_request(url_request));
DROP FUNCTION valid_object_event_protected_url_request(jsonb);
DROP FUNCTION valid_object_event_write_protection(jsonb);
-- +goose StatementEnd
