-- filename: 20261128135329200_mcp_tool_scan_acceptance.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-20261128135329: a candidate whose tool scan has high findings the
-- effective baseline had not reviewed is not accepted without a tool_scan
-- override. Otherwise unchanged from ADR-20261127191645.
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
        -- ADR-20261128135329: high scan findings the baseline had not
        -- reviewed need a tool_scan override.
        IF prior.tool_scan_new_high > 0 AND prior.tool_scan_override_id IS NULL THEN
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
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
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
-- +goose StatementEnd
