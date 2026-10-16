-- +goose Up
-- These tables contain private ciphertext/control restoration data. Customer
-- views use only logical bindings and content hashes, never backup bodies.
CREATE TABLE application_standard_control_bindings (
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    field text NOT NULL CHECK (field IN ('log_destinations', 'trusted_publishers')),
    resource_id uuid NOT NULL,
    physical_id text NOT NULL CHECK (octet_length(physical_id) BETWEEN 1 AND 128),
    PRIMARY KEY (app_id, field, resource_id),
    UNIQUE (app_id, field, physical_id)
);
CREATE TABLE application_standard_control_backups (
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    field text NOT NULL CHECK (field IN ('log_destinations', 'trusted_publishers')),
    logical_id uuid NOT NULL,
    body jsonb NOT NULL CHECK (jsonb_typeof(body) = 'object'),
    config_hash text NOT NULL CHECK (config_hash ~ '^[a-f0-9]{64}$'),
    PRIMARY KEY (app_id, field, logical_id)
);

-- Scalar writers can install the current resolved projection; legacy patches
-- cannot change a managed value outside the shared intent/resolver boundary.
-- No session flag or privileged bypass is used by the materializer.
-- +goose StatementBegin
CREATE FUNCTION application_standard_scalar_control_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE e jsonb;
BEGIN
    SELECT effective INTO e FROM app_application_standards WHERE app_id = OLD.id;
    IF (coalesce(jsonb_array_length(e->'sources'->'require_signed'), 0) > 0
        AND to_jsonb(NEW.require_signed) IS DISTINCT FROM e->'values'->'require_signed')
       OR (coalesce(jsonb_array_length(e->'sources'->'security_policy'), 0) > 0
        AND to_jsonb(NEW.security_policy) IS DISTINCT FROM e->'values'->'security_policy')
       OR (coalesce(jsonb_array_length(e->'sources'->'egress_cidrs'), 0) > 0
        AND to_jsonb(NEW.egress_allowlist::text[]) IS DISTINCT FROM e->'values'->'egress_cidrs')
       OR (coalesce(jsonb_array_length(e->'sources'->'egress_extra_ports'), 0) > 0
        AND to_jsonb(NEW.egress_ports) IS DISTINCT FROM e->'values'->'egress_extra_ports') THEN
        RAISE EXCEPTION 'application standard manages this control'
            USING ERRCODE = '23514', CONSTRAINT = 'application_standard_managed_control';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_scalar_control_guard
BEFORE UPDATE OF require_signed, security_policy, egress_allowlist, egress_ports ON apps
FOR EACH ROW EXECUTE FUNCTION application_standard_scalar_control_guard();

-- Shared control fences run first (trigger names are ordered). A materializer
-- also try-locks existing child rows before touching them, avoiding a reverse
-- wait on a writer that acquired its row before reaching the advisory fence.
-- +goose StatementBegin
CREATE FUNCTION application_standard_drain_projection_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE app uuid; logical uuid; e jsonb; expected record; backup jsonb; desired jsonb;
BEGIN
    IF TG_OP = 'DELETE' THEN app := OLD.app_id; ELSE app := NEW.app_id; END IF;
    IF NOT EXISTS (SELECT 1 FROM apps WHERE id = app AND status <> 'deleted') THEN
        IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
        RETURN NEW;
    END IF;
    SELECT effective INTO e FROM app_application_standards WHERE app_id = app;
    IF coalesce(jsonb_array_length(e->'sources'->'log_destinations'), 0) = 0 THEN
        IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' THEN logical := OLD.id; ELSE logical := NEW.id; END IF;
    SELECT resource_id INTO logical FROM application_standard_control_bindings
    WHERE app_id = app AND field = 'log_destinations' AND physical_id = logical::text;
    IF logical IS NULL THEN
        IF TG_OP = 'DELETE' THEN logical := OLD.id; ELSE logical := NEW.id; END IF;
    END IF;
    desired := coalesce(e->'values'->'log_destinations', '[]'::jsonb);
    IF TG_OP = 'DELETE' THEN
        IF NOT desired ? logical::text THEN RETURN OLD; END IF;
    ELSIF desired ? logical::text THEN
        SELECT d.* INTO expected FROM application_standard_log_destinations d
        JOIN apps a ON a.org_id = d.org_id AND a.id = app
        JOIN application_standard_control_bindings b ON b.app_id = app AND b.field = 'log_destinations'
            AND b.resource_id = d.id AND b.physical_id = NEW.id::text
        WHERE d.id = logical;
        IF FOUND AND NEW.app_id = app AND NEW.account_id = (SELECT account_id FROM apps WHERE id = app)
            AND NEW.kind = expected.kind AND NEW.target_url = expected.target_url
            AND coalesce(NEW.auth_header_sealed, ''::bytea) = coalesce(expected.auth_header_sealed, ''::bytea)
            AND NEW.enabled THEN RETURN NEW; END IF;
        SELECT body INTO backup FROM application_standard_control_backups
        WHERE app_id = app AND field = 'log_destinations' AND logical_id = logical;
        IF backup IS NOT NULL AND NEW.id::text = backup->>'ID' AND NEW.app_id::text = backup->>'AppID'
            AND NEW.account_id::text = backup->>'AccountID' AND NEW.kind = backup->>'Kind'
            AND NEW.target_url = backup->>'TargetURL' AND NEW.enabled = (backup->>'Enabled')::boolean
            AND coalesce(NEW.auth_header_sealed, ''::bytea) = decode(coalesce(backup->>'AuthHeaderSealed', ''), 'base64') THEN RETURN NEW; END IF;
    END IF;
    RAISE EXCEPTION 'application standard manages this control'
        USING ERRCODE = '23514', CONSTRAINT = 'application_standard_managed_control';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_z_drain_projection_guard
