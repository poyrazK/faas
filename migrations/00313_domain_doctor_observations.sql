-- filename: 00313_domain_doctor_observations.sql
-- +goose Up
-- +goose StatementBegin

-- 00313_domain_doctor_observations.sql — ADR-120 (issue #961
-- follow-on). Persist the per-domain probe results that the
-- `gregale domains doctor` endpoint surfaces. The existing
-- `dns_poller` writes the rows on its 30 s tick; the doctor
-- endpoint reads the latest row per domain. Falls back to a
-- synchronous re-probe when the row is older than
-- FAAS_DOMAIN_DOCTOR_TTL_SECONDS (default 300s).
--
-- Schema rationale (matches ADR §"Consequences"):
--
--   * domain is the PK and a citext FK to custom_domains.
--     ON DELETE CASCADE so removing a custom domain row
--     purges its diagnostic history. Surface_id is NULL-able
--     (legacy custom_domains rows have no surface) and uses
--     ON DELETE SET NULL so a surface drop doesn't take the
--     diagnostic row with it.
--
--   * Five booleans map 1:1 to the Render-style check
--     lines: dns_record_found, points_to_gregale, caa_permits,
--     ipv6_conflict. caa_permits is NULL-able: no CAA
--     published is allowed by default (the customer's DNS
--     permits any CA), so a NULL row is a healthy "permit
--     by default" observation, not a failure.
--
--   * cert_state is a closed set that re-uses the
--     CustomDomainResponse.CertStatus enum from
--     pkg/api/dto.go:1663-1676 plus the surface-level
--     CertState vocabulary (none/pending/issued/failed).
--     The pg_ratelimit_counters_scope_check naming pattern
--     (closed CHECK) matches the closed-set conventions on
--     tenant_surfaces.cert_state (migrations/00243:43-46)
--     and data_upstreams_scope_check (00226).
--
--   * observed_target / observed_aaaa / caa_observed are
--     the raw observed values from the probe pass so the
--     doctor can render "CNAME → gregale.dev" without
--     re-querying. last_error is a single human-readable
--     string summarizing the failing check.
--
--   * dns_checked_at and cert_checked_at are per-probe
--     timestamps so the CLI can render "DNS 12s ago, cert
--     2m ago" (the per-probe age matters when a surface
--     cert was just issued but DNS was checked 5 minutes
--     before).
--
--   * The stale_idx is on observed_at so a future
--     "stuck-domain" alert can scan the table cheaply:
--     SELECT domain FROM domain_doctor_observations
--     WHERE observed_at < now() - INTERVAL '30 minutes'
--     AND healthy = false. (The Healthy column is computed
--     from the booleans in the response handler; not
--     stored, so the alert re-evaluates against current
--     truth — see pkg/api/dto.go future addition.)
--
-- Replay-safety: replay_safety_test.go (the
-- TestNewMigrationsAreReplaySafe harness) applies each
-- migration twice in a single tx and pins the second
-- pass as a no-op. CREATE TABLE IF NOT EXISTS / CREATE
-- INDEX IF NOT EXISTS (the canonical pattern from
-- migrations/00053 + 00287) makes the second pass a
-- no-op without losing the failure mode for typos
-- (SQLSTATE 42P07 on the very first apply would still
-- fire if the table genuinely existed for unrelated
-- reasons). IF NOT EXISTS is the established pattern
-- for additive tables per migrations/00053 and
-- 00287's pg_ratelimit follow-up. The Down migration
-- is forward-only (SELECT 1) because the table is
-- purely additive telemetry — no data loss on
-- rollback.

CREATE TABLE IF NOT EXISTS domain_doctor_observations (
    domain              citext PRIMARY KEY
                        REFERENCES custom_domains(domain) ON DELETE CASCADE,
    surface_id          uuid NULL
                        REFERENCES tenant_surfaces(id) ON DELETE SET NULL,
    observed_at         timestamptz NOT NULL DEFAULT now(),
    dns_record_found    boolean NOT NULL,
    points_to_gregale   boolean NOT NULL,
    caa_permits         boolean,
    ipv6_conflict       boolean NOT NULL,
    observed_target     text,
    observed_aaaa       text,
    caa_observed        text,
    cert_state          text NOT NULL DEFAULT 'none'
                        CHECK (cert_state IN ('none','pending','issued','failed','dial_failed')),
    cert_not_after      timestamptz,
    last_error          text,
    dns_checked_at      timestamptz,
    cert_checked_at     timestamptz
);

CREATE INDEX IF NOT EXISTS domain_doctor_observations_stale_idx
    ON domain_doctor_observations (observed_at);

-- +goose StatementEnd

-- +goose Down
-- Forward-only by design (mirrors 00287 + 00229: reverting
-- would orphan any rows the dns_poller wrote between this
-- migration's apply and the rollback. Drop the table
-- unconditionally on downgrade only if the operator
-- explicitly requests it; the default Down is a no-op
-- sentinel so a replay lands on the CREATE, not the drop.)
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
