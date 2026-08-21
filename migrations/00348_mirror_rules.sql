-- filename: 00348_mirror_rules.sql
-- +goose Up
-- +goose StatementBegin
--
-- issue #72 / ADR-125 — traffic mirroring. Adds the per-deployment
-- mirror_rules table that links a live source_deployment to a live
-- mirror_deployment. Every request served by `source` is duplicated
-- to `mirror` asynchronously; the customer response is always the
-- `source` response (the mirror is shadow-only, never served to the
-- customer). Each invocation produces a row in
-- mirror_invocation_results (00350) so the customer can compare
-- status / latency / schema / body / crash drift across the two
-- deployments.
--
-- Schema posture:
--   * account_id + app_id FKs both ON DELETE CASCADE — mirror
--     rules live as long as the owning app does, never orphan.
--   * source_deployment_id <> mirror_deployment_id CHECK prevents
--     self-mirror (a deployment cannot shadow itself — that's a
--     copy-paste loop, not a canary).
--   * percent ∈ [0, 100] — defaults to 100 (every request). Lower
--     values mirror a sample, but the canonical canary use case is
--     100; the column exists for the future "sample 10% of traffic"
--     lever that ADR-125 captures as a follow-on.
--   * enabled defaults true; the column exists so a rule can be
--     paused without losing the (source, mirror) pairing.
--   * include_body defaults false — safe-by-default per spec hint
--     ("sensitive headers and bodies must be redacted or disabled
--     by default"). When true, the gateway records the (redacted)
--     body SHA-256 in mirror_invocation_results.body_hash; when
--     false the column stays NULL and only metadata (status,
--     latency, schema hash) is captured. Customer opts in per rule
--     via the API.
--   * redact_headers is a closed text[] of header names the
--     customer wants stripped from the mirror request beyond the
--     always-stripped list (Authorization / Cookie / Set-Cookie /
--     X-API-Key / Proxy-Authorization / WWW-Authenticate). Capped
--     at 32 entries via CHECK to prevent abuse — 32 is enough for
--     any realistic customer header set.
--   * created_at / updated_at + set_updated_at trigger mirror the
--     edge_rules pattern (migrations/00192_edge_rules.sql).
--
-- Indexing posture:
--   * mirror_rules_app_idx on (app_id) WHERE enabled — the gateway
--     picker cache refresh path keys on app_id for active rules
--     only. Disabled rules are invisible to the picker but stay in
--     the table for the customer's history.
--   * mirror_rules_source_idx on (source_deployment_id) WHERE
--     enabled — the gateway hot path: after Pick decides the
--     production deployment, a single index lookup answers "is
--     there a mirror rule for this source?". This is the critical
--     p99 lookup and the partial index keeps it O(log n_active).
--
-- Migration slot reservation: 00348 is the lowest free slot above
-- main's claim (00346_deployments_annotation, the latest merge
-- per PR #984). Open PR #? (per-service wire-protocol selector,
-- ADR-124 on a non-main branch) holds 00347 + a real at 00347
-- (apps.app_protocol column); this PR starts at 00348 to avoid
-- the cross-PR slot precheck collision documented in the 00345
-- slot-choice comment.
--
-- ADR-125 §Decision cites ADR-016 (additive proto widening),
-- ADR-041 (slot reservation), ADR-070 (gatewayd-public/internal
-- split), ADR-084 (traffic-splitting precedent), ADR-098 (wake-
-- coord detached-ctx pattern). The runtime extension (apid
-- handlers + CLI + gateway picker integration + mirror goroutine
-- + redactor + comparison surface) lands in the same mega-PR
-- cluster.

-- Replay-safe posture: every CREATE in this Up block uses IF NOT EXISTS
-- (or DROP TRIGGER IF EXISTS before CREATE TRIGGER) so the migration
-- is idempotent on re-apply. TestNewMigrationsAreReplaySafe walks
-- each new migration in isolation on a fresh DB, replays it, and
-- asserts no 42P07 / 42710 (relation-already-exists) errors — the
-- same pattern 00304_cors_presets + 00329_consumer_keys use.
CREATE TABLE IF NOT EXISTS mirror_rules (
    id                   uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id           uuid        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id               uuid        NOT NULL REFERENCES apps(id)     ON DELETE CASCADE,
    source_deployment_id uuid        NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    mirror_deployment_id uuid        NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    percent              int         NOT NULL DEFAULT 100
        CHECK (percent BETWEEN 0 AND 100),
    enabled              boolean     NOT NULL DEFAULT true,
    include_body         boolean     NOT NULL DEFAULT false,
    redact_headers       text[]      NOT NULL DEFAULT '{}',
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    CHECK (source_deployment_id <> mirror_deployment_id),
    CHECK (array_length(redact_headers, 1) IS NULL OR array_length(redact_headers, 1) <= 32)
);

CREATE INDEX IF NOT EXISTS mirror_rules_app_idx
    ON mirror_rules (app_id)
    WHERE enabled;

CREATE INDEX IF NOT EXISTS mirror_rules_source_idx
    ON mirror_rules (source_deployment_id)
    WHERE enabled;

CREATE OR REPLACE FUNCTION mirror_rules_set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS mirror_rules_set_updated_at_trg ON mirror_rules;
CREATE TRIGGER mirror_rules_set_updated_at_trg
    BEFORE UPDATE ON mirror_rules
    FOR EACH ROW EXECUTE FUNCTION mirror_rules_set_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Forward-only widening: dropping this migration's objects is
-- safe (the runtime hasn't shipped yet), but mirror_invocation_results
-- (00350) and instances.mode (00349) reference this table by FK. A
-- reverse order is therefore required: drop 00350, 00349, then this
-- one. The goose Down sequence runs migrations in reverse apply
-- order so 00349 + 00350 are already gone by the time this Down
-- fires.
DROP TRIGGER IF EXISTS mirror_rules_set_updated_at_trg ON mirror_rules;
DROP FUNCTION IF EXISTS mirror_rules_set_updated_at();
DROP INDEX  IF EXISTS mirror_rules_source_idx;
DROP INDEX  IF EXISTS mirror_rules_app_idx;
DROP TABLE IF EXISTS mirror_rules;

-- +goose StatementEnd
