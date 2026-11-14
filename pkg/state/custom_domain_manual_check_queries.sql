-- name: BeginCustomDomainManualCheck :execrows
UPDATE custom_domains SET verification_manual_check_at = sqlc.arg(checked_at)::timestamptz
WHERE domain = sqlc.arg(domain)::text::citext
  AND app_id = sqlc.arg(app_id)::text::uuid AND challenge_token = sqlc.arg(token)::text
  AND (verified_at IS NOT NULL OR verification_expires_at > sqlc.arg(checked_at)::timestamptz)
  AND (verification_manual_check_at IS NULL OR verification_manual_check_at <= sqlc.arg(due_before)::timestamptz);

-- name: ReadCustomDomainCheckSchedule :one
SELECT cert_retry_at, verification_manual_check_at FROM custom_domains
WHERE domain = sqlc.arg(domain)::text::citext
  AND app_id = sqlc.arg(app_id)::text::uuid AND challenge_token = sqlc.arg(token)::text;
