-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- Resolve only VM shape from immutable deployment inputs. Enrollment, signer,
-- egress and artifact authorization remain on their existing locked sources.
-- +goose StatementBegin
CREATE FUNCTION application_standard_deployment_runtime(a apps, d deployments) RETURNS jsonb
LANGUAGE plpgsql STABLE AS $$
DECLARE settings jsonb;
BEGIN
 SELECT s.settings::jsonb INTO settings
 FROM project_environment_workload_deployment_specs p
 JOIN project_environment_workload_specs s ON s.id=p.spec_id AND s.app_id=a.id
 WHERE p.deployment_id=d.id;
 RETURN jsonb_build_object(
  'type',coalesce(d.environment_workload_runtime->>'app_type',settings->>'type',a.type),
  'runtime',coalesce(d.environment_workload_runtime->>'runtime_base',settings->>'runtime',a.runtime,''),
  'ram_mb',coalesce(settings->'ram_mb',to_jsonb(a.ram_mb)),
  'app_protocol',coalesce(settings->'app_protocol',to_jsonb(a.app_protocol)));
END $$;
-- +goose StatementEnd

-- Patch the established locked snapshot, preserving every control fence and
-- native artifact check. Producers also need the pinned runtime base.
-- +goose StatementBegin
DO $runtime$
DECLARE definition text; original text; replacement text;
BEGIN
 original:='IF artifact_id IS NOT NULL THEN producers:=application_standard_runtime_producers(a,d); END IF;';
 replacement:='IF artifact_id IS NOT NULL THEN
  a.type := application_standard_deployment_runtime(a,d)->>''type'';
  a.runtime := application_standard_deployment_runtime(a,d)->>''runtime'';
  a.ram_mb := (application_standard_deployment_runtime(a,d)->>''ram_mb'')::integer;
  a.app_protocol := application_standard_deployment_runtime(a,d)->>''app_protocol'';
  producers:=application_standard_runtime_producers(a,d);
 END IF;';
 definition:=pg_get_functiondef('application_standard_runtime_snapshot(uuid,uuid)'::regprocedure);
 IF position(original IN definition)=0 THEN
  RAISE EXCEPTION 'runtime capture differs from the expected deployment shape contract';
 END IF;
 EXECUTE replace(definition,original,replacement);
END;
$runtime$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $runtime$
DECLARE definition text; original text; replacement text;
BEGIN
 original:='IF artifact_id IS NOT NULL THEN
  a.type := application_standard_deployment_runtime(a,d)->>''type'';
  a.runtime := application_standard_deployment_runtime(a,d)->>''runtime'';
  a.ram_mb := (application_standard_deployment_runtime(a,d)->>''ram_mb'')::integer;
  a.app_protocol := application_standard_deployment_runtime(a,d)->>''app_protocol'';
  producers:=application_standard_runtime_producers(a,d);
 END IF;';
 replacement:='IF artifact_id IS NOT NULL THEN producers:=application_standard_runtime_producers(a,d); END IF;';
 definition:=pg_get_functiondef('application_standard_runtime_snapshot(uuid,uuid)'::regprocedure);
 IF position(original IN definition)=0 THEN
  RAISE EXCEPTION 'runtime capture differs from the expected deployment shape contract';
 END IF;
 EXECUTE replace(definition,original,replacement);
END;
$runtime$;
-- +goose StatementEnd
DROP FUNCTION application_standard_deployment_runtime(apps,deployments);
