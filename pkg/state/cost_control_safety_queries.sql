-- name: ReserveBillingUsageIntent :one
INSERT INTO billing_usage_intents(provider, account_id, meter, window_start, quantity)
VALUES (sqlc.arg(provider)::text, sqlc.arg(account_id)::uuid, sqlc.arg(meter)::text, sqlc.arg(window_start)::timestamptz, sqlc.arg(quantity)::bigint)
ON CONFLICT (provider, account_id, meter, window_start) DO UPDATE
SET quantity = billing_usage_intents.quantity
RETURNING quantity;

-- name: PutSpendCapHoldWithNotice :exec
WITH held AS (
    INSERT INTO account_spend_cap_holds(account_id, month, cap_cents, observed_cents)
    VALUES (sqlc.arg(account_id)::uuid, sqlc.arg(month)::date, sqlc.arg(cap_cents)::bigint, sqlc.arg(observed_cents)::bigint)
    ON CONFLICT (account_id) DO UPDATE SET month = EXCLUDED.month,
        cap_cents = EXCLUDED.cap_cents, observed_cents = EXCLUDED.observed_cents,
        reached_at = CASE WHEN account_spend_cap_holds.month = EXCLUDED.month AND account_spend_cap_holds.cap_cents = EXCLUDED.cap_cents
            THEN account_spend_cap_holds.reached_at ELSE now() END
    RETURNING account_id, month, cap_cents, observed_cents, reached_at
)
INSERT INTO spend_cap_notice_outbox(id, account_id, month, cap_cents, observed_cents, reached_at)
SELECT sqlc.arg(id)::uuid, account_id, month::timestamp AT TIME ZONE 'UTC', cap_cents, observed_cents, reached_at FROM held
ON CONFLICT (account_id, month, cap_cents) DO NOTHING;

-- name: ClaimSpendCapNotice :one
WITH candidate AS (
    SELECT id FROM spend_cap_notice_outbox
    WHERE state <> 'delivered' AND next_attempt_at <= sqlc.arg(now_at)::timestamptz
    ORDER BY next_attempt_at, id FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE spend_cap_notice_outbox n SET state = 'sending', lease_token = sqlc.arg(lease_token)::uuid,
    next_attempt_at = sqlc.arg(lease_until)::timestamptz
FROM candidate WHERE n.id = candidate.id RETURNING n.*;

-- name: SettleSpendCapNotice :one
UPDATE spend_cap_notice_outbox SET state = sqlc.arg(state)::text, lease_token = NULL,
    delivered_at = sqlc.narg(delivered_at)::timestamptz, next_attempt_at = sqlc.arg(next_attempt_at)::timestamptz
WHERE id = sqlc.arg(id)::uuid AND state = 'sending' AND lease_token = sqlc.arg(lease_token)::uuid
    AND next_attempt_at > sqlc.arg(now_at)::timestamptz
RETURNING id;

-- name: BillingPlanActivationHead :one
SELECT coalesce(max(sequence), 0)::bigint FROM billing_plan_activations;

-- name: ListBillingPlanActivations :many
SELECT sequence, from_plan, to_plan, effective_at FROM billing_plan_activations
WHERE subject_id = sqlc.arg(subject_id)::uuid AND effective_at >= sqlc.arg(period_start)::timestamptz
    AND effective_at <= sqlc.arg(through_at)::timestamptz
ORDER BY effective_at, sequence;

-- name: FinancialBudgetJobs :many
SELECT id, account_id, name, status FROM jobs
WHERE account_id = sqlc.arg(account_id)::uuid AND status <> 'deleted'
ORDER BY created_at DESC, id LIMIT sqlc.arg(page_size)::integer;
