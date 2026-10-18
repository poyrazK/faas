-- migration-compat: expand
-- filename: 20261014195747120_canary_preset_adaptive.sql
--
-- ADR-1321: evidence-paced default rollouts. deployments.canary_preset
-- admits "adaptive", the balanced ladder whose stages advance on clean
-- circuit-breaker evidence after one minute. The catalog in
-- pkg/api/canary.AllowedCanaryPresets is the source of truth; this check
-- mirrors it. Every existing row carries a name the widened check still
-- admits.

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- Extend whatever the constraint admits now (goose runs with allow-missing).
DO $presets$
DECLARE previous_check text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO previous_check FROM pg_constraint
  WHERE conrelid = 'deployments'::regclass AND conname = 'deployments_canary_preset_chk';
 IF previous_check IS NOT NULL AND position('adaptive' IN previous_check) = 0 THEN
  ALTER TABLE deployments DROP CONSTRAINT deployments_canary_preset_chk;
  EXECUTE format('ALTER TABLE deployments ADD CONSTRAINT deployments_canary_preset_chk CHECK ((%s) OR canary_preset = ''adaptive'')',
   regexp_replace(previous_check, '^CHECK \((.*)\)$', '\1'));
 END IF;
END $presets$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- An adaptive rollout keeps its ladder under the balanced name: same
-- percentages and timers, without the early advance.
UPDATE deployments SET canary_preset = 'balanced' WHERE canary_preset = 'adaptive';
DO $presets$
DECLARE previous_check text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO previous_check FROM pg_constraint
  WHERE conrelid = 'deployments'::regclass AND conname = 'deployments_canary_preset_chk';
 IF previous_check IS NOT NULL AND position('adaptive' IN previous_check) > 0 THEN
  ALTER TABLE deployments DROP CONSTRAINT deployments_canary_preset_chk;
  EXECUTE 'ALTER TABLE deployments ADD CONSTRAINT deployments_canary_preset_chk ' ||
   regexp_replace(previous_check, ' OR \(canary_preset = ''adaptive''::text\)', '');
 END IF;
END $presets$;
-- +goose StatementEnd
