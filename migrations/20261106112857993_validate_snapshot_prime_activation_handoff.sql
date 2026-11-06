-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- Validate separately so checking existing rows does not hold the column
-- addition's ACCESS EXCLUSIVE lock against live runtime publications.
ALTER TABLE runtime_instance_config_proofs
 VALIDATE CONSTRAINT runtime_instance_config_proofs_prime_activation_handoff_check;

-- +goose Down
-- Validation changes no accepted writes and needs no inverse.
SELECT 1;
