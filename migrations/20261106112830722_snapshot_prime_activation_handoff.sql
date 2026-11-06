-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- Private capture evidence belongs to the exact published wake and survives
-- a scheduler restart before the activation notification can be enqueued.
ALTER TABLE runtime_instance_config_proofs
 ADD COLUMN prime_activation_handoff jsonb;
ALTER TABLE runtime_instance_config_proofs
 ADD CONSTRAINT runtime_instance_config_proofs_prime_activation_handoff_check
 CHECK (prime_activation_handoff IS NULL OR jsonb_typeof(prime_activation_handoff) = 'object') NOT VALID;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE runtime_instance_config_proofs DROP COLUMN prime_activation_handoff;
