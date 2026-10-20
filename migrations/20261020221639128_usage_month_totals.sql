-- filename: 20261020221639128_usage_month_totals.sql
-- migration-compat: expand
-- Trigger-kept month-to-date usage totals for UsageByMonth (see below).
-- +goose Up
-- Month-to-date usage totals per (account, app or job, UTC month), kept exact
-- by statement-level triggers on usage_minutes in the writer's transaction.
-- UsageByMonth summed every usage minute of the month on each call; meterd's
-- quota and residency loops call it for every account each minute, and on
-- production-us one call summed 136,884 rows in 226 ms (5.7% of all statement
-- time, growing through the month).
--
-- Every statement is replay-safe; a re-run keeps the original tracked_from.
-- No backfill: totals are authoritative only for months starting at or after
-- usage_month_totals_tracking.tracked_from, the first UTC month that begins
-- after these triggers exist, so every one of its rows is counted from its
-- first write. Earlier months keep the aggregate read. A migration in the
-- last hour of a month starts tracking a month later, so writer clock skew
-- cannot place a row in the tracked month before the triggers commit.
--
-- The triggers ignore rows before tracked_from: those months were never
-- totalled, so a delta there (an account deletion removing them) would be
-- meaningless. minute_rows counts the usage_minutes rows behind a total, so a
-- subject whose rows are all deleted drops out of the read exactly as it did
-- from the aggregate. Totals carry no CHECK: a bug here must never fail a
-- metering write. Deltas are applied in key order, so two writers touching
-- several totals cannot deadlock on them.
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS usage_month_totals (
    account_id      uuid        NOT NULL,
    month           timestamptz NOT NULL,
    subject_id      uuid        NOT NULL,
    minute_rows     bigint      NOT NULL DEFAULT 0,
    mb_seconds      bigint      NOT NULL DEFAULT 0,
    cpu_usec        bigint      NOT NULL DEFAULT 0,
    requests        bigint      NOT NULL DEFAULT 0,
    tx_bytes        bigint      NOT NULL DEFAULT 0,
    net_tx_bytes    bigint      NOT NULL DEFAULT 0,
    net_rx_bytes    bigint      NOT NULL DEFAULT 0,
    cold_boot_count bigint      NOT NULL DEFAULT 0,
    PRIMARY KEY (account_id, month, subject_id)
);

CREATE TABLE IF NOT EXISTS usage_month_totals_tracking (
    singleton    boolean     PRIMARY KEY DEFAULT true,
    tracked_from timestamptz NOT NULL,
    CONSTRAINT usage_month_totals_tracking_singleton_check CHECK (singleton)
);

