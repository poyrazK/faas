-- Flag experiments and conversion events (ADR 20261115163113).

-- name: EnsureFlagEventUsage :exec
INSERT INTO flag_event_usage (account_id, month)
VALUES (sqlc.arg(account_id)::uuid, sqlc.arg(month)::date)
ON CONFLICT (account_id, month) DO NOTHING;

-- name: LockFlagEventUsage :one
SELECT accepted, dropped FROM flag_event_usage
WHERE account_id = sqlc.arg(account_id)::uuid AND month = sqlc.arg(month)::date
FOR UPDATE;

-- name: AddFlagEventUsage :one
UPDATE flag_event_usage
SET accepted = accepted + sqlc.arg(accepted)::bigint, dropped = dropped + sqlc.arg(dropped)::bigint
WHERE account_id = sqlc.arg(account_id)::uuid AND month = sqlc.arg(month)::date
RETURNING accepted, dropped;

-- name: AddFlagEventName :execrows
-- Registers an event name while the environment holds fewer than max_names.
INSERT INTO flag_event_names (environment_id, name, account_id)
SELECT sqlc.arg(environment_id)::uuid, sqlc.arg(name)::text, sqlc.arg(account_id)::uuid
WHERE (SELECT count(*) FROM flag_event_names n WHERE n.environment_id = sqlc.arg(environment_id)::uuid) < sqlc.arg(max_names)::bigint
ON CONFLICT (environment_id, name) DO NOTHING;

-- name: FlagEventNamesKnown :many
SELECT name FROM flag_event_names
WHERE environment_id = sqlc.arg(environment_id)::uuid AND name = ANY(sqlc.arg(names)::text[]);

-- name: ActivePlatformTenantIDs :many
SELECT id FROM platform_tenants
WHERE account_id = sqlc.arg(account_id)::uuid AND id = ANY(sqlc.arg(ids)::uuid[]) AND status = 'active';

-- name: AddFlagConversionDaily :exec
INSERT INTO flag_conversion_daily (environment_id, event_name, customer_id, day, account_id, count, value_sum, first_at, last_at)
VALUES (sqlc.arg(environment_id)::uuid, sqlc.arg(event_name)::text, sqlc.arg(customer_id)::uuid, sqlc.arg(day)::date,
        sqlc.arg(account_id)::uuid, sqlc.arg(count)::bigint, sqlc.arg(value_sum)::bigint,
        sqlc.arg(first_at)::timestamptz, sqlc.arg(last_at)::timestamptz)
ON CONFLICT (environment_id, event_name, customer_id, day) DO UPDATE
SET count = flag_conversion_daily.count + EXCLUDED.count,
    value_sum = flag_conversion_daily.value_sum + EXCLUDED.value_sum,
    first_at = LEAST(flag_conversion_daily.first_at, EXCLUDED.first_at),
    last_at = GREATEST(flag_conversion_daily.last_at, EXCLUDED.last_at);

-- name: FlagConversionAccounts :many
-- Accounts holding conversion rows older than cutoff, for the retention sweep.
SELECT DISTINCT d.account_id, a.plan::text AS plan
FROM flag_conversion_daily d JOIN accounts a ON a.id = d.account_id
WHERE d.day < sqlc.arg(cutoff)::date
LIMIT sqlc.arg(max_accounts)::int;

-- name: DeleteFlagConversionsBefore :execrows
DELETE FROM flag_conversion_daily
WHERE account_id = sqlc.arg(account_id)::uuid AND day < sqlc.arg(before)::date;
