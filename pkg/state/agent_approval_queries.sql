-- name: InsertAPIKeyApprovalCategories :exec
-- ADR-20261104140058: the categories a key must have approved; set once, when it is minted.
INSERT INTO api_key_approval_categories (key_id, categories)
VALUES (sqlc.arg(key_id)::uuid, sqlc.arg(categories)::text[]);

-- name: GetAPIKeyApprovalCategories :one
SELECT categories FROM api_key_approval_categories WHERE key_id = sqlc.arg(key_id)::uuid;

-- name: FindPendingAgentApproval :one
-- An undecided, unexpired request with the same binding, so a retried
-- request reuses its review link.
SELECT id, expires_at FROM agent_approval_requests
WHERE key_id = sqlc.arg(key_id)::uuid
  AND method = sqlc.arg(method)
  AND path = sqlc.arg(path)
  AND body_sha256 IS NOT DISTINCT FROM sqlc.narg(body_sha256)::bytea
  AND status = 'pending'
  AND expires_at > now()
ORDER BY created_at DESC
LIMIT 1;

-- name: CountPendingAgentApprovals :one
SELECT count(*)::int FROM agent_approval_requests
WHERE key_id = sqlc.arg(key_id)::uuid AND status = 'pending' AND expires_at > now();

-- name: PruneAgentApprovals :exec
-- Requests of the key that expired a week ago or more.
DELETE FROM agent_approval_requests
WHERE key_id = sqlc.arg(key_id)::uuid AND expires_at < now() - interval '7 days';

-- name: InsertAgentApproval :exec
INSERT INTO agent_approval_requests (id, key_id, account_id, org_id, category, method, path, body_sha256, expires_at)
VALUES (sqlc.arg(id)::uuid, sqlc.arg(key_id)::uuid, sqlc.arg(account_id)::uuid, sqlc.arg(org_id)::uuid,
        sqlc.arg(category), sqlc.arg(method), sqlc.arg(path), sqlc.narg(body_sha256)::bytea, sqlc.arg(expires_at));

