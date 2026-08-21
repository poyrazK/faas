-- filename: 00365_account_spend_snapshot.sql
-- +goose Up
-- +goose StatementBegin

-- Issue #1233 / ADR-123 — backing table for the account_spend_eur
-- alert metric. meterd ticks at AlertEvalInterval (default 60 s,
-- see cmd/meterd/main.go::buildAlertEvaluator) and INSERTs a row
-- summarising the customer's rolling spend since the previous tick.
-- The alert evaluator reads the SUM(eur_cents) for the MTD window
-- via a Store method that mirrors CountFailedInvocationsSince.
--
-- Storage shape:
--   - account_id scopes the row to a customer.
--   - period_start / period_end bound the slice the snapshot
--     covers. A tick's period_start is the previous tick's
--     period_end; period_end is the wall-clock at INSERT time.
--     Gaps are acceptable — the alert evaluator reads the SUM
--     over a UTC-day or MTD window, not contiguous slices.
--   - gb_seconds is the raw usage_minutes snapshot from the
--     meterd usage ticker (mirrors the existing pkg/meter/quota.go
--     accounting path; the rate constant lives in
--     pkg/api/limits.go per the per-plan RAM + 8 MB per-second
--     billing model — never inline).
--   - eur_cents is the derived value: gb_seconds * rate_eur_per_gb_hour
--     / 3600 * 100. Stored for two reasons: (a) the alert path
--     avoids the multiplication at evaluation time, (b) the
--     dashboard can render historical spend without re-pricing
--     if rates change (advisory: rate changes should back-fill
--     via a separate migration; this table is append-only).
--   - source is a closed-set marker for which meter path the row
--     came from ('running_seconds' today; 'overage' for future
--     billing-cap paths). Lets an operator split the spend
--     picture in operator-side reporting without confusing the
--     customer-facing SUM.
--
-- Replay-safety: CREATE TABLE IF NOT EXISTS, CREATE INDEX IF NOT
-- EXISTS. No trigger (rows are append-only — meterd INSERTs them
-- on each tick).

CREATE TABLE IF NOT EXISTS account_spend_snapshot (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id    uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    period_start  timestamptz NOT NULL,
    period_end    timestamptz NOT NULL DEFAULT now(),
    gb_seconds    double precision NOT NULL,
    eur_cents     bigint NOT NULL,
    source        text NOT NULL DEFAULT 'running_seconds',
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT account_spend_snapshot_source_chk CHECK (
        source IN ('running_seconds', 'overage', 'build_seconds', 'snapshot_storage')
    ),
    CONSTRAINT account_spend_snapshot_period_chk CHECK (period_end > period_start),
    CONSTRAINT account_spend_snapshot_gb_seconds_chk CHECK (gb_seconds >= 0),
    CONSTRAINT account_spend_snapshot_eur_cents_chk CHECK (eur_cents >= 0)
);

-- MTD aggregator scan: SUM(eur_cents) WHERE account_id = $1 AND
-- period_start >= $2 (the MTD boundary). Partial index keeps the
-- most-recent slices on the hot path.
CREATE INDEX IF NOT EXISTS account_spend_snapshot_account_period_idx
    ON account_spend_snapshot (account_id, period_start DESC);

-- Contiguity guard for meterd's tick loop. A partial unique on
-- (account_id, period_end) is wrong (we want duplicates if a tick
-- fires twice); the unique is on (account_id, source, period_end)
-- so each meterd can claim one slice per source. Real uniqueness
-- guarantee comes from the meterd side — the DB constraint is
-- defence-in-depth, not the source of truth.
CREATE UNIQUE INDEX IF NOT EXISTS account_spend_snapshot_account_source_period_uniq
    ON account_spend_snapshot (account_id, source, period_end);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS account_spend_snapshot_account_source_period_uniq;
DROP INDEX IF EXISTS account_spend_snapshot_account_period_idx;
DROP TABLE IF EXISTS account_spend_snapshot;

-- +goose StatementEnd