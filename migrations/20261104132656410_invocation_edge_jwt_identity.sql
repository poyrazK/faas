-- filename: 20261104132656410_invocation_edge_jwt_identity.sql

-- ADR-091 amendment 2026-11-04 (review round 11): an async route stores the
-- caller a kind=jwt edge rule verified, because the queued request keeps no
-- Authorization header. Only gatewayd-internal writes the columns, on async
-- route admission; delivery renders them as X-Faas-Edge-Jwt-*. Both are
-- nullable without a default, so adding them is metadata-only and an older
-- daemon ignores them.
-- migration-compat: expand
--
-- The CHECK is added NOT VALID and validated in its own statement, so the
-- table keeps being written during the scan. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE invocations ADD COLUMN IF NOT EXISTS edge_jwt_issuer text;
ALTER TABLE invocations ADD COLUMN IF NOT EXISTS edge_jwt_subject text;

-- +goose StatementBegin
DO $edge_jwt$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'invocations'::regclass AND conname = 'invocations_edge_jwt_identity_chk') THEN
    ALTER TABLE invocations ADD CONSTRAINT invocations_edge_jwt_identity_chk
      CHECK ((edge_jwt_subject IS NULL AND edge_jwt_issuer IS NULL)
        OR (octet_length(edge_jwt_subject) BETWEEN 1 AND 1024
          AND (edge_jwt_issuer IS NULL OR octet_length(edge_jwt_issuer) BETWEEN 1 AND 1024))) NOT VALID;
  END IF;
END
$edge_jwt$;
-- +goose StatementEnd

ALTER TABLE invocations VALIDATE CONSTRAINT invocations_edge_jwt_identity_chk;

-- +goose Down
SET lock_timeout = '5s';

ALTER TABLE invocations DROP CONSTRAINT IF EXISTS invocations_edge_jwt_identity_chk;
ALTER TABLE invocations DROP COLUMN IF EXISTS edge_jwt_subject;
ALTER TABLE invocations DROP COLUMN IF EXISTS edge_jwt_issuer;
