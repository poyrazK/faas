-- name: AppLabels :one
SELECT labels FROM apps WHERE id = sqlc.arg(id)::uuid;

-- name: AppLabelsByID :many
-- Only apps that carry labels: an app absent from the result has none.
SELECT id, labels FROM apps
WHERE id = ANY(sqlc.arg(ids)::uuid[]) AND labels <> '{}'::jsonb;

-- name: UpdateAppLabels :one
-- Removes, then sets, in one statement. No row comes back when the app is
-- gone or when the result would carry more than max_labels labels.
UPDATE apps
SET labels = (labels - sqlc.arg(remove)::text[]) || sqlc.arg(set)::jsonb
WHERE id = sqlc.arg(id)::uuid
  AND (SELECT count(*) FROM jsonb_object_keys((labels - sqlc.arg(remove)::text[]) || sqlc.arg(set)::jsonb)) <= sqlc.arg(max_labels)::bigint
RETURNING labels;
