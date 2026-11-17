-- ListDormantFreeSnapshotApps (Free-tier disk reaper, spec §4.3,
-- ADR-20261105120000) returns apps whose every possible billing
-- subject is on Free (the creator's account, and its shared organization when
-- it has one), with a snapshot, no live instance, and neither a request, a
-- start, a park nor a deployment since the cutoff. Instance rows are retained
-- 30 days, longer than the 14-day window, so a request inside the window is
-- always seen.
-- name: ListDormantFreeSnapshotApps :many
SELECT a.id
FROM apps a
JOIN accounts acc ON acc.id = a.account_id
LEFT JOIN orgs o ON o.id = a.org_id AND NOT o.personal_org
WHERE a.status IN ('active', 'evicted_cold')
  AND acc.plan = 'free'
  AND (o.id IS NULL OR o.plan = 'free')
  AND a.created_at < sqlc.arg(cutoff)::timestamptz
  AND EXISTS (
    SELECT 1 FROM snapshots s JOIN deployments d ON d.id = s.deployment_id
    WHERE d.app_id = a.id AND NOT s.delete_pending)
  AND NOT EXISTS (
    SELECT 1 FROM deployments d
    WHERE d.app_id = a.id AND d.created_at >= sqlc.arg(cutoff)::timestamptz)
  AND NOT EXISTS (
    SELECT 1 FROM instances i
    WHERE i.app_id = a.id
      AND (i.state NOT IN ('parked', 'stopped', 'failed')
        OR greatest(i.last_request_at, i.started_at, i.parked_at) >= sqlc.arg(cutoff)::timestamptz))
ORDER BY a.id
LIMIT sqlc.arg(max_apps);
