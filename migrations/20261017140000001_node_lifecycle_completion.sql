-- +goose Up
-- Quiesce old recovery controllers before applying; unfenced completion writes
-- are rejected once the guard is installed. Existing cycles start at 1.
ALTER TABLE compute_nodes ADD COLUMN lifecycle_generation bigint NOT NULL DEFAULT 1
 CONSTRAINT compute_nodes_lifecycle_generation_check CHECK (lifecycle_generation > 0);

CREATE TABLE node_lifecycle_completions (
 node_id uuid NOT NULL CHECK (node_id <> '00000000-0000-0000-0000-000000000000'),
 source_generation bigint NOT NULL CHECK (source_generation > 0),
 result_generation bigint NOT NULL CHECK (result_generation > source_generation),
 source_lifecycle text NOT NULL CHECK (source_lifecycle IN ('draining','force_draining','recovering')),
 result_lifecycle text NOT NULL CHECK (result_lifecycle IN ('maintenance','active')),
 node_name text NOT NULL,
 initiated_at timestamptz NOT NULL,
 completed_at timestamptz NOT NULL,
 drained_count bigint NOT NULL CHECK (drained_count >= 0),
 migrated_count bigint NOT NULL CHECK (migrated_count >= 0),
 recreated_count bigint NOT NULL CHECK (recreated_count >= 0),
 audit_outbox_id bigint NOT NULL CHECK (audit_outbox_id > 0),
 PRIMARY KEY (node_id,source_generation),
 CHECK (result_generation=source_generation+1),
 CHECK ((source_lifecycle='recovering' AND result_lifecycle='active') OR
        (source_lifecycle IN ('draining','force_draining') AND result_lifecycle='maintenance'))
);

-- +goose StatementBegin
CREATE FUNCTION node_lifecycle_completion_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'Node lifecycle completion receipts are immutable' USING ERRCODE='23514';
END $$;
-- +goose StatementEnd
CREATE TRIGGER node_lifecycle_completion_immutable BEFORE UPDATE OR DELETE ON node_lifecycle_completions
 FOR EACH ROW EXECUTE FUNCTION node_lifecycle_completion_immutable();

-- +goose StatementBegin
CREATE FUNCTION compute_node_lifecycle_generation_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  NEW.lifecycle_generation:=1;
 ELSE
  IF NEW.lifecycle IS DISTINCT FROM OLD.lifecycle
   OR NEW.drain_initiated_at IS DISTINCT FROM OLD.drain_initiated_at
   OR NEW.recovery_initiated_at IS DISTINCT FROM OLD.recovery_initiated_at THEN
   NEW.lifecycle_generation:=OLD.lifecycle_generation+1;
  ELSIF NEW.lifecycle_generation IS DISTINCT FROM OLD.lifecycle_generation THEN
   RAISE EXCEPTION 'Node lifecycle generation is database-owned' USING ERRCODE='23514';
  END IF;
  IF ((OLD.lifecycle IN ('draining','force_draining') AND NEW.lifecycle='maintenance')
   OR (OLD.lifecycle='recovering' AND NEW.lifecycle='active')) AND NOT EXISTS (
   SELECT 1 FROM node_lifecycle_completions c WHERE c.node_id=OLD.id
    AND c.source_generation=OLD.lifecycle_generation AND c.result_generation=NEW.lifecycle_generation
    AND c.source_lifecycle=OLD.lifecycle::text AND c.result_lifecycle=NEW.lifecycle::text
  ) THEN
   RAISE EXCEPTION 'Node lifecycle completion requires a generation-bound receipt' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER compute_node_lifecycle_generation_guard
 BEFORE INSERT OR UPDATE OF lifecycle,lifecycle_generation,drain_initiated_at,recovery_initiated_at ON compute_nodes
 FOR EACH ROW EXECUTE FUNCTION compute_node_lifecycle_generation_guard();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM node_lifecycle_completions)
  OR EXISTS (SELECT 1 FROM compute_nodes WHERE lifecycle_generation>1) THEN
  RAISE EXCEPTION 'Preserve advanced node lifecycle generations and completion evidence before downgrade';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER compute_node_lifecycle_generation_guard ON compute_nodes;
DROP FUNCTION compute_node_lifecycle_generation_guard();
DROP TRIGGER node_lifecycle_completion_immutable ON node_lifecycle_completions;
DROP FUNCTION node_lifecycle_completion_immutable();
DROP TABLE node_lifecycle_completions;
ALTER TABLE compute_nodes DROP COLUMN lifecycle_generation;
