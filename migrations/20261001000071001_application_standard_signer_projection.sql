-- +goose Up
-- Disambiguate the local signer label from publisher.name in projection queries.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_signer_projection_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE app uuid; control_name text; logical uuid; e jsonb; expected record; backup jsonb; desired jsonb;
BEGIN
    IF TG_OP = 'DELETE' THEN app := OLD.app_id; control_name := OLD.signer_name;
    ELSE app := NEW.app_id; control_name := NEW.signer_name; END IF;
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
    WHERE app_id = app AND field = 'trusted_publishers' AND physical_id = control_name;
    IF logical IS NULL THEN
        SELECT logical_id INTO logical FROM application_standard_control_backups
        WHERE app_id = app AND field = 'trusted_publishers' AND body->>'SignerName' = control_name
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
            AND b.resource_id = p.id AND b.physical_id = control_name
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

-- +goose Down
-- The prior definition has ambiguous identifier resolution and is not restored.