BEFORE INSERT OR UPDATE OR DELETE ON app_log_drains
FOR EACH ROW EXECUTE FUNCTION application_standard_drain_projection_guard();

-- +goose StatementBegin
CREATE FUNCTION application_standard_signer_projection_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE app uuid; name text; logical uuid; e jsonb; expected record; backup jsonb; desired jsonb;
BEGIN
    IF TG_OP = 'DELETE' THEN app := OLD.app_id; name := OLD.signer_name;
    ELSE app := NEW.app_id; name := NEW.signer_name; END IF;
    IF NOT EXISTS (SELECT 1 FROM apps WHERE id = app AND status <> 'deleted') THEN
        IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
        RETURN NEW;
    END IF;
    SELECT effective INTO e FROM app_application_standards WHERE app_id = app;
    IF coalesce(jsonb_array_length(e->'sources'->'trusted_publishers'), 0) = 0 THEN
        IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
        RETURN NEW;
    END IF;
    SELECT resource_id INTO logical FROM application_standard_control_bindings
    WHERE app_id = app AND field = 'trusted_publishers' AND physical_id = name;
    IF logical IS NULL THEN
        SELECT logical_id INTO logical FROM application_standard_control_backups
        WHERE app_id = app AND field = 'trusted_publishers' AND body->>'SignerName' = name
          AND decode(coalesce(body->>'CosignPublicKey', ''), 'base64') = CASE WHEN TG_OP = 'DELETE' THEN OLD.cosign_public_key ELSE NEW.cosign_public_key END
        ORDER BY logical_id LIMIT 1;
    END IF;
    desired := coalesce(e->'values'->'trusted_publishers', '[]'::jsonb);
    IF TG_OP = 'DELETE' THEN
        IF logical IS NULL OR NOT desired ? logical::text THEN RETURN OLD; END IF;
    ELSIF logical IS NOT NULL AND desired ? logical::text THEN
        SELECT p.* INTO expected FROM application_standard_publishers p
        JOIN apps a ON a.org_id = p.org_id AND a.id = app
        JOIN application_standard_control_bindings b ON b.app_id = app AND b.field = 'trusted_publishers'
            AND b.resource_id = p.id AND b.physical_id = name
        WHERE p.id = logical;
        IF FOUND AND NEW.app_id = app AND NEW.account_id = (SELECT account_id FROM apps WHERE id = app)
            AND NEW.cosign_public_key = expected.public_key_der THEN RETURN NEW; END IF;
        SELECT body INTO backup FROM application_standard_control_backups
        WHERE app_id = app AND field = 'trusted_publishers' AND logical_id = logical;
        IF backup IS NOT NULL AND NEW.app_id::text = backup->>'AppID' AND NEW.account_id::text = backup->>'AccountID'
            AND NEW.signer_name = backup->>'SignerName'
            AND NEW.cosign_public_key = decode(coalesce(backup->>'CosignPublicKey', ''), 'base64') THEN RETURN NEW; END IF;
    END IF;
    RAISE EXCEPTION 'application standard manages this control'
        USING ERRCODE = '23514', CONSTRAINT = 'application_standard_managed_control';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_z_signer_projection_guard
BEFORE INSERT OR UPDATE OR DELETE ON app_trusted_signers
FOR EACH ROW EXECUTE FUNCTION application_standard_signer_projection_guard();

-- +goose Down
DROP TRIGGER application_standard_z_signer_projection_guard ON app_trusted_signers;
DROP FUNCTION application_standard_signer_projection_guard();
DROP TRIGGER application_standard_z_drain_projection_guard ON app_log_drains;
DROP FUNCTION application_standard_drain_projection_guard();
DROP TRIGGER application_standard_scalar_control_guard ON apps;
DROP FUNCTION application_standard_scalar_control_guard();
DROP TABLE application_standard_control_backups;
DROP TABLE application_standard_control_bindings;
