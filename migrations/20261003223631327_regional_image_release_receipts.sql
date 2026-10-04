-- +goose Up
-- +goose StatementBegin
CREATE TABLE regional_image_release_receipts (
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    release_id uuid NOT NULL CHECK (release_id <> '00000000-0000-0000-0000-000000000000'),
    cell_id uuid NOT NULL CHECK (cell_id <> '00000000-0000-0000-0000-000000000000'),
    -- Historical authorizing owner: survive account deletion after app transfer.
    account_id uuid NOT NULL CHECK (account_id <> '00000000-0000-0000-0000-000000000000'),
    image_ref text NOT NULL CHECK (octet_length(image_ref) BETWEEN 1 AND 2048),
    deployment_id uuid REFERENCES deployments(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (app_id, release_id)
);
CREATE FUNCTION protect_regional_image_release_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF EXISTS (SELECT 1 FROM apps WHERE id = OLD.app_id) THEN
            RAISE EXCEPTION 'release receipts remain until the app identity is removed'
                USING ERRCODE = '23514', CONSTRAINT = 'regional_image_release_identity';
        END IF;
        RETURN OLD;
    END IF;
    IF TG_OP = 'UPDATE' THEN
        IF ROW(NEW.app_id, NEW.release_id, NEW.cell_id, NEW.account_id, NEW.image_ref, NEW.created_at)
            IS DISTINCT FROM ROW(OLD.app_id, OLD.release_id, OLD.cell_id, OLD.account_id, OLD.image_ref, OLD.created_at)
            OR (NEW.deployment_id IS DISTINCT FROM OLD.deployment_id AND NEW.deployment_id IS NOT NULL) THEN
            RAISE EXCEPTION 'regional release identity is immutable'
                USING ERRCODE = '23514', CONSTRAINT = 'regional_image_release_identity';
        END IF;
        RETURN NEW;
    END IF;
    PERFORM 1 FROM control_plane_cells WHERE id = NEW.cell_id FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'release receipts require the initialized regional cell'
            USING ERRCODE = '23514', CONSTRAINT = 'regional_image_release_scope';
    END IF;
    PERFORM 1 FROM apps WHERE id = NEW.app_id AND account_id = NEW.account_id
        AND status IN ('active', 'evicted_cold') FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'regional release app ownership changed'
            USING ERRCODE = '23514', CONSTRAINT = 'regional_image_release_owner';
    END IF;
    PERFORM 1 FROM deployments WHERE id = NEW.deployment_id AND app_id = NEW.app_id
        AND image_digest = NEW.image_ref AND kind = 'image';
    IF NOT FOUND THEN
        RAISE EXCEPTION 'regional receipt must identify the accepted image deployment'
            USING ERRCODE = '23514', CONSTRAINT = 'regional_image_release_deployment';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER regional_image_release_identity_guard
    BEFORE INSERT OR UPDATE OR DELETE ON regional_image_release_receipts
    FOR EACH ROW EXECUTE FUNCTION protect_regional_image_release_receipt();
CREATE FUNCTION refuse_regional_image_release_receipt_truncate() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'regional release receipts cannot be truncated'
        USING ERRCODE = '23514', CONSTRAINT = 'regional_image_release_identity';
END;
$$;
CREATE TRIGGER regional_image_release_truncate_guard
    BEFORE TRUNCATE ON regional_image_release_receipts
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_regional_image_release_receipt_truncate();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    LOCK TABLE regional_image_release_receipts IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT 1 FROM regional_image_release_receipts) THEN
        RAISE EXCEPTION 'cannot downgrade while durable regional release receipts exist';
    END IF;
END;
$$;
DROP TABLE regional_image_release_receipts;
DROP FUNCTION protect_regional_image_release_receipt();
DROP FUNCTION refuse_regional_image_release_receipt_truncate();
-- +goose StatementEnd
