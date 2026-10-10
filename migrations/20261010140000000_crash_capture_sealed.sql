-- filename: 20261010140000000_crash_capture_sealed.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-733 sealed captures. On a remote storage backend vmmd encrypts a
-- capture on the compute node before publishing it, so only encrypted twins
-- ever reach the shared store or a node's read-through cache. Such a row is
-- ready with plaintext_state 'sealed' from the start: imaged never encrypts,
-- purges or stages it, and the vmmd restoring a fork of it decrypts into
-- that instance's own staging. Expiry still deletes every object and drops
-- sealed_key (the row then reads absent, as for any expired capture).
ALTER TABLE crash_captures DROP CONSTRAINT IF EXISTS crash_captures_encryption_chk;
ALTER TABLE crash_captures ADD CONSTRAINT crash_captures_encryption_chk CHECK (
    plaintext_state IN ('present', 'purging', 'absent', 'staging', 'staged', 'sealed')
    AND (encrypted_at IS NULL OR status IN ('ready', 'expired'))
    AND (status = 'expired' OR (encrypted_at IS NULL) = (plaintext_state = 'present'))
    AND (status = 'expired' OR (encrypted_at IS NULL) = (sealed_key IS NULL))
    AND (status <> 'expired' OR (sealed_key IS NULL AND plaintext_state = 'absent'))
    AND (plaintext_state <> 'sealed' OR status = 'ready')
    AND (sealed_key IS NULL OR octet_length(sealed_key) BETWEEN 1 AND 4096)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- A sealed capture has no plaintext an older imaged could stage; drop them.
DELETE FROM crash_captures WHERE plaintext_state = 'sealed';
ALTER TABLE crash_captures DROP CONSTRAINT IF EXISTS crash_captures_encryption_chk;
ALTER TABLE crash_captures ADD CONSTRAINT crash_captures_encryption_chk CHECK (
    plaintext_state IN ('present', 'purging', 'absent', 'staging', 'staged')
    AND (encrypted_at IS NULL OR status IN ('ready', 'expired'))
    AND (status = 'expired' OR (encrypted_at IS NULL) = (plaintext_state = 'present'))
    AND (status = 'expired' OR (encrypted_at IS NULL) = (sealed_key IS NULL))
    AND (status <> 'expired' OR (sealed_key IS NULL AND plaintext_state = 'absent'))
    AND (sealed_key IS NULL OR octet_length(sealed_key) BETWEEN 1 AND 4096)
);
-- +goose StatementEnd
