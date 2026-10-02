-- +goose Up
ALTER TABLE builds ADD COLUMN builder_node_id uuid REFERENCES compute_nodes(id);
ALTER TABLE builder_vm_cleanup ADD COLUMN builder_node_id uuid REFERENCES compute_nodes(id);
CREATE INDEX builds_builder_node_idx ON builds (builder_node_id) WHERE builder_node_id IS NOT NULL;
CREATE INDEX builder_vm_cleanup_node_idx ON builder_vm_cleanup (builder_node_id, next_attempt_at);

-- A claim is placed using the persisted app region and canonical node ID.
-- Cache affinity is only a preference inside that region. Direct SQL and
-- older workers cannot start a regional build without an eligible owner.
-- +goose StatementBegin
CREATE FUNCTION enforce_build_compute_region() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE required_region text; node_region text; node_active boolean;
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.status <> 'queued' AND
       (NEW.builder_node_id IS DISTINCT FROM OLD.builder_node_id OR NEW.deployment_id IS DISTINCT FROM OLD.deployment_id) THEN
        RAISE EXCEPTION 'build claim placement is immutable until requeued'
            USING ERRCODE = '23514', CONSTRAINT = 'builds_claim_placement_immutable';
    END IF;
    IF NEW.status = 'running' THEN
        SELECT a.compute_region INTO required_region FROM deployments d JOIN apps a ON a.id = d.app_id WHERE d.id = NEW.deployment_id;
        IF coalesce(required_region, '') <> '' THEN
            SELECT region, active INTO node_region, node_active FROM compute_nodes WHERE id = NEW.builder_node_id FOR SHARE;
            IF node_region IS DISTINCT FROM required_region OR node_active IS DISTINCT FROM true THEN
                RAISE EXCEPTION 'builder is outside selected compute region or inactive'
                    USING ERRCODE = '23514', CONSTRAINT = 'builds_compute_region';
            END IF;
        END IF;
        IF (TG_OP = 'INSERT' OR OLD.status <> 'running') AND EXISTS (SELECT 1 FROM builder_vm_cleanup WHERE build_id = NEW.id) THEN
            RAISE EXCEPTION 'previous builder VM cleanup is pending'
                USING ERRCODE = '23514', CONSTRAINT = 'builds_vm_cleanup_pending';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER builds_compute_region_guard BEFORE INSERT OR UPDATE OF status, deployment_id, builder_node_id
    ON builds FOR EACH ROW EXECUTE FUNCTION enforce_build_compute_region();

-- Every cancellation/supersede/timeout path already inserts a cleanup row.
-- Capture its owner here, in the same transaction, without requiring the
-- cancelling control-plane process to know where the builder VM lives.
-- +goose StatementBegin
CREATE FUNCTION capture_builder_vm_cleanup_node() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT builder_node_id INTO NEW.builder_node_id FROM builds WHERE id = NEW.build_id;
    ELSIF NEW.builder_node_id IS DISTINCT FROM OLD.builder_node_id OR NEW.build_id IS DISTINCT FROM OLD.build_id THEN
        RAISE EXCEPTION 'builder VM cleanup owner is immutable'
            USING ERRCODE = '23514', CONSTRAINT = 'builder_vm_cleanup_owner_immutable';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER builder_vm_cleanup_node_guard BEFORE INSERT OR UPDATE OF build_id, builder_node_id
    ON builder_vm_cleanup FOR EACH ROW EXECUTE FUNCTION capture_builder_vm_cleanup_node();

-- +goose Down
DROP TRIGGER builder_vm_cleanup_node_guard ON builder_vm_cleanup;
DROP FUNCTION capture_builder_vm_cleanup_node();
DROP TRIGGER builds_compute_region_guard ON builds;
DROP FUNCTION enforce_build_compute_region();
DROP INDEX builder_vm_cleanup_node_idx;
DROP INDEX builds_builder_node_idx;
ALTER TABLE builder_vm_cleanup DROP COLUMN builder_node_id;
ALTER TABLE builds DROP COLUMN builder_node_id;
