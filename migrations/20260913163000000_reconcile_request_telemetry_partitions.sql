-- +goose Up
-- +goose StatementBegin
-- Repair missing current/future request telemetry partitions. Rows that
-- already landed in the default partition move under an exclusive parent lock
-- before ATTACH, so deployment is idempotent and cannot lose concurrent writes.
DO $$
DECLARE
    month_offset integer;
    range_start timestamptz;
    range_end timestamptz;
    partition_name text;
    is_attached boolean;
BEGIN
    LOCK TABLE public.request_telemetry IN ACCESS EXCLUSIVE MODE;
    FOR month_offset IN 0..2 LOOP
        range_start := date_trunc('month', now()) + make_interval(months => month_offset);
        range_end := range_start + interval '1 month';
        partition_name := 'request_telemetry_' || to_char(range_start, 'YYYYMM');

        SELECT EXISTS (
            SELECT 1
              FROM pg_inherits i
              JOIN pg_class child ON child.oid = i.inhrelid
              JOIN pg_class parent ON parent.oid = i.inhparent
              JOIN pg_namespace ns ON ns.oid = child.relnamespace
             WHERE parent.oid = 'public.request_telemetry'::regclass
               AND ns.nspname = 'public'
               AND child.relname = partition_name
        ) INTO is_attached;
        IF is_attached THEN
            CONTINUE;
        END IF;
        IF to_regclass('public.' || partition_name) IS NOT NULL THEN
            RAISE EXCEPTION 'request telemetry partition % exists but is not attached', partition_name;
        END IF;

        EXECUTE format(
            'CREATE TABLE public.%I (LIKE public.request_telemetry INCLUDING ALL)',
            partition_name);
        EXECUTE format(
            'INSERT INTO public.%I SELECT * FROM public.request_telemetry_default WHERE received_at >= %L AND received_at < %L',
            partition_name, range_start, range_end);
        EXECUTE format(
            'DELETE FROM public.request_telemetry_default WHERE received_at >= %L AND received_at < %L',
            range_start, range_end);
        EXECUTE format(
            'ALTER TABLE public.request_telemetry ATTACH PARTITION public.%I FOR VALUES FROM (%L) TO (%L)',
            partition_name, range_start, range_end);
    END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Forward-only: detaching partitions would move production back to the
-- unbounded default-partition failure mode.
SELECT 1;
