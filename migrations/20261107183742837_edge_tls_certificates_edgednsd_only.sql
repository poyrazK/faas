-- filename: 20261107183742837_edge_tls_certificates_edgednsd_only.sql
-- migration-compat: contract
-- Apply in a full release only. edgednsd runs on the control plane as the
-- faas OS user, which logs in as the schema owner or faas_shared; both keep
-- writing. Compute nodes (faas_compute) and every other runtime role lose
-- their writes; the edges only read.

-- +goose Up
-- +goose StatementBegin
-- ADR-20261107180000: every compute node logs in as a faas_runtime member.
-- A row in edge_tls_certificates is served by every edge and trusted by the
-- Certificate Transparency watch, so only the control plane's faas_shared
-- (edgednsd's role) may insert or prune one. The postgres role's
-- runtime-roles.sql repeats this on every converge.
DO $$
BEGIN
    SET LOCAL lock_timeout = '5s';
    IF to_regrole('faas_runtime') IS NOT NULL THEN
        REVOKE INSERT, UPDATE, DELETE ON edge_tls_certificates FROM faas_runtime;
    END IF;
    IF to_regrole('faas_shared') IS NOT NULL THEN
        GRANT INSERT, DELETE ON edge_tls_certificates TO faas_shared;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    SET LOCAL lock_timeout = '5s';
    IF to_regrole('faas_runtime') IS NOT NULL THEN
        GRANT INSERT, UPDATE, DELETE ON edge_tls_certificates TO faas_runtime;
    END IF;
END
$$;
-- +goose StatementEnd
