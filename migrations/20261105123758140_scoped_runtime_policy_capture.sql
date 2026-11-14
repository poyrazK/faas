-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- Resolve environment-owned scalar policy from the immutable deployment pin.
-- Current enrollment sources keep their existing exact materialized values;
-- a pin cannot override a standard, including a retained default or exception.
-- +goose StatementBegin
DO $install$
BEGIN
 IF to_regprocedure('application_standard_deployment_controls(apps,deployments,app_application_standards)') IS NULL THEN
 EXECUTE $function$
CREATE FUNCTION application_standard_deployment_controls(a apps, d deployments, e app_application_standards) RETURNS jsonb
LANGUAGE plpgsql STABLE AS $$
DECLARE settings jsonb; controls jsonb; projection jsonb; field text;
BEGIN
 SELECT s.settings::jsonb INTO settings
 FROM project_environment_workload_deployment_specs p
 JOIN project_environment_workload_specs s ON s.id=p.spec_id AND s.app_id=a.id
 WHERE p.deployment_id=d.id;
 IF settings IS NULL THEN
  controls:=jsonb_build_object('require_signed',a.require_signed,'security_policy',a.security_policy,
   'egress_allowlist',to_jsonb(a.egress_allowlist::text[]),'egress_ports',to_jsonb(a.egress_ports));
 ELSE
  controls:=jsonb_build_object(
   'require_signed',coalesce(settings->'require_signed','false'::jsonb),
   'security_policy',CASE WHEN settings->>'security_policy' IN ('off','warn','enforce') THEN settings->>'security_policy' ELSE 'off' END,
   'egress_allowlist',coalesce(nullif(settings->'egress_allowlist','null'::jsonb),'[]'::jsonb),
   'egress_ports',coalesce(nullif(settings->'egress_ports','null'::jsonb),'[]'::jsonb));
 END IF;
 projection:=jsonb_build_object('require_signed',controls->'require_signed','security_policy',controls->'security_policy',
  'egress_cidrs',controls->'egress_allowlist','egress_extra_ports',controls->'egress_ports');
 FOREACH field IN ARRAY ARRAY['require_signed','security_policy','egress_cidrs','egress_extra_ports'] LOOP
  IF coalesce(jsonb_array_length(e.effective->'sources'->field),0)>0
     AND projection->field IS DISTINCT FROM e.effective->'values'->field THEN
   RAISE EXCEPTION 'scoped runtime setting conflicts with current application standard'
    USING ERRCODE='23514',CONSTRAINT='application_standards_pending';
  END IF;
 END LOOP;
 RETURN controls;
END $$;
$function$;
 END IF;
END;
$install$;
-- +goose StatementEnd

-- Keep the original ownership locks, enrollment/exception fencing, native
-- producer verification, and signer/drain sources. Only the policy values used
-- by producer authorization and the resulting runtime capture are scoped.
-- +goose StatementBegin
DO $policy$
DECLARE definition text; original text; replacement text;
BEGIN
 original:='producers:=application_standard_runtime_producers(a,d);';
 replacement:='a := jsonb_populate_record(a,application_standard_deployment_controls(a,d,e));
  producers:=application_standard_runtime_producers(a,d);';
 definition:=pg_get_functiondef('application_standard_runtime_snapshot(uuid,uuid)'::regprocedure);
 IF position(replacement IN definition)>0 THEN
  RETURN; -- Never replace a guard already extended by this migration.
 END IF;
 IF position(original IN definition)=0 OR position('application_standard_deployment_controls' IN definition)>0 THEN
  RAISE EXCEPTION 'runtime snapshot differs from expected scoped-policy migration contract';
 END IF;
 EXECUTE replace(definition,original,replacement);
END;
$policy$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $policy$
DECLARE definition text; original text; replacement text;
BEGIN
 original:='a := jsonb_populate_record(a,application_standard_deployment_controls(a,d,e));
  producers:=application_standard_runtime_producers(a,d);';
 replacement:='producers:=application_standard_runtime_producers(a,d);';
 definition:=pg_get_functiondef('application_standard_runtime_snapshot(uuid,uuid)'::regprocedure);
 IF position(original IN definition)=0 THEN
  RAISE EXCEPTION 'runtime snapshot differs from expected scoped-policy rollback contract';
 END IF;
 EXECUTE replace(definition,original,replacement);
END;
$policy$;
-- +goose StatementEnd
DROP FUNCTION application_standard_deployment_controls(apps,deployments,app_application_standards);