-- name: ConsumeAgentApproval :one
-- Uses one approved, unexpired request with the same binding, in the same
-- statement that finds it, so one approval allows exactly one request.
UPDATE agent_approval_requests
SET status = 'consumed', consumed_at = now()
WHERE id = (
    SELECT a.id FROM agent_approval_requests a
    WHERE a.key_id = sqlc.arg(key_id)::uuid
      AND a.method = sqlc.arg(method)
      AND a.path = sqlc.arg(path)
      AND a.body_sha256 IS NOT DISTINCT FROM sqlc.narg(body_sha256)::bytea
      AND a.status = 'approved'
      AND a.expires_at > now()
    ORDER BY a.decided_at
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING id;

-- name: GetAgentApproval :one
SELECT id, key_id, account_id, org_id, category, method, path, body_sha256, status, decided_by,
       created_at, decided_at, consumed_at, expires_at, decision_note
FROM agent_approval_requests WHERE id = sqlc.arg(id)::uuid;

-- name: ListAgentApprovalsForKey :many
SELECT id, key_id, account_id, org_id, category, method, path, body_sha256, status, decided_by,
       created_at, decided_at, consumed_at, expires_at, decision_note
FROM agent_approval_requests
WHERE key_id = sqlc.arg(key_id)::uuid
ORDER BY created_at DESC
LIMIT sqlc.arg(max_rows)::int;

-- name: ListPendingAgentApprovals :many
-- Undecided requests of agents the account connected, or of agents acting in
-- the organizations given (those the account owns or administers).
SELECT id, key_id, account_id, org_id, category, method, path, body_sha256, status, decided_by,
       created_at, decided_at, consumed_at, expires_at, decision_note
FROM agent_approval_requests
WHERE status = 'pending' AND expires_at > now()
  AND (account_id = sqlc.arg(account_id)::uuid OR org_id = ANY(sqlc.arg(org_ids)::uuid[]))
ORDER BY created_at DESC
LIMIT sqlc.arg(max_rows)::int;

-- name: RecentAgentApprovalOutcome :one
-- ADR-20261107140000 §6: the key's most recent request with the same binding,
-- when it was denied, or expired undecided or unused, within the window. A
-- newer request of any other status (pending, approved, consumed) hides it.
SELECT id, key_id, account_id, org_id, category, method, path, body_sha256, status, decided_by,
       created_at, decided_at, consumed_at, expires_at, decision_note
FROM (
    SELECT * FROM agent_approval_requests
    WHERE key_id = sqlc.arg(key_id)::uuid
      AND method = sqlc.arg(method)
      AND path = sqlc.arg(path)
      AND body_sha256 IS NOT DISTINCT FROM sqlc.narg(body_sha256)::bytea
    ORDER BY created_at DESC
    LIMIT 1
) latest
WHERE (status = 'denied' AND decided_at > now() - sqlc.arg(window_seconds)::int * interval '1 second')
   OR (status IN ('pending', 'approved') AND expires_at <= now()
       AND expires_at > now() - sqlc.arg(window_seconds)::int * interval '1 second');

-- name: InsertAgentApprovalWebhookDeliveries :execrows
-- ADR-20261107140000 §4, ADR-224: one pending delivery per enabled account
-- receiver of the connector's account subscribed to the event. The ADR-076
-- dispatcher signs, sends and retries it. source_event_id is unique per
-- receiver, so each event of a request is enqueued once.
INSERT INTO app_webhook_deliveries (webhook_id, app_id, account_id, event, payload, source_event_id)
SELECT w.id, NULL, w.account_id, sqlc.arg(event)::text, sqlc.arg(payload)::jsonb, sqlc.arg(source_event_id)::uuid
  FROM app_webhooks w
 WHERE w.account_id = sqlc.arg(account_id)::uuid AND w.scope = 'account' AND w.enabled
   AND sqlc.arg(event)::text = ANY(w.event_filter)
ON CONFLICT DO NOTHING;

-- name: DecideAgentApproval :one
-- A person approves or denies a request that is still pending and unexpired;
-- a denial may carry a note for the agent (ADR-20261107140000 §6). It returns
-- the decided request for its webhook event (§4).
UPDATE agent_approval_requests
SET status = sqlc.arg(status), decided_by = sqlc.arg(decided_by)::uuid, decided_at = now(),
    decision_note = sqlc.narg(decision_note)
WHERE id = sqlc.arg(id)::uuid AND status = 'pending' AND expires_at > now()
RETURNING id, key_id, account_id, org_id, category, method, path, body_sha256, status, decided_by,
          created_at, decided_at, consumed_at, expires_at, decision_note;

-- name: InsertAgentApprovalNotification :exec
-- ADR-20261107140000 §5: tell people about a new request; apid inserts it in
-- the request's transaction.
INSERT INTO agent_approval_notifications (approval_id, account_id, org_id, key_id, agent_name, review_url)
VALUES (sqlc.arg(approval_id)::uuid, sqlc.arg(account_id)::uuid, sqlc.narg(org_id)::uuid, sqlc.arg(key_id)::uuid,
        sqlc.arg(agent_name), sqlc.arg(review_url))
ON CONFLICT (approval_id) DO NOTHING;

-- name: ClaimAgentApprovalNotifications :many
-- meterd leases due notifications; a lapsed lease makes a row due again.
UPDATE agent_approval_notifications n
SET lease_until = now() + sqlc.arg(lease_seconds)::int * interval '1 second'
WHERE n.approval_id IN (
    SELECT c.approval_id FROM agent_approval_notifications c
    WHERE c.state = 'pending' AND c.available_at <= now()
      AND (c.lease_until IS NULL OR c.lease_until < now())
    ORDER BY c.available_at
    LIMIT sqlc.arg(max_rows)::int
    FOR UPDATE SKIP LOCKED
)
RETURNING n.approval_id, n.account_id, n.org_id, n.key_id, n.agent_name, n.review_url, n.attempts;

-- name: PendingAgentApprovalNotificationsForKey :many
-- The key's notifications still waiting whose requests are still pending and
-- unexpired: what one email lists.
SELECT n.approval_id, n.review_url, r.category, r.method, r.path, r.expires_at
FROM agent_approval_notifications n
JOIN agent_approval_requests r ON r.id = n.approval_id
WHERE n.key_id = sqlc.arg(key_id)::uuid AND n.state = 'pending'
  AND r.status = 'pending' AND r.expires_at > now()
ORDER BY r.created_at;

-- name: GetAgentApprovalRequestState :one
SELECT status, expires_at FROM agent_approval_requests WHERE id = sqlc.arg(id)::uuid;

-- name: FinishAgentApprovalNotifications :exec
UPDATE agent_approval_notifications
SET state = sqlc.arg(state), finished_at = now(), lease_until = NULL, last_error = sqlc.narg(last_error)
WHERE approval_id = ANY(sqlc.arg(approval_ids)::uuid[]) AND state = 'pending';

-- name: RetryAgentApprovalNotification :exec
-- A failed send waits and tries again; after the last attempt it fails.
UPDATE agent_approval_notifications
SET attempts = attempts + 1, lease_until = NULL, last_error = sqlc.narg(last_error),
    available_at = now() + sqlc.arg(delay_seconds)::int * interval '1 second',
    state = CASE WHEN attempts + 1 >= sqlc.arg(max_attempts)::int THEN 'failed' ELSE 'pending' END,
    finished_at = CASE WHEN attempts + 1 >= sqlc.arg(max_attempts)::int THEN now() ELSE NULL END
WHERE approval_id = sqlc.arg(approval_id)::uuid AND state = 'pending';

-- name: DeferAgentApprovalNotification :exec
-- The recipient was emailed about this key recently: wait for the window.
UPDATE agent_approval_notifications
SET lease_until = NULL, available_at = sqlc.arg(available_at)
WHERE approval_id = sqlc.arg(approval_id)::uuid AND state = 'pending';

-- name: GetAgentApprovalEmailLastSent :one
SELECT last_sent_at FROM agent_approval_email_log
WHERE recipient_account_id = sqlc.arg(recipient_account_id)::uuid AND key_id = sqlc.arg(key_id)::uuid;

-- name: RecordAgentApprovalEmailSent :exec
INSERT INTO agent_approval_email_log (recipient_account_id, key_id, last_sent_at)
VALUES (sqlc.arg(recipient_account_id)::uuid, sqlc.arg(key_id)::uuid, now())
ON CONFLICT (recipient_account_id, key_id) DO UPDATE SET last_sent_at = now();
