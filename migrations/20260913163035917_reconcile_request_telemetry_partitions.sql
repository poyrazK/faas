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
    schema_name text := current_schema();
    is_attached boolean;
BEGIN
    EXECUTE format(
        'LOCK TABLE %I.request_telemetry IN ACCESS EXCLUSIVE MODE',
        schema_name);
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
             WHERE parent.oid = format('%I.request_telemetry', schema_name)::regclass
               AND ns.nspname = schema_name
               AND child.relname = partition_name
        ) INTO is_attached;
        IF is_attached THEN
            CONTINUE;
        END IF;
        IF to_regclass(format('%I.%I', schema_name, partition_name)) IS NOT NULL THEN
            RAISE EXCEPTION 'request telemetry partition % exists but is not attached', partition_name;
        END IF;

        EXECUTE format(
            'CREATE TABLE %I.%I (LIKE %I.request_telemetry INCLUDING ALL)',
            schema_name, partition_name, schema_name);
        EXECUTE format(
            'INSERT INTO %I.%I SELECT * FROM %I.request_telemetry_default WHERE received_at >= %L AND received_at < %L',
            schema_name, partition_name, schema_name, range_start, range_end);
        EXECUTE format(
            'DELETE FROM %I.request_telemetry_default WHERE received_at >= %L AND received_at < %L',
            schema_name, range_start, range_end);
        EXECUTE format(
            'ALTER TABLE %I.request_telemetry ATTACH PARTITION %I.%I FOR VALUES FROM (%L) TO (%L)',
            schema_name, schema_name, partition_name, range_start, range_end);
    END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Forward-only: detaching partitions would move production back to the
-- unbounded default-partition failure mode.
SELECT 1;
