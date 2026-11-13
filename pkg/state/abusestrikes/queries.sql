-- name: PruneStrikes :exec
-- Strikes older than the escalation window never count again; dropping them
-- on every write keeps the table to the last window of abuse.
DELETE FROM account_abuse_strikes WHERE struck_at <= sqlc.arg(cutoff)::timestamptz;

-- name: InsertStrike :exec
INSERT INTO account_abuse_strikes (account_id, reason, app_id, struck_at)
VALUES (sqlc.arg(account_id)::uuid, sqlc.arg(reason)::text, sqlc.narg(app_id)::uuid, sqlc.arg(struck_at)::timestamptz);

-- name: CountStrikes :one
-- Runs after InsertStrike commits, so of two schedds striking one account at
-- once, the later count sees both strikes.
SELECT count(*)::int FROM account_abuse_strikes
WHERE account_id = sqlc.arg(account_id)::uuid AND struck_at > sqlc.arg(cutoff)::timestamptz;
