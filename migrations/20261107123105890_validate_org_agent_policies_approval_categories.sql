-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- ADR-20261104140058: the approval floor's CHECK was added NOT VALID so the
-- column addition did not scan org_agent_policies under ACCESS EXCLUSIVE.
-- Validating takes only SHARE UPDATE EXCLUSIVE; rows written since then were
-- already checked, and older rows hold the default '{}'.
ALTER TABLE org_agent_policies
 VALIDATE CONSTRAINT org_agent_policies_approval_categories_check;

-- +goose Down
-- Validation changes no accepted writes and needs no inverse.
SELECT 1;
