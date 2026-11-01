-- filename: 20261101083902561_runtime_guard_ignores_rescans.sql
-- migration-compat: expand

-- +goose Up
-- ADR 20261031190030: the runtime input guard compared each instance's captured
-- artifact scan mirrors with the deployment's current ones, so every image
-- rescan refused the next frozen thaw or warm promotion of an ordinary app
-- (native inputs already left them out). Only the comparison changes; captures
-- keep their bytes, and an older scheduler is unaffected.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_runtime_inputs_match(captured jsonb, current_input jsonb)
 RETURNS boolean
 LANGUAGE plpgsql
 IMMUTABLE
AS $function$
BEGIN
 IF jsonb_typeof(captured) IS DISTINCT FROM 'object' OR jsonb_typeof(current_input) IS DISTINCT FROM 'object' THEN RETURN false; END IF;
 captured:=application_standard_stable_runtime_input(captured); current_input:=application_standard_stable_runtime_input(current_input);
 -- ADR 20261031190030: an image rescan renews the deployment's scan mirrors
 -- (scan_status, scan_result_hash) and changes nothing the instance runs; a
 -- regressed scan parks the deployment, and artifact.parked_reason stays compared.
 IF jsonb_typeof(captured->'artifact')='object' THEN
  captured:=captured #- '{artifact,scan_status}' #- '{artifact,scan_result_hash}';
 END IF;
 IF jsonb_typeof(current_input->'artifact')='object' THEN
  current_input:=current_input #- '{artifact,scan_status}' #- '{artifact,scan_result_hash}';
 END IF;
 IF NOT application_standard_runtime_requires_native(captured)
  AND NOT application_standard_runtime_requires_native(current_input)
  AND captured ? 'account_plan' AND current_input ? 'account_plan' THEN
  captured:=captured-'account_plan'; current_input:=current_input-'account_plan';
  -- Mirror classification changes billing, but conveys no worker or native
  -- authority. Preserve the pre-standards normal/mirror retrofit contract.
  IF captured->>'instance_mode' IN ('normal','mirror')
   AND current_input->>'instance_mode' IN ('normal','mirror') THEN
   captured:=captured-'instance_mode'; current_input:=current_input-'instance_mode';
  END IF;
 END IF;
 RETURN captured=current_input;
END;
$function$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_runtime_inputs_match(captured jsonb, current_input jsonb)
 RETURNS boolean
 LANGUAGE plpgsql
 IMMUTABLE
AS $function$
BEGIN
 IF jsonb_typeof(captured) IS DISTINCT FROM 'object' OR jsonb_typeof(current_input) IS DISTINCT FROM 'object' THEN RETURN false; END IF;
 captured:=application_standard_stable_runtime_input(captured); current_input:=application_standard_stable_runtime_input(current_input);
 IF NOT application_standard_runtime_requires_native(captured)
  AND NOT application_standard_runtime_requires_native(current_input)
  AND captured ? 'account_plan' AND current_input ? 'account_plan' THEN
  captured:=captured-'account_plan'; current_input:=current_input-'account_plan';
  -- Mirror classification changes billing, but conveys no worker or native
  -- authority. Preserve the pre-standards normal/mirror retrofit contract.
  IF captured->>'instance_mode' IN ('normal','mirror')
   AND current_input->>'instance_mode' IN ('normal','mirror') THEN
   captured:=captured-'instance_mode'; current_input:=current_input-'instance_mode';
  END IF;
 END IF;
 RETURN captured=current_input;
END;
$function$;
-- +goose StatementEnd
