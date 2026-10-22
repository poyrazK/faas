-- filename: 20261011214416456_object_compute_trigger_ownership.sql

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION object_compute_trigger_ownership_valid(rules jsonb, managed jsonb)
RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE WHEN jsonb_typeof(rules) <> 'array' OR jsonb_typeof(managed) <> 'array' THEN false ELSE
   jsonb_array_length(managed) <= 1000
   AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(managed) m WHERE jsonb_typeof(m) <> 'object' OR jsonb_typeof(m->'id') IS DISTINCT FROM 'string' OR length(m->>'id') = 0 OR NOT EXISTS (SELECT 1 FROM jsonb_array_elements(rules) r WHERE r = m))
   AND (SELECT count(*) = count(DISTINCT m->>'id') FROM jsonb_array_elements(managed) m)
 END
$$;
ALTER TABLE object_bucket_notifications ADD COLUMN IF NOT EXISTS managed_rules jsonb NOT NULL DEFAULT '[]';
ALTER TABLE object_bucket_notifications DROP CONSTRAINT IF EXISTS object_compute_trigger_ownership_check;
ALTER TABLE object_bucket_notifications ADD CONSTRAINT object_compute_trigger_ownership_check
 CHECK (object_compute_trigger_ownership_valid(rules, managed_rules));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM object_bucket_notifications WHERE managed_rules <> '[]'::jsonb) THEN
   RAISE EXCEPTION 'cannot discard active managed compute trigger ownership';
 END IF;
END $$;
ALTER TABLE object_bucket_notifications DROP CONSTRAINT object_compute_trigger_ownership_check;
ALTER TABLE object_bucket_notifications DROP COLUMN managed_rules;
DROP FUNCTION object_compute_trigger_ownership_valid(jsonb,jsonb);
-- +goose StatementEnd
