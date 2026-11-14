-- name: InsertSignupFacts :exec
-- One row per account; a retried sign-up never overwrites the first record.
INSERT INTO account_signup_facts (
  account_id, method, signup_ip, user_agent, country, asn, asn_org,
  email_domain, disposable_email, created_at
) VALUES (
  sqlc.arg(account_id)::uuid, sqlc.arg(method)::text, sqlc.narg(signup_ip)::inet,
  sqlc.narg(user_agent)::text, sqlc.narg(country)::text, sqlc.narg(asn)::bigint,
  sqlc.narg(asn_org)::text, sqlc.arg(email_domain)::text,
  sqlc.arg(disposable_email)::boolean, sqlc.arg(created_at)::timestamptz
)
ON CONFLICT (account_id) DO NOTHING;

-- name: GetSignupFacts :one
SELECT account_id, method, coalesce(host(signup_ip), '')::text AS signup_ip, user_agent, country, asn, asn_org,
       email_domain, disposable_email, created_at, reduced_at
FROM account_signup_facts
WHERE account_id = sqlc.arg(account_id)::uuid;

-- name: ReduceSignupFacts :execrows
-- Drops the personal fields of rows older than the cutoff, a batch at a time.
UPDATE account_signup_facts
SET signup_ip = NULL, user_agent = NULL, reduced_at = sqlc.arg(reduced_at)::timestamptz
WHERE account_id IN (
  SELECT account_id FROM account_signup_facts
  WHERE reduced_at IS NULL AND created_at < sqlc.arg(cutoff)::timestamptz
  ORDER BY created_at
  LIMIT sqlc.arg(batch)::int
);