INSERT INTO usage_month_totals_tracking (tracked_from)
VALUES ((date_trunc('month', (now() + interval '1 hour') AT TIME ZONE 'UTC') + interval '1 month') AT TIME ZONE 'UTC')
ON CONFLICT (singleton) DO NOTHING;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION usage_month_totals_apply() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_from timestamptz;
BEGIN
    SELECT tracked_from INTO v_from FROM usage_month_totals_tracking;
    IF v_from IS NULL THEN
        RETURN NULL;
    END IF;
    IF TG_OP = 'INSERT' THEN
        INSERT INTO usage_month_totals AS t (account_id, month, subject_id, minute_rows, mb_seconds, cpu_usec,
                                             requests, tx_bytes, net_tx_bytes, net_rx_bytes, cold_boot_count)
        SELECT r.account_id, date_trunc('month', r.minute AT TIME ZONE 'UTC') AT TIME ZONE 'UTC', coalesce(r.app_id, r.job_id),
               count(*), sum(r.mb_seconds), sum(r.cpu_usec), sum(r.requests), sum(r.tx_bytes),
               sum(r.net_tx_bytes), sum(r.net_rx_bytes), sum(r.cold_boot_count)
          FROM usage_month_totals_new r
         WHERE r.minute >= v_from
         GROUP BY 1, 2, 3
         ORDER BY 1, 2, 3
        ON CONFLICT (account_id, month, subject_id) DO UPDATE SET
            minute_rows     = t.minute_rows     + EXCLUDED.minute_rows,
            mb_seconds      = t.mb_seconds      + EXCLUDED.mb_seconds,
            cpu_usec        = t.cpu_usec        + EXCLUDED.cpu_usec,
            requests        = t.requests        + EXCLUDED.requests,
            tx_bytes        = t.tx_bytes        + EXCLUDED.tx_bytes,
            net_tx_bytes    = t.net_tx_bytes    + EXCLUDED.net_tx_bytes,
            net_rx_bytes    = t.net_rx_bytes    + EXCLUDED.net_rx_bytes,
            cold_boot_count = t.cold_boot_count + EXCLUDED.cold_boot_count;
    ELSIF TG_OP = 'UPDATE' THEN
        INSERT INTO usage_month_totals AS t (account_id, month, subject_id, minute_rows, mb_seconds, cpu_usec,
                                             requests, tx_bytes, net_tx_bytes, net_rx_bytes, cold_boot_count)
        SELECT d.account_id, d.month, d.subject_id,
               sum(d.n), sum(d.mb_seconds), sum(d.cpu_usec), sum(d.requests), sum(d.tx_bytes),
               sum(d.net_tx_bytes), sum(d.net_rx_bytes), sum(d.cold_boot_count)
          FROM (
                SELECT r.account_id, date_trunc('month', r.minute AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' AS month,
                       coalesce(r.app_id, r.job_id) AS subject_id, 1 AS n, r.mb_seconds, r.cpu_usec,
                       r.requests::bigint AS requests, r.tx_bytes, r.net_tx_bytes, r.net_rx_bytes,
                       r.cold_boot_count::bigint AS cold_boot_count
                  FROM usage_month_totals_new r
                 WHERE r.minute >= v_from
                UNION ALL
                SELECT r.account_id, date_trunc('month', r.minute AT TIME ZONE 'UTC') AT TIME ZONE 'UTC',
                       coalesce(r.app_id, r.job_id), -1, -r.mb_seconds, -r.cpu_usec,
                       -r.requests::bigint, -r.tx_bytes, -r.net_tx_bytes, -r.net_rx_bytes,
                       -r.cold_boot_count::bigint
                  FROM usage_month_totals_old r
                 WHERE r.minute >= v_from
               ) d
         GROUP BY 1, 2, 3
        HAVING sum(d.n) <> 0 OR sum(d.mb_seconds) <> 0 OR sum(d.cpu_usec) <> 0 OR sum(d.requests) <> 0
            OR sum(d.tx_bytes) <> 0 OR sum(d.net_tx_bytes) <> 0 OR sum(d.net_rx_bytes) <> 0 OR sum(d.cold_boot_count) <> 0
         ORDER BY 1, 2, 3
        ON CONFLICT (account_id, month, subject_id) DO UPDATE SET
            minute_rows     = t.minute_rows     + EXCLUDED.minute_rows,
            mb_seconds      = t.mb_seconds      + EXCLUDED.mb_seconds,
            cpu_usec        = t.cpu_usec        + EXCLUDED.cpu_usec,
            requests        = t.requests        + EXCLUDED.requests,
            tx_bytes        = t.tx_bytes        + EXCLUDED.tx_bytes,
            net_tx_bytes    = t.net_tx_bytes    + EXCLUDED.net_tx_bytes,
            net_rx_bytes    = t.net_rx_bytes    + EXCLUDED.net_rx_bytes,
            cold_boot_count = t.cold_boot_count + EXCLUDED.cold_boot_count;
    ELSE
        INSERT INTO usage_month_totals AS t (account_id, month, subject_id, minute_rows, mb_seconds, cpu_usec,
                                             requests, tx_bytes, net_tx_bytes, net_rx_bytes, cold_boot_count)
        SELECT r.account_id, date_trunc('month', r.minute AT TIME ZONE 'UTC') AT TIME ZONE 'UTC', coalesce(r.app_id, r.job_id),
               -count(*), -sum(r.mb_seconds), -sum(r.cpu_usec), -sum(r.requests), -sum(r.tx_bytes),
               -sum(r.net_tx_bytes), -sum(r.net_rx_bytes), -sum(r.cold_boot_count)
          FROM usage_month_totals_old r
         WHERE r.minute >= v_from
         GROUP BY 1, 2, 3
         ORDER BY 1, 2, 3
        ON CONFLICT (account_id, month, subject_id) DO UPDATE SET
            minute_rows     = t.minute_rows     + EXCLUDED.minute_rows,
            mb_seconds      = t.mb_seconds      + EXCLUDED.mb_seconds,
            cpu_usec        = t.cpu_usec        + EXCLUDED.cpu_usec,
            requests        = t.requests        + EXCLUDED.requests,
            tx_bytes        = t.tx_bytes        + EXCLUDED.tx_bytes,
            net_tx_bytes    = t.net_tx_bytes    + EXCLUDED.net_tx_bytes,
            net_rx_bytes    = t.net_rx_bytes    + EXCLUDED.net_rx_bytes,
            cold_boot_count = t.cold_boot_count + EXCLUDED.cold_boot_count;
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE OR REPLACE TRIGGER usage_month_totals_insert AFTER INSERT ON usage_minutes
    REFERENCING NEW TABLE AS usage_month_totals_new
    FOR EACH STATEMENT EXECUTE FUNCTION usage_month_totals_apply();
CREATE OR REPLACE TRIGGER usage_month_totals_update AFTER UPDATE ON usage_minutes
    REFERENCING OLD TABLE AS usage_month_totals_old NEW TABLE AS usage_month_totals_new
    FOR EACH STATEMENT EXECUTE FUNCTION usage_month_totals_apply();
CREATE OR REPLACE TRIGGER usage_month_totals_delete AFTER DELETE ON usage_minutes
    REFERENCING OLD TABLE AS usage_month_totals_old
    FOR EACH STATEMENT EXECUTE FUNCTION usage_month_totals_apply();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS usage_month_totals_delete ON usage_minutes;
DROP TRIGGER IF EXISTS usage_month_totals_update ON usage_minutes;
DROP TRIGGER IF EXISTS usage_month_totals_insert ON usage_minutes;
DROP FUNCTION IF EXISTS usage_month_totals_apply();
DROP TABLE IF EXISTS usage_month_totals_tracking;
DROP TABLE IF EXISTS usage_month_totals;
