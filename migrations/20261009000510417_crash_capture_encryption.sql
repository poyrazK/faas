-- filename: 20261009000510417_crash_capture_encryption.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-733 encryption at rest. imaged encrypts each ready capture's four
-- objects with a fresh per-capture age key, seals that key to the fleet
-- recipient (sealed_key), and deletes the plaintext. A plaintext copy exists
-- again only while a fork pinned to the capture is active:
--
--   present  captured, not yet encrypted (forks may restore)
--   purging  encrypted; plaintext delete pending or being retried
--   absent   encrypted; no plaintext on storage
--   staging  encrypted; plaintext being restored for an active fork
--   staged   encrypted; plaintext restored (forks may restore)
--
-- Expiry deletes every object, drops sealed_key and leaves the row absent,
-- so an expired capture's key is gone even if a delete missed a copy.
ALTER TABLE crash_captures
    ADD COLUMN IF NOT EXISTS plaintext_state text NOT NULL DEFAULT 'present',
    ADD COLUMN IF NOT EXISTS sealed_key bytea,
    ADD COLUMN IF NOT EXISTS encrypted_at timestamptz;

-- Rows that expired before this migration hold no objects any more.
ALTER TABLE crash_captures DISABLE TRIGGER crash_captures_status_transition;
UPDATE crash_captures SET plaintext_state = 'absent' WHERE status = 'expired';
ALTER TABLE crash_captures ENABLE TRIGGER crash_captures_status_transition;

ALTER TABLE crash_captures DROP CONSTRAINT IF EXISTS crash_captures_encryption_chk;
ALTER TABLE crash_captures ADD CONSTRAINT crash_captures_encryption_chk CHECK (
    plaintext_state IN ('present', 'purging', 'absent', 'staging', 'staged')
    AND (encrypted_at IS NULL OR status IN ('ready', 'expired'))
    AND (status = 'expired' OR (encrypted_at IS NULL) = (plaintext_state = 'present'))
    AND (status = 'expired' OR (encrypted_at IS NULL) = (sealed_key IS NULL))
    AND (status <> 'expired' OR (sealed_key IS NULL AND plaintext_state = 'absent'))
    AND (sealed_key IS NULL OR octet_length(sealed_key) BETWEEN 1 AND 4096)
);

CREATE INDEX IF NOT EXISTS crash_captures_plaintext_idx
    ON crash_captures (captured_at, id)
    WHERE status = 'ready' AND plaintext_state <> 'absent';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS crash_captures_plaintext_idx;
ALTER TABLE crash_captures DROP CONSTRAINT IF EXISTS crash_captures_encryption_chk;
ALTER TABLE crash_captures
    DROP COLUMN IF EXISTS encrypted_at,
    DROP COLUMN IF EXISTS sealed_key,
    DROP COLUMN IF EXISTS plaintext_state;
-- +goose StatementEnd
