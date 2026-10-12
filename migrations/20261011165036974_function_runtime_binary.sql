-- filename: 20261011165036974_function_runtime_binary.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-987: the binary function runtime runs one static executable built from
-- the function's Dockerfile, on the go124 base and runner. Widen the two
-- runtime CHECKs that name it: apps.runtime (admission, in lockstep with
-- api.FunctionRuntimes and the imaged runtime matrix) and
-- request_telemetry.guest_runtime (the runner reports runtime=binary).
-- DROP-and-re-ADD is the contract because CHECK constraints cannot be altered
-- in place; the partitioned request_telemetry parent propagates to its
-- partitions. Existing rows are untouched.
ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_runtime_check;
ALTER TABLE apps ADD CONSTRAINT apps_runtime_check
  CHECK (runtime IS NULL OR runtime IN ('node22', 'python312', 'go124', 'go124-alpine', 'node24', 'python313', 'binary'));

ALTER TABLE request_telemetry DROP CONSTRAINT IF EXISTS request_telemetry_guest_runtime_check;
ALTER TABLE request_telemetry ADD CONSTRAINT request_telemetry_guest_runtime_check
  CHECK (guest_runtime = ANY (ARRAY['node22','node24','python312','python313','go124','binary','__unknown__']));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Narrow both CHECKs back. A binary function app or telemetry row written
-- since the Up makes the re-ADD fail with 23514, which is the desired safety:
-- a downgrade must never silently delete runtime data.
ALTER TABLE request_telemetry DROP CONSTRAINT IF EXISTS request_telemetry_guest_runtime_check;
ALTER TABLE request_telemetry ADD CONSTRAINT request_telemetry_guest_runtime_check
  CHECK (guest_runtime = ANY (ARRAY['node22','node24','python312','python313','go124','__unknown__']));

ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_runtime_check;
ALTER TABLE apps ADD CONSTRAINT apps_runtime_check
  CHECK (runtime IS NULL OR runtime IN ('node22', 'python312', 'go124', 'go124-alpine', 'node24', 'python313'));
-- +goose StatementEnd
