-- Bound legacy custom-domain polling and make due work indexable. Existing
-- rows are spread over the next hour so a deployment cannot re-query the
-- entire historical backlog at once.
ALTER TABLE custom_domains
    ADD COLUMN IF NOT EXISTS verification_next_check_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN IF NOT EXISTS verification_attempts integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS verification_expires_at timestamptz NOT NULL DEFAULT (now() + interval '7 days');

UPDATE custom_domains
SET verification_next_check_at = now() +
    (mod(abs(hashtext(domain::text)), 3600) * interval '1 second')
WHERE verified_at IS NULL;

CREATE INDEX IF NOT EXISTS custom_domains_verification_due_idx
    ON custom_domains (verification_next_check_at, app_id)
    WHERE verified_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS custom_domains_verification_due_idx;
ALTER TABLE custom_domains
    DROP COLUMN IF EXISTS verification_expires_at,
    DROP COLUMN IF EXISTS verification_attempts,
    DROP COLUMN IF EXISTS verification_next_check_at;
