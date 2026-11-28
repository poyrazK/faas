-- filename: 20261128073719300_mcp_contract_checks.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- One check per production candidate of a listed app. imaged writes it;
-- the deployments trigger below accepts it when the candidate goes live.
CREATE TABLE IF NOT EXISTS mcp_contract_checks (
    deployment_id    uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    app_id           uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    account_id       uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    baseline_sha256  text NOT NULL CHECK (baseline_sha256 ~ '^[0-9a-f]{64}$'),
    candidate_sha256 text CHECK (candidate_sha256 ~ '^[0-9a-f]{64}$'),
    candidate        jsonb CHECK (candidate IS NULL OR (jsonb_typeof(candidate) = 'object' AND octet_length(candidate::text) <= 2097152)),
    verdict          text NOT NULL CHECK (verdict IN ('unchanged', 'compatible', 'review', 'breaking', 'unverified')),
    changes          jsonb NOT NULL DEFAULT '[]'::jsonb
                     CHECK (jsonb_typeof(changes) = 'array' AND octet_length(changes::text) <= 262144),
    override_id      uuid REFERENCES mcp_contract_overrides(id) ON DELETE SET NULL,
    checked_at       timestamptz NOT NULL DEFAULT now(),
    -- accepted_at: when the candidate first went live (the changelog date).
    -- live_at: the last time it went live, rollbacks included; the newest
    -- live_at is the effective baseline, so it follows what serves.
    accepted_at      timestamptz,
    live_at          timestamptz,
    CHECK ((accepted_at IS NULL) = (live_at IS NULL)),
    CHECK ((candidate IS NULL) = (candidate_sha256 IS NULL)),
    CHECK ((verdict = 'unverified') = (candidate IS NULL))
);
CREATE INDEX IF NOT EXISTS mcp_contract_checks_live_idx
    ON mcp_contract_checks (app_id, live_at DESC) WHERE live_at IS NOT NULL;

-- Accept the check when its candidate goes live, and tell the seller's
-- webhooks when the accepted contract changed. A rollback that makes an
-- older candidate live again moves live_at (the baseline) but sends nothing.
-- A refused check (breaking or unverified without an override) is never
-- accepted.
CREATE OR REPLACE FUNCTION accept_mcp_contract_check() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    accepted mcp_contract_checks%ROWTYPE;
    event_payload jsonb;
BEGIN
    IF NEW.status <> 'live' OR (TG_OP = 'UPDATE' AND OLD.status = 'live') THEN
        RETURN NEW;
    END IF;
    UPDATE mcp_contract_checks SET live_at = now(), accepted_at = COALESCE(accepted_at, now())
     WHERE deployment_id = NEW.id
       AND (verdict IN ('unchanged', 'compatible', 'review') OR override_id IS NOT NULL)
    RETURNING * INTO accepted;
    -- now() is fixed within the statement: a first acceptance has
    -- accepted_at = live_at.
    IF accepted.deployment_id IS NULL OR accepted.verdict = 'unchanged' OR accepted.accepted_at <> accepted.live_at THEN
        RETURN NEW;
    END IF;
    event_payload := jsonb_build_object(
        'app_id', NEW.app_id,
        'deployment_id', NEW.id,
        'verdict', accepted.verdict,
        'baseline_sha256', accepted.baseline_sha256,
        'contract_sha256', accepted.candidate_sha256,
        'overridden', accepted.override_id IS NOT NULL,
        'tools', COALESCE((SELECT jsonb_agg(DISTINCT c->>'tool') FROM jsonb_array_elements(accepted.changes) c
                            WHERE COALESCE(c->>'tool', '') <> ''), '[]'::jsonb),
        'changes', jsonb_array_length(accepted.changes)
    );
    INSERT INTO app_webhook_deliveries
        (webhook_id, app_id, account_id, event, payload)
    SELECT h.id, NEW.app_id, h.account_id, 'mcp.contract.changed', event_payload
      FROM app_webhooks h
      JOIN apps a ON a.id = NEW.app_id AND a.account_id = h.account_id
     WHERE h.scope = 'app'
       AND h.app_id = NEW.app_id
       AND h.enabled
       AND (cardinality(h.event_filter) = 0 OR 'mcp.contract.changed' = ANY(h.event_filter));
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS deployments_accept_mcp_contract_check ON deployments;
CREATE TRIGGER deployments_accept_mcp_contract_check
    AFTER INSERT OR UPDATE OF status ON deployments
    FOR EACH ROW EXECUTE FUNCTION accept_mcp_contract_check();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS deployments_accept_mcp_contract_check ON deployments;
DROP FUNCTION IF EXISTS accept_mcp_contract_check();
DROP TABLE IF EXISTS mcp_contract_checks;
-- +goose StatementEnd
