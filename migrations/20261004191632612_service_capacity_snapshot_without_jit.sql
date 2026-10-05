-- filename: 20261004191632612_service_capacity_snapshot_without_jit.sql
-- ADR-422: service_capacity_snapshot() plans above jit_above_cost, so a
-- JIT-enabled PostgreSQL compiled it with LLVM on every call. Measured on the
-- production control plane (PostgreSQL 16, jit=on): 6.0-7.9 s per call with
-- JIT, 10-11 ms without. Protected writes call it twice inside statement
-- triggers while holding the policy row lock, so JIT turned each admission into
-- a fleet-wide multi-second stall. The fleet-sized result never repays JIT.
-- Any later CREATE OR REPLACE of the function (including a replay of
-- 20261001084654053) drops this setting and must restate it.

-- +goose Up
ALTER FUNCTION service_capacity_snapshot() SET jit = off;

-- +goose Down
ALTER FUNCTION service_capacity_snapshot() RESET jit;
