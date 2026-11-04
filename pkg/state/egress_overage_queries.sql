-- ADR-20261103131742 amendment 2: egress overage choices per billing subject.

-- name: RecordEgressOverageChoice :exec
INSERT INTO egress_overage_choices (subject_id, changed_at, enabled)
VALUES (sqlc.arg(subject_id)::uuid, sqlc.arg(changed_at)::timestamptz, sqlc.arg(enabled)::boolean)
ON CONFLICT (subject_id, changed_at) DO UPDATE SET enabled=EXCLUDED.enabled;

-- name: EgressOverageChoice :one
SELECT COALESCE((SELECT c.enabled FROM egress_overage_choices c
 WHERE c.subject_id=sqlc.arg(subject_id)::uuid
 ORDER BY c.changed_at DESC LIMIT 1), false)::boolean AS enabled;

-- name: EgressOverageEnabledSubjects :many
SELECT latest.subject_id::text AS subject_id FROM (
  SELECT DISTINCT ON (c.subject_id) c.subject_id, c.enabled
  FROM egress_overage_choices c
  ORDER BY c.subject_id, c.changed_at DESC
) latest WHERE latest.enabled;

-- The changes in [start, end) and the last change before start, oldest first.
-- name: EgressOverageChoices :many
SELECT c.changed_at, c.enabled FROM egress_overage_choices c
WHERE c.subject_id=sqlc.arg(subject_id)::uuid
  AND c.changed_at < sqlc.arg(end_at)::timestamptz
  AND c.changed_at >= COALESCE((SELECT max(p.changed_at) FROM egress_overage_choices p
    WHERE p.subject_id=sqlc.arg(subject_id)::uuid AND p.changed_at <= sqlc.arg(start_at)::timestamptz),
    sqlc.arg(start_at)::timestamptz)
ORDER BY c.changed_at;
