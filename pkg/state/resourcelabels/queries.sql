-- name: DatabaseLabels :one
SELECT labels FROM managed_postgres_databases WHERE id = sqlc.arg(id)::uuid;

-- name: DatabaseLabelsByID :many
-- Only rows that carry labels: a row absent from the result has none.
SELECT id, labels FROM managed_postgres_databases
WHERE id = ANY(sqlc.arg(ids)::uuid[]) AND labels <> '{}'::jsonb;

-- name: UpdateDatabaseLabels :one
-- Removes, then sets, in one statement. No row comes back when the row is
-- gone or when the result would carry more than max_labels labels.
UPDATE managed_postgres_databases
SET labels = (labels - sqlc.arg(remove)::text[]) || sqlc.arg(set)::jsonb
WHERE id = sqlc.arg(id)::uuid
  AND (SELECT count(*) FROM jsonb_object_keys((labels - sqlc.arg(remove)::text[]) || sqlc.arg(set)::jsonb)) <= sqlc.arg(max_labels)::bigint
RETURNING labels;

-- name: CronLabels :one
SELECT labels FROM crons WHERE id = sqlc.arg(id)::uuid;

-- name: CronLabelsByID :many
-- Only rows that carry labels: a row absent from the result has none.
SELECT id, labels FROM crons
WHERE id = ANY(sqlc.arg(ids)::uuid[]) AND labels <> '{}'::jsonb;

-- name: UpdateCronLabels :one
-- Removes, then sets, in one statement. No row comes back when the row is
-- gone or when the result would carry more than max_labels labels.
UPDATE crons
SET labels = (labels - sqlc.arg(remove)::text[]) || sqlc.arg(set)::jsonb
WHERE id = sqlc.arg(id)::uuid
  AND (SELECT count(*) FROM jsonb_object_keys((labels - sqlc.arg(remove)::text[]) || sqlc.arg(set)::jsonb)) <= sqlc.arg(max_labels)::bigint
RETURNING labels;

-- name: JobLabels :one
SELECT labels FROM jobs WHERE id = sqlc.arg(id)::uuid;

-- name: JobLabelsByID :many
-- Only rows that carry labels: a row absent from the result has none.
SELECT id, labels FROM jobs
WHERE id = ANY(sqlc.arg(ids)::uuid[]) AND labels <> '{}'::jsonb;

-- name: UpdateJobLabels :one
-- Removes, then sets, in one statement. No row comes back when the row is
-- gone or when the result would carry more than max_labels labels.
UPDATE jobs
SET labels = (labels - sqlc.arg(remove)::text[]) || sqlc.arg(set)::jsonb
WHERE id = sqlc.arg(id)::uuid
  AND (SELECT count(*) FROM jsonb_object_keys((labels - sqlc.arg(remove)::text[]) || sqlc.arg(set)::jsonb)) <= sqlc.arg(max_labels)::bigint
RETURNING labels;
