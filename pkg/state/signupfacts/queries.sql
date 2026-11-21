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

-- name: LinkHeldAccounts :many
-- ADR-20261114174600: links a new account to every abuse-held account (or
-- held organization's owner) whose sign-up address or recent sign-in
-- addresses share its /24 (IPv4) or /48 (IPv6) network, or whose email domain
-- equals its own when the caller marks the domain linkable (not a public
-- provider, not disposable). Reads the new account's own sign-up record.
WITH me AS (
  SELECT f.signup_ip, f.email_domain
  FROM account_signup_facts f
  WHERE f.account_id = sqlc.arg(account_id)::uuid
),
net AS (
  SELECT network(set_masklen(me.signup_ip, CASE family(me.signup_ip) WHEN 4 THEN 24 ELSE 48 END)) AS cidr
  FROM me WHERE me.signup_ip IS NOT NULL
),
held AS (
  SELECT a.id AS held_account_id, NULL::uuid AS held_org_id, a.abuse_hold_reason AS held_reason
  FROM accounts a
  WHERE a.abuse_hold_at IS NOT NULL AND a.id <> sqlc.arg(account_id)::uuid
  UNION ALL
  SELECT m.account_id, o.id, o.abuse_hold_reason
  FROM orgs o
  JOIN org_memberships m ON m.org_id = o.id AND m.role = 'owner' AND m.removed_at IS NULL
  WHERE o.abuse_hold_at IS NOT NULL AND NOT o.personal_org AND m.account_id <> sqlc.arg(account_id)::uuid
),
matches AS (
  SELECT h.held_account_id, h.held_org_id, h.held_reason, 'signup_network'::text AS signal
  FROM held h, net
  WHERE EXISTS (SELECT 1 FROM account_signup_facts f
                WHERE f.account_id = h.held_account_id AND f.signup_ip <<= net.cidr)
     OR EXISTS (SELECT 1 FROM sessions s
                WHERE s.account_id = h.held_account_id AND s.issued_at >= sqlc.arg(since)::timestamptz
                  AND s.issued_ip <<= net.cidr)
  UNION ALL
  SELECT h.held_account_id, h.held_org_id, h.held_reason, 'email_domain'::text
  FROM held h
  JOIN accounts a ON a.id = h.held_account_id
  CROSS JOIN me
  WHERE sqlc.arg(domain_linkable)::boolean AND me.email_domain <> ''
    AND lower(split_part(a.email, '@', 2)) = me.email_domain
)
INSERT INTO account_held_links (account_id, held_account_id, held_org_id, held_reason, signal, detected_at)
SELECT DISTINCT ON (held_account_id, signal)
       sqlc.arg(account_id)::uuid, held_account_id, held_org_id, held_reason, signal, sqlc.arg(detected_at)::timestamptz
FROM matches
WHERE held_reason IS NOT NULL
ORDER BY held_account_id, signal
LIMIT 50
ON CONFLICT DO NOTHING
RETURNING held_account_id, held_org_id, held_reason, signal;

-- name: HeldLinksForAccount :many
SELECT held_account_id, held_org_id, held_reason, signal, detected_at
FROM account_held_links
WHERE account_id = sqlc.arg(account_id)::uuid
ORDER BY detected_at, held_account_id, signal;
