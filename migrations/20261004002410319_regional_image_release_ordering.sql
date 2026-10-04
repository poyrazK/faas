-- +goose Up
-- +goose StatementBegin
ALTER TABLE regional_image_release_receipts
    ADD COLUMN global_app_id uuid,
    ADD COLUMN generation bigint,
    ADD CONSTRAINT regional_image_release_ordering_shape CHECK (
        (global_app_id IS NULL AND generation IS NULL) OR
        (global_app_id IS NOT NULL AND global_app_id <> '00000000-0000-0000-0000-000000000000'
         AND generation IS NOT NULL AND generation BETWEEN 1 AND 9007199254740991)
    );
CREATE UNIQUE INDEX regional_image_release_generation_unique
    ON regional_image_release_receipts(app_id, global_app_id, generation)
    WHERE global_app_id IS NOT NULL;
CREATE TABLE regional_image_release_heads (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    cell_id uuid NOT NULL CHECK (cell_id <> '00000000-0000-0000-0000-000000000000'),
    account_id uuid NOT NULL CHECK (account_id <> '00000000-0000-0000-0000-000000000000'),
    global_app_id uuid NOT NULL CHECK (global_app_id <> '00000000-0000-0000-0000-000000000000'),
    generation bigint NOT NULL CHECK (generation BETWEEN 1 AND 9007199254740991),
    release_id uuid NOT NULL,
    FOREIGN KEY (app_id, release_id) REFERENCES regional_image_release_receipts(app_id, release_id)
);
CREATE FUNCTION protect_regional_image_release_head() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF EXISTS (SELECT 1 FROM apps WHERE id = OLD.app_id) THEN
            RAISE EXCEPTION 'regional release heads remain until app identity removal'
                USING ERRCODE = '23514', CONSTRAINT = 'regional_image_release_order';
        END IF;
        RETURN OLD;
    END IF;
    IF TG_OP = 'UPDATE' THEN
        IF ROW(NEW.app_id, NEW.cell_id, NEW.account_id, NEW.global_app_id)
            IS DISTINCT FROM ROW(OLD.app_id, OLD.cell_id, OLD.account_id, OLD.global_app_id)
            OR NEW.generation <= OLD.generation THEN
            RAISE EXCEPTION 'regional release head identity is immutable and generation must increase'
                USING ERRCODE = '23514', CONSTRAINT = 'regional_image_release_order';
        END IF;
    END IF;
    PERFORM 1 FROM regional_image_release_receipts r JOIN apps a ON a.id = r.app_id
    WHERE r.app_id = NEW.app_id AND r.release_id = NEW.release_id AND r.cell_id = NEW.cell_id
      AND r.account_id = NEW.account_id AND r.global_app_id = NEW.global_app_id
      AND r.generation = NEW.generation AND a.account_id = NEW.account_id
      AND a.status IN ('active', 'evicted_cold');
    IF NOT FOUND THEN
        RAISE EXCEPTION 'regional release head must identify its accepted ordered receipt'
            USING ERRCODE = '23514', CONSTRAINT = 'regional_image_release_order';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER regional_image_release_head_guard BEFORE INSERT OR UPDATE OR DELETE
    ON regional_image_release_heads FOR EACH ROW EXECUTE FUNCTION protect_regional_image_release_head();
CREATE TRIGGER regional_image_release_head_truncate_guard BEFORE TRUNCATE
    ON regional_image_release_heads FOR EACH STATEMENT EXECUTE FUNCTION refuse_regional_image_release_receipt_truncate();
CREATE FUNCTION protect_regional_image_release_ordering() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF ROW(NEW.global_app_id, NEW.generation) IS DISTINCT FROM ROW(OLD.global_app_id, OLD.generation) THEN
        RAISE EXCEPTION 'accepted regional release ordering is immutable'
            USING ERRCODE = '23514', CONSTRAINT = 'regional_image_release_identity';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER regional_image_release_ordering_identity_guard BEFORE UPDATE
    ON regional_image_release_receipts FOR EACH ROW EXECUTE FUNCTION protect_regional_image_release_ordering();
CREATE FUNCTION advance_regional_image_release_head() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.global_app_id IS NULL THEN
        RETURN NEW;
    END IF;
    INSERT INTO regional_image_release_heads(app_id, cell_id, account_id, global_app_id, generation, release_id)
        VALUES (NEW.app_id, NEW.cell_id, NEW.account_id, NEW.global_app_id, NEW.generation, NEW.release_id)
    ON CONFLICT (app_id) DO UPDATE SET generation = EXCLUDED.generation, release_id = EXCLUDED.release_id
        WHERE regional_image_release_heads.cell_id = EXCLUDED.cell_id
          AND regional_image_release_heads.account_id = EXCLUDED.account_id
          AND regional_image_release_heads.global_app_id = EXCLUDED.global_app_id
          AND regional_image_release_heads.generation < EXCLUDED.generation;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'regional global release generation is stale or stream identity conflicts'
            USING ERRCODE = '23514', CONSTRAINT = 'regional_image_release_order';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER regional_image_release_advance_head AFTER INSERT
    ON regional_image_release_receipts FOR EACH ROW EXECUTE FUNCTION advance_regional_image_release_head();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    LOCK TABLE regional_image_release_receipts, regional_image_release_heads IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT 1 FROM regional_image_release_receipts WHERE global_app_id IS NOT NULL)
        OR EXISTS (SELECT 1 FROM regional_image_release_heads) THEN
        RAISE EXCEPTION 'cannot downgrade while ordered regional release receipts exist';
    END IF;
END;
$$;
DROP TRIGGER regional_image_release_advance_head ON regional_image_release_receipts;
DROP TRIGGER regional_image_release_ordering_identity_guard ON regional_image_release_receipts;
DROP TABLE regional_image_release_heads;
DROP FUNCTION advance_regional_image_release_head();
DROP FUNCTION protect_regional_image_release_ordering();
DROP FUNCTION protect_regional_image_release_head();
DROP INDEX regional_image_release_generation_unique;
ALTER TABLE regional_image_release_receipts DROP CONSTRAINT regional_image_release_ordering_shape,
    DROP COLUMN global_app_id, DROP COLUMN generation;
-- +goose StatementEnd
