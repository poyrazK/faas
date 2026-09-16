-- +goose Up
-- Browser login keys created before CLI session tracking were permanent. Give
-- those orphanable credentials a 30-day cleanup window; newer login keys are
-- minted with the same bounded lifetime and are revoked immediately on logout.
UPDATE api_keys
SET expires_at = now() + interval '30 days'
WHERE label = 'cli-login'
  AND expires_at IS NULL
  AND revoked_at IS NULL
  AND status IN ('active', 'grace');

-- +goose Down
-- Security expiry backfills are intentionally irreversible.
SELECT 1;
