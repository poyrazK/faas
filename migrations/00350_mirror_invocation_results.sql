-- filename: 00350_mirror_invocation_results.sql
-- +goose Up
-- +goose StatementBegin
--
-- issue #72 / ADR-125 — traffic mirroring comparison ledger.
-- One row per mirror invocation; the per-request data the gateway
-- captures about how the mirror response compared to the source
-- response (status, latency, schema hash, body hash, crash flag).
--
-- Why a dedicated table and not the audit_events table:
--   * audit_events is append-only with a kind discriminant — the
--     dashboard's audit panel reads it. mirror_invocation_results
--     is queryable by (rule_id, time range), supports an indexed
--     p99 latency diff aggregate, and exposes the data via
--     `GET /v1/apps/{slug}/mirrors/{id}/summary?window=1h`. The
--     query shape is wrong for audit_events (which is append-only
--     and read-by-event-id).
--   * The customer-facing mirror comparison surface is part of
--     the API contract — it deserves first-class schema, not a
--     jsonb payload inside a polymorphic audit row.
--   * The retention sweep (7-day cap, see ADR-125 §follow-on 3)
--     runs against this table directly; an audit_events rollup
--     would require un-nesting the jsonb at sweep time.
--
-- Column semantics:
--
--   * id, account_id, app_id, mirror_rule_id — the join keys for
--     IDOR-safe retrieval. account_id + app_id are denormalised
--     off mirror_rules so the summary endpoint doesn't need a
--     3-table join per request.
--   * source_deployment_id, mirror_deployment_id — denormalised
--     for the same reason; the source's status_code/latency/body
--     hash columns below need a stable anchor that survives a
--     deployment deletion (mirrors DELETEs from deployments with
--     CASCADE — see 00348). Without denormalisation, deleting the
--     source would cascade-delete historical comparison rows and
--     lose the customer's drift history.
--   * status_code / source_status_code — int. NULL when the
--     mirror wake failed (the customer-facing POST returned a
--     valid source response, but the mirror never came up).
--   * latency_ms / source_latency_ms — int. Wall-clock from
--     `runMirror` start to response receipt (mirror side) and
--     from WithFirstByteRecorder to body complete (source side).
--   * body_hash / source_body_hash — bytea (32 bytes). SHA-256 of
--     the (possibly redacted) response body. NULL when the rule
--     has include_body=false — explicit opt-in per rule (00348).
--   * schema_hash / source_schema_hash — bytea. SHA-256 of the
--     JCS-canonicalized (RFC 8785) JSON body. Distinct from body
--     hash: two responses can have identical schemas but
--     different values (a 200 with `{user: alice}` vs `{user:
--     bob}` should NOT count as a diff — only as a same-schema
--     match). The schema hash is always populated for JSON
--     responses regardless of include_body (no PII concerns —
--     it's structural, not value-bearing).
--   * status_diff / schema_diff / body_diff — boolean pre-
--     computed at write time. The summary endpoint SUMs these
--     columns instead of comparing values client-side; the
--     customer's comparison query stays O(1) per row.
--   * crashed — boolean. True when the mirror instance entered
--     FAILED state during the request (boot error, OOM kill,
--     guest-init hang) OR when the wake itself failed
--     (WakeQueueTTL exceeded, admission denied).
--   * request_id — text. The inbound X-Request-Id or generated
--     request_id; the customer can correlate a mirror row back
--     to a specific production request via the request log.
--   * completed_at — timestamptz. Defaults to now() at insert;
--     the gateway stamps this from the same clock it stamps the
--     audit row, so mirror_invocation_results.completed_at and
--     the mirror_invocation audit kind are within the same
--     microsecond.
--
-- Indexing posture:
--   * mirror_invocation_results_rule_time_idx on (mirror_rule_id,
--     completed_at DESC) — the canonical read pattern:
--     "give me the last N results for this rule" + "give me
--     results for this rule in the last 1h/24h/7d". The DESC
--     direction matches the read order so no sort step is
--     needed at query time.
--
-- Retention posture:
--   * 7-day cap is enforced by a meterd-side sweeper
--     (ADR-125 §follow-on 3). The sweeper is a follow-on PR
--     because it touches pkg/meter (the sweeper's home) and
--     needs its own table-level access. No partial-index /
--     partitioned-table trick here — the table is small (~200
--     bytes per row, ~1.7 GB/day at 100 RPS sustained) and the
--     Postgres planner handles 7-day pruning fine via the DESC
--     index.

-- Replay-safe posture: every CREATE in this Up block uses IF NOT EXISTS
-- (same pattern 00348_mirror_rules uses). TestNewMigrationsAreReplaySafe
-- replays each new migration on a fresh DB; without IF NOT EXISTS the
-- second run fails with 42P07 relation-already-exists.
CREATE TABLE IF NOT EXISTS mirror_invocation_results (
    id                      uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    mirror_rule_id          uuid        NOT NULL REFERENCES mirror_rules(id) ON DELETE CASCADE,
    account_id              uuid        NOT NULL,
    app_id                  uuid        NOT NULL,
    source_deployment_id    uuid        NOT NULL,
    mirror_deployment_id    uuid        NOT NULL,
    instance_id             text,
    source_instance_id      text,
    status_code             int,
    source_status_code      int,
    latency_ms              int,
    source_latency_ms       int,
    body_hash               bytea,
    source_body_hash        bytea,
    schema_hash             bytea,
    source_schema_hash      bytea,
    status_diff             boolean     NOT NULL DEFAULT false,
    schema_diff             boolean     NOT NULL DEFAULT false,
    body_diff               boolean     NOT NULL DEFAULT false,
    crashed                 boolean     NOT NULL DEFAULT false,
    request_id              text        NOT NULL,
    completed_at            timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS mirror_invocation_results_rule_time_idx
    ON mirror_invocation_results (mirror_rule_id, completed_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS mirror_invocation_results_rule_time_idx;
DROP TABLE IF EXISTS mirror_invocation_results;
-- +goose StatementEnd
