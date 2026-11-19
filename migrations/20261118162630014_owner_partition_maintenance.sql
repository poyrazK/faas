-- filename: 20261118162630014_owner_partition_maintenance.sql
--
-- meterd keeps request_telemetry and log_events partitioned by month: it
-- creates the next months' partitions and drops those past retention. Since
-- the runtime roles lost DDL (ADR-20261023104734), meterd's login cannot: on
-- prod every hourly drop failed with "must be owner of table
-- request_telemetry_202610" (FaasDBPermissionDenied, 2026-11-17), and the
-- first create (the 2027-02 partitions, due 2026-12-01) would fail the same
-- way. These two functions run that maintenance as the schema owner and do
-- nothing else, so meterd only needs EXECUTE on them.
--
-- SECURITY DEFINER with search_path pinned to pg_catalog, pg_temp: every
-- table is qualified with the schema of the parent the caller names. The
-- parent must be one of the two tables, partitioned, and owned by the
-- function's owner, so a caller cannot point them at anything else.
--
-- migration-compat: contract
-- migration-lint: allow dynamic-ddl: the DROP runs later, from meterd, only on monthly partitions past retention, which the previous release's meterd drops the same way; the migration itself drops nothing

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION monthly_partitions_ensure(parent regclass) RETURNS void
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path = pg_catalog, pg_temp
    AS $function$
DECLARE
    schema_name text;
    table_name text;
    time_column text;
    month_offset integer;
    range_start timestamptz;
    range_end timestamptz;
    partition_name text;
    attached_count integer;
    month_zero timestamp;
BEGIN
    SELECT n.nspname, c.relname INTO schema_name, table_name
      FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE c.oid = parent AND c.relkind = 'p' AND c.relowner = current_user::regrole;
    time_column := CASE table_name WHEN 'request_telemetry' THEN 'received_at' WHEN 'log_events' THEN 'occurred_at' END;
    IF time_column IS NULL THEN
        RAISE EXCEPTION 'monthly_partitions_ensure: % is not a maintained partitioned table', parent;
    END IF;

    -- log_events months are UTC months; request_telemetry's follow the
    -- session time zone, as the partitions the migrations created do (UTC on
    -- every production host).
    month_zero := CASE table_name
        WHEN 'log_events' THEN date_trunc('month', now() AT TIME ZONE 'UTC')
        ELSE date_trunc('month', now())::timestamp END;

    -- Every hourly pass but about one a month finds the three months
    -- attached and returns before taking any lock.
    SELECT count(*) INTO attached_count
      FROM generate_series(0, 2) AS n
     WHERE EXISTS (
         SELECT 1 FROM pg_inherits i JOIN pg_class child ON child.oid = i.inhrelid
          WHERE i.inhparent = parent
            AND child.relname = table_name || '_' || to_char(month_zero + make_interval(months => n), 'YYYYMM'));
    IF attached_count = 3 THEN
        RETURN;
    END IF;

    -- ACCESS EXCLUSIVE makes moving rows out of the default partition and
    -- attaching the month atomic with inserts. A short lock timeout makes the
    -- pass retry next hour instead of queueing ahead of every reader.
    PERFORM set_config('lock_timeout', '2s', true);
    EXECUTE format('LOCK TABLE %I.%I IN ACCESS EXCLUSIVE MODE', schema_name, table_name);
    FOR month_offset IN 0..2 LOOP
        partition_name := table_name || '_' || to_char(month_zero + make_interval(months => month_offset), 'YYYYMM');
        IF table_name = 'log_events' THEN
            range_start := (month_zero + make_interval(months => month_offset)) AT TIME ZONE 'UTC';
            range_end := (month_zero + make_interval(months => month_offset + 1)) AT TIME ZONE 'UTC';
        ELSE
            range_start := (month_zero + make_interval(months => month_offset))::timestamptz;
            range_end := (month_zero + make_interval(months => month_offset + 1))::timestamptz;
        END IF;
        IF EXISTS (SELECT 1 FROM pg_inherits i JOIN pg_class child ON child.oid = i.inhrelid
                    WHERE i.inhparent = parent AND child.relname = partition_name) THEN
            CONTINUE;
        END IF;
        IF to_regclass(format('%I.%I', schema_name, partition_name)) IS NOT NULL THEN
            RAISE EXCEPTION '% partition % exists but is not attached', table_name, partition_name;
        END IF;
        EXECUTE format('CREATE TABLE %I.%I (LIKE %I.%I INCLUDING ALL)', schema_name, partition_name, schema_name, table_name);
        EXECUTE format('INSERT INTO %I.%I SELECT * FROM %I.%I WHERE %I >= %L AND %I < %L',
            schema_name, partition_name, schema_name, table_name || '_default', time_column, range_start, time_column, range_end);
        EXECUTE format('DELETE FROM %I.%I WHERE %I >= %L AND %I < %L',
            schema_name, table_name || '_default', time_column, range_start, time_column, range_end);
        EXECUTE format('ALTER TABLE %I.%I ATTACH PARTITION %I.%I FOR VALUES FROM (%L) TO (%L)',
            schema_name, table_name, schema_name, partition_name, range_start, range_end);
    END LOOP;
