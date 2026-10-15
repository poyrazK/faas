-- filename: 20261005111040937_application_standard_unmanaged_mirror_compatibility.sql
-- adr: 590. Preserve unmanaged normal/mirror classification without granting native authority.

-- +goose Up
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

-- +goose Down
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
 END IF;
 RETURN captured=current_input;
END;
$function$;
-- +goose StatementEnd
