-- filename: 00351_meterd_tenant_surface_cert_expiry_state.sql
-- +goose Up
-- +goose StatementBegin

-- Issue #1233 / ADR-123 — backing state for the per-app
-- cert_expiry_seconds alert metric. The existing edge-daemon
-- TLS cache (pkg/gateway/cert_expiry.go) exposes
-- gateway_tls_cert_expiry_seconds / _by_host_seconds — those are
-- the operator's edge cert, NOT the customer's per-app domain
-- cert on tenant_surfaces (migrations/00243). This migration
-- stands up the meterd-side walker target so a per-app refresher
-- goroutine can write a small row per tenant_surfaces.cert_state =
-- 'issued' entry and emit meterd_tenant_surface_cert_expiry_seconds
-- for the alert evaluator.
--
-- Walker pattern mirrors pkg/gateway/cert_expiry.go: a single
-- SELECT of the current set, a bulk UPDATE with the new
-- observed cert_not_after, then increment the
-- meterd_tenant_surface_cert_expiry_refresher_walk_complete_total
-- gauge. The state table is the read-cache the alert evaluator
-- reads from; the walker is the sole writer.
--
-- Ownership: CLAUDE.md rule "apid is the ONLY writer to
-- customer-intent tables" — this is a DERIVED signal cache,
-- not customer intent. The meter daemon (cmd/meterd) owns the
-- walker, the gauge, and this table; the apid process only
-- READS via state.MinCertExpiryForApp. The meterd_* prefix makes
-- the ownership obvious to any reader and survives the future
-- split of apid + meterd into separate Postgres roles for §11
-- hardening (no cross-role writes).
--
-- Storage shape:
--   - tenant_surface_id is the FK root — ON DELETE CASCADE mirrors
--     the parent table's lifecycle.
--   - account_id + app_id are denormalised from tenant_surfaces
--     so the alert evaluator's read can be `WHERE app_id = $N`
--     without a join. Same denormalisation precedent as
--     alert_rules (which also carries account_id + app_id
--     directly per migrations/00062_alert_rules.sql).
--   - hostname is denormalised for the same reason; the alert
--     evaluator's gauge label set is {account_id, app_id,
--     hostname}.
--   - last_observed_cert_not_after is the value the walker most
--     recently saw on the parent row. NULL on first walk.
--   - last_walk_status is the closed-set marker
--     ('ok' | 'stale_parent' | 'cert_unissued' | 'error') the
--     walker stamps. Lets the alert evaluator's degraded-source
--     rule (mirroring pkg/alerts/evaluator.go:505-507) skip
--     rules whose backing signal is stale.
--   - last_refreshed_at is the tick the walker last touched this
--     row; an alert evaluator reading `last_refreshed_at <
--     now() - 24h` treats the row as stale.
--
-- Replay-safety: CREATE TABLE IF NOT EXISTS, CREATE INDEX IF NOT
-- EXISTS. No trigger (rows are walker-owned).

CREATE TABLE IF NOT EXISTS meterd_tenant_surface_cert_expiry_state (
    tenant_surface_id        uuid PRIMARY KEY REFERENCES tenant_surfaces(id) ON DELETE CASCADE,
    account_id               uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id                   uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    hostname                 text NOT NULL,
    last_observed_cert_not_after timestamptz,
    last_walk_status         text NOT NULL DEFAULT 'ok',
    last_refreshed_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT meterd_tenant_surface_cert_expiry_status_chk CHECK (
        last_walk_status IN ('ok', 'stale_parent', 'cert_unissued', 'error')
    )
);

-- Hot read path: WHERE app_id = $N ORDER BY hostname. Partial
-- index on ok-status rows so the alert evaluator's degraded-source
-- branch (mirroring pkg/alerts/evaluator.go:505) skips rows the
-- walker has flagged.
CREATE INDEX IF NOT EXISTS meterd_tenant_surface_cert_expiry_app_idx
    ON meterd_tenant_surface_cert_expiry_state (app_id, hostname)
    WHERE last_walk_status = 'ok';

-- Walker's UPDATE target: WHERE last_refreshed_at < $N. Partial
-- index keeps the walker from scanning rows it just touched.
CREATE INDEX IF NOT EXISTS meterd_tenant_surface_cert_expiry_stale_idx
    ON meterd_tenant_surface_cert_expiry_state (last_refreshed_at)
    WHERE last_walk_status <> 'ok';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS meterd_tenant_surface_cert_expiry_stale_idx;
DROP INDEX IF EXISTS meterd_tenant_surface_cert_expiry_app_idx;
DROP TABLE IF EXISTS meterd_tenant_surface_cert_expiry_state;

-- +goose StatementEnd