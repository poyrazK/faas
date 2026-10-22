-- +goose Up
ALTER TABLE workflow_recovery_policies ADD COLUMN IF NOT EXISTS automatic_poll_failure jsonb
CHECK (automatic_poll_failure IS NULL OR (
 jsonb_typeof(automatic_poll_failure)='object'
 AND automatic_poll_failure->>'code' IN ('invalid_policy_authority','invalid_polling_data','invalid_recovery_identity')
 AND (automatic_poll_failure->>'attempts')::bigint BETWEEN 1 AND 32
 AND isfinite((automatic_poll_failure->>'failed_at')::timestamptz)
 AND isfinite((automatic_poll_failure->>'retry_at')::timestamptz)
 AND (automatic_poll_failure->>'retry_at')::timestamptz>(automatic_poll_failure->>'failed_at')::timestamptz
) IS TRUE);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM workflow_recovery_policies WHERE automatic_poll_failure IS NOT NULL) THEN
  RAISE EXCEPTION 'automatic polling failures are retained';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE workflow_recovery_policies DROP COLUMN automatic_poll_failure;
