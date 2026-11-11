-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- Private capture evidence belongs to the exact published wake and survives
-- a scheduler restart before the activation notification can be enqueued.
ALTER TABLE runtime_instance_config_proofs
 ADD COLUMN IF NOT EXISTS prime_activation_handoff jsonb;
-- +goose StatementBegin
DO $install$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='runtime_instance_config_proofs'::regclass AND conname='runtime_instance_config_proofs_prime_activation_handoff_check') THEN
ALTER TABLE runtime_instance_config_proofs
 ADD CONSTRAINT runtime_instance_config_proofs_prime_activation_handoff_check
 CHECK (prime_activation_handoff IS NULL OR jsonb_typeof(prime_activation_handoff) = 'object') NOT VALID;
 END IF;
END;
$install$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE runtime_instance_config_proofs DROP COLUMN prime_activation_handoff;
