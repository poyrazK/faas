-- filename: 20261006153244436_global_runtime_quota_issuer.sql

-- +goose Up
-- ADR-644: a directory issuer is distinct from every regional cell and survives key rotation.
CREATE TABLE global_runtime_quota_issuer (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    issuer_id uuid NOT NULL UNIQUE CHECK (issuer_id <> '00000000-0000-0000-0000-000000000000')
);

-- +goose StatementBegin
CREATE FUNCTION protect_global_runtime_quota_issuer() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM assert_global_directory_scope();
    PERFORM pg_advisory_xact_lock(hashtextextended('gregale.global-runtime-quota-issuer', 0));
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'global runtime quota issuer is immutable' USING ERRCODE='23514', CONSTRAINT='global_runtime_quota_identity';
    END IF;
    IF EXISTS (SELECT 1 FROM global_directory_cells WHERE id=NEW.issuer_id) THEN
        RAISE EXCEPTION 'directory issuer cannot identify a regional cell' USING ERRCODE='23514', CONSTRAINT='global_runtime_quota_identity';
    END IF;
    RETURN NEW;
END; $$;
CREATE TRIGGER global_runtime_quota_issuer_identity BEFORE INSERT OR UPDATE OR DELETE
ON global_runtime_quota_issuer FOR EACH ROW EXECUTE FUNCTION protect_global_runtime_quota_issuer();
CREATE TRIGGER global_runtime_quota_issuer_truncate BEFORE TRUNCATE
ON global_runtime_quota_issuer FOR EACH STATEMENT EXECUTE FUNCTION protect_global_runtime_quota_issuer();
CREATE FUNCTION guard_global_runtime_quota_issuer_scope() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME='global_directory_cells' THEN
        PERFORM assert_global_directory_scope();
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('gregale.global-runtime-quota-issuer', 0));
    IF EXISTS (SELECT 1 FROM global_runtime_quota_issuer WHERE TG_TABLE_NAME='control_plane_cells' OR issuer_id=NEW.id) THEN
        RAISE EXCEPTION 'retained directory issuer conflicts with regional cell identity' USING ERRCODE='23514', CONSTRAINT='global_runtime_quota_identity';
    END IF;
    RETURN NEW;
END; $$;
CREATE TRIGGER global_runtime_quota_issuer_cell_scope BEFORE INSERT ON control_plane_cells
FOR EACH ROW EXECUTE FUNCTION guard_global_runtime_quota_issuer_scope();
CREATE TRIGGER global_runtime_quota_issuer_registry_scope BEFORE INSERT OR UPDATE ON global_directory_cells
FOR EACH ROW EXECUTE FUNCTION guard_global_runtime_quota_issuer_scope();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    LOCK TABLE global_runtime_quota_issuer IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT 1 FROM global_runtime_quota_issuer) THEN
        RAISE EXCEPTION 'cannot downgrade a retained global runtime quota issuer';
    END IF;
END; $$;
DROP TRIGGER global_runtime_quota_issuer_cell_scope ON control_plane_cells;
DROP TRIGGER global_runtime_quota_issuer_registry_scope ON global_directory_cells;
DROP FUNCTION guard_global_runtime_quota_issuer_scope();
DROP TABLE global_runtime_quota_issuer;
DROP FUNCTION protect_global_runtime_quota_issuer();
-- +goose StatementEnd
