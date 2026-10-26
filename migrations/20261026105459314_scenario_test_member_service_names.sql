-- migration-compat: contract
-- filename: 20261026105459314_scenario_test_member_service_names.sql
--
-- ADR-20261026105459: a `gregale dev --all` member is registered under the
-- name the other services call it by, which is any service DNS label (`ui`,
-- 1 to 63 characters), not only an app slug (3 to 40). The new check
-- replaces the old one in three transactions; the previous release never
-- writes a name the new check refuses, but dropping a constraint is a
-- contract change.

-- +goose NO TRANSACTION

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE scenario_test_members ADD CONSTRAINT scenario_test_members_service_name_check
    CHECK (workload_name ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$') NOT VALID;
-- +goose StatementEnd

-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE scenario_test_members VALIDATE CONSTRAINT scenario_test_members_service_name_check;
-- +goose StatementEnd

-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE scenario_test_members DROP CONSTRAINT IF EXISTS scenario_test_members_workload_name_check;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE scenario_test_members ADD CONSTRAINT scenario_test_members_workload_name_check
    CHECK (workload_name ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$') NOT VALID;
-- +goose StatementEnd

-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE scenario_test_members DROP CONSTRAINT IF EXISTS scenario_test_members_service_name_check;
-- +goose StatementEnd