END;
$function$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION monthly_partitions_drop_expired(parent regclass) RETURNS integer
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path = pg_catalog, pg_temp
    AS $function$
DECLARE
    schema_name text;
    table_name text;
    keep interval;
    partname text;
    partstart date;
    dropped integer := 0;
BEGIN
    SELECT n.nspname, c.relname INTO schema_name, table_name
      FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE c.oid = parent AND c.relkind = 'p' AND c.relowner = current_user::regrole;
    -- The longest plan retention of each table: Scale keeps 14 days of
    -- request telemetry and 90 days of logs.
    keep := CASE table_name WHEN 'request_telemetry' THEN interval '14 days' WHEN 'log_events' THEN interval '90 days' END;
    IF keep IS NULL THEN
        RAISE EXCEPTION 'monthly_partitions_drop_expired: % is not a maintained partitioned table', parent;
    END IF;

    -- DROP takes ACCESS EXCLUSIVE on the parent; queued behind a long reader
    -- it would block every later one. Retry on the next pass instead.
    PERFORM set_config('lock_timeout', '2s', true);
    FOR partname IN
        SELECT child.relname FROM pg_inherits i JOIN pg_class child ON child.oid = i.inhrelid
         WHERE i.inhparent = parent
           AND child.relname ~ ('^' || table_name || '_[0-9]{6}$')
         ORDER BY child.relname
    LOOP
        BEGIN
            partstart := to_date(substring(partname from '[0-9]{6}$'), 'YYYYMM');
        EXCEPTION WHEN OTHERS THEN
            CONTINUE;
        END;
        IF (CASE table_name WHEN 'log_events' THEN (partstart + interval '1 month') AT TIME ZONE 'UTC'
                ELSE (partstart + interval '1 month')::timestamptz END) <= now() - keep THEN
            EXECUTE format('DROP TABLE IF EXISTS %I.%I', schema_name, partname);
            dropped := dropped + 1;
        END IF;
    END LOOP;
    RETURN dropped;
END;
$function$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $grant$
BEGIN
    EXECUTE 'REVOKE ALL ON FUNCTION monthly_partitions_ensure(regclass) FROM PUBLIC';
    EXECUTE 'REVOKE ALL ON FUNCTION monthly_partitions_drop_expired(regclass) FROM PUBLIC';
    -- On a fresh cluster the runtime roles may not exist yet;
    -- runtime-roles.sql grants the same when it creates them.
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'faas_meterd') THEN
        EXECUTE 'GRANT EXECUTE ON FUNCTION monthly_partitions_ensure(regclass) TO faas_meterd';
        EXECUTE 'GRANT EXECUTE ON FUNCTION monthly_partitions_drop_expired(regclass) TO faas_meterd';
    END IF;
END
$grant$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS monthly_partitions_drop_expired(regclass);
DROP FUNCTION IF EXISTS monthly_partitions_ensure(regclass);
-- +goose StatementEnd
