-- filename: 20261107175452204_request_analytics_hist_int4.sql
-- migration-compat: expand
-- ADR-20261103113952 amendment 3: the rollup latency histogram moves from
-- bigint[] (360 bytes of a 689-byte row) to integer[]; a bucket counts one
-- app's requests in one minute or hour, far below 2^31. Expand step: both
-- tiers gain the nullable latency_hist_int4, and latency_hist becomes
-- nullable, with a CHECK that a row holds at least one of the two. Readers
-- of this release add both columns bucket by bucket. Writers keep writing
-- latency_hist until a later release, whose writer fills latency_hist_int4
-- alone, folding a row's bigint histogram into it when it updates the row,
-- with request_analytics_hist_add_int4. The previous release neither reads
-- nor writes the new column, and its rows satisfy the new CHECKs. New
-- columns are catalog-only (nullable, no default); CHECKs are added NOT
-- VALID and validated separately (SHARE UPDATE EXCLUSIVE), so apid keeps
-- writing. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE request_analytics_minutes ADD COLUMN IF NOT EXISTS latency_hist_int4 integer[];
ALTER TABLE request_analytics_hours ADD COLUMN IF NOT EXISTS latency_hist_int4 integer[];

-- +goose StatementBegin
DO $hist_int4$
DECLARE
  t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['request_analytics_minutes', 'request_analytics_hours'] LOOP
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = t::regclass AND conname = t || '_latency_hist_int4_chk') THEN
      EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I CHECK (latency_hist_int4 IS NULL OR (cardinality(latency_hist_int4) = 45 AND array_ndims(latency_hist_int4) = 1 AND array_position(latency_hist_int4, NULL) IS NULL)) NOT VALID',
        t, t || '_latency_hist_int4_chk');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = t::regclass AND conname = t || '_latency_hist_present_chk') THEN
      EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I CHECK (latency_hist IS NOT NULL OR latency_hist_int4 IS NOT NULL) NOT VALID',
        t, t || '_latency_hist_present_chk');
    END IF;
  END LOOP;
END
$hist_int4$;
-- +goose StatementEnd

ALTER TABLE request_analytics_minutes VALIDATE CONSTRAINT request_analytics_minutes_latency_hist_int4_chk;
ALTER TABLE request_analytics_minutes VALIDATE CONSTRAINT request_analytics_minutes_latency_hist_present_chk;
ALTER TABLE request_analytics_hours VALIDATE CONSTRAINT request_analytics_hours_latency_hist_int4_chk;
ALTER TABLE request_analytics_hours VALIDATE CONSTRAINT request_analytics_hours_latency_hist_present_chk;

ALTER TABLE request_analytics_minutes ALTER COLUMN latency_hist DROP NOT NULL;
ALTER TABLE request_analytics_hours ALTER COLUMN latency_hist DROP NOT NULL;

-- Element-wise sum for the integer histogram's additive upsert, as
-- request_analytics_hist_add is for the bigint one.
CREATE OR REPLACE FUNCTION request_analytics_hist_add_int4(a integer[], b integer[])
RETURNS integer[]
LANGUAGE sql
IMMUTABLE
STRICT
PARALLEL SAFE
AS $$
    SELECT array_agg(x + y ORDER BY i) FROM unnest(a, b) WITH ORDINALITY AS u(x, y, i)
$$;

-- +goose Down
SET lock_timeout = '5s';
-- Rows holding only the integer histogram cannot meet NOT NULL; drop them.
DELETE FROM request_analytics_minutes WHERE latency_hist IS NULL;
DELETE FROM request_analytics_hours WHERE latency_hist IS NULL;
ALTER TABLE request_analytics_minutes ALTER COLUMN latency_hist SET NOT NULL;
ALTER TABLE request_analytics_hours ALTER COLUMN latency_hist SET NOT NULL;
ALTER TABLE request_analytics_minutes DROP CONSTRAINT IF EXISTS request_analytics_minutes_latency_hist_present_chk;
ALTER TABLE request_analytics_minutes DROP CONSTRAINT IF EXISTS request_analytics_minutes_latency_hist_int4_chk;
ALTER TABLE request_analytics_hours DROP CONSTRAINT IF EXISTS request_analytics_hours_latency_hist_present_chk;
ALTER TABLE request_analytics_hours DROP CONSTRAINT IF EXISTS request_analytics_hours_latency_hist_int4_chk;
ALTER TABLE request_analytics_minutes DROP COLUMN IF EXISTS latency_hist_int4;
ALTER TABLE request_analytics_hours DROP COLUMN IF EXISTS latency_hist_int4;
DROP FUNCTION IF EXISTS request_analytics_hist_add_int4(integer[], integer[]);
