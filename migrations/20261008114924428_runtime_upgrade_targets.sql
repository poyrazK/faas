-- filename: 20261008114924428_runtime_upgrade_targets.sql

-- +goose Up
CREATE TABLE IF NOT EXISTS deployment_runtime_upgrade_targets (
 deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
 release_id text NOT NULL REFERENCES runtime_releases(id),
 source_sha256 text NOT NULL CHECK (source_sha256 ~ '^[a-f0-9]{64}$'),
 source_root text NOT NULL CHECK (octet_length(source_root) <= 4096),
 source_bytes bigint NOT NULL CHECK (source_bytes > 0),
 kind text NOT NULL CHECK (kind IN ('tarball','github','preview')),
 handler text NOT NULL CHECK (octet_length(handler) <= 4096)
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_runtime_upgrade_target() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP = 'DELETE' THEN
  IF EXISTS(SELECT 1 FROM deployments WHERE id = OLD.deployment_id) THEN
   RAISE EXCEPTION 'runtime upgrade target is immutable' USING ERRCODE = '23514';
  END IF;
  RETURN OLD;
 END IF;
 IF NEW IS DISTINCT FROM OLD THEN
  RAISE EXCEPTION 'runtime upgrade target is immutable' USING ERRCODE = '23514';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS runtime_upgrade_target_immutable ON deployment_runtime_upgrade_targets;
CREATE TRIGGER runtime_upgrade_target_immutable BEFORE UPDATE OR DELETE ON deployment_runtime_upgrade_targets
FOR EACH ROW EXECUTE FUNCTION guard_runtime_upgrade_target();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_runtime_upgrade_source() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM deployment_runtime_upgrade_targets WHERE deployment_id = OLD.id) AND
  ROW(NEW.app_id, NEW.source_sha256, NEW.source_root, NEW.source_bytes, NEW.kind, NEW.handler)
  IS DISTINCT FROM ROW(OLD.app_id, OLD.source_sha256, OLD.source_root, OLD.source_bytes, OLD.kind, OLD.handler) THEN
  RAISE EXCEPTION 'runtime upgrade source is immutable' USING ERRCODE = '23514';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS runtime_upgrade_source_immutable ON deployments;
CREATE TRIGGER runtime_upgrade_source_immutable BEFORE UPDATE ON deployments
FOR EACH ROW EXECUTE FUNCTION guard_runtime_upgrade_source();

-- +goose Down
-- Forward-only: retries must retain the exact chosen runtime and source.
SELECT 1;
