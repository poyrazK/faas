-- filename: 20261103093241680_issue_http_time_buckets.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE issue_events ADD COLUMN IF NOT EXISTS http_time_buckets jsonb NOT NULL DEFAULT '[]'::jsonb;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION issue_window_weight(sample_time timestamptz, total bigint, buckets jsonb,
 window_start timestamptz, window_end timestamptz, start_inclusive boolean, end_inclusive boolean)
RETURNS bigint LANGUAGE plpgsql STABLE PARALLEL SAFE AS $$
DECLARE result bigint;
BEGIN
 -- A whole second is inside the window, so no histogram needs loading.
 IF (date_trunc('second',sample_time) > window_start OR
    (start_inclusive AND date_trunc('second',sample_time) = window_start))
    AND date_trunc('second',sample_time) + interval '1 second' <= window_end THEN RETURN total; END IF;
 IF jsonb_array_length(buckets) = 0 THEN
  IF (sample_time > window_start OR (start_inclusive AND sample_time = window_start))
   AND (sample_time < window_end OR (end_inclusive AND sample_time = window_end)) THEN RETURN total; END IF;
  RETURN 0;
 END IF;
 SELECT COALESCE(sum((b->>1)::bigint),0)::bigint INTO result
 FROM jsonb_array_elements(buckets) b
 WHERE ((b->>0)::numeric > extract(epoch FROM window_start)*1000 OR
        (start_inclusive AND (b->>0)::numeric = extract(epoch FROM window_start)*1000))
   AND ((b->>0)::numeric < extract(epoch FROM window_end)*1000 OR
        (end_inclusive AND (b->>0)::numeric = extract(epoch FROM window_end)*1000));
 RETURN result;
END $$;
-- +goose StatementEnd
-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM issue_events WHERE jsonb_array_length(http_time_buckets)>0) THEN
  RAISE EXCEPTION 'HTTP occurrence time buckets are retained; dropping them would lose window evidence';
 END IF;
END $$;
-- +goose StatementEnd
DROP FUNCTION IF EXISTS issue_window_weight(timestamptz,bigint,jsonb,timestamptz,timestamptz,boolean,boolean);
ALTER TABLE issue_events DROP COLUMN IF EXISTS http_time_buckets;
