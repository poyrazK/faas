-- Cost-control audit CC-002, CC-015 and CC-018.
-- migration-compat: expand
-- Immutable delivery quantities and durable notices are additive. The plan
-- trigger captures N-1 plan writers as well as this release's mutations.
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS billing_usage_intents (
    provider text NOT NULL CHECK (provider <> ''),
    account_id uuid NOT NULL REFERENCES resource_namespaces(id) ON DELETE CASCADE,
    meter text NOT NULL CHECK (meter IN ('compute', 'egress')),
    window_start timestamptz NOT NULL CHECK (window_start = date_trunc('hour', window_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
    quantity bigint NOT NULL CHECK (quantity >= 0),
    created_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(created_at)),
    PRIMARY KEY (provider, account_id, meter, window_start)
);

CREATE TABLE IF NOT EXISTS spend_cap_notice_outbox (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES resource_namespaces(id) ON DELETE CASCADE,
    month timestamptz NOT NULL CHECK (month = date_trunc('month', month AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
    cap_cents bigint NOT NULL CHECK (cap_cents >= 0),
    observed_cents bigint NOT NULL CHECK (observed_cents >= cap_cents),
    reached_at timestamptz NOT NULL CHECK (isfinite(reached_at)),
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'sending', 'delivered')),
    lease_token uuid,
    next_attempt_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(next_attempt_at)),
    delivered_at timestamptz CHECK (delivered_at IS NULL OR isfinite(delivered_at)),
    UNIQUE (account_id, month, cap_cents),
    CHECK ((state = 'sending') = (lease_token IS NOT NULL)),
    CHECK ((state = 'delivered') = (delivered_at IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS spend_cap_notice_pending_idx ON spend_cap_notice_outbox(next_attempt_at, id) WHERE state <> 'delivered';

CREATE TABLE IF NOT EXISTS billing_plan_activations (
    sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    subject_id uuid NOT NULL REFERENCES resource_namespaces(id) ON DELETE CASCADE,
    from_plan text NOT NULL CHECK (from_plan IN ('free', 'hobby', 'pro', 'scale')),
    to_plan text NOT NULL CHECK (to_plan IN ('free', 'hobby', 'pro', 'scale')),
    effective_at timestamptz NOT NULL CHECK (isfinite(effective_at))
);
CREATE INDEX IF NOT EXISTS billing_plan_activations_subject_idx ON billing_plan_activations(subject_id, effective_at, sequence);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION retain_billing_plan_activation() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
    IF NEW.plan IS DISTINCT FROM OLD.plan THEN
        INSERT INTO public.billing_plan_activations(subject_id, from_plan, to_plan, effective_at)
        VALUES (NEW.id, OLD.plan, NEW.plan, NEW.plan_changed_at);
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION retain_billing_plan_activation() FROM PUBLIC;
DROP TRIGGER IF EXISTS accounts_retain_billing_plan ON accounts;
CREATE TRIGGER accounts_retain_billing_plan AFTER UPDATE OF plan ON accounts
    FOR EACH ROW EXECUTE FUNCTION retain_billing_plan_activation();
DROP TRIGGER IF EXISTS orgs_retain_billing_plan ON orgs;
CREATE TRIGGER orgs_retain_billing_plan AFTER UPDATE OF plan ON orgs
    FOR EACH ROW EXECUTE FUNCTION retain_billing_plan_activation();

-- A retained hold without a notice is pending, including after a prior
-- release crashed between writing the hold and sending mail. Provider message
-- identity remains account/month/cap, independent of the outbox UUID.
INSERT INTO spend_cap_notice_outbox(id, account_id, month, cap_cents, observed_cents, reached_at)
SELECT gen_random_uuid(), account_id, month::timestamp AT TIME ZONE 'UTC', cap_cents, observed_cents, reached_at
FROM account_spend_cap_holds ON CONFLICT (account_id, month, cap_cents) DO NOTHING;

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'faas_runtime') THEN
        GRANT SELECT, INSERT, UPDATE ON billing_usage_intents, spend_cap_notice_outbox TO faas_runtime;
        GRANT SELECT ON billing_plan_activations TO faas_runtime;
        REVOKE INSERT, UPDATE, DELETE ON billing_plan_activations FROM faas_runtime;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS orgs_retain_billing_plan ON orgs;
DROP TRIGGER IF EXISTS accounts_retain_billing_plan ON accounts;
DROP FUNCTION IF EXISTS retain_billing_plan_activation();
DROP TABLE IF EXISTS billing_plan_activations;
DROP TABLE IF EXISTS spend_cap_notice_outbox;
DROP TABLE IF EXISTS billing_usage_intents;
