-- +goose Up
ALTER TABLE migration_verifications ADD COLUMN next_attempt_at timestamptz
 CHECK (next_attempt_at IS NULL OR isfinite(next_attempt_at));
UPDATE migration_verifications SET next_attempt_at = greatest(now(),(snapshot->>'LeaseUntil')::timestamptz)
 WHERE snapshot->'Report'->>'status' = 'pending';
CREATE INDEX migration_verifications_due_idx ON migration_verifications(next_attempt_at,id)
 WHERE next_attempt_at IS NOT NULL;
ALTER TABLE migration_verifications DROP CONSTRAINT migration_verification_report_check;
ALTER TABLE migration_verifications ADD CONSTRAINT migration_verification_report_check CHECK (COALESCE(
 jsonb_typeof(snapshot->'Report') = 'object'
 AND snapshot->'Report'->>'status' IN ('pending','passed','failed','blocked','expired','cancelled')
 AND snapshot->'Report'->>'phase' IN ('dependencies','startup','park','drain','wake','waking','cold_smoke','cold_dependencies','restoring','complete')
 AND snapshot->'Report'->>'id' = id::text, false));

-- +goose Down
-- Cancellation and cleanup receipts must retain their meaning on rollback.
-- Deliberately fail rather than silently converting them to successful evidence.
ALTER TABLE migration_verifications DROP CONSTRAINT migration_verification_report_check;
ALTER TABLE migration_verifications ADD CONSTRAINT migration_verification_report_check CHECK (COALESCE(
 jsonb_typeof(snapshot->'Report') = 'object'
 AND snapshot->'Report'->>'status' IN ('pending','passed','failed','blocked','expired')
 AND snapshot->'Report'->>'phase' IN ('dependencies','startup','park','drain','wake','waking','cold_smoke','cold_dependencies','complete')
 AND snapshot->'Report'->>'id' = id::text, false));
DROP INDEX migration_verifications_due_idx;
ALTER TABLE migration_verifications DROP COLUMN next_attempt_at;
