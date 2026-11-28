-- filename: 20261128123909300_mcp_contract_checks.sql
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
    -- 'unknown' marks a deployment that went live without a check (it
    -- predates the listing or the gate): its tools are unknown, so the
    -- baseline falls back to the listing's contract.
    verdict          text NOT NULL CHECK (verdict IN ('unchanged', 'compatible', 'review', 'breaking', 'unverified', 'unknown')),
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
    CHECK ((verdict IN ('unverified', 'unknown')) = (candidate IS NULL))
);
CREATE INDEX IF NOT EXISTS mcp_contract_checks_live_idx
    ON mcp_contract_checks (app_id, live_at DESC) WHERE live_at IS NOT NULL;

-- Accept the check when its production candidate goes live, and tell the
-- seller's webhooks when the accepted contract changed. A rollback that makes
-- an older candidate live again moves live_at (the baseline) but sends
-- nothing. A refused check (breaking or unverified without an override), or
-- one made against a baseline that has moved since, is never accepted. A
-- deployment without a check that goes live records an 'unknown' marker.
CREATE OR REPLACE FUNCTION accept_mcp_contract_check() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    accepted mcp_contract_checks%ROWTYPE;
    prior mcp_contract_checks%ROWTYPE;
    listing_published timestamptz;
    listing_sha text;
    current_sha text;
    event_payload jsonb;
BEGIN
    IF NEW.status <> 'live' OR (TG_OP = 'UPDATE' AND OLD.status = 'live')
       OR COALESCE(NEW.scope, '') NOT IN ('', 'default', 'production') THEN
        RETURN NEW;
    END IF;
    SELECT l.published_at, l.contract_sha256 INTO listing_published, listing_sha
      FROM mcp_listings l WHERE l.app_id = NEW.app_id AND l.contract IS NOT NULL;
    IF listing_sha IS NULL THEN
        RETURN NEW;
    END IF;
    SELECT * INTO prior FROM mcp_contract_checks WHERE deployment_id = NEW.id FOR UPDATE;
    IF prior.deployment_id IS NULL THEN
        INSERT INTO mcp_contract_checks (deployment_id, app_id, account_id, baseline_sha256, verdict, accepted_at, live_at)
        SELECT NEW.id, NEW.app_id, a.account_id, listing_sha, 'unknown', now(), now() FROM apps a WHERE a.id = NEW.app_id;
        RETURN NEW;
    END IF;
    IF prior.accepted_at IS NULL THEN
        IF prior.verdict IN ('breaking', 'unverified') AND prior.override_id IS NULL THEN
            RETURN NEW;
        END IF;
        -- The baseline GetMCPContractBaseline returns right now.
        SELECT COALESCE(k.candidate_sha256, listing_sha) INTO current_sha
          FROM mcp_contract_checks k
         WHERE k.app_id = NEW.app_id AND k.live_at IS NOT NULL AND k.live_at >= listing_published
         ORDER BY k.live_at DESC LIMIT 1;
        IF COALESCE(current_sha, listing_sha) <> prior.baseline_sha256 THEN
            RETURN NEW;
        END IF;
    END IF;
    UPDATE mcp_contract_checks SET live_at = now(), accepted_at = COALESCE(accepted_at, now())
     WHERE deployment_id = NEW.id
    RETURNING * INTO accepted;
    -- now() is fixed within the statement: a first acceptance has
    -- accepted_at = live_at.
    IF accepted.verdict IN ('unchanged', 'unknown') OR accepted.accepted_at <> accepted.live_at THEN
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
