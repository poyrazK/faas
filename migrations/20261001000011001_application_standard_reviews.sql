-- +goose Up
CREATE TABLE application_standard_review_plans (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    created_by uuid NOT NULL,
    request jsonb NOT NULL CHECK (jsonb_typeof(request) = 'object'),
    approval_inputs jsonb NOT NULL CHECK (jsonb_typeof(approval_inputs) = 'object'),
    approval_hash text NOT NULL CHECK (approval_hash ~ '^[a-f0-9]{64}$'),
    applications jsonb NOT NULL CHECK (jsonb_typeof(applications) = 'array'),
    blockers jsonb NOT NULL CHECK (jsonb_typeof(blockers) = 'array'),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at),
    UNIQUE (org_id, id)
);
CREATE INDEX application_standard_review_plans_org_idx
ON application_standard_review_plans (org_id, created_at DESC, id);

CREATE TABLE application_standard_operations (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    plan_id uuid NOT NULL,
    assignment_id uuid NOT NULL,
    approval_hash text NOT NULL CHECK (approval_hash ~ '^[a-f0-9]{64}$'),
    approved_by uuid NOT NULL,
    batch_size integer NOT NULL CHECK (batch_size BETWEEN 1 AND 100),
    state text NOT NULL DEFAULT 'queued'
        CHECK (state IN ('queued', 'running', 'waiting', 'paused', 'completed', 'failed', 'rolled_back', 'superseded')),
    lease_owner text NOT NULL DEFAULT '' CHECK (octet_length(lease_owner) <= 128),
    lease_generation bigint NOT NULL DEFAULT 0 CHECK (lease_generation >= 0),
    lease_until timestamptz,
    error_code text NOT NULL DEFAULT '' CHECK (error_code ~ '^[a-z0-9_]{0,128}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((lease_owner = '') = (lease_until IS NULL)),
    UNIQUE (org_id, plan_id),
    UNIQUE (org_id, id),
    FOREIGN KEY (org_id, plan_id) REFERENCES application_standard_review_plans(org_id, id),
    FOREIGN KEY (org_id, assignment_id) REFERENCES application_standard_assignments(org_id, id)
);
CREATE UNIQUE INDEX application_standard_operation_active_assignment_idx
ON application_standard_operations (assignment_id)
WHERE state IN ('queued', 'running', 'waiting', 'paused');
CREATE INDEX application_standard_operation_claim_idx
ON application_standard_operations (created_at, id)
WHERE state IN ('queued', 'running', 'waiting');

-- App identities survive service deletion so a skipped target remains visible.
-- The approved body is immutable; checkpoints cannot rewrite approved intent.
CREATE TABLE application_standard_operation_targets (
    operation_id uuid NOT NULL REFERENCES application_standard_operations(id) ON DELETE CASCADE,
    app_id uuid NOT NULL,
    position integer NOT NULL CHECK (position >= 0),
    approved_app jsonb NOT NULL CHECK (jsonb_typeof(approved_app) = 'object'),
    state text NOT NULL DEFAULT 'queued'
        CHECK (state IN ('queued', 'applying', 'persisted', 'observed', 'blocked', 'skipped', 'rolled_back')),
    desired_revision bigint NOT NULL DEFAULT 0 CHECK (desired_revision >= 0),
    error_code text NOT NULL DEFAULT '' CHECK (error_code ~ '^[a-z0-9_]{0,128}$'),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (operation_id, app_id),
    UNIQUE (operation_id, position)
);

-- Automatic enrollment uses the same fenced lease pattern as reviewed work.
-- Lease authority stays private to the control plane, outside API responses.
ALTER TABLE app_application_standards
    ADD COLUMN lease_owner text NOT NULL DEFAULT '' CHECK (octet_length(lease_owner) <= 128),
    ADD COLUMN lease_generation bigint NOT NULL DEFAULT 0 CHECK (lease_generation >= 0),
    ADD COLUMN lease_until timestamptz,
    ADD CONSTRAINT app_application_standards_lease_check CHECK ((lease_owner = '') = (lease_until IS NULL));

-- +goose StatementBegin
CREATE FUNCTION application_standard_review_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND pg_trigger_depth() > 1
       AND NOT EXISTS (SELECT 1 FROM orgs WHERE id = OLD.org_id) THEN
        RETURN OLD;
    END IF;
    IF TG_OP = 'DELETE' AND OLD.expires_at < now()
       AND NOT EXISTS (SELECT 1 FROM application_standard_operations WHERE plan_id = OLD.id) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'application standard review plans are immutable'
        USING ERRCODE = '23514', CONSTRAINT = 'application_standard_review_immutable';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_review_immutable
BEFORE UPDATE OR DELETE ON application_standard_review_plans
FOR EACH ROW EXECUTE FUNCTION application_standard_review_immutable();

-- +goose StatementBegin
CREATE FUNCTION application_standard_operation_intent_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF pg_trigger_depth() > 1 AND NOT EXISTS (SELECT 1 FROM orgs WHERE id = OLD.org_id) THEN
            RETURN OLD;
        END IF;
        RAISE EXCEPTION 'application standard operation history is retained'
            USING ERRCODE = '23514', CONSTRAINT = 'application_standard_operation_intent_immutable';
    END IF;
    IF NEW.lease_generation < OLD.lease_generation THEN
        RAISE EXCEPTION 'application standard worker generation regressed'
            USING ERRCODE = '23514', CONSTRAINT = 'application_standard_operation_generation';
    END IF;
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.org_id IS DISTINCT FROM OLD.org_id
       OR NEW.plan_id IS DISTINCT FROM OLD.plan_id OR NEW.assignment_id IS DISTINCT FROM OLD.assignment_id
       OR NEW.approval_hash IS DISTINCT FROM OLD.approval_hash OR NEW.approved_by IS DISTINCT FROM OLD.approved_by
       OR NEW.batch_size IS DISTINCT FROM OLD.batch_size OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'application standard operation intent is immutable'
            USING ERRCODE = '23514', CONSTRAINT = 'application_standard_operation_intent_immutable';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_operation_intent_immutable
BEFORE UPDATE OR DELETE ON application_standard_operations
FOR EACH ROW EXECUTE FUNCTION application_standard_operation_intent_immutable();

-- +goose StatementBegin
CREATE FUNCTION application_standard_target_intent_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF pg_trigger_depth() > 1 AND NOT EXISTS (SELECT 1 FROM application_standard_operations WHERE id = OLD.operation_id) THEN
            RETURN OLD;
        END IF;
        RAISE EXCEPTION 'application standard target history is retained'
            USING ERRCODE = '23514', CONSTRAINT = 'application_standard_target_intent_immutable';
    END IF;
    IF NEW.operation_id IS DISTINCT FROM OLD.operation_id OR NEW.app_id IS DISTINCT FROM OLD.app_id
       OR NEW.position IS DISTINCT FROM OLD.position OR NEW.approved_app IS DISTINCT FROM OLD.approved_app THEN
        RAISE EXCEPTION 'application standard target intent is immutable'
            USING ERRCODE = '23514', CONSTRAINT = 'application_standard_target_intent_immutable';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_target_intent_immutable
BEFORE UPDATE OR DELETE ON application_standard_operation_targets
FOR EACH ROW EXECUTE FUNCTION application_standard_target_intent_immutable();

-- +goose Down
DROP TRIGGER application_standard_target_intent_immutable ON application_standard_operation_targets;
DROP FUNCTION application_standard_target_intent_immutable();
DROP TRIGGER application_standard_operation_intent_immutable ON application_standard_operations;
DROP FUNCTION application_standard_operation_intent_immutable();
DROP TRIGGER application_standard_review_immutable ON application_standard_review_plans;
DROP FUNCTION application_standard_review_immutable();
ALTER TABLE app_application_standards
    DROP CONSTRAINT app_application_standards_lease_check,
    DROP COLUMN lease_owner, DROP COLUMN lease_generation, DROP COLUMN lease_until;
DROP TABLE application_standard_operation_targets;
DROP TABLE application_standard_operations;
DROP TABLE application_standard_review_plans;
