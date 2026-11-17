-- filename: 20261117095922720_sandbox_session_domains.sql
-- migration-compat: expand

-- +goose Up
-- The foreign key locks sandbox_sessions briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261117091026 §4: the domains a web-mode session was granted, or
-- refused and why, each once. vmmd reports them on the session's stream and
-- agentd records them, fenced on the session's lease. refused is '' for a
-- grant. A session keeps at most its plan's distinct-domain budget of
-- grants and SandboxEgressRefusedLogMax refusals (agentd bounds them).
-- A new table, so apid and older agentd keep working while this lands.
CREATE TABLE IF NOT EXISTS sandbox_session_domains (
    sandbox_id uuid NOT NULL REFERENCES sandbox_sessions(id) ON DELETE CASCADE,
    domain     text NOT NULL CHECK (char_length(domain) BETWEEN 1 AND 253),
    refused    text NOT NULL DEFAULT '' CHECK (refused IN ('', 'policy', 'budget', 'platform')),
    first_seen timestamptz NOT NULL,
    PRIMARY KEY (sandbox_id, refused, domain)
);
COMMENT ON TABLE sandbox_session_domains IS
    'ADR-20261117091026 domains a web-mode sandbox session was granted or refused, written by agentd from vmmd''s domain log.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS sandbox_session_domains;
-- +goose StatementEnd
