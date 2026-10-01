-- Quarterly customer IAM inventory for access review.
--
-- Run with a read-only database role. This script starts a repeatable,
-- read-only transaction and does not return credential hashes, session
-- fingerprints, invitation tokens, IP addresses, or user-agent strings.
-- Its output still contains customer email addresses and access metadata;
-- handle it as restricted evidence and do not commit production output.

BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY;
SET LOCAL TIME ZONE 'UTC';

-- Active organization memberships, including suspended accounts so they
-- can be investigated rather than silently omitted from the inventory.
SELECT
    o.slug AS org_slug,
    o.name AS org_name,
    o.personal_org,
    o.status AS org_status,
    a.id AS account_id,
    a.email,
    a.status AS account_status,
    m.role,
    m.joined_at
FROM org_memberships AS m
JOIN orgs AS o ON o.id = m.org_id
JOIN accounts AS a ON a.id = m.account_id
WHERE m.removed_at IS NULL
ORDER BY o.slug, m.role, lower(a.email);

-- Active and rotation-grace API keys. The result flags keys that are old,
-- unused, past their expiry timestamp, or whose creator is no longer an
-- active member of the key's organization. It never returns key material.
SELECT
    o.slug AS org_slug,
    a.email AS creator_email,
    a.status AS creator_account_status,
    k.id AS key_id,
    k.label,
    k.scopes,
    k.status AS key_status,
    k.created_at,
    k.last_used_at,
    k.expires_at,
    (k.created_at < CURRENT_TIMESTAMP - INTERVAL '365 days') AS older_than_365_days,
    (k.last_used_at IS NULL OR k.last_used_at < CURRENT_TIMESTAMP - INTERVAL '90 days') AS unused_or_idle_over_90_days,
    (k.expires_at IS NOT NULL AND k.expires_at <= CURRENT_TIMESTAMP) AS expiry_timestamp_passed,
    EXISTS (
        SELECT 1
        FROM org_memberships AS m
        WHERE m.org_id = k.org_id
          AND m.account_id = k.account_id
          AND m.removed_at IS NULL
    ) AS creator_is_active_org_member
FROM api_keys AS k
JOIN orgs AS o ON o.id = k.org_id
JOIN accounts AS a ON a.id = k.account_id
WHERE k.status IN ('active', 'grace')
ORDER BY o.slug, lower(a.email), k.created_at;

-- Server-side dashboard sessions. Session identifiers are included so the
-- account holder can match and revoke a session through the authenticated
-- session-management surface; network and browser fingerprints are omitted.
SELECT
    a.email,
    a.status AS account_status,
    s.id AS session_id,
    s.issued_at,
    s.last_seen_at
FROM sessions AS s
JOIN accounts AS a ON a.id = s.account_id
WHERE s.revoked_at IS NULL
ORDER BY lower(a.email), s.issued_at DESC;

-- Pending invitations, including expired ones that still need cleanup.
SELECT
    o.slug AS org_slug,
    i.id AS invitation_id,
    i.email AS invitee_email,
    i.role,
    inviter.email AS invited_by_email,
    i.created_at,
    i.expires_at,
    (i.expires_at <= CURRENT_TIMESTAMP) AS expired
FROM org_invitations AS i
JOIN orgs AS o ON o.id = i.org_id
LEFT JOIN accounts AS inviter ON inviter.id = i.invited_by_account_id
WHERE i.consumed_at IS NULL
  AND i.revoked_at IS NULL
ORDER BY o.slug, i.created_at;

-- Access-related audit activity during the previous completed UTC quarter.
-- Extract only fields useful to the review instead of returning arbitrary
-- event JSON, which can contain additional customer data.
SELECT
    e.at,
    e.actor,
    e.kind,
    e.subject,
    e.data ->> 'org_id' AS org_id,
    e.data ->> 'account_id' AS affected_account_id,
    e.data ->> 'key_id' AS key_id,
    e.data ->> 'role' AS role,
    e.data ->> 'from_role' AS from_role,
    e.data ->> 'to_role' AS to_role
FROM events AS e
WHERE e.at >= date_trunc('quarter', CURRENT_TIMESTAMP) - INTERVAL '3 months'
  AND e.at < date_trunc('quarter', CURRENT_TIMESTAMP)
  AND (
      e.kind LIKE 'org.member.%'
      OR e.kind LIKE 'org.invitation.%'
      OR e.kind LIKE 'org.ownership_%'
      OR e.kind LIKE 'api_key.%'
      OR e.kind LIKE 'key.%'
      OR e.kind LIKE 'auth.session.%'
      OR e.kind LIKE 'auth.sessions.%'
      OR e.kind = 'auth.sessions_revoked_on_password_change'
  )
ORDER BY e.at, e.id;

COMMIT;
