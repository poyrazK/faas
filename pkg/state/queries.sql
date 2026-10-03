-- name: ClaimNotificationForNode :one
WITH candidate AS (
    SELECT id
    FROM notification_outbox
    WHERE channel = ANY(sqlc.arg(channels)::text[])
      -- A fixed-size index key accepts oversized poison events. The full
      -- identity check is required too; hash equality never grants ownership.
      AND (sqlc.arg(node_id)::text = '' OR
           (md5(notification_outbox_target_node(channel, payload)) IN (md5(''), md5(sqlc.arg(node_id)::text))
            AND notification_outbox_target_node(channel, payload) IN ('', sqlc.arg(node_id)::text)))
      AND ((state = 'pending' AND available_at <= now()) OR
           (state = 'processing' AND lease_until <= clock_timestamp()))
    ORDER BY id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE notification_outbox o
SET state = 'processing', attempts = o.attempts + 1,
    claimed_by = sqlc.arg(claim_token)::text, claimed_at = now(),
    lease_until = clock_timestamp() + sqlc.arg(lease_milliseconds)::bigint * interval '1 millisecond'
FROM candidate
WHERE o.id = candidate.id
RETURNING o.id, o.channel, o.payload, o.attempts,
          COALESCE(o.claimed_by, '')::text AS claim_token;

-- name: ClaimImmediateNotificationForNode :one
WITH candidate AS (
    SELECT id
    FROM notification_outbox
    WHERE id = sqlc.arg(id)::bigint AND channel = sqlc.arg(channel)::text
      AND (sqlc.arg(node_id)::text = '' OR
           notification_outbox_target_node(channel, payload) IN ('', sqlc.arg(node_id)::text))
      -- Only the first delivery bypasses the LISTEN grace period. A repeated
      -- notification must not bypass a failed handler's retry backoff.
      AND ((state = 'pending' AND (attempts = 0 OR available_at <= now())) OR
           (state = 'processing' AND lease_until <= clock_timestamp()))
    FOR UPDATE SKIP LOCKED
)
UPDATE notification_outbox o
SET state = 'processing', attempts = o.attempts + 1,
    claimed_by = sqlc.arg(claim_token)::text, claimed_at = now(),
    lease_until = clock_timestamp() + sqlc.arg(lease_milliseconds)::bigint * interval '1 millisecond'
FROM candidate
WHERE o.id = candidate.id
RETURNING o.id, o.channel, o.payload, o.attempts,
          COALESCE(o.claimed_by, '')::text AS claim_token;

-- name: RenewNotificationClaim :execrows
-- Materialize the locked row before evaluating expiry. A valid predicate
-- evaluated before waiting for a row lock must not resurrect an expired lease.
WITH owned AS MATERIALIZED (
    SELECT id, lease_until FROM notification_outbox
    WHERE id = sqlc.arg(id)::bigint AND state = 'processing'
      AND claimed_by = sqlc.arg(claim_token)::text
    FOR UPDATE
)
UPDATE notification_outbox o
SET lease_until = clock_timestamp() + sqlc.arg(lease_milliseconds)::bigint * interval '1 millisecond'
FROM owned
WHERE o.id = owned.id AND owned.lease_until > clock_timestamp();

-- name: CompleteNotificationClaim :execrows
WITH owned AS MATERIALIZED (
    SELECT id, lease_until FROM notification_outbox
    WHERE id = sqlc.arg(id)::bigint AND state = 'processing'
      AND claimed_by = sqlc.arg(claim_token)::text
    FOR UPDATE
)
UPDATE notification_outbox o
SET state = 'delivered', delivered_at = now(),
    claimed_by = NULL, claimed_at = NULL, lease_until = NULL, last_error = NULL
FROM owned
WHERE o.id = owned.id AND owned.lease_until > clock_timestamp();

-- name: AcknowledgePendingNotification :exec
-- Legacy scheduler subscribers cannot acknowledge another worker's lease.
UPDATE notification_outbox
SET state = 'delivered', delivered_at = now(),
    claimed_by = NULL, claimed_at = NULL, lease_until = NULL, last_error = NULL
WHERE id = sqlc.arg(id)::bigint AND state = 'pending';

-- name: NotificationClaimAttempts :one
SELECT attempts FROM notification_outbox
WHERE id = sqlc.arg(id)::bigint AND state = 'processing'
  AND claimed_by = sqlc.arg(claim_token)::text AND lease_until > clock_timestamp();

-- name: FailNotificationClaim :execrows
WITH owned AS MATERIALIZED (
    SELECT id, lease_until FROM notification_outbox
    WHERE id = sqlc.arg(id)::bigint AND state = 'processing'
      AND claimed_by = sqlc.arg(claim_token)::text
    FOR UPDATE
)
UPDATE notification_outbox o
SET state = CASE WHEN o.attempts >= sqlc.arg(max_attempts)::integer THEN 'dead_letter' ELSE 'pending' END,
    available_at = clock_timestamp() + sqlc.arg(retry_milliseconds)::bigint * interval '1 millisecond',
    claimed_by = NULL, claimed_at = NULL, lease_until = NULL,
    last_error = sqlc.arg(message)::text
FROM owned
WHERE o.id = owned.id AND owned.lease_until > clock_timestamp();

-- name: ReleaseUnownedNotification :execrows
WITH owned AS MATERIALIZED (
    SELECT id, lease_until FROM notification_outbox
    WHERE id = sqlc.arg(id)::bigint AND state = 'processing'
      AND claimed_by = sqlc.arg(claim_token)::text AND attempts > 0
    FOR UPDATE
)
UPDATE notification_outbox o
SET state = 'pending', attempts = o.attempts - 1,
    claimed_by = NULL, claimed_at = NULL, lease_until = NULL
FROM owned
WHERE o.id = owned.id AND owned.lease_until > clock_timestamp();

-- name: LockDeploymentHostingFailure :one
SELECT app_id, status FROM deployments
WHERE id = sqlc.arg(deployment_id)::uuid
FOR UPDATE;

-- name: LockDeploymentHostingVerification :one
SELECT status, stage_state FROM deployments
WHERE id = sqlc.arg(deployment_id)::uuid
FOR UPDATE;

-- name: WriteDeploymentHostingVerification :execrows
UPDATE deployments
SET stage_state = jsonb_set(stage_state, '{hosting_verification}', sqlc.arg(progress)::jsonb)
WHERE id = sqlc.arg(deployment_id)::uuid AND status = 'snapshotting';

-- name: WriteDeploymentHostingFailureReceipt :execrows
UPDATE deployments SET api_hosting_receipt = sqlc.arg(receipt)::jsonb
WHERE id = sqlc.arg(deployment_id)::uuid AND status = 'snapshotting';

-- name: PutTCPListenerTLSObservation :execrows
INSERT INTO app_tcp_listener_tls_observations
    (listener_id, edge_id, hostname, intent_updated_at, observed_at, ready, not_after)
SELECT l.id, sqlc.arg(edge_id)::text, sqlc.arg(hostname)::text,
       sqlc.arg(intent_updated_at)::timestamptz, sqlc.arg(observed_at)::timestamptz,
       sqlc.arg(ready)::boolean, sqlc.narg(not_after)::timestamptz
FROM app_tcp_listeners l
WHERE l.id = sqlc.arg(listener_id)::uuid AND l.enabled
  AND l.tls_mode = 'terminate' AND l.tls_hostname = sqlc.arg(hostname)::text
  AND l.updated_at = sqlc.arg(intent_updated_at)::timestamptz
ON CONFLICT (listener_id, edge_id) DO UPDATE
SET hostname = EXCLUDED.hostname, intent_updated_at = EXCLUDED.intent_updated_at,
    observed_at = EXCLUDED.observed_at, ready = EXCLUDED.ready, not_after = EXCLUDED.not_after
WHERE app_tcp_listener_tls_observations.observed_at < EXCLUDED.observed_at;

-- name: ListTCPListenerTLSObservations :many
SELECT listener_id, edge_id, hostname, intent_updated_at, observed_at, ready, not_after
FROM app_tcp_listener_tls_observations
WHERE listener_id = sqlc.arg(listener_id)::uuid
ORDER BY edge_id;

-- name: PruneTCPListenerTLSObservations :execrows
DELETE FROM app_tcp_listener_tls_observations
WHERE observed_at <= sqlc.arg(before_at)::timestamptz;

-- name: ReadAccountCreditConsumption :one
-- An unqualified legacy row blocks the whole key; guessing could double-debit.
SELECT coalesce(sum(-delta_cents) FILTER (WHERE provider = sqlc.arg(provider)::text), 0)::bigint AS consumed_cents,
       coalesce(bool_or(delta_cents < 0) FILTER (WHERE provider = sqlc.arg(provider)), false)::boolean AS has_prior,
       coalesce(bool_or(provider = ''), false)::boolean AS has_unqualified
FROM credit_ledger
WHERE account_id = sqlc.arg(account_id)::uuid
  AND provider_invoice_id = sqlc.arg(provider_invoice_id)::text;

-- name: ReverseAccountInvoiceCreditConsumption :execrows
WITH consumed AS (
    SELECT ledger.credit_id, sum(-ledger.delta_cents)::bigint AS cents
    FROM credit_ledger AS ledger
    JOIN account_credits AS credit
      ON credit.id = ledger.credit_id AND credit.account_id = ledger.account_id
    WHERE ledger.account_id = sqlc.arg(account_id)::uuid
      AND ledger.provider_invoice_id = sqlc.arg(provider_invoice_id)::text
      AND ledger.provider = sqlc.arg(provider)::text
    GROUP BY ledger.credit_id
    HAVING sum(-ledger.delta_cents) > 0
), inserted AS (
    INSERT INTO credit_ledger
        (account_id, credit_id, delta_cents, reason, actor, provider, provider_invoice_id, refund_reversal_id)
    SELECT sqlc.arg(account_id), credit_id, cents, 'provider refund failed',
           'apid-refund-reversal', sqlc.arg(provider), sqlc.arg(provider_invoice_id), sqlc.arg(refund_id)::uuid
    FROM consumed
    ON CONFLICT (refund_reversal_id, credit_id) WHERE refund_reversal_id IS NOT NULL
        DO NOTHING
    RETURNING credit_id, delta_cents
)
UPDATE account_credits AS credit
SET cents_remaining = credit.cents_remaining + inserted.delta_cents
FROM inserted
WHERE credit.id = inserted.credit_id AND credit.account_id = sqlc.arg(account_id);

-- name: ListAppSecretRuntimeReloadTargets :many
-- Build the complete active roster for each secret from the deployment's
-- persisted scope/allowlist and reload opt-in. A missing observation remains
-- a target with nullable outcome fields rather than disappearing from the
-- denominator.
SELECT s.scope,
       s.key,
       i.id::text AS instance_id,
       ''::text AS workload_name,
       i.state AS runtime_state,
       CASE
         WHEN d.secret_reload_signal IS NULL THEN 'unknown'
         WHEN d.secret_reload_signal = '' OR jsonb_array_length(d.sidecars) > 0 THEN 'disabled'
         ELSE 'enabled'
       END AS reload_support,
       o.secret_version,
       o.projection,
       o.signal,
       o.observed_at,
       o.error_code,
       o.application_ack_version,
       o.application_ack_status,
       o.application_ack_at,
       o.application_ack_error_code
  FROM instances i
  JOIN deployments d ON d.id = i.deployment_id AND d.app_id = i.app_id
  JOIN app_secrets s ON s.app_id = i.app_id AND s.scope = d.scope
  LEFT JOIN app_secret_runtime_reload_observations o
    ON o.app_id = s.app_id AND o.scope = s.scope AND o.key = s.key AND o.instance_id = i.id
   AND o.workload_name = ''
 WHERE s.account_id = sqlc.arg(account_id)::uuid
   AND i.app_id = sqlc.arg(app_id)::uuid
   AND (sqlc.arg(scope)::text = '' OR s.scope = sqlc.arg(scope)::text)
   AND (sqlc.arg(key)::text = '' OR s.key = sqlc.arg(key)::text)
   AND NOT EXISTS (SELECT 1 FROM managed_postgres_bindings b WHERE b.id = s.managed_postgres_binding_id AND b.access = 'migration')
   AND i.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm')
   AND ((coalesce(d.override_env_secrets, '{}'::jsonb) = '{}'::jsonb
         AND jsonb_array_length(coalesce(d.sidecars, '[]'::jsonb)) = 0)
        OR d.override_env_secrets ? s.key)
UNION ALL
SELECT s.scope,
       s.key,
       i.id::text AS instance_id,
       sidecar.value->>'name' AS workload_name,
       i.state AS runtime_state,
       CASE
         WHEN reload.signal IS NULL THEN 'unknown'
         WHEN reload.signal = '' THEN 'disabled'
         ELSE 'enabled'
       END AS reload_support,
       o.secret_version,
       o.projection,
       o.signal,
       o.observed_at,
       o.error_code,
       o.application_ack_version,
       o.application_ack_status,
       o.application_ack_at,
       o.application_ack_error_code
  FROM instances i
  JOIN deployments d ON d.id = i.deployment_id AND d.app_id = i.app_id
  JOIN app_secrets s ON s.app_id = i.app_id AND s.scope = d.scope
 CROSS JOIN LATERAL jsonb_array_elements(coalesce(d.sidecars, '[]'::jsonb)) AS sidecar(value)
  LEFT JOIN deployment_sidecar_secret_reload_signals reload
    ON reload.deployment_id = d.id AND reload.sidecar_name = sidecar.value->>'name'
  LEFT JOIN app_secret_runtime_reload_observations o
    ON o.app_id = s.app_id AND o.scope = s.scope AND o.key = s.key AND o.instance_id = i.id
   AND o.workload_name = sidecar.value->>'name'
 WHERE s.account_id = sqlc.arg(account_id)::uuid
   AND i.app_id = sqlc.arg(app_id)::uuid
   AND (sqlc.arg(scope)::text = '' OR s.scope = sqlc.arg(scope)::text)
   AND (sqlc.arg(key)::text = '' OR s.key = sqlc.arg(key)::text)
   AND NOT EXISTS (SELECT 1 FROM managed_postgres_bindings b WHERE b.id = s.managed_postgres_binding_id AND b.access = 'migration')
   AND i.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm')
   AND sidecar.value->>'type' = 'sidecar'
   AND coalesce(sidecar.value->'env_secrets', '{}'::jsonb) ? s.key
ORDER BY scope ASC, key ASC, instance_id ASC, workload_name ASC;

-- name: CreateAppSecretRevocation :one
INSERT INTO app_secret_revocations (id, account_id, app_id, scope, key, created_at)
VALUES (sqlc.arg(id)::uuid, sqlc.arg(account_id)::uuid, sqlc.arg(app_id)::uuid,
        sqlc.arg(scope)::text, sqlc.arg(key)::text, sqlc.arg(created_at)::timestamptz)
RETURNING id::text, account_id::text, app_id::text, scope, key, created_at;

-- name: GetCustomerAppSecretForDeletion :one
SELECT EXISTS (
           SELECT 1 FROM app_secrets
            WHERE account_id = sqlc.arg(account_id)::uuid
              AND app_id = sqlc.arg(app_id)::uuid
              AND scope = sqlc.arg(scope)::text
              AND key = sqlc.arg(key)::text
       ) AS present,
       EXISTS (
           SELECT 1 FROM app_secrets
            WHERE account_id = sqlc.arg(account_id)::uuid
              AND app_id = sqlc.arg(app_id)::uuid
              AND scope = sqlc.arg(scope)::text
              AND key = sqlc.arg(key)::text
              AND (managed_postgres_binding_id IS NOT NULL OR managed_object_storage_credential_id IS NOT NULL)
       ) AS managed;

-- name: CreateAppSecretRevocationTarget :exec
INSERT INTO app_secret_revocation_targets
    (revocation_id, instance_id, workload_name, runtime_state, reload_support)
VALUES (sqlc.arg(revocation_id)::uuid, sqlc.arg(instance_id)::uuid,
        sqlc.arg(workload_name)::text, sqlc.arg(runtime_state)::text,
        sqlc.arg(reload_support)::text);

-- name: GetAppSecretRevocation :one
SELECT id::text, account_id::text, app_id::text, scope, key, created_at
  FROM app_secret_revocations
 WHERE account_id = sqlc.arg(account_id)::uuid
   AND app_id = sqlc.arg(app_id)::uuid
   AND id = sqlc.arg(id)::uuid;

-- name: ListAppSecretRevocationTargets :many
SELECT instance_id::text, workload_name, runtime_state, reload_support,
       status, coalesce(ack_revision, ''), ack_at, coalesce(error_code, '')
  FROM app_secret_revocation_targets
 WHERE revocation_id = sqlc.arg(revocation_id)::uuid
 ORDER BY instance_id, workload_name;

-- name: RecordAppSecretRevocationAck :execrows
UPDATE app_secret_revocation_targets t
   SET status = sqlc.arg(status)::text,
       ack_revision = sqlc.arg(ack_revision)::text,
       ack_at = sqlc.arg(ack_at)::timestamptz,
       error_code = nullif(sqlc.arg(error_code)::text, '')
  FROM app_secret_revocations r
 WHERE t.revocation_id = r.id
   AND r.account_id = sqlc.arg(account_id)::uuid
   AND r.app_id = sqlc.arg(app_id)::uuid
   AND t.instance_id = sqlc.arg(instance_id)::uuid
   AND t.workload_name = sqlc.arg(workload_name)::text
   AND r.created_at <= sqlc.arg(ack_at)::timestamptz
   AND EXISTS (SELECT 1 FROM instances i WHERE i.id = t.instance_id AND i.app_id = r.app_id)
   AND NOT EXISTS (
       SELECT 1 FROM app_secrets s
        WHERE s.account_id = r.account_id AND s.app_id = r.app_id
          AND s.scope = r.scope AND s.key = r.key
   )
   AND t.status <> 'applied';

-- name: DeleteCustomerAppSecret :execrows
DELETE FROM app_secrets
 WHERE account_id = sqlc.arg(account_id)::uuid
   AND app_id = sqlc.arg(app_id)::uuid
   AND scope = sqlc.arg(scope)::text
   AND key = sqlc.arg(key)::text
   AND managed_postgres_binding_id IS NULL
   AND managed_object_storage_credential_id IS NULL;

-- name: SetDeploymentSecretReloadSignal :execrows
-- imaged persists the validated image opt-in on each newly built deployment;
-- the state query keeps legacy NULL rows distinct from explicit opt-outs.
UPDATE deployments
   SET secret_reload_signal = sqlc.arg(signal)::text
 WHERE id = sqlc.arg(id)::uuid;

-- name: SumAccountCreditRefundReversal :one
SELECT coalesce(sum(delta_cents), 0)::bigint AS reversed_cents
FROM credit_ledger
WHERE account_id = sqlc.arg(account_id)::uuid
  AND refund_reversal_id = sqlc.arg(refund_id)::uuid;

-- name: AppendAccountCreditLedgerEntry :exec
INSERT INTO credit_ledger (account_id, credit_id, delta_cents, reason, actor, provider, provider_invoice_id)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ReserveAccountCreditConsumption :one
INSERT INTO credit_ledger (account_id, credit_id, delta_cents, reason, actor, provider, provider_invoice_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (provider, provider_invoice_id, credit_id)
    WHERE provider_invoice_id IS NOT NULL AND delta_cents < 0 DO NOTHING
RETURNING id;

-- name: LockInvoiceForRefund :one
SELECT account_id, provider, provider_invoice_id, amount_paid_cents,
       total_cents, amount_refunded_cents, amount_refund_pending_cents, credits_applied_cents
FROM invoices WHERE id = $1 FOR UPDATE;

-- name: LockCreditConsumption :exec
-- Keep the historical broad lock key, also shared with refund compensation.
SELECT pg_advisory_xact_lock(hashtextextended('consume-account-credit:' || sqlc.arg(provider_invoice_id)::text, 0));

-- name: FindInvoiceIDsByProviderKey :many
-- Two matches mean an invoice ID collides with another invoice's charge ID.
SELECT id FROM invoices
WHERE account_id = sqlc.arg(account_id)::uuid
  AND provider = sqlc.arg(provider)::text
  AND (provider_invoice_id = sqlc.arg(provider_key)::text
       OR provider_charge_id = sqlc.arg(provider_key))
LIMIT 2;

-- name: ListInvoiceSnapshots :many
SELECT id, account_id, provider, provider_invoice_id, provider_charge_id, number, status,
       period_start, period_end, subtotal_cents, tax_cents, total_cents, amount_paid_cents,
       plan, amount_refunded_cents, amount_refund_pending_cents, credits_applied_cents,
       currency, pdf_available, created_at, updated_at, details, detail_lifecycle FROM invoices
WHERE account_id = sqlc.arg(account_id)::uuid
  AND (sqlc.narg(month_start)::timestamptz IS NULL OR period_end >= sqlc.narg(month_start))
  AND (sqlc.narg(month_end)::timestamptz IS NULL OR period_end < sqlc.narg(month_end))
  AND (sqlc.narg(before_time)::timestamptz IS NULL OR period_end < sqlc.narg(before_time))
ORDER BY period_end DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: GetInvoiceSnapshot :one
SELECT id, account_id, provider, provider_invoice_id, provider_charge_id, number, status,
       period_start, period_end, subtotal_cents, tax_cents, total_cents, amount_paid_cents,
       plan, amount_refunded_cents, amount_refund_pending_cents, credits_applied_cents,
       currency, pdf_available, created_at, updated_at, details, detail_lifecycle FROM invoices WHERE id = $1;

-- name: LockOwnedInvoiceSnapshot :one
SELECT id, account_id, provider, provider_invoice_id, provider_charge_id, number, status,
       period_start, period_end, subtotal_cents, tax_cents, total_cents, amount_paid_cents,
       plan, amount_refunded_cents, amount_refund_pending_cents, credits_applied_cents,
       currency, pdf_available, created_at, updated_at, details, detail_lifecycle FROM invoices WHERE id = $1 AND account_id = $2 FOR UPDATE;

-- name: InvoiceRefreshTime :one
SELECT clock_timestamp()::timestamptz;

-- name: SetInvoiceEnrichment :exec
UPDATE invoices SET details = $2, detail_lifecycle = $3, updated_at = $4 WHERE id = $1;

-- name: UpsertInvoiceSnapshot :one
INSERT INTO invoices (
  account_id, provider, provider_invoice_id, provider_charge_id, number, status,
  period_start, period_end, subtotal_cents, tax_cents, total_cents,
  amount_paid_cents, plan, currency, pdf_available, details, detail_lifecycle, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, clock_timestamp())
ON CONFLICT (account_id, provider, provider_invoice_id) DO UPDATE SET
  provider_charge_id = coalesce(nullif(excluded.provider_charge_id, ''), invoices.provider_charge_id),
  number = excluded.number, status = excluded.status,
  period_start = excluded.period_start, period_end = excluded.period_end,
  subtotal_cents = excluded.subtotal_cents, tax_cents = excluded.tax_cents,
  total_cents = excluded.total_cents, amount_paid_cents = excluded.amount_paid_cents,
  currency = excluded.currency, pdf_available = excluded.pdf_available,
  details = invoices.details || CASE WHEN excluded.details ? 'lines' THEN
    jsonb_set(excluded.details, '{lines,items}', coalesce((
      SELECT jsonb_agg(incoming.item || jsonb_build_object(
        'created_at', coalesce(invoices.details->'line_first_seen'->(incoming.item->>'id'), previous.item->'created_at', incoming.item->'created_at'),
        'updated_at', CASE WHEN incoming.item - 'created_at' - 'updated_at' = previous.item - 'created_at' - 'updated_at'
          THEN previous.item->'updated_at' ELSE incoming.item->'updated_at' END) ORDER BY incoming.ordinality)
      FROM jsonb_array_elements(excluded.details->'lines'->'items') WITH ORDINALITY AS incoming(item, ordinality)
      LEFT JOIN LATERAL (
        SELECT stored.item FROM jsonb_array_elements(coalesce(invoices.details->'lines'->'items', '[]'::jsonb)) AS stored(item)
        WHERE stored.item->>'id' = incoming.item->>'id' LIMIT 1
      ) AS previous ON true
    ), '[]'::jsonb))
  ELSE excluded.details END || CASE WHEN excluded.details ? 'lines' THEN
    jsonb_build_object('line_first_seen', coalesce(excluded.details->'line_first_seen', '{}'::jsonb) || coalesce(invoices.details->'line_first_seen', '{}'::jsonb))
  ELSE '{}'::jsonb END, updated_at = clock_timestamp()
RETURNING id, account_id, provider, provider_invoice_id, provider_charge_id, number, status,
          period_start, period_end, subtotal_cents, tax_cents, total_cents, amount_paid_cents,
          plan, amount_refunded_cents, amount_refund_pending_cents, credits_applied_cents,
          currency, pdf_available, created_at, updated_at, details, detail_lifecycle;

-- name: InsertInvoiceHistorySnapshot :one
INSERT INTO invoices (
  account_id, provider, provider_invoice_id, provider_charge_id, number, status,
  period_start, period_end, subtotal_cents, tax_cents, total_cents,
  amount_paid_cents, plan, currency, pdf_available, details, detail_lifecycle
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
ON CONFLICT (account_id, provider, provider_invoice_id) DO NOTHING
RETURNING id, updated_at;

-- name: SetInvoiceDetailLifecycle :exec
-- The caller retains the natural-key upsert's row lock in the same transaction.
UPDATE invoices SET detail_lifecycle = $2 WHERE id = $1;

-- name: RollupMirrorResults :execrows
-- ADR-221: claiming and counting share one statement/transaction. SKIP LOCKED
-- permits concurrent workers without counting the same result twice.
WITH pending AS MATERIALIZED (
    SELECT id FROM mirror_invocation_results
    WHERE NOT rollup_counted
      AND completed_at >= sqlc.arg(window_start)::timestamptz
      AND completed_at < sqlc.arg(window_end)::timestamptz
    ORDER BY id
    FOR UPDATE SKIP LOCKED
), counted AS (
    UPDATE mirror_invocation_results AS result
    SET rollup_counted = true
    FROM pending
    WHERE result.id = pending.id
    RETURNING
        result.mirror_rule_id, result.app_id, result.completed_at,
        result.status_diff, result.schema_diff, result.body_diff,
        result.crashed, result.latency_ms
)
INSERT INTO mirror_invocation_summary (
    rule_id, app_id, hour_bucket, total_invocations,
    status_diff_count, schema_diff_count, body_diff_count, crash_count,
    cap_at_max_count, sum_latency_ms, rolled_up_at
)
SELECT mirror_rule_id, app_id,
    date_trunc('hour', completed_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC',
    count(*), count(*) FILTER (WHERE status_diff),
    count(*) FILTER (WHERE schema_diff), count(*) FILTER (WHERE body_diff),
    count(*) FILTER (WHERE crashed), 0, coalesce(sum(latency_ms), 0), now()
FROM counted
GROUP BY 1, 2, 3
ORDER BY 1, 3
ON CONFLICT (rule_id, hour_bucket) DO UPDATE SET
    total_invocations = mirror_invocation_summary.total_invocations + EXCLUDED.total_invocations,
    status_diff_count = mirror_invocation_summary.status_diff_count + EXCLUDED.status_diff_count,
    schema_diff_count = mirror_invocation_summary.schema_diff_count + EXCLUDED.schema_diff_count,
    body_diff_count = mirror_invocation_summary.body_diff_count + EXCLUDED.body_diff_count,
    crash_count = mirror_invocation_summary.crash_count + EXCLUDED.crash_count,
    cap_at_max_count = mirror_invocation_summary.cap_at_max_count + EXCLUDED.cap_at_max_count,
    sum_latency_ms = mirror_invocation_summary.sum_latency_ms + EXCLUDED.sum_latency_ms,
    rolled_up_at = now();

-- name: SweepCountedMirrorResults :execrows
DELETE FROM mirror_invocation_results
WHERE completed_at < sqlc.arg(cutoff)::timestamptz AND rollup_counted;

-- name: LockMirrorRuleForSlotLease :one
-- Serializes reservation attempts for one rule across every gateway replica.
SELECT id::text FROM mirror_rules
WHERE id = sqlc.arg(rule_id)::uuid
FOR UPDATE;

-- name: DeleteExpiredMirrorSlotLeases :execrows
DELETE FROM mirror_slot_leases
WHERE mirror_rule_id = sqlc.arg(rule_id)::uuid
  AND expires_at <= clock_timestamp();

-- name: CountActiveMirrorSlotLeases :one
SELECT count(*)::bigint FROM mirror_slot_leases
WHERE mirror_rule_id = sqlc.arg(rule_id)::uuid
  AND expires_at > clock_timestamp();

-- name: CreateMirrorSlotLease :one
INSERT INTO mirror_slot_leases (lease_id, mirror_rule_id, expires_at)
VALUES (
    sqlc.arg(lease_id)::uuid,
    sqlc.arg(rule_id)::uuid,
    clock_timestamp() + sqlc.arg(ttl_millis)::bigint * interval '1 millisecond'
)
RETURNING lease_id::text;

-- name: ReleaseMirrorSlotLease :exec
DELETE FROM mirror_slot_leases
WHERE mirror_rule_id = sqlc.arg(rule_id)::uuid
  AND lease_id = sqlc.arg(lease_id)::uuid;

-- name: CreateAccount :one
insert into accounts (id, email, plan, status, provider_customer_id)
values (gen_random_uuid(), $1, $2, $3, null)
returning id, email, plan, status, coalesce(provider_customer_id, ''), created_at;

-- name: AccountByID :one
select id, email, plan, status, coalesce(provider_customer_id, ''), created_at
from accounts where id = $1;

-- name: AccountsByIDs :many
select id, email, plan, status, coalesce(provider_customer_id, ''), coalesce(stripe_subscription_item, ''), created_at, deletion_requested_at, last_quota_warning_at, past_due_at, mfa_enrolled_at, mfa_secret_encrypted, mfa_recovery_codes_hash, mfa_required
from accounts where id = any($1::uuid[]);

-- name: AccountByEmail :one
select id, email, plan, status, coalesce(provider_customer_id, ''), created_at
from accounts where email = $1;

-- name: AccountByKeyHash :one
select a.id, a.email, a.plan, a.status, coalesce(a.provider_customer_id, ''), a.created_at
from accounts a
join api_keys k on k.account_id = a.id
where k.key_sha256 = $1;

-- name: UpdateAccountPlan :exec
update accounts set plan = $2 where id = $1;

-- name: UpdateAccountStatus :exec
update accounts set status = $2 where id = $1;

-- name: CreateAPIKey :one
-- scopes is $4 (text[]). The handler is responsible for validating the
-- scope vocabulary; the store does not. See ADR-034 rev2.
insert into api_keys (account_id, key_sha256, label, scopes)
values ($1, $2, $3, $4)
returning id, account_id, key_sha256, coalesce(label, ''), scopes, created_at, coalesce(last_used_at, 'epoch'::timestamptz);

-- name: DeleteAPIKey :exec
delete from api_keys where id = $1 and account_id = $2;

-- name: DeleteAPIKeyReturning :one
-- IAM-1 (ADR-034 rev2): delete a key and return the row in one
-- statement so the handler can emit `key.deleted` audit with the
-- dismissed scopes. list_secrets-shaped variant of DeleteAPIKey.
delete from api_keys where id = $1 and account_id = $2
returning id, account_id, key_sha256, coalesce(label, ''), scopes, created_at, coalesce(last_used_at, 'epoch'::timestamptz);

-- name: ListAPIKeys :many
-- scopes is the auth permission set surfaced to the dashboard and the
-- /v1/keys listing. See ADR-034 rev2.
select id, account_id, key_sha256, coalesce(label, ''), scopes, created_at, coalesce(last_used_at, 'epoch'::timestamptz)
from api_keys where account_id = $1 order by created_at desc;

-- name: APIKeyByHash :one
-- Used by handlers_auth.go so an operator investigating "who signed in
-- as alice?" can identify the key that authenticated. See ADR-034 rev2.
select id, account_id, key_sha256, coalesce(label, ''), scopes, created_at, coalesce(last_used_at, 'epoch'::timestamptz)
from api_keys where key_sha256 = $1;

-- name: TouchKeyLastUsed :exec
update api_keys set last_used_at = now() where id = $1;

-- name: CreateApp :one
insert into apps (id, account_id, slug, type, runtime, ram_mb, idle_timeout_s, max_concurrency, status, manifest)
values (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, 'active', coalesce($8, '{}'::jsonb))
returning id, account_id, slug, type, coalesce(runtime, ''), ram_mb, coalesce(idle_timeout_s, 0),
          max_concurrency, status, manifest, created_at;

-- name: AppByID :one
select id, account_id, slug, type, coalesce(runtime, ''), ram_mb, coalesce(idle_timeout_s, 0),
       max_concurrency, status, manifest, created_at
from apps where id = $1;

-- name: AppBySlug :one
select id, account_id, slug, type, coalesce(runtime, ''), ram_mb, coalesce(idle_timeout_s, 0),
       max_concurrency, status, manifest, created_at
from apps where slug = $1;

-- name: ListApps :many
select id, account_id, slug, type, coalesce(runtime, ''), ram_mb, coalesce(idle_timeout_s, 0),
       max_concurrency, status, manifest, created_at
from apps where account_id = $1 order by created_at desc;

-- name: CountDeployedApps :one
select count(*) from apps where account_id = $1 and status in ('active', 'evicted_cold')
  and not (preview_of_slug is not null and coalesce(preview_pr_number, 0) = 0);

-- name: UpdateApp :one
update apps set
  ram_mb = coalesce($2, ram_mb),
  idle_timeout_s = case when $3::boolean then $4 else idle_timeout_s end,
  max_concurrency = coalesce($5, max_concurrency),
  status = coalesce($6, status)
where id = $1
returning id, account_id, slug, type, coalesce(runtime, ''), ram_mb, coalesce(idle_timeout_s, 0),
          max_concurrency, status, manifest, created_at;

-- name: SetAppManifest :exec
update apps set manifest = $2 where id = $1;

-- name: DeleteApp :exec
update apps
set status = 'deleted',
    deleted_at = coalesce(deleted_at, now()),
    delete_grace_until = coalesce(delete_grace_until, now() + interval '7 days')
where id = $1;

-- name: CreateDeployment :one
insert into deployments (id, app_id, build_id, image_digest, kind, source_path, source_root, source_bytes, handler, log_path, status)
values (gen_random_uuid(), $1, null, $2, $3, $4, $5, $6, $7, $8, 'pending')
returning id, app_id, coalesce(build_id::text, ''), image_digest, kind,
          coalesce(source_path, ''), coalesce(source_root, ''), coalesce(source_bytes, 0),
          coalesce(handler, ''), coalesce(log_path, ''),
          status, coalesce(error, ''), created_at;

-- name: DeploymentByID :one
select id, app_id, coalesce(build_id::text, ''), image_digest, kind,
       coalesce(source_path, ''), coalesce(source_root, ''), coalesce(source_bytes, 0),
       coalesce(handler, ''), coalesce(log_path, ''),
       status, coalesce(error, ''), created_at
from deployments where id = $1;

-- name: LatestDeployment :one
select id, app_id, coalesce(build_id::text, ''), image_digest, kind,
       coalesce(source_path, ''), coalesce(source_root, ''), coalesce(source_bytes, 0),
       coalesce(handler, ''), coalesce(log_path, ''),
       status, coalesce(error, ''), created_at
from deployments where app_id = $1 order by created_at desc limit 1;

-- name: ListDeploymentsForApp :many
select id, app_id, coalesce(build_id::text, ''), image_digest, kind,
       coalesce(source_path, ''), coalesce(source_root, ''), coalesce(source_bytes, 0),
       coalesce(handler, ''), coalesce(log_path, ''),
       status, coalesce(error, ''), created_at
from deployments where app_id = $1 order by created_at desc limit $2 offset $3;

-- name: ListLatestDeploymentPerApp :many
select distinct on (d.app_id) d.*
from deployments d
join apps a on a.id = d.app_id
where a.account_id = $1 and a.status <> 'deleted' and d.deleted_at IS NULL
order by d.app_id, d.created_at desc, d.id desc;

-- name: LatestSupersededDeployment :one
select id, app_id, coalesce(build_id::text, ''), image_digest, kind,
       coalesce(source_path, ''), coalesce(source_root, ''), coalesce(source_bytes, 0),
       coalesce(handler, ''), coalesce(log_path, ''),
       status, coalesce(error, ''), created_at
from deployments
where app_id = $1 and status = 'superseded'
order by created_at desc limit 1;

-- name: UpdateDeploymentStatus :exec
update deployments
set status = $2,
    error = $3,
    traffic_percent = case when $2 = 'failed' then 0 else traffic_percent end,
    rollout_state = case when $2 = 'failed' then 'aborted' else rollout_state end,
    rollout_completed_at = case when $2 = 'failed' then null else rollout_completed_at end,
    rollout_aborted_at = case when $2 = 'failed' then coalesce(rollout_aborted_at, now()) else rollout_aborted_at end,
    rollout_aborted_reason = case when $2 = 'failed' then coalesce(nullif($3, ''), 'deployment failed') else rollout_aborted_reason end
where id = $1;

-- name: SetDeploymentFailed :one
-- ADR-021 (G1, image digest enforcement hardening): durable
-- carrier for the RFC 7807 failure code that imaged writes when a
-- deployment transitions to `failed`. pkg/api.SentinelToCode maps
-- the three puller-side sentinels to the codes pkg/api.CodeImage*
-- (image_not_found / image_egress_denied / image_manifest_invalid)
-- and imaged passes the resulting code as $3 here. The free-text
-- error column ($2) is preserved for debugging. Status is pinned
-- to 'failed' (caller's status argument is ignored — this is a
-- failure-specific helper, not a generic update).
--
-- errcode is omitted in the scan (empty string on success means
-- "no code mapped"; null in the column means "not yet stamped" —
-- both render as "" on the Go side via the coalesce in the SELECT).
update deployments
   set status = 'failed', error = $2, error_code = $3,
       traffic_percent = 0, rollout_state = 'aborted',
       rollout_completed_at = null,
       rollout_aborted_at = coalesce(rollout_aborted_at, now()),
       rollout_aborted_reason = coalesce(nullif($2, ''), 'deployment failed')
 where id = $1
returning id, app_id, coalesce(build_id::text, ''), image_digest, kind,
          coalesce(source_path, ''), coalesce(source_root, ''), coalesce(source_bytes, 0),
          coalesce(handler, ''), coalesce(log_path, ''),
          coalesce(rootfs_path, ''), coalesce(rootfs_key, ''), coalesce(rootfs_bytes, 0),
          status, coalesce(error, ''), coalesce(error_code, ''), created_at;

-- name: MarkDeploymentSuperseded :exec
update deployments set status = 'superseded' where id = $1;

-- name: MarkDeploymentLive :exec
update deployments set status = 'live' where id = $1;

-- name: CreateCustomDomain :one
insert into custom_domains (domain, app_id, challenge_token)
values ($1, $2, $3)
returning domain, app_id, challenge_token, verified_at, environment_id;

-- name: DomainByName :one
select domain, app_id, challenge_token, verified_at, environment_id
from custom_domains where domain = $1;

-- name: ListDomainsForApp :many
select domain, app_id, challenge_token, verified_at, environment_id
from custom_domains where app_id = $1 order by domain;

-- name: ListDomainsForAccount :many
select d.domain, d.app_id, d.challenge_token, d.verified_at, d.environment_id
from custom_domains d join apps a on a.id = d.app_id
where a.account_id = $1 order by d.domain;

-- name: MarkDomainVerified :exec
update custom_domains set verified_at = now() where domain = $1;

-- name: DeleteCustomDomain :exec
delete from custom_domains where domain = $1;

-- name: CreateCron :one
insert into crons (id, app_id, schedule, path, enabled, timezone, skip_if_running)
values (gen_random_uuid(), $1, $2, $3, $4, $5, $6)
returning id, app_id, schedule, path, enabled, suspended_reason, timezone, skip_if_running, last_fired_at, created_at;

-- name: UpdateCron :one
update crons set
  schedule = coalesce($2, schedule),
  path = coalesce($3, path),
  enabled = coalesce($4, enabled)
where id = $1
returning id, app_id, schedule, path, enabled, suspended_reason, timezone, skip_if_running, last_fired_at, created_at;

-- name: DeleteCron :exec
delete from crons where id = $1 and app_id = $2;

-- name: ListCronsForApp :many
select id, app_id, schedule, path, enabled, suspended_reason, timezone, skip_if_running, last_fired_at, created_at
from crons where app_id = $1 order by created_at desc;

-- name: ListEnabledCrons :many
select id, app_id, schedule, path, enabled, suspended_reason, timezone, skip_if_running, last_fired_at, created_at
from crons where enabled = true and suspended_reason = '';

-- name: CronByID :one
select id, app_id, schedule, path, enabled, suspended_reason, timezone, skip_if_running, last_fired_at, created_at
from crons where id = $1;

-- name: AppendEvent :exec
insert into events (actor, kind, subject, data)
values ($1, $2, $3, $4);

-- name: ListEvents :many
select id, at, actor, kind, subject, data
from events where subject = $1 order by at desc limit $2;

-- EPIC #1278 / Workstream B — durable internal event subscriptions.
-- A subscription is app-owned but keeps account_id denormalized so scheduler
-- fan-out can enforce tenant isolation without joining apps.

-- name: ListEventSubscriptionsForApp :many
select id, account_id, app_id, source, type, filter, enabled,
       created_at, updated_at
from event_subscriptions
where app_id = $1
order by created_at asc, id asc;

-- name: ListEnabledEventSubscriptionsForAccount :many
select s.id, s.account_id, s.app_id, s.source, s.type, s.filter, s.enabled,
       s.created_at, s.updated_at
from event_subscriptions s
join apps a on a.id = s.app_id
where s.account_id = $1 and s.enabled and a.status <> 'deleted'
order by s.created_at asc, s.id asc;

-- name: ListMatchingEventSubscriptionsForAccount :many
-- Candidate lookup for schedd fan-out. The final JSON filter matcher remains
-- in pkg/events; these predicates only prune source/type patterns and page
-- through the tenant's enabled subscriptions without an unbounded scan.
select s.id, s.account_id, s.app_id, s.source, s.type, s.filter, s.enabled,
       s.created_at, s.updated_at
from event_subscriptions s
join apps a on a.id = s.app_id
where s.account_id = sqlc.arg('account_id')::uuid
  and s.enabled
  and a.status <> 'deleted'
  and case
        when s.source = '*' then true
        when left(s.source, 1) = '*' and right(s.source, 1) = '*' then
          position(substring(s.source, 2, greatest(length(s.source) - 2, 0)) in sqlc.arg('source')::text) > 0
        when left(s.source, 1) = '*' then
          right(sqlc.arg('source')::text, greatest(length(s.source) - 1, 0)) = right(s.source, greatest(length(s.source) - 1, 0))
        when right(s.source, 1) = '*' then
          left(sqlc.arg('source')::text, greatest(length(s.source) - 1, 0)) = left(s.source, greatest(length(s.source) - 1, 0))
        else s.source = sqlc.arg('source')::text
      end
  and case
        when s.type = '*' then true
        when left(s.type, 1) = '*' and right(s.type, 1) = '*' then
          position(substring(s.type, 2, greatest(length(s.type) - 2, 0)) in sqlc.arg('type')::text) > 0
        when left(s.type, 1) = '*' then
          right(sqlc.arg('type')::text, greatest(length(s.type) - 1, 0)) = right(s.type, greatest(length(s.type) - 1, 0))
        when right(s.type, 1) = '*' then
          left(sqlc.arg('type')::text, greatest(length(s.type) - 1, 0)) = left(s.type, greatest(length(s.type) - 1, 0))
        else s.type = sqlc.arg('type')::text
      end
  and (sqlc.arg('cursor_created_at')::timestamptz is null
       or (s.created_at, s.id) > (sqlc.arg('cursor_created_at')::timestamptz,
                                  sqlc.arg('cursor_id')::uuid))
order by s.created_at asc, s.id asc
limit sqlc.arg('limit')::int;

-- name: UpsertEventSubscription :one
-- (xmax = 0) distinguishes a declaration first installed by this deploy from
-- an idempotent replay of the same manifest row.
insert into event_subscriptions (account_id, app_id, source, type, filter)
values ($1, $2, $3, $4, $5::jsonb)
on conflict (app_id, source, type, filter) do update
set enabled = true,
    updated_at = now()
returning id, account_id, app_id, source, type, filter, enabled,
          created_at, updated_at, (xmax = 0) as inserted;

-- name: DeleteEventSubscription :exec
delete from event_subscriptions
where id = $1 and account_id = $2 and app_id = $3;

-- name: ListEventsByWakeID :many
-- issue #517 / PR-C / ADR-064 — wake-timeline read-side query.
-- Filters on the jsonb expression index events_wake_id_idx
-- (migrations/00114_events_wake_id_idx.sql) and orders by at ASC
-- so the customer-facing timeline endpoint surfaces a forward
-- narrative. schedd is canonical for wake.boot_started; vmmd's
-- same-kind row is a corroborating observation. Rank that pair
-- before applying the $2 `since` lower bound and $3 limit so a
-- later mirror cannot appear on a subsequent page. Index path:
-- partial index on
-- (data->>'wake_id') WHERE data->>'wake_id' IS NOT NULL means
-- only rows with a wake_id tag (i.e. the 13 wake.* kinds) are
-- indexed — legacy audit rows are not in scope of PR-C, see
-- ADR-064 §"Compatibility".
with wake_events as (
  select id, at, actor, kind, subject, data,
         row_number() over (
           partition by kind
           order by (actor = 'schedd') desc, at asc, id asc
         ) as boot_rank
  from events
  where data->>'wake_id' = $1
)
select id, at, actor, kind, subject, data
from wake_events
where (kind <> 'wake.boot_started' or boot_rank = 1)
  and at > $2
order by at asc, id asc
limit $3;

-- name: ListAllEventsPaged :many
-- ADR-091 §3.7 / PR #3 — operator-obs backend audit-reading surface.
-- Reads the live events table (NOT audit_log — distinct source of
-- truth per ADR-091 §3.7.4). Optional filters:
--   * $1 actor    — exact match (handler passes "" to skip)
--   * $2 kind_prefix — LIKE 'prefix%' (handler passes "" to skip)
--   * $3 subject  — exact match (handler passes "" to skip)
--   * $4 since    — RFC 3339 timestamptz (handler passes zero time to skip)
--   * $5 limit    — top-N rows (handler default 200, cap 500;
--                   cast to int8 so sqlc emits int64 Params and the
--                   handler's int→int64 widening is safe)
-- Order: at DESC, id DESC — the id tiebreaker keeps the planner on
-- the (kind, at DESC) index added by 00190_admin_obs_index.sql for
-- kind-prefix queries and avoids an unstable sort on the
-- over-read window.
-- Subject is uuid (nullable in the schema); the cast is left to
-- the handler so the handler can pass an empty string for "no
-- subject filter" without a NULL literal.
select id, at, actor, kind, subject, data
from events
where ($1 = '' or actor = $1)
  and ($2 = '' or kind like $2 || '%')
  and ($3 = '' or subject = $3::uuid)
  and ($4 = '0001-01-01 00:00:00+00:00'::timestamptz or at >= $4)
order by at desc, id desc
limit $5::int8;

-- name: ListRecentEventsForAccount :many
-- ADR-091 §3.7 / PR #3 — per-account events drill-down. Backed by
-- the partial index events_actor_account_idx on
-- (actor_account_id) WHERE actor_account_id IS NOT NULL
-- (migrations/00099_orgs_memberships_invitations.sql). Filters:
--   * $1 actor_account_id — uuid (the account the actor belonged to)
--   * $2 since             — RFC 3339 timestamptz (handler passes
--                            zero time to skip; the predicate is
--                            uniform with ListAllEventsPaged)
--   * $3 limit             — top-N rows (handler default 200, cap 500;
--                            cast to int8 so sqlc emits int64 Params
--                            and the handler's int→int64 widening is
--                            safe)
-- Order: at DESC, id DESC — same rationale as ListAllEventsPaged.
-- PR #3 wires the per-account filter on the SSE mirror's
-- per-account projections; the broader ?actor + ?subject filter
-- shape lives on ListAllEventsPaged.
select id, at, actor, kind, subject, data
from events
where actor_account_id = $1
  and ($2 = '0001-01-01 00:00:00+00:00'::timestamptz or at >= $2)
order by at desc, id desc
limit $3::int8;

-- name: AppendUsage :exec
-- Idempotent on (instance_id, minute) for mb_seconds / requests
-- (M7 hardening, PR feat/m7-beta-hardening): a redelivered
-- minute is a no-op for the billing-floor columns so a meterd
-- restart / network blip / two meterd instances cannot inflate
-- billing. cpu_usec, tx_bytes, net_tx_bytes, net_rx_bytes,
-- cold_boot_count, and tail_seconds are ADDITIVE on the same
-- conflict key — the schedd / meterd accumulators can each call
-- AppendUsage many times within the same minute; the columns are
-- the sum of all per-tick deltas.
--   cpu_usec         — issue #279 / PR-B / ADR-039
--   tx_bytes         — ADR-046 (gateway HTTP response body bytes)
--   net_tx_bytes     — ADR-046 (root-side vethHost.rx_bytes delta)
--   net_rx_bytes     — ADR-048 (root-side vethHost.tx_bytes delta; ingress)
--   cold_boot_count  — ADR-048 (WAKE_RESTORE→WAKE_COLD_BOOT transitions)
--   tail_seconds     — issue #667 / ADR-078 (per-minute wall-clock seconds
--                      draining waitUntil tasks; INFORMATIONAL ONLY — pinned
--                      by pkg/meter/pusher_shadow_test.go::TestPushHour_ExcludesTailSeconds)
insert into usage_minutes (account_id, app_id, instance_id, minute, mb_seconds, requests, cpu_usec, tx_bytes, net_tx_bytes, net_rx_bytes, cold_boot_count, tail_seconds)
values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
on conflict (instance_id, minute) do update
   set mb_seconds      = case when usage_minutes.mb_seconds = 0 and EXCLUDED.mb_seconds > 0 then EXCLUDED.mb_seconds else usage_minutes.mb_seconds end,
       cpu_usec        = usage_minutes.cpu_usec        + EXCLUDED.cpu_usec,
       tx_bytes        = usage_minutes.tx_bytes        + EXCLUDED.tx_bytes,
       net_tx_bytes    = usage_minutes.net_tx_bytes    + EXCLUDED.net_tx_bytes,
       net_rx_bytes    = usage_minutes.net_rx_bytes    + EXCLUDED.net_rx_bytes,
       cold_boot_count = usage_minutes.cold_boot_count + EXCLUDED.cold_boot_count,
       tail_seconds    = usage_minutes.tail_seconds    + EXCLUDED.tail_seconds;

-- name: RegisterGatewayUsageEvent :one
with inserted as (
  insert into meter_gateway_usage_events (node_id, event_id, instance_id, minute)
  values ($1, $2, $3, $4)
  on conflict (node_id, event_id) do nothing
  returning 1
)
select exists(select 1 from inserted) as inserted;

-- name: ApplyGatewayUsageEvent :execrows
insert into usage_minutes (account_id, app_id, instance_id, minute, mb_seconds, requests, cpu_usec, tx_bytes, net_tx_bytes, net_rx_bytes, cold_boot_count, tail_seconds)
select a.account_id, i.app_id, i.id, $2::timestamptz, 0, $3::int, 0, $4::bigint, 0, 0, $5::int, 0
  from instances i
  join apps a on a.id = i.app_id
 where i.id = $1
on conflict (instance_id, minute) do update
   set requests        = usage_minutes.requests        + EXCLUDED.requests,
       tx_bytes        = usage_minutes.tx_bytes        + EXCLUDED.tx_bytes,
       cold_boot_count = usage_minutes.cold_boot_count + EXCLUDED.cold_boot_count;

-- name: UsageByMonth :many
select account_id, app_id, month, mb_seconds, cpu_usec, requests, tx_bytes, net_tx_bytes
from usage_monthly
where account_id = $1 and month = $2
order by app_id, month;

-- name: CreateInstance :one
insert into instances (id, app_id, deployment_id, state, ram_mb)
values (gen_random_uuid(), $1, $2, $3, $4)
returning id, app_id, deployment_id, state, coalesce(netns, ''), coalesce(guest_uid, 0),
          coalesce(host_ip::text, ''), ram_mb, started_at, last_request_at, parked_at;

-- name: InstanceByID :one
select id, app_id, deployment_id, state, coalesce(netns, ''), coalesce(guest_uid, 0),
       coalesce(host_ip::text, ''), ram_mb, started_at, last_request_at, parked_at
from instances where id = $1;

-- name: ListInstancesForApp :many
select id, app_id, deployment_id, state, coalesce(netns, ''), coalesce(guest_uid, 0),
       coalesce(host_ip::text, ''), ram_mb, started_at, last_request_at, parked_at
from instances where app_id = $1 order by started_at desc;

-- name: ListFirstSuccessfulRequestsForAccountsCreatedSince :many
-- Operator beta funnel: one bounded aggregate read replaces an N+1
-- ListInstancesForAccount loop. last_request_at is stamped only after a
-- successful public request and terminal instances remain for 30 days, which
-- fully covers the 14-day beta cohort window.
select a.account_id, min(i.last_request_at)::timestamptz as first_success_at
from instances i
join apps a on a.id = i.app_id
join accounts acct on acct.id = a.account_id
where acct.created_at >= $1
  and i.last_request_at is not null
group by a.account_id
order by a.account_id;

-- name: UpdateInstanceState :exec
update instances set state = $2 where id = $1;

-- name: BumpInstanceTailCount :one
-- issue #667 / ADR-078 — atomically apply delta to the instance's
-- `tail_count` column and return the post-update value. The
-- GREATEST(…, 0) floor mirrors DecrementInstanceTailCount's safety
-- property: a stale receipt from a guest that just parked cannot
-- underflow the counter, and the 5s watchdog in snapshotAndPark
-- force-parks regardless. RETURNING tail_count lets the caller
-- (vmmd's MarkInstanceTailTerminal) learn the new value without a
-- follow-up SELECT. Returns ErrNotFound when the instance row is
-- missing (pgx.ErrNoRows maps to state.ErrNotFound in pgstore).
update instances
   set tail_count = GREATEST(tail_count + $2, 0)
 where id = $1
returning tail_count;

-- name: DecrementInstanceTailCount :exec
-- issue #667 / ADR-078 — canonical "tail task reached terminal" path.
-- Equivalent to BumpInstanceTailCount(ctx, id, -n) but kept as a
-- separate method because every decrement site is a terminal event
-- receipt, and the explicit name makes the call sites self-
-- documenting. n is the number of tail tasks to decrement by (1 for
-- the steady-state path, the full unfinished-tail count for the
-- snapshotAndPark watchdog). The GREATEST(…, 0) floor is a
-- defence-in-depth guard against races where a receipt lands after
-- the runner exited cleanly: the counter floors at 0 rather than
-- underflowing, which would permanently stall the schedd reaper's
-- tail_count > 0 early-out. Returns ErrNotFound when the instance
-- row is missing.
update instances
   set tail_count = GREATEST(tail_count - $2, 0)
 where id = $1;

-- name: GetInstanceTailCount :one
-- issue #667 / ADR-078 — read-only probe for the snapshotAndPark
-- 5s watchdog's poll loop. Single SELECT … FROM instances WHERE
-- id = $1; the column is on the hot path so the row is already in
-- shared_buffers under normal load. Returns ErrNotFound when the
-- instance row is missing.
select tail_count from instances where id = $1;

-- name: CreateBuild :one
insert into builds (id, deployment_id, kind, source_bytes, status, log_path)
values (gen_random_uuid(), $1, $2, $3, 'queued', $4)
returning id, deployment_id, kind, source_bytes, status, failure_class, log_path, started_at, finished_at, enqueued_at, cache_status, cache_key_sha256;

-- name: BuildByID :one
select id, deployment_id, kind, source_bytes, status, failure_class, log_path, started_at, finished_at, enqueued_at, cache_status, cache_key_sha256
from builds where id = $1;

-- name: BuildByDeployment :one
select id, deployment_id, kind, source_bytes, status, failure_class, log_path, started_at, finished_at, enqueued_at, cache_status, cache_key_sha256
from builds where deployment_id = $1 order by started_at desc nulls last limit 1;

-- name: UpdateBuildStatus :exec
update builds set
  status = $2,
  failure_class = $3,
  started_at = case when $4::boolean then now() else started_at end,
  finished_at = case when $5::boolean then now() else finished_at end
where id = $1;

-- name: CreateSession :one
-- IAM-3 (ADR-039, issue #187 + #244 merged). One row per dashboard login.
-- Caller has already generated the uuid (the envelope seal needs the same
-- value). issued_ip is an inet ('' cast to NULL means "RemoteAddr
-- unparseable" — surfaced as "" on read by coalesce(host(...))).
insert into sessions (id, account_id, issued_ip, issued_ua)
values ($1, $2, nullif($3, '')::inet, nullif($4, ''))
returning id, account_id,
          coalesce(host(issued_ip), '') as issued_ip,
          coalesce(issued_ua, '') as issued_ua,
          issued_at, last_seen_at, revoked_at;

-- name: GetSession :one
-- Primary-key lookup; called on every authenticated dashboard request.
-- sql.ErrNoRows from pgx maps to state.ErrNotFound in pgstore.
select id, account_id,
       coalesce(host(issued_ip), '') as issued_ip,
       coalesce(issued_ua, '') as issued_ua,
       issued_at, last_seen_at, revoked_at
from sessions where id = $1;

-- name: RevokeSession :one
-- Account-scoped atomic stamp. WHERE includes account_id so a
-- cross-account DELETE returns 0 rows (handler maps false → 404) —
-- IDOR is a persistence invariant, not a handler check.
-- coalesce(revoked_at, now()) makes the call idempotent on already
-- revoked rows (returns 0 rows).
update sessions set revoked_at = coalesce(revoked_at, now())
where id = $1 and account_id = $2 and revoked_at is null
returning id;

-- name: ListSessions :many
-- Active rows only, newest first. Partial index keeps the scan tight.
select id, account_id,
       coalesce(host(issued_ip), '') as issued_ip,
       coalesce(issued_ua, '') as issued_ua,
       issued_at, last_seen_at, revoked_at
from sessions where account_id = $1 and revoked_at is null
order by issued_at desc;

-- name: RevokeAllSessions :many
-- Revokes every active row for accountID except the supplied sid
-- (the calling session). Returns the revoked ids for audit.
update sessions set revoked_at = now()
where account_id = $1 and id <> $2 and revoked_at is null
returning id;

-- name: TouchSessionLastSeen :exec
-- Best-effort, fire-and-forget. Allowed on revoked rows (observability
-- signal only; not authorization). pgx interface returns nothing.
update sessions set last_seen_at = now() where id = $1;

-- name: InsertComputeNodeHeartbeat :exec
-- CP-1 (operator observability): append one row to the heartbeat
-- history. The schedd Heartbeat.Tick goroutine is the only writer.
-- We deliberately do NOT use ON CONFLICT DO NOTHING — a duplicate
-- (node_id, received_at) is observable as a SQLSTATE 23505 unique-
-- violation, which the writer logs as a warning. A silently-deduped
-- stamp would mask a future bug where the scheduler tick fires twice.
-- received_at and last_heartbeat_at are passed by the caller; the
-- column default now() is intentionally NOT used so the writer
-- controls the wall-clock pair (the property test depends on
-- caller-supplied timestamps for deterministic gap classification).
insert into compute_node_heartbeats (node_id, received_at, last_heartbeat_at, source)
values ($1, $2, $3, $4);

-- name: ListComputeNodeHeartbeats :many
-- CP-1: read heartbeat history for one node, newest first. The
-- $2 parameter is nullable: passing pgtype.Timestamptz{} (the Go
-- zero value, mapped to SQL NULL by sqlc) means "no lower bound,
-- return most-recent N"; passing a populated timestamptz means
-- "history since t". The composite index
-- compute_node_heartbeats_node_at_idx (node_id, received_at desc)
-- matches this read shape.
--
-- The endpoint passes a hard-cap limit (default 200, max 2000). The
-- composite index is enough for the routine 30s × 60 nodes × 24h
-- steady-state workload; a 7-day retention sweep is a follow-on.
select id, node_id, received_at, last_heartbeat_at, source
from compute_node_heartbeats
where node_id = $1
  and ($2::timestamptz is null or received_at >= $2)
order by received_at desc
limit $3;

-- --- Organizations (ADR-061, IAM-6, PR 2) -------------------------------
--
-- PR 2's sqlc queries cover the deterministic reads + simple writes. The
-- tx-heavy methods (CreateOrg with initial owner membership; RemoveOrgMember
-- with last-owner FOR UPDATE; ConsumeOrgInvitation with cap check + email
-- equality + membership insert + invitation UPDATE in one tx) stay as
-- inline SQL in pgstore.go — they don't fit the :one / :many / :exec
-- sqlc surface cleanly and the existing precedent (CreateAppIfUnderQuota,
-- ConsumeRecoveryCode, ApplyProjectPlan) renders those inline.

-- name: CreateOrg :one
insert into orgs (
    slug,
    name,
    personal_org,
    personal_owner_account_id,
    plan,
    status,
    provider_customer_id,
    stripe_subscription_item,
    deleted_pending
) values (
    $1, $2, $3, $4, $5, $6, nullif($7, ''), nullif($8, ''), false
)
returning
    id, slug, name, personal_org,
    coalesce(personal_owner_account_id::text, ''),
    plan, status,
    coalesce(provider_customer_id, ''),
    coalesce(stripe_subscription_item, ''),
    deleted_pending,
    created_at, updated_at;

-- name: OrgByID :one
select
    id, slug, name, personal_org,
    coalesce(personal_owner_account_id::text, ''),
    plan, status,
    coalesce(provider_customer_id, ''),
    coalesce(stripe_subscription_item, ''),
    deleted_pending,
    created_at, updated_at
from orgs
where id = $1;

-- name: OrgBySlug :one
select
    id, slug, name, personal_org,
    coalesce(personal_owner_account_id::text, ''),
    plan, status,
    coalesce(provider_customer_id, ''),
    coalesce(stripe_subscription_item, ''),
    deleted_pending,
    created_at, updated_at
from orgs
where lower(slug) = lower($1);

-- name: OrgByPersonalAccount :one
select
    id, slug, name, personal_org,
    coalesce(personal_owner_account_id::text, ''),
    plan, status,
    coalesce(provider_customer_id, ''),
    coalesce(stripe_subscription_item, ''),
    deleted_pending,
    created_at, updated_at
from orgs
where personal_org = true
  and personal_owner_account_id = $1;

-- name: ListOrgsForAccount :many
select
    o.id, o.slug, o.name, o.personal_org,
    coalesce(o.personal_owner_account_id::text, ''),
    o.plan, o.status,
    coalesce(o.provider_customer_id, ''),
    coalesce(o.stripe_subscription_item, ''),
    o.deleted_pending,
    o.created_at, o.updated_at
from orgs o
join org_memberships m on m.org_id = o.id
where m.account_id = $1
  and m.removed_at is null
order by o.slug;

-- name: UpdateOrgPlan :exec
update orgs set plan = $2, updated_at = now() where id = $1;

-- name: UpdateOrgStatus :exec
update orgs set status = $2, updated_at = now() where id = $1;

-- name: SoftDeleteOrg :exec
update orgs set deleted_pending = true, status = 'deleted_pending', updated_at = now() where id = $1;

-- name: ListOrgMembers :many
select
    org_id, account_id, role,
    coalesce(invited_by_account_id::text, ''),
    joined_at, removed_at
from org_memberships
where org_id = $1
order by joined_at;

-- name: OrgMemberByAccount :one
select
    org_id, account_id, role,
    coalesce(invited_by_account_id::text, ''),
    joined_at, removed_at
from org_memberships
where org_id = $1 and account_id = $2;

-- name: OrgInvitationByTokenHash :one
select
    id,
    org_id,
    email::text as email,
    role,
    token_hash,
    coalesce(invited_by_account_id::text, ''),
    expires_at,
    consumed_at,
    revoked_at,
    coalesce(accepting_account_id::text, ''),
    created_at
from org_invitations
where token_hash = $1;

-- name: ListOrgInvitationsForOrg :many
select
    id,
    org_id,
    email::text as email,
    role,
    token_hash,
    coalesce(invited_by_account_id::text, ''),
    expires_at,
    consumed_at,
    revoked_at,
    coalesce(accepting_account_id::text, ''),
    created_at
from org_invitations
where org_id = $1
order by created_at desc;

-- name: ExpireOrgInvitations :execrows
update org_invitations
set revoked_at = now()
where consumed_at is null
  and revoked_at is null
  and expires_at <= $1;

-- name: TrafficAnomalyAggregate :many
-- ADR-091 §3.6 — operator observability backend (PR #2).
-- Hour-of-day baseline over a rolling 7-day window:
--   * baseline is per (account_id, app_id, EXTRACT(HOUR FROM minute))
--   * an anomaly is a row whose current mb_seconds exceeds
--     baseline_mean + 3.0*baseline_stddev (or baseline_mean * 5.0 when
--     baseline_stddev < 1.0 — guards against noisy-low-traffic apps
--     where a tiny stddev explodes the Z-score).
--   * $1 since       — RFC 3339 lower bound for "current" rows
--                      (handler default: now() - 24h, hard cap 168h)
--   * $2 baseline    — RFC 3339 lower bound for the baseline pool
--                      (handler default: now() - 7d, fixed by ADR)
--   * $3 limit       — top-N by deviation (handler default 50, cap 200;
--                      cast to int8 so sqlc emits int64 Params and the
--                      handler's int→int64 widening is safe)
-- Result columns:
--   * account_id, app_id, minute, current_mb_seconds
--   * baseline_mean, baseline_stddev, baseline_samples
--   * z_score, reason ('hour_of_day' | 'raw_z')
-- Index path: usage_minutes primary key (instance_id, minute) is
-- fine for current-minute scans in a 24h window. The 7-day baseline
-- pool scans the same primary key. For the fleet-wide aggregate a
-- future ADR adds (account_id, app_id, minute) as a covering index;
-- PR #2 does NOT add it (single-box posture; multi-host moves to
-- PromQL per ADR-091 §3.6).
with baseline as (
    select account_id,
           app_id,
           extract(hour from usage_minutes.minute) as hour_of_day,
           avg(mb_seconds)::float8 as mean_mb_seconds,
           coalesce(stddev_pop(mb_seconds), 0)::float8 as stddev_mb_seconds,
           count(*)::int as sample_count
    from usage_minutes
    where usage_minutes.minute >= $2
      and usage_minutes.minute <  $1
      and mb_seconds > 0
    group by account_id, app_id, extract(hour from usage_minutes.minute)
),
current_pool as (
    select account_id,
           app_id,
           minute,
           sum(mb_seconds)::float8 as current_mb_seconds
    from usage_minutes
    where minute >= $1
      and mb_seconds > 0
    group by account_id, app_id, minute
),
scored as (
    select c.account_id,
           c.app_id,
           c.minute,
           c.current_mb_seconds,
           b.mean_mb_seconds,
           b.stddev_mb_seconds,
           b.sample_count,
           case
               when b.sample_count < 3 then null
               when b.stddev_mb_seconds < 1.0 and c.current_mb_seconds >= 5.0 * b.mean_mb_seconds
                   and b.mean_mb_seconds > 0 then (c.current_mb_seconds - b.mean_mb_seconds) / 5.0
               when b.stddev_mb_seconds >= 1.0
                   and c.current_mb_seconds >= b.mean_mb_seconds + 3.0 * b.stddev_mb_seconds then
                   (c.current_mb_seconds - b.mean_mb_seconds) / b.stddev_mb_seconds
               else null
           end as z_score,
           case
               when b.stddev_mb_seconds < 1.0 then 'raw_z'
               else 'hour_of_day'
           end as reason
    from current_pool c
    join baseline b
      on c.account_id = b.account_id
     and c.app_id = b.app_id
     and extract(hour from c.minute) = b.hour_of_day
)
select account_id,
       app_id,
       minute,
       current_mb_seconds,
       mean_mb_seconds,
       stddev_mb_seconds,
       sample_count,
       z_score,
       reason
from scored
where z_score is not null
order by z_score desc
limit $3::int8;

-- name: PerAccountRateLimitAggregate :many
-- ADR-091 §3.5 — operator observability backend (PR #2) durable view.
-- Aggregates `events` rows of kind='auth.rate_limited' over a rolling
-- window, grouped by subject (account_id, NULL for anonymous actors).
--   * $1 since  — RFC 3339 lower bound (handler default: now() - 24h,
--                 hard cap 168h per pkg/api/limits.go::ObsAdminWindowMaxHours)
--   * $2 limit  — top-N by hits (handler default 100, cap 500;
--                 cast to int8 so sqlc emits int64 Params and the
--                 handler's int→int64 widening is safe)
-- Anonymous (subject IS NULL) rows are bucketed under a single
-- account_id = NULL row so the operator UI can render the "anon
-- credential stuffing" signal distinctly from named-account bursts.
-- Index path: events_kind_at_idx (added in migration 00190) covers
-- the kind + at DESC predicate. The subject grouping is in-memory
-- after the index scan.
select coalesce(subject, '00000000-0000-0000-0000-000000000000'::uuid) as account_id,
       count(*)::int as hits,
       max(at) as last_event_at
from events
where kind = 'auth.rate_limited'
  and at >= $1
group by coalesce(subject, '00000000-0000-0000-0000-000000000000'::uuid)
order by hits desc, last_event_at desc
limit $2::int8;


-- name: TrafficAnomalyAggregateByNode :many
-- PR #4 (ADR-092 §3.4 amendment) — per-node variant of
-- TrafficAnomalyAggregate. Joins usage_minutes to instances to
-- recover the hosting node_id, then groups by
-- (account_id, app_id, node_id, EXTRACT(HOUR FROM minute)) for
-- the baseline. The current_pool also groups by node_id so the
-- "today" anomaly is per-node, not per-app-wide.
--
-- Why a separate query and not a sqlc parameter on the existing
-- one: the baseline math is identical, but the GROUP BY keys
-- differ by one column, and trying to thread that through a
-- nullable WHERE filter would either lose the per-node grain
-- (NULL filter collapses the group) or return the wrong rollup
-- (a per-node "current" against an app-wide baseline reports
-- spurious anomalies when the fleet is unevenly loaded). A
-- separate query keeps each path simple and self-contained.
--
-- Index path: same as TrafficAnomalyAggregate — usage_minutes
-- primary key (instance_id, minute) is fine for the 24h current
-- window in single-box posture. The instances.node_id lookup
-- is by PK; the join is O(matches) on the PK.
with baseline as (
    select um.account_id,
           um.app_id,
           n.id as node_id,
           extract(hour from um.minute) as hour_of_day,
           avg(um.mb_seconds)::float8 as mean_mb_seconds,
           coalesce(stddev_pop(um.mb_seconds), 0)::float8 as stddev_mb_seconds,
           count(*)::int as sample_count
    from usage_minutes um
    join instances i on i.id = um.instance_id
    join compute_nodes n on n.id = i.node_id
    where um.minute >= $2
      and um.minute <  $1
      and um.mb_seconds > 0
    group by um.account_id, um.app_id, n.id, extract(hour from um.minute)
),
current_pool as (
    select um.account_id,
           um.app_id,
           n.id as node_id,
           um.minute,
           sum(um.mb_seconds)::float8 as current_mb_seconds
    from usage_minutes um
    join instances i on i.id = um.instance_id
    join compute_nodes n on n.id = i.node_id
    where um.minute >= $1
      and um.mb_seconds > 0
    group by um.account_id, um.app_id, n.id, um.minute
),
scored as (
    select c.account_id,
           c.app_id,
           c.node_id,
           c.minute,
           c.current_mb_seconds,
           b.mean_mb_seconds,
           b.stddev_mb_seconds,
           b.sample_count,
           case
               when b.sample_count < 3 then null
               when b.stddev_mb_seconds < 1.0 and c.current_mb_seconds >= 5.0 * b.mean_mb_seconds
                   and b.mean_mb_seconds > 0 then (c.current_mb_seconds - b.mean_mb_seconds) / 5.0
               when b.stddev_mb_seconds >= 1.0
                   and c.current_mb_seconds >= b.mean_mb_seconds + 3.0 * b.stddev_mb_seconds then
                   (c.current_mb_seconds - b.mean_mb_seconds) / b.stddev_mb_seconds
               else null
           end as z_score,
           case
               when b.stddev_mb_seconds < 1.0 then 'raw_z'
               else 'hour_of_day'
           end as reason
    from current_pool c
    join baseline b
      on c.account_id = b.account_id
     and c.app_id    = b.app_id
     and c.node_id   = b.node_id
     and extract(hour from c.minute) = b.hour_of_day
)
select account_id,
       app_id,
       node_id,
       minute,
       current_mb_seconds,
       mean_mb_seconds,
       stddev_mb_seconds,
       sample_count,
       z_score,
       reason
from scored
where z_score is not null
order by z_score desc
limit $3::int8;

-- ---------------------------------------------------------------------------
-- PR-D / ADR-012 §7 amendment — per-tenant GitHub App webhook secret.
--
-- The two queries below are exposed by pkg/state/pgstore.go as
-- (s *PgStore).UpsertGithubWebhookSecret and
-- (s *PgStore).GetGithubWebhookSecret. The body is hand-curated
-- rather than sqlc-generated because the github_installations pair
-- is also hand-curated (same precedent). The schema lives in
-- migrations/00212_github_webhook_secrets.sql (renumbered from
-- 00208 → 00209 → 00212 in the slot-collision cluster; see the
-- migration's header for the cross-pr-slot-fence chain).
-- ---------------------------------------------------------------------------

-- name: UpsertGithubWebhookSecret :execrows
-- Installs or rotates the per-tenant webhook secret for an
-- installation_id. ON CONFLICT (installation_id) DO UPDATE so a
-- rotation is one statement. upgradedAt + upgradedBy form a §11
-- audit trail.
INSERT INTO github_webhook_secrets (installation_id, secret_value, upgraded_by)
VALUES ($1, $2, $3)
ON CONFLICT (installation_id) DO UPDATE
SET secret_value = EXCLUDED.secret_value,
    upgraded_at  = now(),
    upgraded_by  = EXCLUDED.upgraded_by;

-- name: GetGithubWebhookSecret :one
-- Returns the bytea secret for the given installation_id. The
-- daemon-side resolver treats pgx.ErrNoRows as fail-closed (the
-- webhook is rejected rather than falling back to the platform-
-- wide FAAS_GITHUB_WEBHOOK_SECRET).
SELECT secret_value FROM github_webhook_secrets WHERE installation_id = $1;

-- ---------------------------------------------------------------------------
-- ADR-096 customer-facing automatic error grouping.
-- Tables live in migrations/00222_app_errors.sql. gatewayd-internal
-- writes via the apid gRPC IncrementAppError handler (pkg/apidgrpc/
-- apperrors.proto); apid is the only direct writer to the table
-- per the owner rules. The apid handlers in
-- cmd/apid/handlers_app_errors.go (PR-B) and the nightly purge
-- cron in cmd/apid/app_errors_purge.go (PR-A) read here.
--
-- Index paths pinned in the migration file (NOT regenerated here
-- — sqlc doesn't manage indexes, only the typed query surface).
-- ---------------------------------------------------------------------------

-- name: IncrementAppError :one
-- ADR-096 §3.5 dedupe-merge INSERT. The grpc_server_apperrors.go
-- handler runs this inside a single pgx transaction per stream
-- batch. ON CONFLICT target is app_errors_dedupe_uniq (the
-- migration's UNIQUE on (account_id, app_id, fingerprint)).
-- The dedupe window is enforced by the writer's LRU; this
-- unique constraint is the last-resort tripwire.
--
-- Returns (inserted bool) via the canonical Postgres
-- (xmax = 0) trick: xmax is 0 on a fresh INSERT and non-zero
-- on an UPDATE. This lets the handler distinguish
-- outcomeInserted vs outcomeMerged on the wire — the gateway
-- uses that signal to update its in-process LRU freshness.
INSERT INTO app_errors (
    id, account_id, app_id, deployment_id, fingerprint,
    route, http_status, error_class, sample_message,
    count, request_count, first_seen_at, last_seen_at,
    last_instance_id, last_node_id, last_region, last_commit_sha,
    last_deployment_tag, last_deployment_created_at, last_image_digest
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    1, 1, $10, $10,
    $11, $12, $13, $14, $15, $16, $17
)
ON CONFLICT (account_id, app_id, fingerprint) DO UPDATE SET
    count         = app_errors.count + 1,
    request_count = app_errors.request_count + 1,
    last_seen_at  = greatest(app_errors.last_seen_at, $10),
    last_instance_id = COALESCE(NULLIF(EXCLUDED.last_instance_id, ''), app_errors.last_instance_id),
    last_node_id = COALESCE(NULLIF(EXCLUDED.last_node_id, ''), app_errors.last_node_id),
    last_region = COALESCE(NULLIF(EXCLUDED.last_region, ''), app_errors.last_region),
    last_commit_sha = COALESCE(NULLIF(EXCLUDED.last_commit_sha, ''), app_errors.last_commit_sha),
    last_deployment_tag = COALESCE(NULLIF(EXCLUDED.last_deployment_tag, ''), app_errors.last_deployment_tag),
    last_deployment_created_at = COALESCE(NULLIF(EXCLUDED.last_deployment_created_at, ''), app_errors.last_deployment_created_at),
    last_image_digest = COALESCE(NULLIF(EXCLUDED.last_image_digest, ''), app_errors.last_image_digest)
RETURNING (xmax = 0) AS inserted;

-- name: InsertAppErrorRequest :exec
-- One row per request that hit the grouped fingerprint. No
-- ON CONFLICT — every request gets its own row. request_count
-- on app_errors is bumped on the paired IncrementAppError
-- call; the read path derives the joined total at query time.
INSERT INTO app_error_requests (
    id, account_id, app_id, fingerprint, request_id, received_at,
    route, http_status, error_class, sample_message,
    deployment_id, headers_sample, redactions, instance_id, node_id,
    region, commit_sha, deployment_tag, deployment_created_at, image_digest
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17, $18, $19, $20
);

-- name: ListAppErrorGroups :many
-- ADR-096 §4.3 summary endpoint. Top-N grouped fingerprints for
-- one (account_id, app_id) over a (since, until) window.
-- Cursor pagination via the (count, last_seen_at, fingerprint)
-- compound tuple (distinct from the operator's (created_at, id)
-- cursor). All three columns are part of the ORDER BY so the
-- cursor predicate must include all three — dropping `count`
-- breaks pagination: rows with smaller count but newer
-- last_seen_at are silently dropped across pages (the cursor
-- predicate on (last_seen_at, fingerprint) only considers the
-- inner order, missing the leading count-DESC boundary).
-- Index path: app_errors_account_app_last_seen_idx covers the
-- primary scan; the (count DESC) sort happens post-filter on the
-- bounded set (limit ≤ AppErrorsSummaryMaxLimit = 100).
--
-- sqlc.arg(name) annotations disambiguate the cursor predicate
-- types — without them sqlc infers the timestamps as timestamptz
-- from the leading (count, last_seen_at) references and breaks
-- pagination.
--
-- cursor_count is a non-nullable bigint, so "no cursor" arrives as 0
-- (count is always >= 1). The predicate used to test IS NULL, which
-- never held: the first page matched no rows and the summary was
-- always empty. fingerprint sorts DESC to agree with the row-value
-- comparison; ASC made pages repeat or skip groups that tie on
-- (count, last_seen_at).
SELECT
    id, fingerprint, error_class, route, http_status,
    count, request_count, first_seen_at, last_seen_at,
    sample_message, last_instance_id, last_node_id, last_region,
    last_commit_sha, last_deployment_tag, last_deployment_created_at,
    last_image_digest
FROM app_errors
WHERE account_id = sqlc.arg('account_id')
  AND app_id     = sqlc.arg('app_id')
  AND last_seen_at >= sqlc.arg('since')
  AND last_seen_at <= sqlc.arg('until')
  AND (sqlc.arg('cursor_count')::bigint = 0
       OR count < sqlc.arg('cursor_count')
       OR (count = sqlc.arg('cursor_count')
           AND (last_seen_at, fingerprint) < (sqlc.arg('cursor_last_seen'), sqlc.arg('cursor_fingerprint')::text)))
ORDER BY count DESC, last_seen_at DESC, fingerprint DESC
LIMIT sqlc.arg('limit');

-- name: ListAppErrorRequests :many
-- Drill-down rows for one fingerprint. Cursor paginated via
-- (received_at, request_id). Index path:
-- app_error_requests_drill_idx. Does NOT include headers_sample
-- or redactions — those are returned only by GetAppErrorSample.
--
-- sqlc.arg(name) annotations disambiguate the cursor predicate
-- types — without them sqlc infers $5 as timestamptz from the
-- leading (received_at) reference, breaking pagination.
SELECT
    id, request_id, received_at, route, http_status,
    error_class, sample_message, deployment_id, instance_id, node_id,
    region, commit_sha, deployment_tag, deployment_created_at, image_digest
FROM app_error_requests
WHERE account_id  = sqlc.arg('account_id')
  AND app_id      = sqlc.arg('app_id')
  AND fingerprint = sqlc.arg('fingerprint')
  AND (sqlc.arg('cursor_received_at')::timestamptz IS NULL
       OR (received_at, request_id) < (sqlc.arg('cursor_received_at'), sqlc.arg('cursor_request_id')::uuid))
ORDER BY received_at DESC, request_id DESC
LIMIT sqlc.arg('limit');

-- name: GetAppErrorSample :one
-- Single oldest request row for one fingerprint, used by the
-- UI's "what does this look like" preview. Returns
-- headers_sample + redactions for the wire-side "we redacted
-- X / Y / Z" badge.
SELECT
    id, request_id, received_at, route, http_status,
    error_class, sample_message, deployment_id,
    headers_sample, redactions, instance_id, node_id, region, commit_sha,
    deployment_tag, deployment_created_at, image_digest
FROM app_error_requests
WHERE account_id  = $1
  AND app_id      = $2
  AND fingerprint = $3
ORDER BY received_at ASC, request_id ASC
LIMIT 1;

-- name: ListAppErrorFingerprintsForPurge :many
-- Nightly retention purge read path (cmd/apid/app_errors_purge.go).
-- Returns IDs of app_errors rows for an account older than
-- `cutoff`. Capped at 10000 per call so the DELETE loop can
-- iterate without blocking. Sorted by last_seen_at ASC so the
-- oldest rows are deleted first (a future eviction policy
-- could swap to "least recent activity" without touching this
-- query).
SELECT id FROM app_errors
WHERE account_id = $1
  AND last_seen_at < $2
ORDER BY last_seen_at ASC
LIMIT $3;

-- name: DeleteAppErrorsByIDs :exec
DELETE FROM app_errors WHERE id = ANY($1::uuid[]);

-- name: DeleteAppErrorRequestsByIDs :exec
DELETE FROM app_error_requests WHERE id = ANY($1::uuid[]);

-- name: DeleteAppErrorRequestsOlderThan :exec
-- The retention purge also runs on app_error_requests
-- independently — a customer's drill-down can age out without
-- the parent fingerprint row being removed (e.g. Hobby with
-- 100 rows/fingerprint cap evicts oldest request rows first).
DELETE FROM app_error_requests
WHERE account_id = $1
  AND received_at < $2;

-- ---------------------------------------------------------------------------
-- ADR-098 connection-aware execution (§9.A). Tables live in
-- migrations/00226_data_upstreams.sql. apid is the only writer to
-- data_upstreams (env-classifier side, PR-B); meterd is the only writer
-- to data_upstream_probes (probe loop, PR-C). schedd reads
-- data_upstream_probes via ListDataUpstreamProbesByHostRegion on wake
-- (PR-B wires pkg/sched/upstream_affinity.go). The Store interface is
-- extended in pkg/state/store.go; pgstore + memstore stubs added in
-- PR-A so the surface compiles — production reads/writes land in PR-B.
--
-- Index paths pinned in the migration file (NOT regenerated here —
-- sqlc doesn't manage indexes, only the typed query surface):
--   - data_upstreams_app_created_idx
--   - data_upstreams_host_redacted_idx
--   - data_upstreams_dedupe_uniq (UNIQUE)
--   - partitioned data_upstream_probes + default partition
-- ---------------------------------------------------------------------------

-- name: InsertDataUpstream :one
-- Dedupe-merge INSERT for data_upstreams. Mirrors the
-- IncrementAppError ON CONFLICT pattern (queries.sql:906).
-- The handler (PR-B's cmd/apid/extract.go) targets
-- data_upstreams_dedupe_uniq on (app_id, scope,
-- deployment_scope, kind, host, port) per ADR-098 amendment
-- (issue #954 / 00281_data_upstreams_deployment_scope.sql).
-- On conflict: bump last_seen_at; refresh last_rtt_ms /
-- last_probed_at / declared_region / deployment_scope
-- from EXCLUDED so a re-classification re-stamps the
-- deployment overlay on the latest observation. id is
-- caller-supplied (uuidv7) so the row identity is stable
-- across the dedupe-merge.
INSERT INTO data_upstreams (
    id, account_id, app_id, source, scope, deployment_scope, kind, host, port,
    host_redacted_hash, declared_region,
    last_rtt_ms, last_probed_at,
    last_seen_at, created_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9,
    $10, $11,
    $12, $13,
    now(), now()
)
ON CONFLICT (app_id, scope, deployment_scope, kind, host, port) DO UPDATE SET
    source          = EXCLUDED.source,
    declared_region = EXCLUDED.declared_region,
    last_rtt_ms     = EXCLUDED.last_rtt_ms,
    last_probed_at  = EXCLUDED.last_probed_at,
    deployment_scope = EXCLUDED.deployment_scope,
    last_seen_at    = now()
RETURNING id;

-- name: ListDataUpstreamsByApp :many
-- GET /v1/apps/{slug}/upstreams (PR-B). Cursor pagination
-- via (created_at, id) — distinct from app_errors'
-- (last_seen_at, fingerprint) cursor because
-- data_upstreams is a stable list, not a hot recency
-- list. Index path: data_upstreams_app_created_idx.
--
-- Optional ?deployment_scope= server-side filter lands via
-- `cursor_deployment_scope` (issue #954 / ADR-098 amendment).
-- Empty string means "no filter; return all deployments"
-- — the wide-open default. Setting a non-empty value restricts
-- to one deployment. Mirrors the existing ?scope= discipline.
--
-- sqlc.arg(...)::type casts disambiguate the cursor params
-- — without them sqlc named both fields `CreatedAt` (taken
-- from the SELECT list) and the generated Go wrapper bound a
-- timestamptz to the $3 uuid slot, tripping a type error on
-- every cursor page past the first. See the cross-PR slot-
-- fence sqlc.arg-disambiguates-cursor memory; the same
-- pattern pins ListAppErrorGroups.
SELECT
    id, account_id, app_id, source, scope, deployment_scope, kind, host, port,
    host_redacted_hash, coalesce(declared_region, ''),
    last_rtt_ms, last_probed_at, last_seen_at, created_at
FROM data_upstreams
WHERE app_id = sqlc.arg('app_id')::uuid
  AND (sqlc.arg('cursor_deployment_scope')::text IS NULL OR sqlc.arg('cursor_deployment_scope')::text = ''
       OR deployment_scope = sqlc.arg('cursor_deployment_scope')::text)
  AND (sqlc.arg('cursor_created_at')::timestamptz IS NULL
       OR (created_at, id) < (sqlc.arg('cursor_created_at')::timestamptz,
                              sqlc.arg('cursor_id')::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('page_limit')::int;

-- name: GetDataUpstreamByID :one
-- Single-row read for the dashboard's "edit upstream"
-- pane (PR-B). Cursor-safe: no pagination; the handler
-- reads the row directly. Projects the new deployment_scope
-- column (issue #954) so the typed DataUpstream.DeploymentScope
-- in pkg/state/types.go round-trips through sqlc.
SELECT
    id, account_id, app_id, source, scope, deployment_scope, kind, host, port,
    host_redacted_hash, coalesce(declared_region, ''),
    last_rtt_ms, last_probed_at, last_seen_at, created_at
FROM data_upstreams
WHERE id = $1;

-- name: DeleteDataUpstreamByID :exec
-- DELETE /v1/apps/{slug}/upstreams/{id} (PR-B). Soft-
-- delete is rejected by ADR-098 (a soft-deleted row
-- would still trigger pg_notify and confuse schedd);
-- the handler is the only path and uses a hard
-- DELETE. The CASCADE on account_id / app_id handles
-- the GDPR path (delete-account cascades through
-- apps → data_upstreams).
DELETE FROM data_upstreams WHERE id = $1;

-- name: InsertDataUpstreamProbe :exec
-- meterd's probe loop writer (PR-C). One row per
-- (host_redacted_hash, region) per 30s sample. The
-- PK is (id, sampled_at) — id is a caller-supplied
-- uuidv7 so dedupe on retry is trivial. Partitioning
-- on sampled_at gives the meterd loop a hot-write
-- path; the partition creator (PR-C) drops old
-- partitions wholesale.
INSERT INTO data_upstream_probes (
    id, host_redacted_hash, region, kind, sampled_at, rtt_ms,
    ok, error_class, probe_node
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9
);

-- name: ListDataUpstreamProbesByHostRegion :many
-- schedd's wake-side read path (PR-B/C). Returns the
-- N most recent probe samples for one (host, region)
-- pair, time-windowed to the meterd sliding window
-- (default: 30s × 5min). Index path: the
-- partitioned table's PARTITION BY RANGE on
-- sampled_at — partition pruning drops everything
-- outside the window. The (kind) projection lets
-- schedd key its upstream-affinity map on
-- (kind, region) without joining data_upstreams.
SELECT
    id, host_redacted_hash, region, kind, sampled_at, rtt_ms,
    ok, error_class, coalesce(probe_node, '')
FROM data_upstream_probes
WHERE host_redacted_hash = $1
  AND region = $2
  AND sampled_at >= $3
ORDER BY sampled_at DESC
LIMIT $4;

-- name: PruneDataUpstreamProbesOlderThan :exec
-- Retention purge. The meterd cron calls this
-- hourly with `cutoff = now() - interval '30 days'`
-- (matches the §12 prom_retention_days:15 floor +
-- a 2× safety margin). The partition pruning on
-- sampled_at makes this O(affected partitions),
-- not O(table size). The PR-C partition creator
-- DROPs whole partitions for ranges entirely older
-- than cutoff; this query handles the partial-
-- partition tail (rows in the default partition or
-- the current month that are older than cutoff).
DELETE FROM data_upstream_probes WHERE sampled_at < $1;

-- Issue #757 / ADR-0NN — Trigger primitive (event-source mappings).
-- Mirrors the cron `CreateCron` / `UpdateCron` / `DeleteCron` /
-- `CronByID` / `ListCronsForApp` shape so the apid handler can stay
-- symmetric with the existing cron surface. The dispatch tick
-- (pkg/sched/dispatch_triggers.go, commit #14) writes
-- ClaimTriggerRecords / MarkTriggerRecordSucceeded /
-- MarkTriggerRecordRetry / MarkTriggerRecordDeadLetter, which the
-- schedd uses under FOR UPDATE SKIP LOCKED to drain batches
-- concurrently.
--
-- The FOR UPDATE SKIP LOCKED on ClaimTriggerRecords mirrors the
-- precedent set by ADR-099 PR-C's claim_job_tasks query (issue
-- tracker 'job-task pull'): concurrent schedd replicas each claim
-- disjoint row sets with no advisory-lock plumbing.

-- name: CreateTrigger :one
insert into triggers (account_id, app_id, kind, slug, enabled, config,
                       batch_size_max, batch_window_ms, max_attempts,
                       cron_id, source, payload_max_bytes,
                       broker_poison_strategy)
values ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, $10, $11, $12, $13)
returning id, account_id, app_id, kind, slug, enabled, config,
          batch_size_max, batch_window_ms, max_attempts,
          cron_id, source, payload_max_bytes, broker_poison_strategy,
          created_at, updated_at;

-- name: UpdateTrigger :one
-- Review finding MED-1 (PR #993): the inline SQL at
-- pkg/state/pgstore.go::UpdateTrigger is the source of truth
-- (sqlc-generated UpdateTrigger stub is bypassed because sqlc
-- doesn't model nullable UPDATE parameters); the projection
-- shape is preserved by mirroring the same column list that
-- ListEnabledTriggers uses (filter_criteria is part of the
-- Trigger struct since commit 6 of issue #757 mega-PR).
update triggers set
  enabled = coalesce($2, enabled),
  config = coalesce($3::jsonb, config),
  batch_size_max = coalesce($4, batch_size_max),
  batch_window_ms = coalesce($5, batch_window_ms),
  max_attempts = coalesce($6, max_attempts),
  payload_max_bytes = coalesce($7, payload_max_bytes),
  broker_poison_strategy = coalesce($8, broker_poison_strategy),
  filter_criteria = coalesce($9::jsonb, filter_criteria)
where id = $1
returning id, account_id, app_id, kind, slug, enabled, config,
          batch_size_max, batch_window_ms, max_attempts,
          cron_id, source, payload_max_bytes, broker_poison_strategy,
          filter_criteria,
          created_at, updated_at;

-- name: DeleteTrigger :exec
delete from triggers where id = $1 and app_id = $2;

-- name: TriggerByID :one
-- ADR-118 / commit 6 of the issue #757 mega-PR: filter_criteria
-- is projected so pgstore.TriggerByID returns the same shape as
-- ListEnabledTriggers (sqlc generates identical column sets as
-- the same Go struct; projections that omit a column produce a
-- distinct Row type that breaks the existing pgstore return
-- type).
select id, account_id, app_id, kind, slug, enabled, config,
       batch_size_max, batch_window_ms, max_attempts,
       cron_id, source, payload_max_bytes, broker_poison_strategy,
       filter_criteria,
       created_at, updated_at
from triggers where id = $1;

-- name: ListTriggersForApp :many
-- Same rationale as TriggerByID — full Trigger projection so
-- sqlc's generated Row type matches the existing pgstore return
-- type. (commit 6 of the issue #757 mega-PR.)
select id, account_id, app_id, kind, slug, enabled, config,
       batch_size_max, batch_window_ms, max_attempts,
       cron_id, source, payload_max_bytes, broker_poison_strategy,
       filter_criteria,
       created_at, updated_at
from triggers where app_id = $1 order by created_at desc;

-- name: ListEnabledTriggers :many
-- Pulled by schedd's runTriggerTick on each 1-second cadence. The
-- query is unfiltered by kind because the dispatch tick reads
-- triggers.enabled = true regardless of kind and dispatches via the
-- per-kind poller (pkg/sched/poller.go).
--
-- ADR-118 / issue #757: filter_criteria is included so the dispatch
-- tick can evaluate per-record predicates without a second round-trip
-- (the column is JSONB; empty/null means "no filter").
select id, account_id, app_id, kind, slug, enabled, config,
       batch_size_max, batch_window_ms, max_attempts,
       cron_id, source, payload_max_bytes, broker_poison_strategy,
       filter_criteria,
       created_at, updated_at
from triggers where enabled = true;

-- name: CountTriggersByApp :one
select count(*) from triggers where app_id = $1;

-- name: CountTriggersByAccount :one
select count(*) from triggers t
join apps a on a.id = t.app_id
where a.account_id = $1 and a.status <> 'deleted';

-- name: ClaimTriggerRecords :many
-- Persist ownership before returning. SKIP LOCKED alone would release the
-- claim at statement end and let another scheduler deliver the same row.
-- A lost dispatcher becomes eligible again after the ten-minute lease.
WITH due AS MATERIALIZED (
    SELECT candidate.id FROM trigger_records candidate
    WHERE candidate.trigger_id = $1
      AND ((candidate.state IN ('pending','retry') AND candidate.next_fire_at <= now())
        OR (candidate.state = 'claimed' AND candidate.claim_expires_at <= now()))
    ORDER BY candidate.next_fire_at, candidate.id
    LIMIT $2
    FOR UPDATE SKIP LOCKED
), claimed AS (
    UPDATE trigger_records r
       SET state = 'claimed',
           claim_generation = r.claim_generation + 1,
           claim_expires_at = now() + INTERVAL '10 minutes'
      FROM due WHERE r.id = due.id
    RETURNING r.id, r.trigger_id, r.item_identifier, r.payload, r.headers,
              r.metadata, r.state, r.attempts, r.next_fire_at, r.received_at,
              r.last_error, r.last_dispatched_at, r.claim_generation,
              r.claim_expires_at
)
SELECT * FROM claimed ORDER BY next_fire_at, id;

-- name: ClaimTriggerRecordsByItems :many
-- The broker batch is authoritative: claiming an unrelated due row would
-- lease it without dispatching it and block its actual broker delivery.
WITH due AS MATERIALIZED (
    SELECT candidate.id FROM trigger_records candidate
    WHERE candidate.trigger_id = $1
      AND candidate.item_identifier = ANY(sqlc.arg(item_identifiers)::text[])
      AND ((candidate.state IN ('pending','retry') AND candidate.next_fire_at <= now())
        OR (candidate.state = 'claimed' AND candidate.claim_expires_at <= now()))
    ORDER BY candidate.next_fire_at, candidate.id
    FOR UPDATE SKIP LOCKED
), claimed AS (
    UPDATE trigger_records r
       SET state = 'claimed',
           claim_generation = r.claim_generation + 1,
           claim_expires_at = now() + INTERVAL '10 minutes'
      FROM due WHERE r.id = due.id
    RETURNING r.id, r.trigger_id, r.item_identifier, r.payload, r.headers,
              r.metadata, r.state, r.attempts, r.next_fire_at, r.received_at,
              r.last_error, r.last_dispatched_at, r.claim_generation,
              r.claim_expires_at
)
SELECT * FROM claimed ORDER BY next_fire_at, id;

-- name: ListTerminalTriggerRecordItems :many
-- A broker may redeliver after Gregale commits a terminal receipt but before
-- the broker acknowledges it. The current delivery handle can be Acked
-- without dispatching the application again.
SELECT item_identifier FROM trigger_records
WHERE trigger_id = $1
  AND item_identifier = ANY(sqlc.arg(item_identifiers)::text[])
  AND state IN ('succeeded', 'superseded', 'cancelled', 'expired');

-- name: MarkClaimedTriggerRecordSucceeded :execrows
UPDATE trigger_records
   SET state = 'succeeded', last_dispatched_at = now(), claim_expires_at = NULL
 WHERE id = $1 AND state = 'claimed' AND claim_generation = $2
   AND claim_expires_at > now();

-- name: MarkClaimedTriggerRecordRetry :execrows
UPDATE trigger_records
   SET state = 'retry', attempts = attempts + 1, last_error = $3,
       last_dispatched_at = now(), next_fire_at = $4, claim_expires_at = NULL
 WHERE id = $1 AND state = 'claimed' AND claim_generation = $2
   AND claim_expires_at > now();

-- name: MarkClaimedTriggerRecordDeadLetter :execrows
UPDATE trigger_records
   SET state = 'dead_letter', attempts = attempts + 1, last_error = $3,
       last_dispatched_at = now(), claim_expires_at = NULL
 WHERE id = $1 AND state = 'claimed' AND claim_generation = $2
   AND claim_expires_at > now();

-- name: InsertTriggerRecord :one
-- Review finding #1 (PR #910): the dispatcher MUST persist every
-- broker-delivered record into trigger_records BEFORE
-- ClaimTriggerRecords can find them. Without this insert the
-- entire dispatch tick is dead — ClaimTriggerRecords returns 0
-- rows, the broker messages accumulate forever in poller.inFlight,
-- and the unified Trigger primitive never fires a function.
--
-- ON CONFLICT (trigger_id, item_identifier) DO NOTHING mirrors the
-- broker-side dedupe guarantee (kafka per-partition offset,
-- NATS stream sequence, Redis entry-id, SQS receipt handle,
-- in-platform invocation_id — all globally unique within their
-- own ledger). A re-poll after a partial commit + Ack timeout
-- therefore never inserts a duplicate row.
--
-- Returning id gives the dispatcher the trigger_records.id that
-- ClaimTriggerRecords surfaces under FOR UPDATE SKIP LOCKED,
-- bridging the item_identifier → row_id namespace the
-- ReportBatchItemFailures handler needs.
insert into trigger_records (trigger_id, item_identifier, payload, headers, metadata)
values ($1, $2, $3::jsonb, $4::jsonb, $5::jsonb)
on conflict (trigger_id, item_identifier) do nothing
returning id;

-- name: MarkTriggerRecordSucceeded :exec
update trigger_records
   set state = 'succeeded',
       last_dispatched_at = now()
 where id = $1;

-- name: MarkTriggerRecordRetry :exec
update trigger_records
   set state = 'retry',
       attempts = attempts + 1,
       last_error = $2,
       last_dispatched_at = now(),
       next_fire_at = $3
 where id = $1;

-- name: MarkTriggerRecordDeadLetter :exec
update trigger_records
   set state = 'dead_letter',
       attempts = attempts + 1,
       last_error = $2,
       last_dispatched_at = now()
 where id = $1;

-- name: InsertTriggerDeadLetter :exec
-- One row per dead-lettered record. The reason is the closed-vocab
-- failure mode (rate_limited, poison_record, max_attempts,
-- broker_error, plan_quota, payload_too_large, customer_disabled);
-- the routed_to is the closed-vocab terminal action (drop,
-- manual_retry, customer_dlq). detail carries any per-reason payload
-- (the broker error text, the payload size that tripped the 6MB
-- cap, etc.) for the dashboard read-back.
insert into trigger_dead_letter (record_id, trigger_id, reason, routed_to, detail)
values ($1, $2, $3, $4, $5::jsonb);

-- name: ListTriggerDeadLetter :many
select record_id, trigger_id, reason, routed_to, detail, created_at
from trigger_dead_letter
where trigger_id = $1
order by created_at desc
limit $2;

-- name: ListTriggerRecordsForTrigger :many
-- Used by GET /v1/triggers/{id}/records (dashboard + apid handler).
-- Returns records in dispatch-time order with the standard projection.
select id, trigger_id, item_identifier, payload, headers, metadata,
       state, attempts, next_fire_at, received_at, last_error,
       last_dispatched_at
from trigger_records
where trigger_id = $1
order by received_at desc
limit $2;

-- name: TriggerRecordIDByItemIdentifier :one
-- Audit round 2 finding #1 (PR #910): deadLetterAll() is invoked
-- with broker-side handles (kafka offset, NATS seq, SQS receipt
-- handle, Redis entry-id, queue invocation_id) but the
-- trigger_dead_letter.record_id column is a UUID FK into
-- trigger_records.id. The dispatcher needs to bridge the
-- item_identifier namespace to the row UUID before calling
-- InsertTriggerDeadLetter — otherwise every rate-limit denial
-- trips SQLSTATE 23503 and the dead_letter row is silently
-- dropped, MarkTriggerRecordDeadLetter updates 0 rows, the
-- record stays in poller.inFlight forever, and the broker
-- offset never advances.
--
-- Returns the trigger_records.id for the (trigger_id,
-- item_identifier) pair, or an empty pgtype.UUID (and nil
-- error) when no row exists yet — that case fires when the
-- rate-limit gate denies a record before InsertTriggerRecord
-- has had a chance to run. Callers MUST treat the empty UUID
-- as "skip the dead_letter insert; leave the record in
-- poller.inFlight for the next tick to retry".
select id
from trigger_records
where trigger_id = $1
  and item_identifier = $2;
-- ─── OIDC / keyless deploy auth (ADR-101, issue #270) ───────────────────
--
-- Per-(account_id, issuer_url) trust policy CRUD + exchanged-token
-- CRUD. PR-A scope. The composite PK on oidc_trust_policies is the
-- lookup key; the per-account dashboard list (PR-C) walks the
-- same index. token_hash UNIQUE on oidc_exchanged_tokens is the
-- bearer hot-path lookup.

-- name: GetOIDCTrustPolicy :one
-- Account-by-issuer lookup. Used by the OIDC exchange handler's
-- first-use auto-create path (PR-A) and the dashboard's Refine
-- form (PR-C).
select account_id, issuer_url, jwks_url, audience,
       coalesce(subject_pattern, '') as subject_pattern,
       algorithms, required_claims, created_at, updated_at,
       audit_login
from oidc_trust_policies
where account_id = $1 and issuer_url = $2;

-- name: UpsertOIDCTrustPolicy :one
-- PK conflict on (account_id, issuer_url) updates the mutable
-- columns (audience, subject_pattern, algorithms, required_claims,
-- updated_at, audit_login) and preserves created_at. Returns the
-- full row as stored.
insert into oidc_trust_policies
    (account_id, issuer_url, jwks_url, audience, subject_pattern,
     algorithms, required_claims, audit_login)
values ($1, $2, $3, $4, $5, $6, $7, $8)
on conflict (account_id, issuer_url) do update
    set audience = excluded.audience,
        subject_pattern = excluded.subject_pattern,
        algorithms = excluded.algorithms,
        required_claims = excluded.required_claims,
        updated_at = now(),
        audit_login = excluded.audit_login
returning account_id, issuer_url, jwks_url, audience,
          coalesce(subject_pattern, '') as subject_pattern,
          algorithms, required_claims, created_at, updated_at,
          audit_login;

-- name: ListOIDCTrustPoliciesForAccount :many
-- Per-account dashboard list (PR-C). Empty slice on miss.
select account_id, issuer_url, jwks_url, audience,
       coalesce(subject_pattern, '') as subject_pattern,
       algorithms, required_claims, created_at, updated_at,
       audit_login
from oidc_trust_policies
where account_id = $1
order by created_at desc;

-- name: AccountByOIDCIssuerSubject :one
-- Resolves an OIDC (issuer, subject) pair to the platform
-- account it's bound to: a trust policy row with matching
-- issuer_url whose subject_pattern matches the subject claim.
-- An empty subject_pattern binds nothing. The legacy first-use
-- policies were written that way (any subject, any audience);
-- treating them as "accept any subject" routed every foreign
-- GitHub Actions subject to such an account.
select a.id, a.email, a.plan, a.status,
       coalesce(a.provider_customer_id, ''), a.created_at
from accounts a
join oidc_trust_policies p on p.account_id = a.id
where p.issuer_url = $1
  and coalesce(p.subject_pattern, '') <> ''
  and $2 ~ p.subject_pattern
order by length(p.subject_pattern) desc,
         a.id
limit 1;

-- name: AccountIDByGitHubOIDCRepositoryIdentity :one
-- First-use GitHub Actions bootstrap through an OAuth-verified install
-- binding. Immutable subjects must match both persisted numeric IDs as well
-- as the current repository name; a zero ID preserves legacy name-only
-- subject behavior.
select min(a.github_install_account_id::text)::uuid as account_id
from apps a
join github_installations gi
  on gi.account_id = a.github_install_account_id
 and gi.installation_id = a.github_install_id
where lower(a.github_repo_full_name) = lower(sqlc.arg(repo_full_name)::text)
  and a.github_install_account_id = a.account_id
  and a.deleted_at is null
  and (sqlc.arg(owner_id)::bigint = 0 or a.github_owner_id = sqlc.arg(owner_id)::bigint)
  and (sqlc.arg(repo_id)::bigint = 0 or a.github_repo_id = sqlc.arg(repo_id)::bigint)
having count(distinct a.github_install_account_id) = 1;

-- name: InsertOIDCExchangedToken :one
-- Fresh-token insert. The id is server-minted by sqlc (gen_random_uuid).
-- Returns the full row (with created_at server-stamped).
insert into oidc_exchanged_tokens
    (account_id, token_hash, expires_at, issuer_url, subject,
     audience, jti, scopes)
values ($1, $2, $3, $4, $5, $6, $7, $8)
returning id, account_id, token_hash, expires_at, issuer_url,
          subject, audience, coalesce(jti, '') as jti, scopes,
          created_at;

-- name: GetOIDCExchangedTokenByHash :one
-- Bearer hot-path lookup. Filters past-TTL rows out at the SQL
-- layer so the pg contract is "WHERE expires_at > NOW()". The
-- MemStore mirror in pkg/state/memstore.go lazy-deletes instead.
select id, account_id, token_hash, expires_at, issuer_url, subject,
       audience, coalesce(jti, '') as jti, scopes, created_at
from oidc_exchanged_tokens
where token_hash = $1
  and expires_at > now();

-- name: DeleteOIDCExchangedToken :exec
-- Operator-driven revoke path (PR-C). Returns 0 rows on miss;
-- the caller maps that to ErrNotFound. The 5-min TTL is the
-- natural expiry path; Delete is the "kill this CI job's
-- credential now" lever.
delete from oidc_exchanged_tokens where id = $1;

-- ---------------------------------------------------------------------------
-- ADR-127 / issue #477 — production debugger per-request telemetry
--
-- The data plane that backs the example insight ("POST /checkout became
-- 38% slower after deployment v81; PostgreSQL queries 82ms → 191ms;
-- 31% of requests affected"). Every gateway-served request lands one row
-- here, keyed on (account_id, app_id, received_at DESC) for the canonical
-- read pattern. trace_id links the row to the in-process TraceRing and
-- to customer-emitted OTel spans.
--
-- Schema is in migrations/00427_request_telemetry.sql (partitioned by
-- RANGE(received_at) so the per-plan retention sweep drops whole monthly
-- partitions rather than per-row DELETEs). Indexes are pinned in the
-- migration; sqlc only generates the typed query surface here.
--
-- Wire path: gatewayd-internal recorder (pkg/gateway/request_telemetry.go)
-- enqueues in-process → publisher batches over unix-socket gRPC to apid's
-- IncrementRequestTelemetry streaming RPC (cmd/apid/grpc_server_request_telemetry.go)
-- → sqlc-generated InsertRequestTelemetry below. The recorder never opens
-- a Postgres connection (CLAUDE.md ownership: apid is the sole writer).
-- ---------------------------------------------------------------------------

-- name: InsertRequestTelemetry :exec
-- One row per gateway-served request. No ON CONFLICT — every request
-- gets its own row (request_id is the natural dedupe, but we don't have
-- it here; TraceRing dedupe is at minute granularity in the recorder).
-- The migration's PK is (id, received_at) because of PARTITION BY RANGE;
-- the id alone is generated by gen_random_uuid() default.
--
-- PR-B (ADR-127 §PR-B): the publisher collapse in
-- pkg/gateway/request_telemetry_publisher.go coalesces requests with
-- the same (app, deployment, route, method, status, dimensions,
-- minute_bucket, latency_bucket) into one row with `count` = the number
-- of originals. count is INT NOT NULL DEFAULT 1 (00440) so pre-PR-B
-- clients keep working — the DEFAULT fires for any INSERT that omits
-- the column. PR-B's publisher always passes it explicitly.
INSERT INTO request_telemetry (
    account_id, app_id, deployment_id, route, method,
    status, latency_ms, cold_boot, trace_id, received_at, count,
    ua_family, referrer_host, country, wake_id, instance_id,
    guest_duration_ms, guest_runtime, guest_outcome, guest_error_class, consumer_id,
    node_id, region, commit_sha, deployment_tag, deployment_created_at, image_digest,
    platform_tenant_id,
    guest_cpu_time_ms, guest_peak_rss_mb, guest_resource_usage_available, flag_evidence
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10, $11,
    $12, $13, $14, $15, $16, $17,
    COALESCE(NULLIF(sqlc.arg('guest_runtime')::text, ''), '__unknown__'),
    COALESCE(NULLIF(sqlc.arg('guest_outcome')::text, ''), 'missing'),
    COALESCE(sqlc.arg('guest_error_class')::text, ''),
    sqlc.arg('consumer_id')::uuid,
    sqlc.arg('node_id')::text,
    sqlc.arg('region')::text,
    sqlc.arg('commit_sha')::text,
    sqlc.arg('deployment_tag')::text,
    sqlc.arg('deployment_created_at')::text,
    sqlc.arg('image_digest')::text,
    sqlc.arg('platform_tenant_id')::uuid,
    sqlc.arg('guest_cpu_time_ms')::int,
    sqlc.arg('guest_peak_rss_mb')::int,
    sqlc.arg('guest_resource_usage_available')::bool,
    COALESCE(NULLIF(sqlc.arg('flag_evidence_json')::text, '')::jsonb, '[]'::jsonb)
);

-- name: ListRequestTelemetryByPlatformTenant :many
-- Cross-app support view for a platform customer. Always constrain by both
-- owning account and the immutable request-time tenant snapshot; do not infer
-- attribution by joining today's consumer/surface links.
SELECT id, app_id, deployment_id, route, method, status, latency_ms, count,
       cold_boot, trace_id, received_at, wake_id, instance_id,
       guest_duration_ms, guest_runtime, guest_outcome, guest_error_class,
       consumer_id, node_id, region, commit_sha, deployment_tag,
       deployment_created_at, image_digest
FROM request_telemetry
WHERE account_id = sqlc.arg('account_id')
  AND platform_tenant_id = sqlc.arg('platform_tenant_id')
  AND received_at >= sqlc.arg('received_from')
  AND received_at < sqlc.arg('received_until')
  AND (sqlc.arg('cursor_received_at')::timestamptz IS NULL
       OR (received_at, id) < (sqlc.arg('cursor_received_at')::timestamptz,
                               sqlc.arg('cursor_id')::uuid))
  AND (sqlc.arg('app_id_filter')::text = ''
       OR app_id = NULLIF(sqlc.arg('app_id_filter')::text, '')::uuid)
  AND (sqlc.arg('status_filter')::int = 0
       OR status = sqlc.arg('status_filter')::int)
ORDER BY received_at DESC, id DESC
LIMIT sqlc.arg('limit')::int;

-- name: ListRequestTelemetryByApp :many
-- Canonical read pattern: "give me the last N requests for this app".
-- Backs GET /v1/apps/{slug}/debug/requests. Uses
-- request_telemetry_app_received_idx. The (since, until) pair is
-- timestamptz; handler-side date parsing is at cmd/apid/
-- handlers_debug_telemetry.go (parseDebugSinceFromString). Cursor pages use
-- the strict (received_at, id) tuple so equal timestamps cannot reorder rows.
SELECT id, deployment_id, route, method, status, latency_ms, count,
       cold_boot, trace_id, received_at, wake_id, instance_id,
       guest_duration_ms, guest_runtime, guest_outcome, guest_error_class,
       consumer_id, node_id, region, commit_sha, deployment_tag,
       deployment_created_at, image_digest
FROM request_telemetry
WHERE app_id = $1
  AND received_at >= $2
  AND received_at <  $3
  AND (sqlc.arg('cursor_received_at')::timestamptz IS NULL
       OR (received_at, id) < (sqlc.arg('cursor_received_at')::timestamptz,
                               sqlc.arg('cursor_id')::uuid))
  AND (sqlc.arg('route')::text = '' OR route = sqlc.arg('route')::text)
  -- Filters are explicit query parameters so sqlc keeps the generated
  -- params stable. The cursor stores the same values and the handler
  -- rejects a page walk when they change.
  AND (sqlc.arg('deployment_id')::text = ''
       OR deployment_id = NULLIF(sqlc.arg('deployment_id')::text, '')::uuid)
  AND (sqlc.arg('status_filter')::int = 0
       OR status = sqlc.arg('status_filter')::int)
  AND (sqlc.arg('cold_boot_filter')::int = -1
       OR cold_boot = (sqlc.arg('cold_boot_filter')::int = 1))
  AND (
       (sqlc.arg('consumer_anonymous')::boolean AND consumer_id IS NULL)
       OR (
           NOT sqlc.arg('consumer_anonymous')::boolean
           AND (
               sqlc.arg('consumer_id')::text = ''
               OR consumer_id = NULLIF(sqlc.arg('consumer_id')::text, '')::uuid
           )
       )
  )
  AND (sqlc.arg('min_latency_ms')::int = 0
       OR latency_ms >= sqlc.arg('min_latency_ms')::int)
ORDER BY received_at DESC, id DESC
LIMIT sqlc.arg('limit')::int;

-- name: ListRequestTelemetryDependencySpans :many
-- Bounded read path for route-scoped dependency analytics. The
-- account_id predicate is defense in depth for callers that accidentally
-- pass an app id from another tenant; the app lookup remains the primary
-- IDOR boundary. Evidence is newest-first within each route/deployment
-- partition, then interleaved so one high-volume revision cannot crowd all
-- prior deployments out of the bounded comparison window.
WITH ranked AS (
    SELECT id, route, method, count, status, trace_id, received_at, spans_summary,
           deployment_id, commit_sha, deployment_tag, deployment_created_at,
           ROW_NUMBER() OVER (
               PARTITION BY route, method, deployment_id
               ORDER BY received_at DESC, id DESC
           ) AS evidence_rank
    FROM request_telemetry
    WHERE app_id = $1
      AND account_id = $2
      AND received_at >= $3
      AND received_at <  $4
      AND spans_summary IS NOT NULL
)
SELECT id, route, method, count, status, trace_id, received_at, spans_summary,
       deployment_id::text, commit_sha, deployment_tag, deployment_created_at
FROM ranked
ORDER BY evidence_rank ASC, received_at DESC, id DESC
LIMIT $5;

-- name: RequestTelemetryRouteCustomers :many
-- One immutable deployment only. Identity joins validate ownership, never
-- infer a tenant from today's consumer link. Counts precede all output caps.
WITH filtered AS MATERIALIZED (
    SELECT rt.route, rt.method, rt.count::bigint AS requests, rt.received_at,
           c.id AS consumer_id, t.id AS platform_tenant_id,
           (rt.consumer_id IS NULL AND rt.platform_tenant_id IS NULL) AS anonymous
    FROM request_telemetry rt
    LEFT JOIN api_consumers c
      ON c.id = rt.consumer_id AND c.account_id = rt.account_id AND c.app_id = rt.app_id
    LEFT JOIN platform_tenants t
      ON t.id = rt.platform_tenant_id AND t.account_id = rt.account_id
    WHERE rt.app_id = sqlc.arg(app_id)
      AND rt.account_id = sqlc.arg(account_id)
      AND rt.deployment_id = sqlc.arg(deployment_id)
      AND rt.received_at >= sqlc.arg(since_at)
      AND rt.received_at < sqlc.arg(until_at)
), route_totals AS (
    SELECT route, method, SUM(requests)::bigint AS requests,
           COALESCE(SUM(requests) FILTER (WHERE consumer_id IS NOT NULL OR platform_tenant_id IS NOT NULL), 0)::bigint AS identified_requests,
           COALESCE(SUM(requests) FILTER (WHERE anonymous), 0)::bigint AS anonymous_requests,
           COALESCE(SUM(requests) FILTER (WHERE NOT anonymous AND consumer_id IS NULL AND platform_tenant_id IS NULL), 0)::bigint AS unresolved_identity_requests,
           COUNT(DISTINCT consumer_id)::bigint AS consumer_count,
           COUNT(DISTINCT platform_tenant_id)::bigint AS platform_tenant_count,
           MAX(received_at) AS last_observed_at
    FROM filtered GROUP BY route, method
), ranked_routes AS (
    SELECT route_totals.*, COUNT(*) OVER ()::bigint AS matched_routes,
           ROW_NUMBER() OVER (ORDER BY requests DESC, route ASC, method ASC) AS route_rank
    FROM route_totals
), top_routes AS (
    SELECT * FROM ranked_routes WHERE route_rank <= sqlc.arg(route_limit)::int
), customer_totals AS (
    SELECT f.route, f.method, f.consumer_id, f.platform_tenant_id,
           SUM(f.requests)::bigint AS requests, MAX(f.received_at) AS last_observed_at
    FROM filtered f JOIN top_routes USING (route, method)
    WHERE f.consumer_id IS NOT NULL OR f.platform_tenant_id IS NOT NULL
    GROUP BY f.route, f.method, f.consumer_id, f.platform_tenant_id
), ranked_customers AS (
    SELECT customer_totals.*,
           ROW_NUMBER() OVER (PARTITION BY route, method ORDER BY requests DESC, consumer_id ASC NULLS LAST, platform_tenant_id ASC NULLS LAST) AS customer_rank
    FROM customer_totals
), customer_bounds AS (
    SELECT route, method, COUNT(*)::bigint AS customer_groups,
           COALESCE(SUM(requests) FILTER (WHERE customer_rank > sqlc.arg(customer_limit)::int), 0)::bigint AS other_customer_requests
    FROM ranked_customers GROUP BY route, method
)
SELECT tr.route, tr.method, tr.requests, tr.identified_requests,
       tr.anonymous_requests, tr.unresolved_identity_requests,
       tr.consumer_count, tr.platform_tenant_count, tr.last_observed_at::timestamptz AS last_observed_at,
       tr.matched_routes,
       COALESCE(cb.customer_groups, 0)::bigint AS customer_groups,
       COALESCE(cb.other_customer_requests, 0)::bigint AS other_customer_requests,
       COALESCE(rc.consumer_id::text, '')::text AS consumer_id,
       COALESCE(rc.platform_tenant_id::text, '')::text AS platform_tenant_id,
       COALESCE(rc.requests, 0)::bigint AS customer_requests,
       rc.last_observed_at::timestamptz AS customer_last_observed_at
FROM top_routes tr
LEFT JOIN customer_bounds cb USING (route, method)
LEFT JOIN ranked_customers rc ON rc.route = tr.route AND rc.method = tr.method AND rc.customer_rank <= sqlc.arg(customer_limit)::int
ORDER BY tr.requests DESC, tr.route ASC, tr.method ASC,
         rc.requests DESC, rc.consumer_id ASC NULLS LAST, rc.platform_tenant_id ASC NULLS LAST;

-- name: RequestTelemetryCoverage :one
-- Signal coverage for the customer debugger. Counts are weighted by the
-- publisher's collapsed-row `count`, while the row totals make the amount
-- of aggregation visible to callers. This query deliberately reports
-- observed coverage only: request_telemetry has no trustworthy denominator
-- for requests dropped before persistence, so the API must not invent a
-- capture percentage.
SELECT
    COUNT(*)::bigint AS telemetry_rows,
    COALESCE(SUM(count::bigint), 0)::bigint AS represented_requests,
    COUNT(*) FILTER (WHERE trace_id IS NOT NULL)::bigint AS trace_linked_rows,
    COALESCE(SUM(count::bigint) FILTER (WHERE trace_id IS NOT NULL), 0)::bigint AS trace_linked_requests,
    COUNT(*) FILTER (WHERE spans_summary IS NOT NULL)::bigint AS span_evidence_rows,
    COALESCE(SUM(count::bigint) FILTER (WHERE spans_summary IS NOT NULL), 0)::bigint AS span_evidence_requests,
    COUNT(*) FILTER (WHERE wake_id IS NOT NULL AND wake_id <> '')::bigint AS wake_evidence_rows,
    COALESCE(SUM(count::bigint) FILTER (WHERE wake_id IS NOT NULL AND wake_id <> ''), 0)::bigint AS wake_evidence_requests,
    COUNT(*) FILTER (WHERE guest_runtime <> '__unknown__' AND guest_outcome <> 'missing')::bigint AS guest_evidence_rows,
    COALESCE(SUM(count::bigint) FILTER (WHERE guest_runtime <> '__unknown__' AND guest_outcome <> 'missing'), 0)::bigint AS guest_evidence_requests,
    COALESCE(SUM(count::bigint) FILTER (WHERE status >= 400), 0)::bigint AS error_requests,
    MIN(received_at) AS oldest_telemetry_at,
    MAX(received_at) AS latest_telemetry_at
FROM request_telemetry
WHERE app_id = $1
  AND account_id = $2
  AND received_at >= $3
  AND received_at <  $4;

-- name: GetRequestTelemetryByAppAndIdentifier :one
-- Direct request drill-down for the customer debugger. Customers normally
-- have the public x-faas-request-id stored as trace_id, while older clients
-- may retain the internal telemetry-row UUID. Accept both without weakening
-- the app_id tenant boundary. Prefer an exact row-id match if a future trace
-- value happens to equal another row's UUID text.
SELECT id, deployment_id, route, method, status, latency_ms, count,
       cold_boot, trace_id, received_at, spans_summary, wake_id, instance_id,
       guest_duration_ms, guest_runtime, guest_outcome, guest_error_class,
       consumer_id, node_id, region, commit_sha, deployment_tag,
       deployment_created_at, image_digest
FROM request_telemetry
WHERE app_id = sqlc.arg(app_id)
  AND (id::text = sqlc.arg(identifier)::text OR trace_id = sqlc.arg(identifier)::text)
  AND received_at >= sqlc.arg(received_from)
  AND received_at <  sqlc.arg(received_until)
ORDER BY (id::text = sqlc.arg(identifier)::text) DESC, received_at DESC
LIMIT 1;

-- name: RequestTelemetryByDeployment :many
-- Per-deployment drilldown. Used by gregale debug compare and the
-- regression detector (PR-B). Includes the publisher's `count`
-- weight so callers can report request totals rather than stored
-- aggregate-row totals. Uses request_telemetry_app_dep_received_idx.
SELECT id, route, method, status, latency_ms, count, cold_boot, trace_id, received_at
FROM request_telemetry
WHERE app_id = $1
  AND deployment_id = $2
  AND received_at >= $3
  AND received_at <  $4
ORDER BY received_at DESC
LIMIT $5;

-- name: RequestTelemetryCircuitBreakerSummary :one
-- Bounded candidate/stable health summary for the deployment circuit breaker.
-- `count` weights collapsed telemetry rows; compute request and 5xx totals,
-- overall p95, and cold-boot-only p95 in SQL so each progression tick transfers
-- only one row.
WITH weighted AS (
    SELECT latency_ms,
           SUM(count::bigint) AS requests,
           SUM(count::bigint) FILTER (WHERE status >= 500 AND status < 600) AS server_errors,
           SUM(count::bigint) FILTER (WHERE cold_boot) AS cold_boot_requests
      FROM request_telemetry
     WHERE app_id = sqlc.arg('app_id')::uuid
       AND deployment_id = sqlc.arg('deployment_id')::uuid
       AND received_at >= sqlc.arg('received_at')::timestamptz
       AND received_at < sqlc.arg('received_at_2')::timestamptz
     GROUP BY latency_ms
), cpu_usage AS (
    -- CPU is sampled per instance/minute. The retained instance row supplies
    -- its deployment identity; old stopped instances remain through the
    -- telemetry window, then state retention removes them. Ignore pure idle
    -- minutes so background CPU with no requests cannot dominate the ratio.
    SELECT COALESCE(SUM(u.cpu_usec) FILTER (WHERE u.requests > 0), 0)::bigint AS cpu_usec,
           COALESCE(SUM(u.requests::bigint) FILTER (WHERE u.requests > 0), 0)::bigint AS cpu_requests
      FROM usage_minutes AS u
      JOIN instances AS ins ON ins.id = u.instance_id
     WHERE u.app_id = sqlc.arg('app_id')::uuid
       AND ins.app_id = sqlc.arg('app_id')::uuid
       AND ins.deployment_id = sqlc.arg('deployment_id')::uuid
       AND u.minute >= date_trunc('minute', sqlc.arg('received_at')::timestamptz)
       AND u.minute < sqlc.arg('received_at_2')::timestamptz
), ranked AS (
      SELECT latency_ms,
             requests,
             server_errors,
             cold_boot_requests,
             SUM(requests) OVER (ORDER BY latency_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
             SUM(requests) OVER () AS total,
             SUM(cold_boot_requests) OVER (ORDER BY latency_ms ROWS UNBOUNDED PRECEDING) AS cold_boot_cumulative,
             SUM(cold_boot_requests) OVER () AS cold_boot_total
        FROM weighted
), summary AS (
    SELECT COALESCE(SUM(requests), 0)::bigint AS requests,
           COALESCE(SUM(server_errors), 0)::bigint AS server_errors,
           COALESCE(
               MIN(latency_ms) FILTER (WHERE cumulative >= CEIL(total * 0.95)::bigint),
               0
           )::double precision AS p95_latency_ms,
           COALESCE(SUM(cold_boot_requests), 0)::bigint AS cold_boot_requests,
           COALESCE(
               MIN(latency_ms) FILTER (
                   WHERE cold_boot_cumulative >= CEIL(cold_boot_total * 0.95)::bigint
                     AND cold_boot_total > 0
               ),
               0
           )::double precision AS cold_boot_p95_latency_ms
      FROM ranked
)
SELECT summary.requests,
       summary.server_errors,
       summary.p95_latency_ms,
       summary.cold_boot_requests,
       summary.cold_boot_p95_latency_ms,
       cpu_usage.cpu_usec,
       cpu_usage.cpu_requests
  FROM summary
 CROSS JOIN cpu_usage;

-- name: RequestTelemetryBaselineP95ByRoute :many
-- Per-route p50/p95/p99 latency + represented request count for the
-- compare endpoint and the regression detector (ADR-127 PR-B
-- cron + PR Debugger UX v1 compare handler). Single index scan
-- over the existing request_telemetry_app_dep_received_idx
-- (PR-A migration 00427) so the four aggregates share one
-- window. The recorder collapses burst traffic into bounded
-- latency-bucket rows with a `count` weight; expand that weight
-- mathematically instead of treating each aggregate row as one
-- request. The rank/floor formulation below is equivalent to
-- percentile_cont over the expanded multiset of bucket
-- representatives, without materializing one row per request.
WITH weighted AS (
    SELECT route,
           latency_ms,
           SUM(count::bigint) AS weight
    FROM request_telemetry
    WHERE app_id = $1
      AND deployment_id = $2
      AND received_at >= $3
      AND received_at <  $4
    GROUP BY route, latency_ms
), ranked AS (
    SELECT route,
           latency_ms,
           SUM(weight) OVER (
               PARTITION BY route
               ORDER BY latency_ms
               ROWS UNBOUNDED PRECEDING
           ) AS cumulative,
           SUM(weight) OVER (PARTITION BY route) AS total
    FROM weighted
), targets AS (
    SELECT route,
           total,
           (total - 1)::numeric * 0.50 AS p50_rank,
           (total - 1)::numeric * 0.95 AS p95_rank,
           (total - 1)::numeric * 0.99 AS p99_rank
    FROM ranked
    GROUP BY route, total
), values_at_rank AS (
    SELECT r.route,
           t.total,
           t.p50_rank,
           t.p95_rank,
           t.p99_rank,
           MIN(r.latency_ms) FILTER (
               WHERE r.cumulative > floor(t.p50_rank)
           ) AS p50_low,
           MIN(r.latency_ms) FILTER (
               WHERE r.cumulative > ceil(t.p50_rank)
           ) AS p50_high,
           MIN(r.latency_ms) FILTER (
               WHERE r.cumulative > floor(t.p95_rank)
           ) AS p95_low,
           MIN(r.latency_ms) FILTER (
               WHERE r.cumulative > ceil(t.p95_rank)
           ) AS p95_high,
           MIN(r.latency_ms) FILTER (
               WHERE r.cumulative > floor(t.p99_rank)
           ) AS p99_low,
           MIN(r.latency_ms) FILTER (
               WHERE r.cumulative > ceil(t.p99_rank)
           ) AS p99_high
    FROM ranked AS r
    JOIN targets AS t USING (route)
    GROUP BY r.route, t.total, t.p50_rank, t.p95_rank, t.p99_rank
)
SELECT route,
       (p50_low + (p50_high - p50_low) *
           (p50_rank - floor(p50_rank)))::int AS p50_ms,
       (p95_low + (p95_high - p95_low) *
           (p95_rank - floor(p95_rank)))::int AS p95_ms,
       (p99_low + (p99_high - p99_low) *
           (p99_rank - floor(p99_rank)))::int AS p99_ms,
       total::bigint AS n
FROM values_at_rank;

-- name: RequestTelemetryAnalyticsSummary :one
-- Customer-facing request analytics over a bounded retention window.
-- The recorder collapses identical requests into bounded latency-bucket
-- rows with `count`, so all request/error/cold-boot totals and percentiles
-- must expand that weight rather than treating each stored row as one
-- request. Latency representatives are conservative within the bucket
-- width documented by requestTelemetryLatencyBucketUpperBound.
WITH filtered AS (
    SELECT latency_ms, cold_boot, status, count::bigint AS request_count
    FROM request_telemetry
    WHERE app_id = $1
      AND account_id = $2
      AND received_at >= $3
      AND received_at <  $4
), latency_values AS (
    SELECT latency_ms,
           SUM(request_count)::bigint AS sample_count
    FROM filtered
    GROUP BY latency_ms
), ranked AS (
    SELECT latency_ms,
           sample_count,
           SUM(sample_count) OVER (ORDER BY latency_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
           SUM(sample_count) OVER () AS total
    FROM latency_values
), totals AS (
    SELECT COALESCE(SUM(request_count), 0)::bigint AS requests,
           COALESCE(SUM(request_count) FILTER (WHERE status >= 400), 0)::bigint AS error_requests,
           COALESCE(SUM(request_count) FILTER (WHERE cold_boot), 0)::bigint AS cold_boots
    FROM filtered
)
SELECT requests,
       error_requests,
       cold_boots,
       COALESCE((SELECT MIN(latency_ms) FROM ranked WHERE cumulative >= total * 0.50), 0)::int AS p50_ms,
       COALESCE((SELECT MIN(latency_ms) FROM ranked WHERE cumulative >= total * 0.95), 0)::int AS p95_ms,
       COALESCE((SELECT MIN(latency_ms) FROM ranked WHERE cumulative >= total * 0.99), 0)::int AS p99_ms
FROM totals;

-- name: RequestTelemetryAnalyticsByRoute :many
-- Top route/method rows for the customer analytics overview. `count` is
-- weighted throughout the same way as RequestTelemetryAnalyticsSummary.
WITH filtered AS (
    SELECT route, method, latency_ms, cold_boot, status,
           count::bigint AS request_count
    FROM request_telemetry
    WHERE app_id = $1
      AND account_id = $2
      AND received_at >= $3
      AND received_at <  $4
), route_totals AS (
    SELECT route,
           method,
           COALESCE(SUM(request_count), 0)::bigint AS requests,
           COALESCE(SUM(request_count) FILTER (WHERE status >= 400), 0)::bigint AS error_requests,
           COALESCE(SUM(request_count) FILTER (WHERE cold_boot), 0)::bigint AS cold_boots
    FROM filtered
    GROUP BY route, method
), latency_values AS (
    SELECT route,
           method,
           latency_ms,
           SUM(request_count)::bigint AS sample_count
    FROM filtered
    GROUP BY route, method, latency_ms
), ranked AS (
    SELECT route,
           method,
           latency_ms,
           sample_count,
           SUM(sample_count) OVER (PARTITION BY route, method ORDER BY latency_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
           SUM(sample_count) OVER (PARTITION BY route, method) AS total
    FROM latency_values
), percentiles AS (
    SELECT route,
           method,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.50), 0)::int AS p50_ms,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.95), 0)::int AS p95_ms,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.99), 0)::int AS p99_ms
    FROM ranked
    GROUP BY route, method
)
SELECT totals.route,
       totals.method,
       totals.requests,
       totals.error_requests,
       totals.cold_boots,
       percentiles.p50_ms,
       percentiles.p95_ms,
       percentiles.p99_ms
FROM route_totals AS totals
JOIN percentiles USING (route, method)
ORDER BY totals.requests DESC, totals.method ASC, totals.route ASC
LIMIT $5;

-- name: RequestTelemetryAnalyticsByDeployment :many
-- Bounded deployment cost allocation for the customer request analytics
-- window. Request counts are weighted by the publisher's collapsed `count`.
-- The window total is computed before LIMIT so the handler can allocate the
-- omitted deployments into a visible __other__ bucket without an unbounded
-- response.
WITH per_deployment AS (
    SELECT deployment_id::text AS deployment_id,
           COALESCE(MAX(NULLIF(commit_sha, '')), '') AS commit_sha,
           COALESCE(MAX(NULLIF(deployment_tag, '')), '') AS deployment_tag,
           COALESCE(MAX(NULLIF(deployment_created_at, '')), '') AS deployment_created_at,
           SUM(count)::bigint AS requests,
           COALESCE(SUM(count) FILTER (WHERE guest_resource_usage_available), 0)::bigint AS guest_cpu_measured_requests,
           COALESCE(
               ROUND(
                   SUM(guest_cpu_time_ms::numeric * count)
                       FILTER (WHERE guest_resource_usage_available)
                   / NULLIF(SUM(count) FILTER (WHERE guest_resource_usage_available), 0)
               ),
               0
           )::int AS guest_cpu_avg_ms
    FROM request_telemetry
    WHERE app_id = $1
      AND account_id = $2
      AND received_at >= $3
      AND received_at <  $4
    GROUP BY deployment_id
)
SELECT deployment_id,
       commit_sha,
       deployment_tag,
       deployment_created_at,
       requests,
       guest_cpu_measured_requests,
       guest_cpu_avg_ms,
       SUM(requests) OVER ()::bigint AS total_requests
FROM per_deployment
ORDER BY requests DESC, deployment_id ASC
LIMIT $5;

-- name: RequestTelemetryAnalyticsByRouteDeployment :many
-- Per-route deployment split for the customer analytics window. Routes are
-- bounded to the same top-N surface as route analytics, and each route keeps
-- only its top deployments by request count; the remaining revisions are
-- folded into __other__ so the response cardinality is bounded by
-- route_limit * (deployment_limit + 1).
WITH filtered AS (
    SELECT route,
           method,
           deployment_id::text AS deployment_id,
           commit_sha,
           deployment_tag,
           deployment_created_at,
           count::bigint AS request_count,
           guest_resource_usage_available,
           guest_cpu_time_ms
    FROM request_telemetry
    WHERE app_id = $1
      AND account_id = $2
      AND received_at >= $3
      AND received_at <  $4
), per_route_deployment AS (
    SELECT route,
           method,
           deployment_id,
           COALESCE(MAX(NULLIF(commit_sha, '')), '') AS commit_sha,
           COALESCE(MAX(NULLIF(deployment_tag, '')), '') AS deployment_tag,
           COALESCE(MAX(NULLIF(deployment_created_at, '')), '') AS deployment_created_at,
           SUM(request_count)::bigint AS requests,
           COALESCE(SUM(request_count) FILTER (WHERE guest_resource_usage_available), 0)::bigint AS guest_cpu_measured_requests,
           COALESCE(
               ROUND(
                   SUM(guest_cpu_time_ms::numeric * request_count)
                       FILTER (WHERE guest_resource_usage_available)
                   / NULLIF(SUM(request_count) FILTER (WHERE guest_resource_usage_available), 0)
               ),
               0
           )::int AS guest_cpu_avg_ms
    FROM filtered
    GROUP BY route, method, deployment_id
), top_routes AS (
    SELECT route, method
    FROM per_route_deployment
    GROUP BY route, method
    ORDER BY SUM(requests) DESC, route ASC, method ASC
    LIMIT sqlc.arg('route_limit')::int
), ranked_deployments AS (
    SELECT per_route_deployment.*,
           ROW_NUMBER() OVER (
               PARTITION BY per_route_deployment.route, per_route_deployment.method
               ORDER BY per_route_deployment.requests DESC, per_route_deployment.deployment_id ASC
           ) AS deployment_rank
    FROM per_route_deployment
    JOIN top_routes USING (route, method)
)
SELECT route,
       method,
       CASE WHEN deployment_rank <= sqlc.arg('deployment_limit')::int THEN deployment_id ELSE '__other__' END AS deployment_id,
       CASE WHEN MAX(deployment_rank) <= sqlc.arg('deployment_limit')::int THEN COALESCE(MAX(commit_sha), '') ELSE '' END AS commit_sha,
       CASE WHEN MAX(deployment_rank) <= sqlc.arg('deployment_limit')::int THEN COALESCE(MAX(deployment_tag), '') ELSE '' END AS deployment_tag,
       CASE WHEN MAX(deployment_rank) <= sqlc.arg('deployment_limit')::int THEN COALESCE(MAX(deployment_created_at), '') ELSE '' END AS deployment_created_at,
       SUM(requests)::bigint AS requests,
       CASE WHEN MAX(deployment_rank) > sqlc.arg('deployment_limit')::int THEN 0
            ELSE COALESCE(MAX(guest_cpu_measured_requests), 0)::bigint END AS guest_cpu_measured_requests,
       CASE WHEN MAX(deployment_rank) > sqlc.arg('deployment_limit')::int THEN 0
            ELSE COALESCE(MAX(guest_cpu_avg_ms), 0)::int END AS guest_cpu_avg_ms
FROM ranked_deployments
GROUP BY route,
         method,
         CASE WHEN deployment_rank <= sqlc.arg('deployment_limit')::int THEN deployment_id ELSE '__other__' END
ORDER BY route ASC,
         method ASC,
         CASE WHEN CASE WHEN deployment_rank <= sqlc.arg('deployment_limit')::int THEN deployment_id ELSE '__other__' END = '__other__' THEN 1 ELSE 0 END,
         MAX(deployment_created_at) DESC,
         CASE WHEN deployment_rank <= sqlc.arg('deployment_limit')::int THEN deployment_id ELSE '__other__' END ASC;

-- name: RequestTelemetryAnalyticsByDimension :many
-- Top-N customer analytics grouped by one of the bounded dimensions. Rows
-- outside the top-N are folded into __other__ so a customer cannot turn this
-- endpoint into an unbounded cardinality surface. Counts and percentiles use
-- the publisher's collapsed row weight.
WITH filtered AS (
    SELECT
        CASE sqlc.arg('group_by')::text
            WHEN 'country' THEN country
            WHEN 'referrer_host' THEN referrer_host
            WHEN 'ua_family' THEN ua_family
            WHEN 'status' THEN status::text
            WHEN 'consumer_id' THEN COALESCE(consumer_id::text, '__anonymous__')
            ELSE route
        END::text AS dimension,
        CASE WHEN sqlc.arg('group_by')::text = 'route' THEN method ELSE '' END AS method,
        latency_ms,
        cold_boot,
        status,
        guest_duration_ms,
        guest_runtime,
        guest_cpu_time_ms,
        guest_peak_rss_mb,
        guest_resource_usage_available,
        wake_id,
        count::bigint AS request_count
    FROM request_telemetry
    WHERE app_id = $1
      AND account_id = $2
      AND received_at >= $3
      AND received_at <  $4
), top_groups AS (
    SELECT dimension, method
    FROM filtered
    GROUP BY dimension, method
    ORDER BY SUM(request_count) DESC, dimension ASC, method ASC
    LIMIT sqlc.arg('limit')::int
), assigned AS (
    SELECT
        CASE WHEN top_groups.dimension IS NULL THEN '__other__' ELSE filtered.dimension END AS dimension,
        CASE
            WHEN top_groups.dimension IS NULL OR sqlc.arg('group_by')::text <> 'route' THEN ''
            ELSE filtered.method
        END AS method,
        filtered.latency_ms,
        filtered.cold_boot,
        filtered.status,
        filtered.guest_duration_ms,
        filtered.guest_runtime,
        filtered.guest_cpu_time_ms,
        filtered.guest_peak_rss_mb,
        filtered.guest_resource_usage_available,
        filtered.wake_id,
        filtered.request_count
    FROM filtered
    LEFT JOIN top_groups
      ON top_groups.dimension = filtered.dimension
     AND top_groups.method = filtered.method
), latency_values AS (
    SELECT dimension, method, latency_ms,
           SUM(request_count)::bigint AS sample_count
    FROM assigned
    GROUP BY dimension, method, latency_ms
), ranked AS (
    SELECT dimension, method, latency_ms, sample_count,
           SUM(sample_count) OVER (PARTITION BY dimension, method ORDER BY latency_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
           SUM(sample_count) OVER (PARTITION BY dimension, method) AS total
    FROM latency_values
), cold_latency_values AS (
    SELECT dimension,
           method,
           latency_ms,
           SUM(request_count)::bigint AS sample_count
    FROM assigned
    WHERE cold_boot
    GROUP BY dimension, method, latency_ms
), cold_wakes AS (
    SELECT DISTINCT dimension, method, wake_id
    FROM assigned
    WHERE cold_boot
      AND wake_id IS NOT NULL
      AND wake_id <> ''
      AND dimension <> '__other__'
      AND sqlc.arg('group_by')::text = 'route'
), wake_events AS (
    SELECT cold_wakes.dimension,
           cold_wakes.method,
           MIN(events.at) FILTER (WHERE events.kind = 'wake.boot_started' AND events.actor = 'schedd') AS started_at,
           MIN(events.at) FILTER (WHERE events.kind = 'wake.boot_completed' AND events.actor = 'schedd') AS completed_at
    FROM cold_wakes
    JOIN events ON events.data->>'wake_id' = cold_wakes.wake_id
    WHERE events.kind IN ('wake.boot_started', 'wake.boot_completed')
    GROUP BY cold_wakes.dimension, cold_wakes.method, cold_wakes.wake_id
), wake_durations AS (
    SELECT dimension,
           method,
           (EXTRACT(EPOCH FROM (completed_at - started_at)) * 1000)::int AS duration_ms
    FROM wake_events
    WHERE started_at IS NOT NULL
      AND completed_at IS NOT NULL
      AND completed_at > started_at
), wake_values AS (
    SELECT dimension,
           method,
           duration_ms,
           COUNT(*)::bigint AS sample_count
    FROM wake_durations
    GROUP BY dimension, method, duration_ms
), wake_ranked AS (
    SELECT dimension,
           method,
           duration_ms,
           SUM(sample_count) OVER (PARTITION BY dimension, method ORDER BY duration_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
           SUM(sample_count) OVER (PARTITION BY dimension, method) AS total
    FROM wake_values
), wake_percentiles AS (
    SELECT dimension,
           method,
           (MIN(duration_ms) FILTER (WHERE cumulative >= total * 0.95))::int AS wake_boot_p95_ms
    FROM wake_ranked
    GROUP BY dimension, method
), cold_ranked AS (
    SELECT dimension,
           method,
           latency_ms,
           SUM(sample_count) OVER (PARTITION BY dimension, method ORDER BY latency_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
           SUM(sample_count) OVER (PARTITION BY dimension, method) AS total
    FROM cold_latency_values
), cold_percentiles AS (
    SELECT dimension,
           method,
           (MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.95))::int AS cold_request_p95_ms
    FROM cold_ranked
    GROUP BY dimension, method
), guest_latency_values AS (
    SELECT dimension,
           method,
           guest_duration_ms,
           SUM(request_count)::bigint AS sample_count
    FROM assigned
    WHERE guest_runtime <> '__unknown__'
    GROUP BY dimension, method, guest_duration_ms
), guest_ranked AS (
    SELECT dimension,
           method,
           guest_duration_ms,
           SUM(sample_count) OVER (PARTITION BY dimension, method ORDER BY guest_duration_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
           SUM(sample_count) OVER (PARTITION BY dimension, method) AS total
    FROM guest_latency_values
), guest_percentiles AS (
    SELECT dimension,
           method,
           (MIN(guest_duration_ms) FILTER (WHERE cumulative >= total * 0.50))::int AS guest_execution_p50_ms,
           (MIN(guest_duration_ms) FILTER (WHERE cumulative >= total * 0.95))::int AS guest_execution_p95_ms
    FROM guest_ranked
    GROUP BY dimension, method
), guest_cpu_values AS (
    SELECT dimension,
           method,
           guest_cpu_time_ms,
           SUM(request_count)::bigint AS sample_count
    FROM assigned
    WHERE guest_resource_usage_available
    GROUP BY dimension, method, guest_cpu_time_ms
), guest_cpu_ranked AS (
    SELECT dimension,
           method,
           guest_cpu_time_ms,
           SUM(sample_count) OVER (PARTITION BY dimension, method ORDER BY guest_cpu_time_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
           SUM(sample_count) OVER (PARTITION BY dimension, method) AS total
    FROM guest_cpu_values
), guest_resource_metrics AS (
    SELECT assigned.dimension,
           assigned.method,
           ROUND(SUM(assigned.guest_cpu_time_ms::numeric * assigned.request_count)
                 FILTER (WHERE assigned.guest_resource_usage_available)
                 / NULLIF(SUM(assigned.request_count) FILTER (WHERE assigned.guest_resource_usage_available), 0))::int AS guest_cpu_avg_ms,
           MAX(assigned.guest_peak_rss_mb) FILTER (WHERE assigned.guest_resource_usage_available)::int AS guest_peak_rss_max_mb
    FROM assigned
    GROUP BY assigned.dimension, assigned.method
), guest_cpu_percentiles AS (
    SELECT dimension,
           method,
           (MIN(guest_cpu_time_ms) FILTER (WHERE cumulative >= total * 0.95))::int AS guest_cpu_p95_ms
    FROM guest_cpu_ranked
    GROUP BY dimension, method
), percentiles AS (
    SELECT dimension,
           method,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.50), 0)::int AS p50_ms,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.95), 0)::int AS p95_ms,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.99), 0)::int AS p99_ms
    FROM ranked
    GROUP BY dimension, method
), totals AS (
    SELECT dimension,
           method,
           COALESCE(SUM(request_count), 0)::bigint AS requests,
           COALESCE(SUM(request_count) FILTER (WHERE status >= 400), 0)::bigint AS error_requests,
           COALESCE(SUM(request_count) FILTER (WHERE cold_boot), 0)::bigint AS cold_boots
    FROM assigned
    GROUP BY dimension, method
)
SELECT totals.dimension,
       totals.method,
       totals.requests,
       totals.error_requests,
       totals.cold_boots,
       percentiles.p50_ms,
       percentiles.p95_ms,
       percentiles.p99_ms,
       cold_percentiles.cold_request_p95_ms,
       wake_percentiles.wake_boot_p95_ms,
       guest_percentiles.guest_execution_p50_ms,
       guest_percentiles.guest_execution_p95_ms,
       guest_resource_metrics.guest_cpu_avg_ms,
       guest_cpu_percentiles.guest_cpu_p95_ms,
       guest_resource_metrics.guest_peak_rss_max_mb
FROM totals
JOIN percentiles USING (dimension, method)
LEFT JOIN cold_percentiles USING (dimension, method)
LEFT JOIN wake_percentiles USING (dimension, method)
LEFT JOIN guest_percentiles USING (dimension, method)
LEFT JOIN guest_resource_metrics USING (dimension, method)
LEFT JOIN guest_cpu_percentiles USING (dimension, method)
ORDER BY totals.requests DESC, totals.dimension ASC, totals.method ASC;

-- name: RequestTelemetryAnalyticsTimeseries :many
-- Zero-filled UTC hourly request analytics for customer charts. The
-- recorder collapses rows by count, so all totals and percentile ranks
-- expand that weight rather than counting stored rows.
WITH buckets AS (
    SELECT generate_series(
        date_bin('1 hour', sqlc.arg('received_at')::timestamptz, 'epoch'::timestamptz),
        date_bin('1 hour', sqlc.arg('received_at_2')::timestamptz - interval '1 microsecond', 'epoch'::timestamptz),
        interval '1 hour'
    )::timestamptz AS bucket_start
), filtered AS (
    SELECT date_bin('1 hour', received_at, 'epoch'::timestamptz) AS bucket_start,
           latency_ms,
           cold_boot,
           status,
           count::bigint AS request_count
    FROM request_telemetry
    WHERE app_id = $1
      AND account_id = $2
      AND received_at >= sqlc.arg('received_at')::timestamptz
      AND received_at <  sqlc.arg('received_at_2')::timestamptz
      AND (sqlc.arg('route')::text = '' OR route = sqlc.arg('route')::text)
      AND (sqlc.arg('method')::text = '' OR method = sqlc.arg('method')::text)
), latency_values AS (
    SELECT bucket_start,
           latency_ms,
           SUM(request_count)::bigint AS sample_count
    FROM filtered
    GROUP BY bucket_start, latency_ms
), ranked AS (
    SELECT bucket_start,
           latency_ms,
           sample_count,
           SUM(sample_count) OVER (PARTITION BY bucket_start ORDER BY latency_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
           SUM(sample_count) OVER (PARTITION BY bucket_start) AS total
    FROM latency_values
), percentiles AS (
    SELECT bucket_start,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.50), 0)::int AS p50_ms,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.95), 0)::int AS p95_ms,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.99), 0)::int AS p99_ms
    FROM ranked
    GROUP BY bucket_start
), totals AS (
    SELECT bucket_start,
           COALESCE(SUM(request_count), 0)::bigint AS requests,
           COALESCE(SUM(request_count) FILTER (WHERE status >= 400), 0)::bigint AS error_requests,
           COALESCE(SUM(request_count) FILTER (WHERE cold_boot), 0)::bigint AS cold_boots
    FROM filtered
    GROUP BY bucket_start
)
SELECT b.bucket_start,
       COALESCE(t.requests, 0)::bigint AS requests,
       COALESCE(t.error_requests, 0)::bigint AS error_requests,
       COALESCE(t.cold_boots, 0)::bigint AS cold_boots,
       COALESCE(p.p50_ms, 0)::int AS p50_ms,
       COALESCE(p.p95_ms, 0)::int AS p95_ms,
       COALESCE(p.p99_ms, 0)::int AS p99_ms
FROM buckets AS b
LEFT JOIN totals AS t USING (bucket_start)
LEFT JOIN percentiles AS p USING (bucket_start)
ORDER BY b.bucket_start ASC;

-- name: RequestTelemetryAnalyticsTimeseriesGrouped :many
-- Zero-filled hourly series for a bounded top-N analytics dimension. The
-- group set is selected over the whole window, then every selected group is
-- zero-filled per hour. This keeps charts stable while preserving the same
-- retention and cardinality bounds as the overview query.
WITH buckets AS (
    SELECT generate_series(
        date_bin('1 hour', sqlc.arg('received_at')::timestamptz, 'epoch'::timestamptz),
        date_bin('1 hour', sqlc.arg('received_at_2')::timestamptz - interval '1 microsecond', 'epoch'::timestamptz),
        interval '1 hour'
    )::timestamptz AS bucket_start
), filtered AS (
    SELECT date_bin('1 hour', received_at, 'epoch'::timestamptz) AS bucket_start,
           CASE sqlc.arg('group_by')::text
               WHEN 'country' THEN country
               WHEN 'referrer_host' THEN referrer_host
               WHEN 'ua_family' THEN ua_family
               WHEN 'status' THEN status::text
               WHEN 'consumer_id' THEN COALESCE(consumer_id::text, '__anonymous__')
               ELSE route
           END::text AS dimension,
           CASE WHEN sqlc.arg('group_by')::text = 'route' THEN method ELSE '' END AS method,
           latency_ms,
           cold_boot,
           status,
           count::bigint AS request_count
    FROM request_telemetry
    WHERE app_id = $1
      AND account_id = $2
      AND received_at >= sqlc.arg('received_at')::timestamptz
      AND received_at <  sqlc.arg('received_at_2')::timestamptz
      AND (sqlc.arg('route')::text = '' OR route = sqlc.arg('route')::text)
      AND (sqlc.arg('method')::text = '' OR method = sqlc.arg('method')::text)
), top_groups AS (
    SELECT dimension, method
    FROM filtered
    GROUP BY dimension, method
    ORDER BY SUM(request_count) DESC, dimension ASC, method ASC
    LIMIT sqlc.arg('limit')::int
), group_set AS (
    SELECT dimension, method FROM top_groups
    UNION ALL
    SELECT '__other__', ''
    WHERE EXISTS (
        SELECT 1
        FROM filtered
        WHERE NOT EXISTS (
            SELECT 1
            FROM top_groups
            WHERE top_groups.dimension = filtered.dimension
              AND top_groups.method = filtered.method
        )
    )
), assigned AS (
    SELECT filtered.bucket_start,
           CASE WHEN top_groups.dimension IS NULL THEN '__other__' ELSE filtered.dimension END AS dimension,
           CASE
               WHEN top_groups.dimension IS NULL OR sqlc.arg('group_by')::text <> 'route' THEN ''
               ELSE filtered.method
           END AS method,
           filtered.latency_ms,
           filtered.cold_boot,
           filtered.status,
           filtered.request_count
    FROM filtered
    LEFT JOIN top_groups
      ON top_groups.dimension = filtered.dimension
     AND top_groups.method = filtered.method
), latency_values AS (
    SELECT bucket_start, dimension, method, latency_ms,
           SUM(request_count)::bigint AS sample_count
    FROM assigned
    GROUP BY bucket_start, dimension, method, latency_ms
), ranked AS (
    SELECT bucket_start, dimension, method, latency_ms, sample_count,
           SUM(sample_count) OVER (PARTITION BY bucket_start, dimension, method ORDER BY latency_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
           SUM(sample_count) OVER (PARTITION BY bucket_start, dimension, method) AS total
    FROM latency_values
), percentiles AS (
    SELECT bucket_start,
           dimension,
           method,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.50), 0)::int AS p50_ms,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.95), 0)::int AS p95_ms,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.99), 0)::int AS p99_ms
    FROM ranked
    GROUP BY bucket_start, dimension, method
), totals AS (
    SELECT bucket_start,
           dimension,
           method,
           COALESCE(SUM(request_count), 0)::bigint AS requests,
           COALESCE(SUM(request_count) FILTER (WHERE status >= 400), 0)::bigint AS error_requests,
           COALESCE(SUM(request_count) FILTER (WHERE cold_boot), 0)::bigint AS cold_boots
    FROM assigned
    GROUP BY bucket_start, dimension, method
)
SELECT b.bucket_start,
       g.dimension,
       g.method,
       COALESCE(t.requests, 0)::bigint AS requests,
       COALESCE(t.error_requests, 0)::bigint AS error_requests,
       COALESCE(t.cold_boots, 0)::bigint AS cold_boots,
       COALESCE(p.p50_ms, 0)::int AS p50_ms,
       COALESCE(p.p95_ms, 0)::int AS p95_ms,
       COALESCE(p.p99_ms, 0)::int AS p99_ms
FROM buckets AS b
CROSS JOIN group_set AS g
LEFT JOIN totals AS t
  ON t.bucket_start = b.bucket_start
 AND t.dimension = g.dimension
 AND t.method = g.method
LEFT JOIN percentiles AS p
  ON p.bucket_start = b.bucket_start
 AND p.dimension = g.dimension
 AND p.method = g.method
ORDER BY g.dimension ASC, g.method ASC, b.bucket_start ASC;

-- PR-B (ADR-127 §PR-B) — regression observation persistence + dashboard
-- read patterns. The cron in cmd/apid/debug_regression_cron.go composes
-- RequestTelemetryBaselineP95ByRoute (above) + RequestTelemetryByDeployment
-- in Go to detect per-route regressions (ADR-127 §Decision 5: p95_ms
-- exceeds p95_base_ms * 1.20). The detections persist here.
--
-- A native sqlc RequestTelemetryRegression query was tried during the PR-A
-- recon: the CTE-on-CTE shape trips sqlc v1.31's "ambiguous column
-- reference" parser on (deployment_id) even after explicit ON clauses;
-- the workaround is a LATERAL join that requires a more invasive query
-- refactor. Go-side composition of the two PR-A queries above is the
-- chosen workaround (cheap, no parser gymnastics).

-- name: UpsertRegressionObservation :exec
-- Persist a regression observation. PRIMARY KEY (app_id, deployment_id,
-- route) — the cron upserts on this triple so the table grows at most
-- one row per (deployment, route) across all cron passes, not one row
-- per cron tick. first_detected_at remains stable during one active
-- lifecycle, then resets when a resolved regression is detected again
-- (or a dismissal expires). The webhook trigger uses that timestamp as
-- the detection transition's idempotency key. last_detected_at is
-- refreshed on every pass and backs the dashboard's since filter.
INSERT INTO debug_regression_observations (
    app_id, deployment_id, route,
    p95_ms, p95_base_ms, affected_count,
    regression_factor, state, first_detected_at, last_detected_at
) VALUES (
    $1, $2, $3,
    $4, $5, $6,
    $7, 'active', now(), now()
)
ON CONFLICT (app_id, deployment_id, route) DO UPDATE SET
    p95_ms            = EXCLUDED.p95_ms,
    p95_base_ms       = EXCLUDED.p95_base_ms,
    affected_count    = EXCLUDED.affected_count,
    regression_factor = EXCLUDED.regression_factor,
    first_detected_at = CASE
        WHEN debug_regression_observations.state = 'resolved'
          OR (debug_regression_observations.state = 'dismissed'
              AND (debug_regression_observations.dismissed_until IS NULL
                   OR debug_regression_observations.dismissed_until <= now()))
        THEN EXCLUDED.last_detected_at
        ELSE debug_regression_observations.first_detected_at
    END,
    last_detected_at  = EXCLUDED.last_detected_at,
    state             = CASE
        WHEN debug_regression_observations.state = 'acknowledged' THEN 'acknowledged'
        WHEN debug_regression_observations.state = 'dismissed'
             AND debug_regression_observations.dismissed_until IS NOT NULL
             AND debug_regression_observations.dismissed_until > now() THEN 'dismissed'
        ELSE 'active'
    END,
    acknowledged_at   = CASE
        WHEN debug_regression_observations.state = 'acknowledged' THEN debug_regression_observations.acknowledged_at
        ELSE NULL
    END,
    dismissed_until   = CASE
        WHEN debug_regression_observations.state = 'dismissed'
             AND debug_regression_observations.dismissed_until IS NOT NULL
             AND debug_regression_observations.dismissed_until > now()
            THEN debug_regression_observations.dismissed_until
        ELSE NULL
    END,
    resolved_at       = NULL;

-- name: GetRegressionObservation :one
-- Read the row after a detector upsert so the notification reflects a
-- preserved acknowledgement/dismissal rather than assuming active state.
SELECT app_id, deployment_id, route,
       p95_ms, p95_base_ms, affected_count,
       regression_factor, first_detected_at, last_detected_at,
       state, acknowledged_at, dismissed_until, resolved_at
FROM debug_regression_observations
WHERE app_id = $1
  AND deployment_id = $2
  AND route = $3;

-- name: ListActiveRegressionsByApp :many
-- Dashboard + GET /v1/apps/{slug}/debug/regressions read pattern.
-- `since` is an interval (e.g. '1 hour') clamped handler-side to the
-- plan's DebugTelemetryRetentionDays cap. ORDER BY regression_factor
-- DESC, last_detected_at DESC matches the dashboard render order
-- (worst regression at the top, then most-recently-reconfirmed).
-- Uses debug_regression_observations_app_idx (00436).
SELECT deployment_id, route,
       p95_ms, p95_base_ms, affected_count,
       regression_factor, first_detected_at, last_detected_at,
       CASE
           WHEN state = 'dismissed'
                AND dismissed_until IS NOT NULL
                AND dismissed_until <= now() THEN 'active'
           ELSE state
       END AS state,
       acknowledged_at, dismissed_until, resolved_at
FROM debug_regression_observations
WHERE app_id = $1
  AND last_detected_at > now() - $2::interval
  AND state <> 'resolved'
  AND (state <> 'dismissed' OR dismissed_until IS NULL OR dismissed_until <= now())
ORDER BY regression_factor DESC, last_detected_at DESC;

-- name: ApplyRegressionAction :one
-- Change only the debugger workflow state for one app-scoped observation.
-- The handler maps reopen to active before calling this query. Reopening a
-- non-active observation starts a new detection lifecycle so the transition
-- webhook gets its own stable id.
UPDATE debug_regression_observations
SET state = $4,
    first_detected_at = CASE
        WHEN $4 = 'active' AND state <> 'active' THEN now()
        ELSE first_detected_at
    END,
    last_detected_at = CASE WHEN $4 = 'active' THEN now() ELSE last_detected_at END,
    acknowledged_at = CASE WHEN $4 = 'acknowledged' THEN now() ELSE NULL END,
    dismissed_until = CASE WHEN $4 = 'dismissed' THEN $5::timestamptz ELSE NULL END,
    resolved_at = CASE WHEN $4 = 'resolved' THEN now() ELSE NULL END
WHERE app_id = $1
  AND deployment_id = $2
  AND route = $3
RETURNING app_id, deployment_id, route,
          p95_ms, p95_base_ms, affected_count,
          regression_factor, first_detected_at, last_detected_at,
          state, acknowledged_at, dismissed_until, resolved_at;

-- name: ResolveStaleRegressionObservations :many
-- A detector pass that no longer sees a regression resolves the previous
-- observation. Returning rows lets apid publish one account-scoped event per
-- lifecycle transition without a second read.
UPDATE debug_regression_observations
SET state = 'resolved',
    resolved_at = COALESCE(resolved_at, now())
WHERE last_detected_at <= now() - $1::interval
  AND state <> 'resolved'
RETURNING app_id, deployment_id, route,
          p95_ms, p95_base_ms, affected_count,
          regression_factor, first_detected_at, last_detected_at,
          state, acknowledged_at, dismissed_until, resolved_at;

-- name: ListDeploymentsForCompare :many
-- Backs the dashboard compare panel's two `<select>` dropdowns: "pick
-- source deployment" and "pick mirror deployment". GROUP BY on
-- request_telemetry reads the distinct deployment_ids that have
-- shipped traffic in the window. first_seen/last_seen row_count give
-- the dashboard enough metadata to render "v81 — 17m of traffic,
-- 4123 rows" without a second query.
--
-- PR-B uses this to seed the dropdowns; the actual percentile
-- comparison is two RequestTelemetryBaselineP95ByRoute calls in Go
-- (PR-A ships that query; the regression cron uses the same shape).
SELECT deployment_id,
       MIN(received_at) AS first_seen,
       MAX(received_at) AS last_seen,
       COUNT(*)         AS row_count
FROM request_telemetry
WHERE app_id = $1
  AND received_at > now() - $2::interval
GROUP BY deployment_id
ORDER BY last_seen DESC
LIMIT $3;

-- name: ListAppsWithRecentTelemetry :many
-- Backs the regression cron's discovery loop. Walks all apps with
-- >=1 row in the regression window so the cron doesn't have to query
-- the full `apps` table on every tick. Returns the app_id and slug
-- (the cron needs slug for log lines; the rest of the cron needs
-- app_id to bound RequestTelemetryBaselineP95ByRoute / ByDeployment).
-- Index: request_telemetry_app_received_idx on (app_id, received_at
-- DESC) makes this DISTINCT scan cheap.
SELECT DISTINCT app_id
FROM request_telemetry
WHERE received_at > now() - $1::interval;

-- name: UpdateSpansSummary :exec
-- ADR-127 PR-D: writer for spans_summary jsonb. The gatewayd-public
-- OTLP/HTTP handler coalesces incoming batches for the same trace_id
-- in-process (pkg/gateway/spans_accumulator.go) and flushes the
-- accumulated summary every FAAS_OTEL_FLUSH_INTERVAL (default 30s).
-- UPDATE (not INSERT) because the row already exists — the recorder
-- wrote it from the gateway edge (pkg/gateway/request_telemetry_publisher.go).
-- Multiple trusted producers can contribute to one trace (for example,
-- gatewayd-internal service bindings and outboundd provider calls), so merge
-- by span identity instead of allowing a later writer to erase earlier spans.
-- Keep the slowest 1000 unique spans, the Scale-tier maximum, to bound storage.
-- $N::jsonb cast is load-bearing — without it sqlc binds as text and
-- Postgres raises SQLSTATE 22P02 (invalid_text_representation).
--
-- PR-D code-review #1: the WHERE clause now also pins account_id.
-- Defense in depth against cross-customer overwrite. The
-- gateway-side accumulator already rejects trace_id/account_id
-- mismatches (pkg/gateway/spans_accumulator.go ErrAccountMismatch),
-- and the apid gRPC handler forwards the same account_id, but a
-- bug at any layer could otherwise let account A's flush wipe
-- account B's row. Adding the account_id = $3::uuid predicate
-- makes cross-customer overwrite impossible regardless of which
-- upstream guard fails. The composite (trace_id, account_id)
-- lookup still hits request_telemetry_trace_idx for the trace_id
-- selectivity; the residual account_id check is a post-fetch
-- row-level filter (one row, microseconds).
update request_telemetry as target
   set spans_summary = (
       select coalesce(jsonb_agg(bounded.span order by bounded.duration_nanos desc, bounded.span_id), '[]'::jsonb)
         from (
           select span, duration_nanos, span_id
             from (
               select distinct on (span_id, end_time_unix_nano)
                      span, duration_nanos, span_id
                 from (
                   select item as span,
                          coalesce(item->>'span_id', '') as span_id,
                          coalesce(item->>'end_time_unix_nano', '') as end_time_unix_nano,
                          case when coalesce(item->>'duration_nanos', '') ~ '^[0-9]{1,20}$'
                               then (item->>'duration_nanos')::numeric
                               else 0::numeric end as duration_nanos
                     from jsonb_array_elements(
                       (case when jsonb_typeof(target.spans_summary) = 'array'
                             then target.spans_summary else '[]'::jsonb end)
                       ||
                       (case when jsonb_typeof($2::jsonb) = 'array'
                             then $2::jsonb else '[]'::jsonb end)
                     ) as source(item)
                 ) normalized
                order by span_id, end_time_unix_nano, duration_nanos desc
             ) deduplicated
            order by duration_nanos desc, span_id
            limit 1000
         ) bounded
   )
 where target.trace_id = $1
   and target.account_id = $3::uuid
   and target.received_at >= now() - interval '24 hours';

-- ---------------------------------------------------------------------------
-- Issue #246 acceptance item 7 — hard-bounce + complaint suppression list
-- (ADR-115 §D.3, RFC 8058 follow-on). One row per (source,
-- provider_event_id) so Resend's webhook redelivery dedupes to the
-- same row instead of double-suppressing. The unique index is the
-- dedupe key. Schema lives in migrations/00562_mail_suppressions.sql.
-- ---------------------------------------------------------------------------

-- name: RecordMailSuppression :one
-- INSERT with ON CONFLICT (source, provider_event_id) DO UPDATE so
-- the Resend webhook redelivery is idempotent. RETURNING (xmax = 0)
-- exposes the canonical "fresh insert vs replay" signal — the
-- bounce handler (pkg/meter/bounce_handler.go) reads it to decide
-- whether to advance dunning (fresh) or skip (replay). The SET
-- clause is intentionally a no-op rewrite of email: the row's
-- contents are already correct, but Postgres needs an UPDATE arm
-- to fire RETURNING when the conflict hits.
--
-- $1 = account_id (nullable — the bounce handler may not have
--      correlated the address to an account yet)
-- $2 = email
-- $3 = reason (closed: hard_bounce / complaint / manual)
-- $4 = source (closed: resend / postmark / operator)
-- $5 = provider_event_id
-- $6 = expires_at (nullable — null means suppression is permanent
--      until operator override; non-null is the TTL deadline)
INSERT INTO mail_suppressions (
    account_id, email, reason, source, provider_event_id, expires_at
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (source, provider_event_id) DO UPDATE
SET email = EXCLUDED.email
RETURNING (xmax = 0) AS inserted;

-- name: IsMailSuppressed :one
-- Returns true if any active suppression matches the address.
-- "Active" means expires_at IS NULL OR expires_at > now(); the
-- partial index mail_suppressions_active_email_idx keeps expired
-- rows out so a row that fell out of TTL doesn't block future
-- mail to that address. Lower-casing on both sides makes the
-- match case-insensitive (Postfix accepts mixed case; the
-- providers' bounce webhooks do too).
--
-- $1 = email
SELECT EXISTS (
    SELECT 1 FROM mail_suppressions
    WHERE lower(email) = lower($1)
      AND (expires_at IS NULL OR expires_at > now())
) AS suppressed;

-- ----------------------------------------------------------------------
-- NodeLifecycleStore (Workstream B, issue #1184)
--
-- 12 queries wrap the recovery arbiter's DB I/O. The arbiter reads via
-- NodeGet/NodeList/NodeListRecoverable/NodeListDrainable and writes via
-- NodeSetLifecycle (CAS on the prior lifecycle, so two competing writers
-- can't race from 'active'→'draining' vs 'active'→'unavailable'). Drain
-- initiation stamps `drain_initiated_at`; the drain-complete sweep marks
-- `drain_completed_at` once the last live instance migrates or recreates.
--
-- DeploymentRecordSnapshotMiss / DeploymentClearSnapshotBackoff are the
-- per-deployment backoff state added by 00585 — wake flow records a
-- miss and stamps a `Retry-After` until, the recovery arbiter clears
-- it after a successful migrate-or-recreate sweep. Partial index
-- `deployments_snapshot_backoff_idx` makes the wake-side check an
-- index-only scan.
-- ----------------------------------------------------------------------

-- name: NodeGet :one
-- Resolve a single compute_nodes row by its UUID. Used by the recovery
-- arbiter's per-tick decision and by the apid drain handler.
SELECT
    id, name, target_url, vpcpus, mem_mb, max_concurrency,
    admission_ceiling_mb,
    lifecycle::text AS lifecycle, active, last_heartbeat_at, created_at,
    region, zone, schedd_target_url, vcpu_budget, public_ip, public_ip_set_at,
    release_id, manifest_hash, host_certificate, cert_fingerprint, role,
    generation, gateway_target_url,
    drain_initiated_at, drain_completed_at, recovery_initiated_at,
    last_recovery_outcome
FROM compute_nodes
WHERE id = $1;

-- name: NodeGetByName :one
-- Same as NodeGet but by the human-stable name. The apid handler
-- (POST /v1/compute-nodes/{name}/drain) and the recovery arbiter's
-- cold-start reconciliation path both key by name because operator
-- input is name-based.
SELECT
    id, name, target_url, vpcpus, mem_mb, max_concurrency,
    admission_ceiling_mb,
    lifecycle::text AS lifecycle, active, last_heartbeat_at, created_at,
    region, zone, schedd_target_url, vcpu_budget, public_ip, public_ip_set_at,
    release_id, manifest_hash, host_certificate, cert_fingerprint, role,
    generation, gateway_target_url,
    drain_initiated_at, drain_completed_at, recovery_initiated_at,
    last_recovery_outcome
FROM compute_nodes
WHERE name = $1;

-- name: NodeList :many
-- All nodes, optionally filtered by lifecycle. The recovery arbiter
-- passes NULL to enumerate every row on cold-start reconciliation;
-- the placement filter passes lifecycle='active' (via the existing
-- `WHERE active = true` partial-index path — unchanged). $1 is the
-- lifecycle filter; pass the empty string for "any".
SELECT
    id, name, target_url, vpcpus, mem_mb, max_concurrency,
    admission_ceiling_mb,
    lifecycle::text AS lifecycle, active, last_heartbeat_at, created_at,
    region, zone, schedd_target_url, vcpu_budget, public_ip, public_ip_set_at,
    release_id, manifest_hash, host_certificate, cert_fingerprint, role,
    generation, gateway_target_url,
    drain_initiated_at, drain_completed_at, recovery_initiated_at,
    last_recovery_outcome
FROM compute_nodes
WHERE ($1 = '' OR lifecycle::text = $1)
ORDER BY name;

-- name: NodeSetLifecycle :execrows
-- CAS lifecycle transition. Returns 0 if the prior state didn't match
-- $2 (i.e. another writer raced us) — the caller treats that as a
-- soft no-op and re-reads via NodeGet. Returns 1 on success.
--
-- Args:
--   $1 = node id (UUID)
--   $2 = expected prior lifecycle text
--   $3 = new lifecycle text
--   $4 = wall-clock timestamp to stamp on the relevant audit column:
--        'draining' | 'force_draining' → drain_initiated_at
--        'unavailable'     → NULL (heartbeat gap is the writer; this
--                             path is for the rare explicit flip)
--        'recovering'      → recovery_initiated_at
--        'maintenance'     → drain_completed_at (last step of a
--                             successful operator drain)
UPDATE compute_nodes
SET lifecycle = $3::compute_node_lifecycle,
    drain_initiated_at    = CASE WHEN $3::compute_node_lifecycle IN ('draining','force_draining') THEN $4 ELSE drain_initiated_at END,
    recovery_initiated_at = CASE WHEN $3::compute_node_lifecycle = 'recovering' THEN $4 ELSE recovery_initiated_at END,
    drain_completed_at    = CASE WHEN $3::compute_node_lifecycle = 'maintenance' THEN $4 ELSE drain_completed_at    END
WHERE id = $1
  AND lifecycle::text = $2;

-- name: NodeMarkDrainCompleted :execrows
-- Stamps drain_completed_at + flips lifecycle='maintenance'. Called once
-- the drain arbiter confirms zero live instances remain on the node.
-- CAS on a draining lifecycle so a concurrent reactivate can't race.
UPDATE compute_nodes
SET lifecycle         = 'maintenance',
    drain_completed_at = $2
WHERE id = $1
  AND lifecycle IN ('draining','force_draining');

-- name: NodeMarkRecovered :execrows
-- Stamps last_recovery_outcome='succeeded' and flips lifecycle='active'.
-- Called by the recovery arbiter after the migrate-or-recreate sweep
-- has cleared all stranded instances on a 'recovering' node. CAS on
-- 'recovering' so a fresh heartbeat-driven reactivate wins cleanly.
UPDATE compute_nodes
SET lifecycle            = 'active',
    last_recovery_outcome = 'succeeded'
WHERE id = $1
  AND lifecycle = 'recovering';

-- name: NodeListRecoverable :many
-- Nodes that need the recovery arbiter's attention. Two lifecycle
-- states qualify:
--   'unavailable'  → heartbeat gap detected; instances stranded.
--   'recovering'   → first post-failure ping succeeded; sweep to
--                    confirm zero stranded instances.
-- Unavailable rows age out of active polling after 24 hours. They remain in
-- inventory for audit; a returning vmmd re-registers through the heartbeat
-- path and becomes active again.
SELECT
    id, name, target_url, vpcpus, mem_mb, max_concurrency,
    admission_ceiling_mb,
    lifecycle::text AS lifecycle, active, last_heartbeat_at, created_at,
    region, zone, schedd_target_url, vcpu_budget, public_ip, public_ip_set_at,
    release_id, manifest_hash, host_certificate, cert_fingerprint, role,
    generation, gateway_target_url,
    drain_initiated_at, drain_completed_at, recovery_initiated_at,
    last_recovery_outcome
FROM compute_nodes
WHERE lifecycle = 'recovering'
   OR (lifecycle = 'unavailable' AND coalesce(last_heartbeat_at, created_at) >= now() - interval '24 hours')
ORDER BY name;

-- name: NodeListDrainable :many
-- Drainable candidates: lifecycle='active' AND zero live instances.
-- The drain handler refuses to flip lifecycle='draining' for nodes
-- with active traffic (it surfaces RFC 7807 `node_draining_refused`
-- instead) and waits for the operator to clear the load first; this
-- query is the "safe to drain right now" enumeration.
SELECT
    id, name, target_url, vpcpus, mem_mb, max_concurrency,
    admission_ceiling_mb,
    lifecycle::text AS lifecycle, active, last_heartbeat_at, created_at,
    region, zone, schedd_target_url, vcpu_budget, public_ip, public_ip_set_at,
    release_id, manifest_hash, host_certificate, cert_fingerprint, role,
    generation, gateway_target_url,
    drain_initiated_at, drain_completed_at, recovery_initiated_at,
    last_recovery_outcome
FROM compute_nodes
WHERE lifecycle = 'active'
  AND NOT EXISTS (
      SELECT 1 FROM instances
      WHERE instances.node_id = compute_nodes.id
        AND instances.state IN ('running', 'cold_booting', 'waking', 'draining', 'snapshotting', 'migrating', 'warm')
  )
ORDER BY name;

-- name: InstanceListByNodeForRecovery :many
-- Live instances on a specific node — input to the arbiter's
-- per-instance decision. Limited to states the arbiter can act on:
-- 'running' (live-migrate), 'cold_booting' (recreate, the snapshot
-- may not have made it to the destination yet), 'waking' (recreate —
-- same reason). The arbiter only needs the
-- (app_id, deployment_id, state, id) tuple — account_id is reachable
-- via the existing app/deployment joins if needed by downstream
-- code, but the per-tick hot loop doesn't pay for it here.
SELECT id, state, app_id, deployment_id, kind
FROM instances
WHERE node_id = $1
  AND state IN ('running', 'cold_booting', 'waking', 'draining', 'snapshotting', 'migrating', 'warm')
ORDER BY started_at;

-- name: DeploymentRecordSnapshotMiss :exec
-- Bump snapshot_miss_count + stamp Retry-After until. Called by the
-- wake flow when the snapshot-fetch path fails (stale cache, missing
-- replica on the destination, etc.). Capped-exponential backoff math
-- lives in pkg/sched/snapshot_backoff.go; this query is the state
-- write only.
--
-- $1 = deployment id
-- $2 = backoff_until timestamp
UPDATE deployments
SET snapshot_miss_count          = snapshot_miss_count + 1,
    snapshot_miss_last_at        = now(),
    snapshot_miss_backoff_until  = $2
WHERE id = $1;

-- name: DeploymentClearSnapshotBackoff :exec
-- Called by the recovery arbiter after a successful migrate-or-
-- recreate sweep has restored the destination's snapshot set, OR by
-- the wake flow on a successful cold boot. Resets the counter and
-- clears the backoff_until so future wakes don't short-circuit.
UPDATE deployments
SET snapshot_miss_count         = 0,
    snapshot_miss_last_at       = NULL,
    snapshot_miss_backoff_until = NULL
WHERE id = $1;

-- name: DeploymentSnapshotBackoffActive :one
-- The wake-side gate. Returns the row while a backoff timestamp is
-- present; the store computes whether it is still active. Returning
-- expired rows preserves the miss count for the next backoff stamp.
-- The partial index `deployments_snapshot_backoff_idx` covers this lookup.
SELECT snapshot_miss_count, snapshot_miss_backoff_until
FROM deployments
WHERE id = $1
  AND snapshot_miss_backoff_until IS NOT NULL;

-- =====================================================================

-- name: CreateUploadSession :one
-- Inserts a fresh upload_sessions row. The handler pre-validates
-- total_size against limits.SourceTarballMaxMB (pkg/api/limits.go)
-- and the per-account open-session cap (5 per (account_id, app_slug))
-- before this INSERT — sqlc only owns the type-safe binding. The
-- 1-GiB hard ceiling in the SQL CHECK is the worst-case spool size,
-- not the customer-facing quota.
INSERT INTO upload_sessions (
    id, account_id, app_slug, total_size, chunk_size, sha256_hex, part_path, deploy_options
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING id, account_id, app_slug, total_size, received_bytes, chunk_size,
          sha256_hex, part_path, status, created_at, last_patched_at, expires_at,
          deployment_id, deploy_options;

-- name: GetUploadSession :one
-- Reads a single upload_sessions row by id. Used by:
--   (a) the handler's POST /commit pre-check (validate status='open',
--       received_bytes == total_size before validating tar shape);
--   (b) the CLI's GET-when-resuming-after-network-drop path
--       (PR-2 cmd/gregale/upload_session.go) which learns the
--       server's current received_bytes to compute the next chunk's
--       Upload-Offset header.
-- No FOR UPDATE here — the row is append-only under normal operation
-- and the CAS in AppendUploadBytes is the serialisation point. If
-- future work needs a transactional read-modify-write (e.g., admin
-- force-close), add a separate GetUploadSessionForUpdate :one.
SELECT id, account_id, app_slug, total_size, received_bytes, chunk_size,
       sha256_hex, part_path, status, created_at, last_patched_at, expires_at,
       deployment_id, deploy_options
FROM upload_sessions
WHERE id = $1;

-- name: AppendUploadBytes :one
-- The atomic CAS that makes the resumable protocol safe under
-- concurrent PATCHes on the same upload_id. The handler reads
-- the client's Upload-Offset header (the offset the client claims
-- the server is currently at) and the chunk_size it sent, then
-- computes expected_new = client_offset + chunk_bytes. The WHERE
-- clause pins the row to the upload id and the explicitly named expected
-- offset — a row whose received_bytes has already
-- advanced (e.g., a racing PATCH from a retry) returns 0 rows and
-- the handler maps that to 409 Conflict with the actual current
-- offset in the body.
--
-- RETURNING exposes the new received_bytes so the handler doesn't
-- need a follow-up SELECT on the happy path. last_patched_at is
-- bumped to now() so the reaper's idle-aware expiry (NOT in PR-1;
-- deferred — see plan "Out of scope") has a fresh anchor.
UPDATE upload_sessions
   SET received_bytes = sqlc.arg(new_received_bytes),
       last_patched_at = now()
 WHERE id = sqlc.arg(id)
   AND status = 'open'
   AND expires_at > now()
   AND received_bytes = sqlc.arg(expected_received_bytes)
RETURNING received_bytes, total_size;

-- name: MarkUploadSessionCommitted :one
-- Final state transition: open → committed. The handler runs
-- validateTarballShape + scanForStatefulShape (cmd/apid/
-- deploy_inputs.go:291-447) BEFORE this UPDATE so a commit that
-- fails validation leaves the row at status='open' and the .part
-- file in place for retry. deployment_id is set after the build
-- row is enqueued (apidsource.Enqueue) so the row points at the
-- deployment that consumed the .part.
--
-- The UPDATE WHERE status='open' is the second-line idempotency
-- guard: a retry of POST /v1/uploads/{id}/commit that races with
-- itself hits 0 rows and the handler reads upload_commit_outcomes
-- to return the original deployment_id.
UPDATE upload_sessions
   SET status = 'committed',
       deployment_id = $2
 WHERE id = $1
   AND status = 'open'
RETURNING id, status, deployment_id;

-- name: CancelUploadSession :exec
-- Explicit cancel from DELETE /v1/uploads/{id}. The handler also
-- removes the .part file via os.Remove AFTER the UPDATE commits —
-- doing it before would leak a file if the UPDATE rolled back.
-- Status transition is open → cancelled; a second cancel or a
-- commit-after-cancel hits 0 rows and the handler returns 409
-- upload_session_already_cancelled.
UPDATE upload_sessions
   SET status = 'cancelled'
 WHERE id = $1
   AND account_id = $2::uuid
   AND status = 'open';

-- name: ReapExpiredUploadSessions :many
-- The reaper's scan query (cmd/apid/upload_session_reaper.go).
-- Returns at most 100 rows per invocation to bound memory; the
-- goroutine ticker at cmd/apid/main.go re-invokes on its 5-minute
-- cadence. partial index upload_sessions_expires_idx makes this
-- an index-only scan over the open sessions whose expires_at has
-- passed. The handler then UPDATEs status='expired' and removes
-- the .part file via os.Remove.
--
-- The status='open' predicate is load-bearing — once a session is
-- committed/cancelled/expired the .part file is already gone and
-- the row is terminal.
SELECT id, part_path
FROM upload_sessions
WHERE status = 'open'
  AND expires_at < now()
ORDER BY expires_at ASC
LIMIT 100;

-- name: ReapStaleUploadPartFiles :many
-- PR-1 fixup #5: sweep .part files for terminal rows whose
-- builderd consumption window has closed. The commit handler
-- leaves .part in place for builderd to consume
-- (pkg/builderd/builderd.go:407 hashFile(SourcePath)); the
-- cancel handler removes its .part at the same time it flips
-- status='cancelled'; but neither has a 1-hour cleanup guarantee
-- for committed rows. This query returns rows in terminal
-- status whose last_patched_at is >1h old and whose part_path
-- cleanup marker is still set; the reaper removes the file and
-- clears the marker after a successful removal.
--
-- The status IN (committed, cancelled, expired) predicate is
-- load-bearing — we never sweep open sessions (could race a
-- PATCH). The last_patched_at < now() - '1 hour' guard stops
-- us from racing a builderd that's mid-consumption right after
-- commit. The 100-row LIMIT bounds the per-tick work the same
-- way ReapExpiredUploadSessions does.
--
-- Index strategy: pg doesn't have an index on (status,
-- last_patched_at) today; the scan is sequential over the
-- terminal-status rows. At expected volumes (≪ 1k terminal
-- rows/day per apid) this is fine. If terminal-row volume
-- grows, add a partial index on (last_patched_at) WHERE
-- status IN ('committed', 'cancelled', 'expired') — leaving
-- as a follow-up ADR rather than conflated into PR-1's
-- migration slot 533.
SELECT id, part_path
FROM upload_sessions
WHERE status IN ('committed', 'cancelled', 'expired')
  AND part_path <> ''
  AND last_patched_at < now() - INTERVAL '1 hour'
ORDER BY last_patched_at ASC
LIMIT 100;

-- name: ClearUploadSessionPartPath :exec
-- Records that the spool file has been removed. Terminal status is
-- required so an out-of-order cleanup call cannot hide the path of
-- an open session that a concurrent PATCH still needs.
UPDATE upload_sessions
   SET part_path = ''
 WHERE id = $1
   AND status IN ('committed', 'cancelled', 'expired')
   AND part_path <> '';

-- name: ExpireUploadSession :exec
-- Marks a single session as expired after the reaper removes its
-- .part file. Split into a separate query from ReapExpiredUploadSessions
-- so the reaper can: (a) scan, (b) delete the file, (c) UPDATE.
-- If (c) fails the row stays at status='open' and the next reaper
-- tick re-runs against it — the file is already gone, so os.Remove
-- returns ErrNotExist and is logged + skipped. This avoids the
-- alternative of a single UPDATE ... RETURNING part_path that
-- would race the file delete across two replicas (single-process
-- for now; future multi-replica deployment needs SELECT ... FOR
-- UPDATE SKIP LOCKED).
UPDATE upload_sessions
   SET status = 'expired'
 WHERE id = $1
   AND status = 'open';

-- name: RecordUploadCommitOutcome :one
-- INSERT ON CONFLICT DO NOTHING for the upload_commit_outcomes
-- companion table. The handler calls this AFTER a successful
-- apidsource.Enqueue and BEFORE writing the 201 response. On
-- retry of POST /v1/uploads/{id}/commit (network blip after the
-- server wrote the row but before the client got the response),
-- the INSERT hits the conflict path and returns 0 rows; the
-- handler then calls GetUploadCommitOutcome to return the
-- original deployment_id. ON CONFLICT DO NOTHING (rather than
-- DO UPDATE) is correct: the original row is canonical.
INSERT INTO upload_commit_outcomes (upload_id, deployment_id, build_id)
VALUES ($1, $2, $3)
ON CONFLICT (upload_id) DO NOTHING
RETURNING upload_id, deployment_id, build_id, finalized_at;

-- name: GetUploadCommitOutcome :one
-- Reads the dedupe row for a retry of POST /v1/uploads/{id}/commit.
-- Returns 0 rows if the original commit never wrote (handler
-- surfaces this as 500 — the prior UPDATE MarkUploadSessionCommitted
-- also failed, so the operator needs the build row's failure
-- class).
SELECT upload_id, deployment_id, build_id, finalized_at
FROM upload_commit_outcomes
WHERE upload_id = $1;

-- name: CountOpenUploadSessionsByAccountApp :one
-- Per-(account_id, app_slug) open-session cap check at the top of
-- POST /v1/uploads. Returns the current count; the handler
-- refuses with 429 upload_session_too_many when count >= 5.
-- Hits the partial index upload_sessions_account_open_idx.
SELECT COUNT(*)::bigint AS count
FROM upload_sessions
WHERE account_id = $1::uuid
  AND app_slug = $2
  AND status = 'open';

-- name: SumOpenUploadSessionBytesByAccount :one
-- Per-account open-spool budget check (4 × SourceTarballMaxMB cap
-- per plan). The handler sums the declared total_size across all
-- open sessions for the account, adds the new total_size, and
-- refuses with 429 upload_session_too_many if the sum exceeds
-- the budget. Hits upload_sessions_account_open_idx for the
-- (account_id) predicate; the SUM is over the partial index.
SELECT COALESCE(SUM(total_size), 0)::bigint AS bytes
FROM upload_sessions
WHERE account_id = $1::uuid
  AND status = 'open';

-- name: ObjectBucketLockApp :one
SELECT id FROM apps WHERE id = $1 AND account_id = $2 AND status <> 'deleted' FOR UPDATE;

-- name: ObjectBucketByName :one
SELECT * FROM object_buckets WHERE app_id = $1 AND account_id = $2 AND name = $3 AND scope = $4 AND state <> 'deleted';

-- name: ObjectBucketCount :one
SELECT count(*) FROM object_buckets WHERE app_id = $1 AND state <> 'deleted';

-- name: ObjectBucketCountForAccount :one
SELECT count(*) FROM object_buckets WHERE account_id = $1 AND state <> 'deleted';

-- name: ObjectBucketPruneTombstones :exec
DELETE FROM object_buckets WHERE account_id = $1 AND state = 'deleted';

-- name: ObjectBucketInsert :one
INSERT INTO object_buckets (id, account_id, app_id, name, scope, region, backend_id, backend_fingerprint, physical_name, public_read, serve_at, environment_clone_source_bucket_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING *;

-- name: ObjectBucketList :many
SELECT * FROM object_buckets WHERE account_id = $1 AND app_id = $2 AND state <> 'deleted' ORDER BY created_at, id;

-- name: ObjectBucketGet :one
SELECT * FROM object_buckets WHERE account_id = $1 AND app_id = $2 AND id = $3 AND state <> 'deleted';

-- name: ObjectBucketAccessGrantList :many
SELECT g.account_id, g.bucket_id, g.api_key_id, g.permission,
       coalesce(k.label, '')::text AS key_label, k.status AS key_status,
       g.created_at, g.updated_at
FROM object_storage_access_grants g
JOIN api_keys k ON k.id = g.api_key_id AND k.account_id = g.account_id
JOIN object_buckets b ON b.id = g.bucket_id AND b.account_id = g.account_id
WHERE g.account_id = $1 AND g.bucket_id = $2 AND b.state <> 'deleted'
ORDER BY g.created_at, g.api_key_id;

-- name: ObjectBucketAccessGrantGet :one
SELECT g.account_id, g.bucket_id, g.api_key_id, g.permission,
       coalesce(k.label, '')::text AS key_label, k.status AS key_status,
       g.created_at, g.updated_at
FROM object_storage_access_grants g
JOIN api_keys k ON k.id = g.api_key_id AND k.account_id = g.account_id
JOIN object_buckets b ON b.id = g.bucket_id AND b.account_id = g.account_id
WHERE g.account_id = $1 AND g.bucket_id = $2 AND g.api_key_id = $3
  AND b.state <> 'deleted';

-- name: ObjectBucketAccessGrantUpsert :execrows
INSERT INTO object_storage_access_grants (account_id, bucket_id, api_key_id, permission)
SELECT b.account_id, b.id, k.id, sqlc.arg(permission)::text
FROM object_buckets b
JOIN api_keys k ON k.account_id = b.account_id
WHERE b.account_id = sqlc.arg(account_id) AND b.id = sqlc.arg(bucket_id)
  AND b.state <> 'deleted' AND k.id = sqlc.arg(api_key_id)
  AND k.status IN ('active', 'grace')
  AND NOT ('admin' = ANY(k.scopes))
  AND (sqlc.arg(permission)::text <> 'read' OR k.scopes @> ARRAY['storage:read']::text[])
  AND (sqlc.arg(permission)::text <> 'write' OR k.scopes @> ARRAY['storage:write']::text[])
  AND (sqlc.arg(permission)::text <> 'read_write' OR k.scopes @> ARRAY['storage:read', 'storage:write']::text[])
ON CONFLICT (bucket_id, api_key_id) DO UPDATE
SET permission = EXCLUDED.permission, updated_at = now();

-- name: ObjectBucketAccessGrantDelete :execrows
DELETE FROM object_storage_access_grants g
USING object_buckets b, api_keys k
WHERE g.account_id = $1 AND g.bucket_id = $2 AND g.api_key_id = $3
  AND b.id = g.bucket_id AND b.account_id = g.account_id AND b.state <> 'deleted'
  AND k.id = g.api_key_id AND k.account_id = g.account_id;

-- name: ObjectBucketAccessCheck :one
SELECT EXISTS (
    SELECT 1
    FROM object_storage_access_grants g
    JOIN object_buckets b ON b.id = g.bucket_id AND b.account_id = g.account_id
    JOIN api_keys k ON k.id = g.api_key_id AND k.account_id = g.account_id
    WHERE g.account_id = $1 AND g.bucket_id = $2 AND g.api_key_id = $3
      AND b.state <> 'deleted' AND k.status IN ('active', 'grace')
      AND (($4::text = 'read' AND g.permission IN ('read', 'read_write'))
        OR ($4::text = 'write' AND g.permission IN ('write', 'read_write')))
      AND (($4::text = 'read' AND k.scopes @> ARRAY['storage:read']::text[])
        OR ($4::text = 'write' AND k.scopes @> ARRAY['storage:write']::text[]))
) AS allowed;

-- name: ObjectBucketListForKey :many
SELECT b.*
FROM object_buckets b
JOIN object_storage_access_grants g
  ON g.bucket_id = b.id AND g.account_id = b.account_id
JOIN api_keys k ON k.id = g.api_key_id AND k.account_id = g.account_id
WHERE b.account_id = $1 AND b.app_id = $2 AND g.api_key_id = $3
  AND b.state <> 'deleted' AND k.status IN ('active', 'grace')
ORDER BY b.created_at, b.id;

-- name: ObjectBucketClaim :one
UPDATE object_buckets SET state = $1, lease_token = $2, lease_until = now() + ($3::int * interval '1 second'), updated_at = now(),
attempt_count = CASE WHEN state <> $1 THEN 1 ELSE least(attempt_count + 1, 30) END,
last_error_code = CASE WHEN state <> $1 THEN '' ELSE last_error_code END, retry_at = now()
WHERE object_buckets.account_id = $4 AND object_buckets.app_id = $5 AND object_buckets.id = $6
AND object_buckets.state <> 'deleted' AND (object_buckets.lease_until IS NULL OR object_buckets.lease_until < now())
AND ($1 = 'deleting' OR object_buckets.state = 'provisioning')
AND ($1 <> 'deleting' OR NOT EXISTS (
  SELECT 1 FROM object_storage_multipart_uploads m WHERE m.bucket_id = object_buckets.id
  AND m.state IN ('initiating','active','completing','aborting')
))
AND (NOT sqlc.arg(recovery)::boolean OR object_buckets.state = $1)
AND (object_buckets.retry_at <= now() OR object_buckets.state <> $1) RETURNING *;

-- name: ObjectBucketFinish :execrows
UPDATE object_buckets SET state = $1, lease_token = NULL, lease_until = NULL, updated_at = now(),
attempt_count = 0, last_error_code = '', retry_at = now() WHERE id = $2 AND lease_token = $3;

-- name: ObjectBucketRetry :execrows
UPDATE object_buckets SET lease_token = NULL, lease_until = NULL, updated_at = now(),
last_error_code = $3, retry_at = now() + ($4::int * interval '1 second')
WHERE id = $1 AND lease_token = $2 AND state IN ('provisioning', 'deleting');

-- name: ObjectBucketsDue :many
SELECT * FROM object_buckets
WHERE (state = 'deleting' OR (sqlc.arg(include_provisioning)::boolean AND state = 'provisioning'))
AND retry_at <= now() AND (lease_until IS NULL OR lease_until < now())
ORDER BY retry_at, id LIMIT sqlc.arg(batch_limit)::int;

-- name: SnapshotLocalityNodes :many
SELECT node_id::text AS node_id, true AS is_origin
FROM snapshot_origins
WHERE snapshot_id = $1::uuid AND node_id IS NOT NULL
UNION ALL
SELECT node_id::text AS node_id, false AS is_origin
FROM snapshot_replicas
WHERE snapshot_id = $1::uuid AND state = 'ready'
ORDER BY node_id, is_origin DESC;

-- name: ObjectUsageLockAccount :one
SELECT id FROM accounts WHERE id = $1 FOR UPDATE;

-- name: ObjectUsageBucketAccount :one
SELECT account_id FROM object_buckets WHERE id=$1;

-- name: ObjectUsageBuckets :many
SELECT b.*, u.baseline_bytes, u.baseline_keys, u.granted_bytes, u.granted_keys,
u.observed_bytes, u.observed_keys, u.observed_at, u.attempt_at, u.lease_until AS inventory_lease_until, u.token
FROM object_buckets b LEFT JOIN object_storage_bucket_usage u ON u.bucket_id = b.id
WHERE b.account_id = $1;

-- name: ObjectUsageGrant :one
SELECT max_bytes FROM object_storage_key_grants WHERE bucket_id = $1 AND key_hash = $2;

-- name: ObjectUsageGrantUpsert :exec
INSERT INTO object_storage_key_grants (bucket_id, key_hash, max_bytes) VALUES ($1,$2,$3)
ON CONFLICT (bucket_id,key_hash) DO UPDATE SET max_bytes = greatest(object_storage_key_grants.max_bytes, EXCLUDED.max_bytes);

-- name: ObjectUsageGrantIncrement :exec
UPDATE object_storage_bucket_usage SET granted_bytes = granted_bytes + $2, granted_keys = granted_keys + $3 WHERE bucket_id = $1;

-- name: ObjectUsageAuthorizationCount :one
SELECT count FROM object_storage_authorizations WHERE account_id=$1 AND period_start=$2;

-- name: ObjectUsageAuthorize :exec
INSERT INTO object_storage_authorizations (account_id, period_start, count) VALUES ($1,$2,1)
ON CONFLICT (account_id,period_start) DO UPDATE SET count = object_storage_authorizations.count + 1;

-- name: ObjectStorageProviderRequestIncrement :exec
INSERT INTO object_storage_request_metrics (bucket_id, period_start, request_count)
VALUES ($1, $2, 1)
ON CONFLICT (bucket_id, period_start) DO UPDATE
SET request_count = object_storage_request_metrics.request_count + 1;

-- name: ObjectStorageProviderEgressIncrement :exec
INSERT INTO object_storage_request_metrics (bucket_id, period_start, egress_bytes)
VALUES ($1, $2, $3)
ON CONFLICT (bucket_id, period_start) DO UPDATE
SET egress_bytes = object_storage_request_metrics.egress_bytes + EXCLUDED.egress_bytes;

-- name: ObjectStorageProviderRequestMetrics :many
SELECT b.id, b.account_id, b.backend_id, b.backend_fingerprint, b.physical_name,
       sqlc.arg(period_start)::timestamptz AS period_start, COALESCE(m.request_count, 0)::bigint AS request_count,
       COALESCE(m.egress_bytes, 0)::bigint AS egress_bytes
FROM object_buckets b
LEFT JOIN object_storage_request_metrics m
  ON m.bucket_id = b.id AND m.period_start = sqlc.arg(period_start)
WHERE b.backend_id = $1 AND b.backend_fingerprint = $2
ORDER BY b.physical_name, b.id;

-- name: ObjectStorageProviderBuckets :many
SELECT * FROM object_buckets
WHERE backend_id = $1 AND backend_fingerprint = $2
ORDER BY physical_name, id;

-- name: ObjectUsageReports :many
SELECT r.* FROM object_storage_usage_heads h JOIN object_storage_usage_reports r
USING (account_id,backend_id,period_start,observed_at)
WHERE h.account_id=$1 AND h.period_start=$2 ORDER BY h.backend_id;

-- name: ObjectUsageReportHead :exec
INSERT INTO object_storage_usage_heads (account_id,backend_id,period_start,observed_at) VALUES ($1,$2,$3,$4)
ON CONFLICT (account_id,period_start,backend_id) DO UPDATE SET observed_at=EXCLUDED.observed_at;

-- name: ObjectUsageReportGet :one
SELECT * FROM object_storage_usage_reports WHERE account_id=$1 AND backend_id=$2 AND period_start=$3 AND observed_at=$4;

-- name: ObjectUsageReportInsert :exec
INSERT INTO object_storage_usage_reports (account_id,backend_id,backend_fingerprint,source,period_start,observed_at,stored_byte_hours,request_count,egress_bytes,cost_millicents)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10);

-- name: ObjectInventoriesDue :many
SELECT b.* FROM object_buckets b LEFT JOIN object_storage_bucket_usage u ON u.bucket_id=b.id
WHERE b.state='ready' AND (u.attempt_at IS NULL OR u.attempt_at < now() - interval '5 minutes')
AND (u.lease_until IS NULL OR u.lease_until < now())
ORDER BY u.attempt_at NULLS FIRST, b.id LIMIT $1;

-- name: ObjectInventoryClaim :execrows
INSERT INTO object_storage_bucket_usage (bucket_id, attempt_at, lease_until, token)
SELECT id, now(), now()+interval '2 minutes', sqlc.arg(token)::text FROM object_buckets WHERE id=$1 AND state='ready'
ON CONFLICT (bucket_id) DO UPDATE SET attempt_at=now(),lease_until=now()+interval '2 minutes',token=EXCLUDED.token
WHERE object_storage_bucket_usage.lease_until IS NULL OR object_storage_bucket_usage.lease_until < now();

-- name: ObjectInventoryFinish :execrows
UPDATE object_storage_bucket_usage SET baseline_bytes = CASE WHEN observed_at IS NULL THEN sqlc.arg(bytes)::bigint ELSE baseline_bytes END,
baseline_keys = CASE WHEN observed_at IS NULL THEN sqlc.arg(objects)::bigint ELSE baseline_keys END,
observed_bytes=sqlc.arg(bytes),observed_keys=sqlc.arg(objects),observed_at=attempt_at,lease_until=NULL,token=''
WHERE bucket_id=$1 AND token=$2 AND lease_until > now()
AND EXISTS (SELECT 1 FROM object_buckets WHERE id=$1 AND state='ready');

-- name: ObjectInventorySample :exec
INSERT INTO object_storage_inventory_samples (token,bucket_id,observed_at,bytes,objects)
SELECT $2,u.bucket_id,u.observed_at,u.observed_bytes,u.observed_keys FROM object_storage_bucket_usage u WHERE u.bucket_id=$1;

-- name: ObjectMultipartByKey :one
SELECT * FROM object_storage_multipart_uploads
WHERE account_id=$1 AND app_id=$2 AND bucket_id=$3 AND object_key=$4
AND state IN ('initiating','active','completing','aborting');

-- name: ObjectMultipartLockBucket :one
SELECT id FROM object_buckets
WHERE id=$1 AND account_id=$2 AND app_id=$3 AND state='ready' FOR UPDATE;

-- name: ObjectMultipartCount :one
SELECT count(*) FROM object_storage_multipart_uploads
WHERE bucket_id=$1 AND state IN ('initiating','active','completing','aborting');

-- name: ObjectMultipartInsert :one
INSERT INTO object_storage_multipart_uploads
(id,account_id,app_id,bucket_id,object_key,size_bytes,part_size_bytes,part_count,content_type,object_metadata,expires_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING *;

-- name: ObjectMultipartGet :one
SELECT * FROM object_storage_multipart_uploads
WHERE account_id=$1 AND app_id=$2 AND bucket_id=$3 AND id=$4;

-- name: ObjectMultipartList :many
SELECT * FROM object_storage_multipart_uploads
WHERE account_id=$1 AND app_id=$2 AND bucket_id=$3 AND id>$4
ORDER BY id LIMIT sqlc.arg(page_limit)::int;

-- name: ObjectMultipartClaim :one
UPDATE object_storage_multipart_uploads SET
state=sqlc.arg(operation), lease_token=sqlc.arg(token),
lease_until=now()+(sqlc.arg(lease_seconds)::int * interval '1 second'),
completion_parts=CASE WHEN state='active' AND sqlc.arg(operation)::text='completing'
  THEN sqlc.arg(completion_parts)::jsonb ELSE completion_parts END,
attempt_count=CASE WHEN state<>sqlc.arg(operation)::text THEN 1 ELSE least(attempt_count+1,30) END,
last_error_code=CASE WHEN state<>sqlc.arg(operation)::text THEN '' ELSE last_error_code END,
retry_at=now(), updated_at=now()
WHERE account_id=sqlc.arg(account_id) AND app_id=sqlc.arg(app_id)
AND bucket_id=sqlc.arg(bucket_id) AND id=sqlc.arg(id)
AND (lease_until IS NULL OR lease_until<now())
AND (retry_at<=now() OR state<>sqlc.arg(operation)::text)
AND (NOT sqlc.arg(recovery)::boolean OR state=sqlc.arg(operation)::text
  OR (state='active' AND sqlc.arg(operation)::text='aborting'))
AND (
  (sqlc.arg(operation)::text='initiating' AND state='initiating' AND provider_upload_id='') OR
  (sqlc.arg(operation)::text='completing' AND state IN ('active','completing')
    AND (state<>'active' OR expires_at>now())
    AND (state<>'active' OR jsonb_array_length(sqlc.arg(completion_parts)::jsonb)>0)) OR
  (sqlc.arg(operation)::text='aborting' AND state IN ('active','aborting') AND provider_upload_id<>'')
) RETURNING *;

-- name: ObjectMultipartActivate :execrows
UPDATE object_storage_multipart_uploads SET state='active',provider_upload_id=$3,
lease_token=NULL,lease_until=NULL,attempt_count=0,last_error_code='',retry_at=now(),updated_at=now()
WHERE id=$1 AND lease_token=$2 AND state='initiating' AND $3<>'';

-- name: ObjectMultipartFinish :execrows
UPDATE object_storage_multipart_uploads SET state=$3,lease_token=NULL,lease_until=NULL,
attempt_count=0,last_error_code='',retry_at=now(),updated_at=now()
WHERE id=$1 AND lease_token=$2 AND
((state='completing' AND $3='completed') OR (state='aborting' AND $3='aborted'));

-- name: ObjectMultipartSetSize :execrows
UPDATE object_storage_multipart_uploads SET size_bytes=$3,updated_at=now()
WHERE id=$1 AND lease_token=$2 AND state='completing';

-- name: ObjectMultipartRetry :execrows
UPDATE object_storage_multipart_uploads SET lease_token=NULL,lease_until=NULL,last_error_code=$3,
retry_at=now()+($4::int * interval '1 second'),updated_at=now()
WHERE id=$1 AND lease_token=$2 AND state IN ('initiating','completing','aborting');

-- name: ObjectMultipartDue :many
SELECT * FROM object_storage_multipart_uploads
WHERE (((state IN ('initiating','completing','aborting')) AND retry_at<=now())
  OR (state='active' AND expires_at<=now()))
AND (lease_until IS NULL OR lease_until<now())
ORDER BY retry_at,id LIMIT sqlc.arg(batch_limit)::int;

-- name: ObjectS3CredentialLockBucket :one
SELECT id FROM object_buckets
WHERE id=$1 AND account_id=$2 AND state='ready' FOR UPDATE;

-- name: ObjectS3CredentialCount :one
SELECT count(*) FROM object_storage_s3_credentials
WHERE bucket_id=$1 AND status='active' AND rotation_parent_id IS NULL;

-- name: ObjectS3BindingLockApp :one
SELECT a.id FROM apps a JOIN object_buckets b ON b.app_id=a.id
WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid
  AND b.id=sqlc.arg(bucket_id)::uuid AND b.account_id=sqlc.arg(account_id)::uuid
FOR UPDATE OF a;

-- name: ObjectS3BindingSecretCount :one
SELECT count(*) FROM app_secrets WHERE account_id=$1 AND app_id=$2;

-- name: ObjectS3BindingSecretInsert :one
INSERT INTO app_secrets (account_id,app_id,scope,key,ciphertext,kid,value_hash,managed_object_storage_credential_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (app_id,scope,key) DO NOTHING RETURNING key;

-- name: ObjectS3BindingStampRuntime :exec
INSERT INTO app_runtime_config_changes (app_id,changed_at) VALUES ($1,clock_timestamp())
ON CONFLICT (app_id) DO UPDATE SET changed_at=excluded.changed_at;

-- name: ObjectS3BindingStaleSnapshots :exec
UPDATE snapshots SET stale=true
WHERE deployment_id IN (SELECT id FROM deployments WHERE app_id=$1) AND stale=false;

-- name: ObjectS3BindingRevokeLock :one
SELECT * FROM object_storage_s3_credentials
WHERE id=$1 AND account_id=$2 AND bucket_id=$3
  AND managed_app_id IS NOT NULL AND rotation_parent_id IS NULL
FOR UPDATE;

-- name: ObjectS3BindingDeleteSecrets :execrows
DELETE FROM app_secrets WHERE managed_object_storage_credential_id=$1;

-- name: ObjectS3CredentialInsert :one
INSERT INTO object_storage_s3_credentials
(id,account_id,bucket_id,access_key_id,secret_sealed,kid,label,permission,status,managed_app_id,managed_scope,managed_prefix)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'active',NULLIF($9::text,'')::uuid,NULLIF($10,''),NULLIF($11,'')) RETURNING *;

-- name: ObjectS3CredentialList :many
SELECT * FROM object_storage_s3_credentials
WHERE account_id=$1 AND bucket_id=$2 AND status='active' AND rotation_parent_id IS NULL
ORDER BY created_at,id;

-- name: ObjectS3CredentialRevoke :execrows
UPDATE object_storage_s3_credentials SET status='revoked',revoked_at=now()
WHERE (id=$1 OR rotation_parent_id=$1) AND account_id=$2 AND bucket_id=$3 AND status='active';

-- name: ObjectS3CredentialGet :one
SELECT * FROM object_storage_s3_credentials
WHERE id=$1 AND account_id=$2 AND bucket_id=$3 AND rotation_parent_id IS NULL;

-- name: ObjectS3CredentialRotationParentForUpdate :one
SELECT * FROM object_storage_s3_credentials
WHERE id=$1 AND account_id=$2 AND bucket_id=$3 AND status='active'
  AND managed_app_id IS NOT NULL AND rotation_parent_id IS NULL
FOR UPDATE;

-- name: ObjectS3CredentialRotationPending :one
SELECT rotation_wake_id::text FROM object_storage_s3_credentials
WHERE account_id=$1 AND bucket_id=$2 AND rotation_parent_id=$3 AND status='active';

-- name: ObjectS3CredentialRotationStage :one
INSERT INTO object_storage_s3_credentials
(id,account_id,bucket_id,access_key_id,secret_sealed,kid,label,permission,status,rotation_parent_id,rotation_wake_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'active',$9,$10)
RETURNING *;

-- name: ObjectS3CredentialRotationReplace :one
UPDATE object_storage_s3_credentials
SET access_key_id=$4, secret_sealed=$5, kid=$6, last_used_at=NULL
WHERE id=$1 AND account_id=$2 AND bucket_id=$3 AND status='active'
  AND managed_app_id IS NOT NULL AND rotation_parent_id IS NULL
RETURNING *;

-- name: ObjectS3CredentialRotationFinalizeForApp :execrows
UPDATE object_storage_s3_credentials AS previous
SET status='revoked', revoked_at=now()
FROM object_storage_s3_credentials AS binding
WHERE previous.rotation_parent_id=binding.id
  AND binding.managed_app_id=$1 AND previous.rotation_wake_id=$2
  AND previous.status='active' AND binding.status='active';

-- name: ObjectS3CredentialRotationStampStage :one
UPDATE object_storage_s3_credentials AS previous
SET rotation_stamped_at=clock_timestamp()
FROM object_storage_s3_credentials AS binding
WHERE previous.rotation_parent_id=binding.id
  AND binding.managed_app_id=$1 AND previous.rotation_wake_id=$2
  AND previous.status='active' AND binding.status='active'
  AND previous.rotation_stamped_at IS NULL
RETURNING previous.rotation_stamped_at;

-- name: ObjectS3CredentialRotationStampApp :exec
INSERT INTO app_runtime_config_changes (app_id, changed_at) VALUES ($1, $2)
ON CONFLICT (app_id) DO UPDATE
SET changed_at=GREATEST(app_runtime_config_changes.changed_at, excluded.changed_at);

-- name: ObjectStorageManagedSecretRotate :execrows
UPDATE app_secrets SET ciphertext=$6, kid=$7, value_hash=$8,
  delivery_version=delivery_version+1, delivery_status='pending',
  last_delivery_attempt_at=NULL, last_delivery_error_code=NULL, updated_at=now()
WHERE account_id=$1 AND app_id=$2 AND scope=$3
  AND managed_object_storage_credential_id=$4 AND key=$5;

-- name: ObjectS3CredentialResolve :one
SELECT c.*, b.app_id, b.name AS bucket_name, b.scope AS bucket_scope,
       b.region AS bucket_region, b.backend_id, b.backend_fingerprint,
       b.physical_name, b.state AS bucket_state, b.created_at AS bucket_created_at,
       b.updated_at AS bucket_updated_at
FROM object_storage_s3_credentials c
JOIN object_buckets b ON b.id=c.bucket_id AND b.account_id=c.account_id
WHERE c.access_key_id=$1 AND c.status='active' AND b.state='ready';

-- name: ObjectS3CredentialTouch :execrows
UPDATE object_storage_s3_credentials
SET last_used_at=sqlc.arg(used_at)::timestamptz
WHERE id=sqlc.arg(id) AND status='active'
  AND (last_used_at IS NULL OR last_used_at < sqlc.arg(used_at)::timestamptz - interval '1 minute');

-- name: ObjectS3CredentialListForRekey :many
SELECT * FROM object_storage_s3_credentials
WHERE status='active' AND id > $1
ORDER BY id LIMIT sqlc.arg(batch_limit)::int;

-- name: ObjectS3CredentialReseal :execrows
UPDATE object_storage_s3_credentials SET secret_sealed=$4,kid=$3
WHERE id=$1 AND kid=$2 AND status='active';

-- name: SnapshotStorageKeys :many
SELECT storage_key FROM snapshots WHERE deployment_id = $1;

-- name: ExecutionLockAccount :one
SELECT id, plan FROM accounts WHERE id = sqlc.arg(account_id) FOR UPDATE;

-- name: ExecutionCountActive :one
SELECT count(*) FROM executions
WHERE account_id = sqlc.arg(account_id)
  AND status IN ('queued', 'restoring', 'running');

-- name: ExecutionInsert :one
INSERT INTO executions (
  account_id, runtime, profile, status, network_mode, timeout_ms, memory_mb,
  cpu_millicores, ephemeral_disk_mb, max_output_bytes, pids_max,
  source_bytes, input_bytes, deadline_at, created_at, updated_at, runs_principal_id,
  workflow_id, step_label
) VALUES (
  sqlc.arg(account_id), sqlc.arg(runtime), sqlc.arg(profile), 'queued', sqlc.arg(network_mode),
  sqlc.arg(timeout_ms), sqlc.arg(memory_mb), sqlc.arg(cpu_millicores),
  sqlc.arg(ephemeral_disk_mb), sqlc.arg(max_output_bytes), sqlc.arg(pids_max),
  sqlc.arg(source_bytes), sqlc.arg(input_bytes), sqlc.arg(deadline_at),
  sqlc.arg(admitted_at), sqlc.arg(admitted_at), sqlc.narg(runs_principal_id)::uuid,
  sqlc.narg(workflow_id), sqlc.narg(step_label)
)
RETURNING *;

-- name: ExecutionPayloadInsert :exec
INSERT INTO execution_payloads (execution_id, sealed_payload, kid, created_at)
VALUES (sqlc.arg(execution_id), sqlc.arg(sealed_payload), sqlc.arg(kid), sqlc.arg(created_at));

-- name: ExecutionGetForAccount :one
SELECT * FROM executions
WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(execution_id);

-- name: ExecutionListForAccount :many
SELECT * FROM executions
WHERE account_id = sqlc.arg(account_id)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int OFFSET sqlc.arg(page_offset)::int;

-- name: ExecutionListForAccountPrincipal :many
SELECT * FROM executions
WHERE account_id = sqlc.arg(account_id)
  AND runs_principal_id = sqlc.arg(runs_principal_id)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int OFFSET sqlc.arg(page_offset)::int;

-- name: ExecutionListForAccountPrincipalStatus :many
SELECT * FROM executions
WHERE account_id = sqlc.arg(account_id)
  AND runs_principal_id = sqlc.arg(runs_principal_id)
  AND status = sqlc.arg(status)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int OFFSET sqlc.arg(page_offset)::int;

-- name: ExecutionListForAccountStatus :many
SELECT * FROM executions
WHERE account_id = sqlc.arg(account_id)
  AND status = sqlc.arg(status)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int OFFSET sqlc.arg(page_offset)::int;

-- name: ExecutionListForWorkflow :many
SELECT * FROM executions
WHERE account_id = sqlc.arg(account_id)::uuid
  AND workflow_id = sqlc.arg(workflow_id)::text
  AND (sqlc.narg(runs_principal_id)::uuid IS NULL OR runs_principal_id = sqlc.narg(runs_principal_id)::uuid)
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int OFFSET sqlc.arg(page_offset)::int;

-- name: ExecutionWorkflowSummary :one
SELECT count(*)::bigint AS run_count,
       count(*) FILTER (WHERE status = 'queued')::bigint AS queued,
       count(*) FILTER (WHERE status = 'restoring')::bigint AS restoring,
       count(*) FILTER (WHERE status = 'running')::bigint AS running,
       count(*) FILTER (WHERE status = 'succeeded')::bigint AS succeeded,
       count(*) FILTER (WHERE status = 'failed')::bigint AS failed,
       count(*) FILTER (WHERE status = 'timed_out')::bigint AS timed_out,
       count(*) FILTER (WHERE status = 'out_of_memory')::bigint AS out_of_memory,
       count(*) FILTER (WHERE status = 'cancelled')::bigint AS cancelled,
       coalesce(sum(wall_time_ms) FILTER (WHERE status IN ('succeeded', 'failed', 'timed_out', 'out_of_memory', 'cancelled')), 0)::bigint AS wall_time_ms,
       coalesce(sum(cpu_time_ms) FILTER (WHERE status IN ('succeeded', 'failed', 'timed_out', 'out_of_memory', 'cancelled')), 0)::bigint AS cpu_time_ms,
       coalesce(max(peak_memory_mb) FILTER (WHERE status IN ('succeeded', 'failed', 'timed_out', 'out_of_memory', 'cancelled')), 0)::bigint AS peak_memory_mb,
       coalesce(sum(result_bytes + octet_length(stdout) + octet_length(stderr) + octet_length(artifacts)) FILTER (WHERE status IN ('succeeded', 'failed', 'timed_out', 'out_of_memory', 'cancelled')), 0)::bigint AS output_bytes
FROM executions
WHERE account_id = sqlc.arg(account_id)::uuid
  AND workflow_id = sqlc.arg(workflow_id)::text
  AND (sqlc.narg(runs_principal_id)::uuid IS NULL OR runs_principal_id = sqlc.narg(runs_principal_id)::uuid);

-- name: ExecutionClaimNext :one
WITH candidate AS (
  SELECT id, deadline_at
  FROM executions
  WHERE status = 'queued'
    AND cancel_requested_at IS NULL
    AND created_at <= sqlc.arg(claimed_at)::timestamptz
    AND deadline_at > sqlc.arg(claimed_at)::timestamptz
  ORDER BY created_at, id
  FOR UPDATE SKIP LOCKED
  LIMIT 1
)
UPDATE executions AS execution
SET status = 'restoring',
    lease_token = sqlc.arg(lease_token),
    lease_owner = sqlc.arg(lease_owner),
    lease_expires_at = LEAST(sqlc.arg(lease_expires_at)::timestamptz, candidate.deadline_at),
    updated_at = sqlc.arg(claimed_at)
FROM candidate
WHERE execution.id = candidate.id
RETURNING execution.*;

-- name: ExecutionClaimNextForAccount :one
WITH candidate AS (
  SELECT id, deadline_at
  FROM executions
  WHERE executions.account_id = sqlc.arg(account_id)
    AND executions.status = 'queued'
    AND executions.cancel_requested_at IS NULL
    AND executions.created_at <= sqlc.arg(claimed_at)::timestamptz
    AND executions.deadline_at > sqlc.arg(claimed_at)::timestamptz
  ORDER BY executions.created_at, executions.id
  FOR UPDATE SKIP LOCKED
  LIMIT 1
)
UPDATE executions AS execution
SET status = 'restoring',
    lease_token = sqlc.arg(lease_token),
    lease_owner = sqlc.arg(lease_owner),
    lease_expires_at = LEAST(sqlc.arg(lease_expires_at)::timestamptz, candidate.deadline_at),
    updated_at = sqlc.arg(claimed_at)
FROM candidate
WHERE execution.id = candidate.id
RETURNING execution.*;

-- name: ExecutionQueueStats :one
SELECT count(*)::bigint AS queued, min(created_at) AS oldest_created_at
FROM executions
WHERE status = 'queued'
  AND cancel_requested_at IS NULL
  AND created_at <= sqlc.arg(at)::timestamptz
  AND deadline_at > sqlc.arg(at)::timestamptz;

-- name: ExecutionQueueAccounts :many
SELECT executions.account_id, count(*)::bigint AS queued_count, min(executions.created_at) AS oldest_created_at
FROM executions
WHERE executions.status = 'queued'
  AND executions.cancel_requested_at IS NULL
  AND executions.created_at <= sqlc.arg(at)::timestamptz
  AND executions.deadline_at > sqlc.arg(at)::timestamptz
GROUP BY executions.account_id
ORDER BY executions.account_id
LIMIT sqlc.arg(page_limit)::int;

-- name: ExecutionPayloadForLease :one
SELECT payload.execution_id, payload.sealed_payload, payload.kid, payload.created_at
FROM execution_payloads AS payload
JOIN executions AS execution ON execution.id = payload.execution_id
WHERE payload.execution_id = sqlc.arg(execution_id)
  AND execution.status = 'restoring'
  AND execution.lease_token = sqlc.arg(lease_token);

-- name: ExecutionMarkRunning :one
UPDATE executions
SET status = 'running', started_at = sqlc.arg(started_at), updated_at = sqlc.arg(started_at)
WHERE id = sqlc.arg(execution_id)
  AND status = 'restoring'
  AND lease_token = sqlc.arg(lease_token)
  AND lease_expires_at > sqlc.arg(started_at)
  AND cancel_requested_at IS NULL
  AND deadline_at > sqlc.arg(started_at)
  AND (profile = 'standard' OR runtime_image_digest IS NOT NULL)
RETURNING *;

-- name: ExecutionPinRuntime :one
UPDATE executions
SET runtime_image_digest = sqlc.arg(image_digest), updated_at = sqlc.arg(pinned_at)
WHERE id = sqlc.arg(execution_id) AND status = 'restoring'
  AND lease_token = sqlc.arg(lease_token)
  AND lease_expires_at > sqlc.arg(pinned_at) AND deadline_at > sqlc.arg(pinned_at)
  AND cancel_requested_at IS NULL
  AND (runtime_image_digest IS NULL OR runtime_image_digest = sqlc.arg(image_digest))
RETURNING *;

-- name: ExecutionLockForLease :one
SELECT * FROM executions
WHERE id = sqlc.arg(execution_id)
  AND status IN ('restoring', 'running')
  AND lease_token = sqlc.arg(lease_token)
FOR UPDATE;

-- name: ExecutionRenewLease :execrows
UPDATE executions
SET lease_expires_at = LEAST(sqlc.arg(lease_expires_at)::timestamptz, deadline_at),
    updated_at = sqlc.arg(renewed_at)
WHERE id = sqlc.arg(execution_id)
  AND status IN ('restoring', 'running')
  AND lease_token = sqlc.arg(lease_token)
  AND lease_expires_at > sqlc.arg(renewed_at)
  AND cancel_requested_at IS NULL
  AND deadline_at > sqlc.arg(renewed_at);

-- name: ExecutionMarkTerminal :one
UPDATE executions
SET status = sqlc.arg(terminal_status),
    lease_token = NULL,
    lease_owner = NULL,
    lease_expires_at = NULL,
    result = NULLIF(sqlc.arg(result_json)::text, '')::jsonb,
    result_bytes = sqlc.arg(result_bytes),
    artifacts = sqlc.arg(artifacts),
    stdout = sqlc.arg(stdout),
    stderr = sqlc.arg(stderr),
    output_truncated = sqlc.arg(output_truncated),
    exit_code = sqlc.narg(exit_code),
    failure_code = NULLIF(sqlc.arg(failure_code), ''),
    failure_message = NULLIF(sqlc.arg(failure_message), ''),
    wall_time_ms = sqlc.arg(wall_time_ms),
    cpu_time_ms = sqlc.arg(cpu_time_ms),
    peak_memory_mb = sqlc.arg(peak_memory_mb),
    finished_at = sqlc.arg(finished_at),
    updated_at = sqlc.arg(finished_at)
WHERE id = sqlc.arg(execution_id)
  AND status IN ('restoring', 'running')
  AND lease_token = sqlc.arg(lease_token)
RETURNING *;

-- name: ExecutionLockForAccount :one
SELECT * FROM executions
WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(execution_id)
FOR UPDATE;

-- name: ExecutionRequestCancel :one
UPDATE executions
SET status = CASE WHEN status = 'queued' THEN 'cancelled' ELSE status END,
    cancel_requested_at = COALESCE(cancel_requested_at, sqlc.arg(requested_at)),
    finished_at = CASE WHEN status = 'queued' THEN sqlc.arg(requested_at) ELSE finished_at END,
    updated_at = sqlc.arg(requested_at)
WHERE id = sqlc.arg(execution_id)
  AND status IN ('queued', 'restoring', 'running')
RETURNING *;

-- name: ExecutionExpireQueued :many
WITH candidates AS (
  SELECT id
  FROM executions
  WHERE status = 'queued' AND deadline_at <= sqlc.arg(sweep_at)::timestamptz
  ORDER BY deadline_at, id
  FOR UPDATE SKIP LOCKED
  LIMIT sqlc.arg(batch_limit)::int
)
UPDATE executions AS execution
SET status = 'timed_out', finished_at = sqlc.arg(sweep_at), updated_at = sqlc.arg(sweep_at),
    failure_code = 'deadline_exceeded', failure_message = 'execution deadline elapsed before dispatch'
FROM candidates
WHERE execution.id = candidates.id
RETURNING execution.*;

-- name: ExecutionRequeueExpiredRestores :many
WITH candidates AS (
  SELECT id
  FROM executions
  WHERE status = 'restoring'
    AND lease_expires_at <= sqlc.arg(sweep_at)::timestamptz
    AND deadline_at > sqlc.arg(sweep_at)::timestamptz
    AND cancel_requested_at IS NULL
  ORDER BY lease_expires_at, id
  FOR UPDATE SKIP LOCKED
  LIMIT sqlc.arg(batch_limit)::int
)
UPDATE executions AS execution
SET status = 'queued', lease_token = NULL, lease_owner = NULL, lease_expires_at = NULL,
    updated_at = sqlc.arg(sweep_at)
FROM candidates
WHERE execution.id = candidates.id
RETURNING execution.*;

-- name: ExecutionFinishExpiredRestores :many
WITH candidates AS (
  SELECT id
  FROM executions
  WHERE status = 'restoring'
    AND lease_expires_at <= sqlc.arg(sweep_at)::timestamptz
    AND (deadline_at <= sqlc.arg(sweep_at)::timestamptz OR cancel_requested_at IS NOT NULL)
  ORDER BY lease_expires_at, id
  FOR UPDATE SKIP LOCKED
  LIMIT sqlc.arg(batch_limit)::int
)
UPDATE executions AS execution
SET status = CASE WHEN cancel_requested_at IS NOT NULL THEN 'cancelled' ELSE 'timed_out' END,
    lease_token = NULL, lease_owner = NULL, lease_expires_at = NULL,
    failure_code = CASE WHEN cancel_requested_at IS NULL THEN 'deadline_exceeded' ELSE NULL END,
    failure_message = CASE WHEN cancel_requested_at IS NULL THEN 'execution deadline elapsed during restore' ELSE NULL END,
    finished_at = sqlc.arg(sweep_at), updated_at = sqlc.arg(sweep_at)
FROM candidates
WHERE execution.id = candidates.id
RETURNING execution.*;

-- name: ExecutionFinishExpiredRuns :many
WITH candidates AS (
  SELECT id
  FROM executions
  WHERE status = 'running' AND lease_expires_at <= sqlc.arg(sweep_at)::timestamptz
  ORDER BY lease_expires_at, id
  FOR UPDATE SKIP LOCKED
  LIMIT sqlc.arg(batch_limit)::int
)
UPDATE executions AS execution
SET status = CASE
      WHEN cancel_requested_at IS NOT NULL THEN 'cancelled'
      WHEN deadline_at <= sqlc.arg(sweep_at)::timestamptz THEN 'timed_out'
      ELSE 'failed'
    END,
    lease_token = NULL, lease_owner = NULL, lease_expires_at = NULL,
    failure_code = CASE
      WHEN cancel_requested_at IS NOT NULL THEN NULL
      WHEN deadline_at <= sqlc.arg(sweep_at)::timestamptz THEN 'deadline_exceeded'
      ELSE 'lease_expired'
    END,
    failure_message = CASE
      WHEN cancel_requested_at IS NOT NULL THEN NULL
      WHEN deadline_at <= sqlc.arg(sweep_at)::timestamptz THEN 'execution deadline elapsed while running'
      ELSE 'scheduler lease expired after execution dispatch'
    END,
    finished_at = sqlc.arg(sweep_at), updated_at = sqlc.arg(sweep_at)
FROM candidates
WHERE execution.id = candidates.id
RETURNING execution.*;

-- name: ExecutionPayloadDelete :execrows
DELETE FROM execution_payloads WHERE execution_id = sqlc.arg(execution_id);

-- name: ExecutionPayloadDeleteMany :execrows
DELETE FROM execution_payloads WHERE execution_id = ANY(sqlc.arg(execution_ids)::uuid[]);

-- name: ExecutionPayloadDeleteTerminal :execrows
WITH candidates AS (
  SELECT payload.execution_id
  FROM execution_payloads AS payload
  JOIN executions AS execution ON execution.id = payload.execution_id
  WHERE execution.status IN ('succeeded', 'failed', 'timed_out', 'out_of_memory', 'cancelled')
  ORDER BY payload.created_at, payload.execution_id
  FOR UPDATE OF payload SKIP LOCKED
  LIMIT sqlc.arg(batch_limit)::int
)
DELETE FROM execution_payloads AS payload
USING candidates
WHERE payload.execution_id = candidates.execution_id;

-- name: ExecutionUsageRecord :exec
-- The execution ID is the idempotency key. Recording from the terminal
-- execution row keeps usage and the lifecycle projection in lockstep and
-- makes retries/recovery harmless.
INSERT INTO execution_usage_ledger (
    execution_id, account_id, runtime, status, wall_time_ms, cpu_time_ms,
    peak_memory_mb, output_bytes, started_at, finished_at, created_at
)
SELECT id, account_id, runtime, status, wall_time_ms, cpu_time_ms,
       peak_memory_mb,
       (result_bytes + octet_length(stdout) + octet_length(stderr) + octet_length(artifacts))::bigint,
       started_at, finished_at, created_at
FROM executions
WHERE id = sqlc.arg(execution_id)
  AND status IN ('succeeded', 'failed', 'timed_out', 'out_of_memory', 'cancelled')
ON CONFLICT (execution_id) DO NOTHING;

-- name: ExecutionUsageByAccount :one
SELECT
    count(*)::bigint AS runs,
    COALESCE(sum(wall_time_ms), 0)::bigint AS wall_time_ms,
    COALESCE(sum(cpu_time_ms), 0)::bigint AS cpu_time_ms,
    COALESCE(max(peak_memory_mb), 0)::bigint AS peak_memory_mb,
    COALESCE(sum(output_bytes), 0)::bigint AS output_bytes,
    count(*) FILTER (WHERE status = 'succeeded')::bigint AS succeeded,
    count(*) FILTER (WHERE status = 'failed')::bigint AS failed,
    count(*) FILTER (WHERE status = 'timed_out')::bigint AS timed_out,
    count(*) FILTER (WHERE status = 'out_of_memory')::bigint AS out_of_memory,
    count(*) FILTER (WHERE status = 'cancelled')::bigint AS cancelled
FROM execution_usage_ledger
WHERE account_id = sqlc.arg(account_id)
  AND finished_at >= sqlc.arg(month_start)::timestamptz
  AND finished_at < sqlc.arg(month_end)::timestamptz;

-- Runtime snapshot catalog (ADR-171 follow-up / durable publication boundary).
-- Publication is insert-only; retirement is the sole mutable transition.
-- name: RuntimeSnapshotInsert :one
INSERT INTO runtime_snapshots (
    catalog_key, runtime, profile, architecture, kernel_digest, guest_executor_digest,
    base_image_digest, memory_mb, ephemeral_disk_mb, format_version,
    storage_key, snapshot_digest, mem_bytes, vm_state_bytes, sanitized,
    payload_free, state, created_at, published_at, retired_at
)
VALUES (
    sqlc.arg(catalog_key), sqlc.arg(runtime), sqlc.arg(profile), sqlc.arg(architecture),
    sqlc.arg(kernel_digest), sqlc.arg(guest_executor_digest),
    sqlc.arg(base_image_digest), sqlc.arg(memory_mb), sqlc.arg(ephemeral_disk_mb),
    sqlc.arg(format_version), sqlc.arg(storage_key), sqlc.arg(snapshot_digest),
    sqlc.arg(mem_bytes), sqlc.arg(vm_state_bytes), sqlc.arg(sanitized),
    sqlc.arg(payload_free), sqlc.arg(state), sqlc.arg(created_at),
    sqlc.arg(published_at), sqlc.arg(retired_at)
)
RETURNING *;

-- name: RuntimeSnapshotByCatalogKey :one
SELECT * FROM runtime_snapshots
WHERE catalog_key = sqlc.arg(catalog_key);

-- name: RuntimeSnapshotRetire :execrows
UPDATE runtime_snapshots
SET state = 'retired', retired_at = sqlc.arg(retired_at)
WHERE catalog_key = sqlc.arg(catalog_key) AND state = 'ready';

-- name: ListEgressCircuitCandidates :many
-- schedd's egress circuit-breaker feed (ADR-201 §3). Returns every
-- opted-in upstream joined to its NEWEST probe verdict, which is the
-- complete input the breaker loop needs for one reconcile pass.
--
-- Only circuit_breaker_enabled rows are considered, so the scan is
-- served by data_upstreams_circuit_enabled_idx (a partial index) and
-- stays proportional to the opt-in count rather than to the whole
-- data_upstreams table, which grows with every captured env var on
-- every app.
--
-- LEFT JOIN, not INNER: an opted-in upstream that has never been
-- probed must still appear, carrying a NULL sampled_at. Dropping it
-- here would make "never probed" indistinguishable from "row gone",
-- and the loop needs the difference — it skips unprobed upstreams but
-- must still count them as live candidates so their dedupe state is
-- not retired out from under them.
--
-- DISTINCT ON picks one row per upstream: the probe table holds one
-- sample per 30s per (host, region), so without it a single upstream
-- would fan out to every sample in the retention window.
--
-- host is projected because schedd resolves it locally to write the
-- nftables element. It never reaches a metric label, a log line, or
-- the customer-facing API — those carry host_redacted_hash only
-- (ADR-098 §11).
SELECT DISTINCT ON (u.app_id, u.host_redacted_hash, u.port)
    u.app_id,
    u.host_redacted_hash,
    u.host,
    u.port,
    u.circuit_breaker_failure_threshold,
    u.circuit_breaker_min_samples,
    u.circuit_breaker_open_seconds,
    p.ok,
    p.sampled_at
FROM data_upstreams u
LEFT JOIN data_upstream_probes p
    ON p.host_redacted_hash = u.host_redacted_hash
   AND p.sampled_at >= $1
WHERE u.circuit_breaker_enabled
ORDER BY u.app_id, u.host_redacted_hash, u.port, p.sampled_at DESC NULLS LAST;

-- name: ListDeploymentAliases :many
-- Stable per-app revision names. Join deployments for the human-readable
-- revision while retaining aliases whose targets later become superseded;
-- the alias continues to identify the same immutable row.
SELECT a.app_id, a.name, a.deployment_id, d.revision, a.created_at, a.updated_at
  FROM deployment_aliases a
  JOIN deployments d ON d.id = a.deployment_id AND d.app_id = a.app_id
 WHERE a.app_id = sqlc.arg(app_id)
 ORDER BY a.name;

-- name: DeploymentAliasByHostLabel :many
-- The hostname label uses the app's immutable UUID so aliases remain stable
-- across app slug renames. Keep the deployment join app-scoped and hide
-- soft-deleted owners/targets.
SELECT a.app_id, a.name, a.deployment_id, d.revision, a.created_at, a.updated_at
  FROM deployment_aliases a
  JOIN apps p ON p.id = a.app_id
             AND p.status <> 'deleted'
             AND p.deleted_at IS NULL
  JOIN deployments d ON d.id = a.deployment_id
                    AND d.app_id = a.app_id
                    AND d.deleted_at IS NULL
 WHERE ('tag-' || a.name || '-' || replace(a.app_id::text, '-', ''))
       = sqlc.arg(host_label)
 ORDER BY a.app_id, a.name;

-- name: UpsertDeploymentAlias :one
-- Accept only a routable target on this app. Using INSERT .. SELECT makes the
-- ownership/status check atomic with writing the alias.
WITH upserted AS (
    INSERT INTO deployment_aliases (app_id, name, deployment_id)
    SELECT d.app_id, sqlc.arg(name), d.id
      FROM deployments d
      JOIN apps a ON a.id = d.app_id
     WHERE d.app_id = sqlc.arg(app_id)
       AND d.id = sqlc.arg(deployment_id)
       AND a.deleted_at IS NULL
       AND a.status <> 'deleted'
       AND d.deleted_at IS NULL
       AND d.status IN ('pending', 'building', 'imaging', 'snapshotting', 'live')
    ON CONFLICT (app_id, name) DO UPDATE
       SET deployment_id = EXCLUDED.deployment_id,
           updated_at = now()
    RETURNING app_id, name, deployment_id, created_at, updated_at
)
SELECT u.app_id, u.name, u.deployment_id, d.revision, u.created_at, u.updated_at
  FROM upserted u
  JOIN deployments d ON d.id = u.deployment_id;

-- name: DeleteDeploymentAlias :execrows
DELETE FROM deployment_aliases
 WHERE app_id = sqlc.arg(app_id) AND name = sqlc.arg(name);

-- name: UpdateDataUpstreamCircuitBreaker :exec
-- ADR-201 §3 per-upstream egress-breaker policy. Each field uses the
-- COALESCE(sqlc.narg, existing) shape so a PATCH that omits a field
-- leaves it untouched — the same partial-update convention the app
-- PATCH paths use.
--
-- The threshold fields are deliberately NOT cleared when enabled flips
-- to false: a customer toggling protection off should not silently lose
-- their tuning, and re-enabling should restore what they configured.
UPDATE data_upstreams
SET circuit_breaker_enabled           = COALESCE(sqlc.narg('circuit_breaker_enabled')::boolean, circuit_breaker_enabled),
    circuit_breaker_failure_threshold = COALESCE(sqlc.narg('circuit_breaker_failure_threshold')::double precision, circuit_breaker_failure_threshold),
    circuit_breaker_min_samples       = COALESCE(sqlc.narg('circuit_breaker_min_samples')::integer, circuit_breaker_min_samples),
    circuit_breaker_open_seconds      = COALESCE(sqlc.narg('circuit_breaker_open_seconds')::integer, circuit_breaker_open_seconds)
WHERE id = $1 AND app_id = $2;

-- name: LatestInstanceReadiness :many
-- Gateway restart hydration: readiness is independent of the instance's
-- RUNNING state, so replay only the latest reversible ready/unready event.
SELECT DISTINCT ON (CAST(data->>'instance_id' AS text))
       CAST(data->>'instance_id' AS text) AS instance_id,
       CAST(data->>'status' AS text) AS status,
       at,
       id
FROM events
WHERE kind IN ('wake.sidecar_health', 'wake.app_readiness')
  AND data->>'status' IN ('ready', 'unready')
  AND data->>'instance_id' = ANY(sqlc.arg(instance_ids)::text[])
ORDER BY CAST(data->>'instance_id' AS text), at DESC, id DESC;

-- name: ReadProjectReleaseSet :one
-- A single statement reads the pointer and its complete membership together.
SELECT (to_jsonb(rs) || jsonb_build_object('environment', rs.environment_slug,
        'members', COALESCE((SELECT jsonb_agg(jsonb_build_object(
            'app_id', rm.app_id, 'deployment_id', rm.deployment_id) ORDER BY rm.app_id)
            FROM project_release_members rm WHERE rm.release_id = rs.id), '[]'::jsonb)))::jsonb AS release
  FROM project_release_sets rs
  JOIN projects p ON p.id = rs.project_id AND p.account_id = rs.account_id
 WHERE rs.account_id = sqlc.arg(account_id) AND rs.project_id = sqlc.arg(project_id)
   AND rs.environment_slug = sqlc.arg(environment)
   AND ((sqlc.narg(release_id)::uuid IS NULL AND rs.active)
     OR rs.id = sqlc.narg(release_id)::uuid);

-- name: ListProjectReleaseSetsBefore :many
-- Retired and expired graphs remain visible for diagnosis. UUID breaks ties.
SELECT (to_jsonb(rs) || jsonb_build_object('environment', rs.environment_slug,
        'members', COALESCE((SELECT jsonb_agg(jsonb_build_object(
            'app_id', rm.app_id, 'deployment_id', rm.deployment_id) ORDER BY rm.app_id)
            FROM project_release_members rm WHERE rm.release_id = rs.id), '[]'::jsonb)))::jsonb AS release
  FROM project_release_sets rs
  JOIN projects p ON p.id = rs.project_id AND p.account_id = rs.account_id
 WHERE rs.account_id = sqlc.arg(account_id) AND rs.project_id = sqlc.arg(project_id)
   AND rs.environment_slug = sqlc.arg(environment)
   AND (sqlc.narg(before_at)::timestamptz IS NULL
     OR (rs.created_at, rs.id) < (sqlc.narg(before_at)::timestamptz, sqlc.narg(before_id)::uuid))
 ORDER BY rs.created_at DESC, rs.id DESC
 LIMIT sqlc.arg(page_limit);
-- name: LatestInstanceReadinessBySource :many
-- Gateway hydration keeps each required readiness source independent so one
-- recovered probe cannot override another probe that is still unready.
SELECT DISTINCT ON (
           CAST(data->>'instance_id' AS text),
           CAST(CASE WHEN kind = 'wake.app_readiness' THEN 'primary_app'
                ELSE 'sidecar:' || CAST(data->>'sidecar_name' AS text) END AS text)
       )
       CAST(data->>'instance_id' AS text) AS instance_id,
       CAST(CASE WHEN kind = 'wake.app_readiness' THEN 'primary_app'
            ELSE 'sidecar:' || CAST(data->>'sidecar_name' AS text) END AS text) AS source,
       CAST(data->>'status' AS text) AS status,
       at,
       id
FROM events
WHERE kind IN ('wake.sidecar_health', 'wake.app_readiness')
  AND data->>'status' IN ('ready', 'unready')
  AND data->>'instance_id' = ANY(sqlc.arg(instance_ids)::text[])
  AND (kind <> 'wake.sidecar_health' OR COALESCE(data->>'sidecar_name', '') <> '')
ORDER BY CAST(data->>'instance_id' AS text), source, at DESC, id DESC;

-- name: StampSafeReleaseWorkerLease :exec
INSERT INTO safe_release_worker_lease (singleton, healthy_at, expires_at)
VALUES (true, now(), now() + (sqlc.arg(ttl_seconds)::bigint * interval '1 second'))
ON CONFLICT (singleton) DO UPDATE SET
    healthy_at = EXCLUDED.healthy_at,
    expires_at = EXCLUDED.expires_at;

-- name: SafeReleaseWorkerLeaseReady :one
SELECT EXISTS(SELECT 1 FROM safe_release_worker_lease
              WHERE singleton = true AND expires_at > now()) AS ready;

-- name: RecordRequestIDJournal :one
-- The request-ID journal is independent from sampled request telemetry. Only
-- insert when the app is still owned by the authenticated account. The
-- caller-generated record UUID makes an RPC retry idempotent without
-- collapsing two customer requests that happen to reuse a public ID.
INSERT INTO request_id_journal (
    id, account_id, app_id, request_id, trace_id, received_at, expires_at
)
SELECT sqlc.arg(id)::uuid,
       a.account_id,
       a.id,
       sqlc.arg(request_id)::text,
       NULLIF(sqlc.arg(trace_id)::text, ''),
       sqlc.arg(received_at)::timestamptz,
       sqlc.arg(expires_at)::timestamptz
  FROM apps a
 WHERE a.id = sqlc.arg(app_id)::uuid
   AND a.account_id = sqlc.arg(account_id)::uuid
ON CONFLICT (id) DO UPDATE SET id = EXCLUDED.id
 WHERE request_id_journal.account_id = EXCLUDED.account_id
   AND request_id_journal.app_id = EXCLUDED.app_id
   AND request_id_journal.request_id = EXCLUDED.request_id
   AND request_id_journal.trace_id IS NOT DISTINCT FROM EXCLUDED.trace_id
   AND request_id_journal.received_at = EXCLUDED.received_at
   AND request_id_journal.expires_at = EXCLUDED.expires_at
RETURNING id;

-- name: GetRequestIDJournalByAppAndIdentifier :one
-- Exact app/account-scoped lookup, latest first when callers reuse an ID.
-- expires_at is checked as well as received_at so plan downgrades do not
-- extend the original request-time retention window.
SELECT id, request_id, trace_id, received_at, expires_at
  FROM request_id_journal
 WHERE account_id = sqlc.arg(account_id)::uuid
   AND app_id = sqlc.arg(app_id)::uuid
   AND request_id = sqlc.arg(request_id)::text
   AND received_at >= sqlc.arg(received_from)::timestamptz
   AND received_at < sqlc.arg(received_until)::timestamptz
   AND expires_at > sqlc.arg(now_at)::timestamptz
 ORDER BY received_at DESC, id DESC
 LIMIT 1;

-- name: IssueLockApp :one
SELECT id, account_id, org_id FROM apps WHERE id = sqlc.arg(app_id) FOR UPDATE;
-- name: IssueDeploymentScope :one
SELECT d.id, d.commit_sha, d.image_digest, d.created_at FROM deployments d JOIN apps a ON a.id = d.app_id
WHERE d.id = sqlc.arg(deployment_id) AND d.app_id = sqlc.arg(app_id) AND a.account_id = sqlc.arg(account_id)
AND (sqlc.arg(environment)::text = 'application' OR EXISTS (
 SELECT 1 FROM project_release_members rm JOIN project_release_sets rs ON rs.id = rm.release_id
 WHERE rm.app_id = a.id AND rm.deployment_id = d.id AND rs.environment_slug = sqlc.arg(environment)::text AND rs.account_id = a.account_id));
-- name: IssueFindGroup :one
SELECT * FROM app_issues WHERE app_id = sqlc.arg(app_id) AND environment = sqlc.arg(environment)
AND grouping_version = sqlc.arg(grouping_version) AND fingerprint = sqlc.arg(fingerprint) FOR UPDATE;
-- name: IssueGet :one
SELECT * FROM app_issues WHERE app_id = sqlc.arg(app_id) AND id = sqlc.arg(id);
-- name: IssueGetLocked :one
SELECT * FROM app_issues WHERE app_id = sqlc.arg(app_id) AND id = sqlc.arg(id) FOR UPDATE;
-- name: IssueCreate :one
INSERT INTO app_issues(account_id,app_id,environment,fingerprint,grouping_version,title,first_seen_at,last_seen_at)
VALUES(sqlc.arg(account_id),sqlc.arg(app_id),sqlc.arg(environment),sqlc.arg(fingerprint),sqlc.arg(grouping_version),sqlc.arg(title),sqlc.arg(occurred_at),sqlc.arg(occurred_at)) RETURNING *;
-- name: IssueList :many
SELECT * FROM app_issues WHERE app_id = sqlc.arg(app_id)
AND (sqlc.arg(state)::text = '' OR state = sqlc.arg(state))
AND (sqlc.arg(environment)::text = '' OR environment = sqlc.arg(environment))
AND (sqlc.narg(assignee_account_id)::uuid IS NULL OR assignee_account_id = sqlc.narg(assignee_account_id))
AND (NOT sqlc.arg(unassigned)::bool OR assignee_account_id IS NULL)
AND (sqlc.narg(cursor_time)::timestamptz IS NULL OR (last_seen_at,id) < (sqlc.narg(cursor_time),sqlc.narg(cursor_id)::uuid))
ORDER BY last_seen_at DESC,id DESC LIMIT sqlc.arg(page_limit);
-- name: IssueListByImpact :many
WITH candidate_issues AS MATERIALIZED (
    SELECT i.* FROM app_issues i
    WHERE i.app_id = sqlc.arg(app_id)
      AND (sqlc.arg(state)::text = '' OR i.state = sqlc.arg(state))
      AND (sqlc.arg(environment)::text = '' OR i.environment = sqlc.arg(environment))
      AND (sqlc.narg(assignee_account_id)::uuid IS NULL OR i.assignee_account_id = sqlc.narg(assignee_account_id))
      AND (NOT sqlc.arg(unassigned)::bool OR i.assignee_account_id IS NULL)
), issue_impact AS (
    SELECT e.issue_id,
           count(DISTINCT COALESCE(e.verified_platform_tenant_id,e.verified_consumer_id)) AS identified_customers,
           count(*) AS observed_events,
           count(*) FILTER(WHERE e.verified_platform_tenant_id IS NULL AND e.verified_consumer_id IS NULL) AS unattributed_events
      FROM candidate_issues i
      JOIN issue_events e ON e.issue_id = i.id
     WHERE e.occurred_at >= sqlc.arg(since)::timestamptz
       AND e.occurred_at <= sqlc.arg(until)::timestamptz
     GROUP BY e.issue_id
)
SELECT i.*,
       COALESCE(impact.identified_customers,0)::bigint AS identified_customers,
       COALESCE(impact.observed_events,0)::bigint AS observed_events,
       COALESCE(impact.unattributed_events,0)::bigint AS unattributed_events
  FROM candidate_issues i
  LEFT JOIN issue_impact impact ON impact.issue_id = i.id
 WHERE COALESCE(impact.identified_customers,0) >= sqlc.arg(min_customers)::bigint
   AND (
       (NOT sqlc.arg(sort_by_impact)::bool AND
        (sqlc.narg(cursor_time)::timestamptz IS NULL OR (i.last_seen_at,i.id) < (sqlc.narg(cursor_time),sqlc.narg(cursor_id)::uuid)))
       OR
       (sqlc.arg(sort_by_impact)::bool AND
        (sqlc.narg(cursor_customers)::bigint IS NULL OR
         COALESCE(impact.identified_customers,0) < sqlc.narg(cursor_customers) OR
         (COALESCE(impact.identified_customers,0) = sqlc.narg(cursor_customers) AND
          (i.last_seen_at,i.id) < (sqlc.narg(cursor_time),sqlc.narg(cursor_id)::uuid))))
   )
 ORDER BY CASE WHEN sqlc.arg(sort_by_impact)::bool THEN COALESCE(impact.identified_customers,0) END DESC,
          i.last_seen_at DESC,i.id DESC
 LIMIT sqlc.arg(page_limit);
-- name: IssueImpactSummaries :many
SELECT issue_id,
       count(*) AS observed_events,
       count(DISTINCT COALESCE(verified_platform_tenant_id,verified_consumer_id)) AS identified_customers,
       count(*) FILTER(WHERE verified_platform_tenant_id IS NULL AND verified_consumer_id IS NULL) AS unattributed_events
FROM issue_events
WHERE issue_id = ANY(sqlc.arg(issue_ids)::uuid[])
  AND occurred_at >= sqlc.arg(since)::timestamptz
  AND occurred_at <= sqlc.arg(until)::timestamptz
GROUP BY issue_id;
-- name: IssueCount :one
SELECT count(*) FROM app_issues WHERE app_id = sqlc.arg(app_id);
-- name: IssueCountEvents :one
SELECT count(*) FROM issue_events WHERE app_id = sqlc.arg(app_id);
-- name: IssueCountRecentEvents :one
SELECT count(*) FROM issue_events WHERE app_id = sqlc.arg(app_id) AND received_at >= sqlc.arg(since);
-- name: IssuePurgeEvents :exec
DELETE FROM issue_events WHERE app_id = sqlc.arg(app_id) AND received_at < sqlc.arg(before);
-- name: IssueFindEvent :one
SELECT issue_id,payload_hash FROM issue_events WHERE app_id=sqlc.arg(app_id) AND deployment_id=sqlc.arg(deployment_id) AND event_id=sqlc.arg(event_id);
-- name: IssueInsertEvent :exec
INSERT INTO issue_events(app_id,deployment_id,event_id,issue_id,payload_hash,payload,occurred_at,received_at,verified_consumer_id,verified_platform_tenant_id)
VALUES(sqlc.arg(app_id),sqlc.arg(deployment_id),sqlc.arg(event_id),sqlc.arg(issue_id),sqlc.arg(payload_hash),sqlc.arg(payload),sqlc.arg(occurred_at),sqlc.arg(received_at),sqlc.narg(consumer_id),sqlc.narg(tenant_id));
-- name: IssueObserve :one
UPDATE app_issues SET event_count=event_count+1,first_seen_at=LEAST(first_seen_at,sqlc.arg(occurred_at)),last_seen_at=GREATEST(last_seen_at,sqlc.arg(occurred_at)),
state=CASE WHEN sqlc.arg(regressed)::boolean THEN 'open' ELSE state END,
regression_count=regression_count+CASE WHEN sqlc.arg(regressed)::boolean THEN 1 ELSE 0 END
WHERE id=sqlc.arg(id) RETURNING *;
-- name: IssueObserveRelease :exec
INSERT INTO issue_releases(issue_id,deployment_id,commit_sha,image_digest,event_count,first_seen_at,last_seen_at)
VALUES(sqlc.arg(issue_id),sqlc.arg(deployment_id),sqlc.arg(commit_sha),sqlc.arg(image_digest),1,sqlc.arg(occurred_at),sqlc.arg(occurred_at))
ON CONFLICT(issue_id,deployment_id) DO UPDATE SET event_count=issue_releases.event_count+1,
first_seen_at=LEAST(issue_releases.first_seen_at,excluded.first_seen_at),last_seen_at=GREATEST(issue_releases.last_seen_at,excluded.last_seen_at);
-- name: IssueListEvents :many
SELECT * FROM issue_events WHERE issue_id=sqlc.arg(issue_id) AND occurred_at >= sqlc.arg(since) AND occurred_at <= sqlc.arg(until)
AND (sqlc.narg(cursor_time)::timestamptz IS NULL OR (occurred_at,id) < (sqlc.narg(cursor_time),sqlc.narg(cursor_id)::uuid))
ORDER BY occurred_at DESC,id DESC LIMIT sqlc.arg(page_limit);
-- name: IssueListReleases :many
SELECT * FROM issue_releases WHERE issue_id=sqlc.arg(issue_id) AND (sqlc.narg(cursor_time)::timestamptz IS NULL OR (first_seen_at,deployment_id) < (sqlc.narg(cursor_time),sqlc.narg(cursor_id)::uuid)) ORDER BY first_seen_at DESC,deployment_id DESC LIMIT sqlc.arg(page_limit);
-- name: IssueListActivity :many
SELECT * FROM issue_activity WHERE issue_id=sqlc.arg(issue_id) AND (sqlc.narg(cursor_time)::timestamptz IS NULL OR (created_at,id) < (sqlc.narg(cursor_time),sqlc.narg(cursor_id)::uuid)) ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_limit);
-- name: IssueImpact :one
SELECT count(*) AS observed_events,count(DISTINCT COALESCE(verified_platform_tenant_id,verified_consumer_id)) AS identified_customers,
count(*) FILTER(WHERE verified_platform_tenant_id IS NULL AND verified_consumer_id IS NULL) AS unattributed_events
FROM issue_events WHERE issue_id=sqlc.arg(issue_id) AND occurred_at >= sqlc.arg(since) AND occurred_at <= sqlc.arg(until);
-- name: IssueImpactCustomerCount :one
SELECT count(DISTINCT COALESCE(verified_platform_tenant_id,verified_consumer_id))
FROM issue_events
WHERE issue_id=sqlc.arg(issue_id)
  AND occurred_at >= sqlc.arg(since)
  AND occurred_at <= sqlc.arg(until);
-- name: IssueGetImpactAlertPolicy :one
SELECT COALESCE((SELECT minimum_customers FROM app_issue_impact_alert_policies WHERE app_id=sqlc.arg(app_id)),0)::integer AS minimum_customers;
-- name: IssueUpsertImpactAlertPolicy :exec
INSERT INTO app_issue_impact_alert_policies(app_id,minimum_customers,updated_at)
VALUES(sqlc.arg(app_id),sqlc.arg(minimum_customers),sqlc.arg(updated_at))
ON CONFLICT(app_id) DO UPDATE SET minimum_customers=excluded.minimum_customers,updated_at=excluded.updated_at;
-- name: IssueDeleteImpactAlertPolicy :exec
DELETE FROM app_issue_impact_alert_policies WHERE app_id=sqlc.arg(app_id);
-- name: IssueListOwnershipRules :many
SELECT rule_order, exception_type, source_kind, route_prefix, assignee_account_id
  FROM app_issue_ownership_rules
 WHERE app_id=sqlc.arg(app_id)
 ORDER BY rule_order;
-- name: IssueDeleteOwnershipRules :exec
DELETE FROM app_issue_ownership_rules WHERE app_id=sqlc.arg(app_id);
-- name: IssueInsertOwnershipRule :exec
INSERT INTO app_issue_ownership_rules(app_id,rule_order,exception_type,source_kind,route_prefix,assignee_account_id)
VALUES(sqlc.arg(app_id),sqlc.arg(rule_order),sqlc.narg(exception_type),sqlc.narg(source_kind),sqlc.narg(route_prefix),sqlc.arg(assignee_account_id));
-- name: IssueSetNewIssueAssignee :one
UPDATE app_issues SET assignee_account_id=sqlc.arg(assignee_account_id)
 WHERE id=sqlc.arg(id) RETURNING *;
-- name: IssueAttribution :many
SELECT DISTINCT consumer_id,platform_tenant_id FROM request_telemetry
WHERE account_id=sqlc.arg(account_id) AND app_id=sqlc.arg(app_id) AND deployment_id=sqlc.arg(deployment_id)
AND trace_id=sqlc.arg(trace_id) AND count=1 AND received_at >= sqlc.arg(since) AND received_at <= sqlc.arg(until) LIMIT 2;
-- name: IssueInvocationScope :one
SELECT id,platform_tenant_id FROM invocations WHERE id=sqlc.arg(id) AND app_id=sqlc.arg(app_id) AND account_id=sqlc.arg(account_id);
-- name: IssueUpdateAction :one
UPDATE app_issues SET state=sqlc.arg(state),assignee_account_id=sqlc.narg(assignee),resolved_at=sqlc.narg(resolved_at),
fixed_deployment_id=sqlc.narg(fixed_deployment_id),fixed_deployment_created_at=sqlc.narg(fixed_deployment_created_at),ignored_until=sqlc.narg(ignored_until) WHERE id=sqlc.arg(id) RETURNING *;
-- name: IssueAddActivity :one
INSERT INTO issue_activity(issue_id,action,actor_account_id,created_at,details) VALUES(sqlc.arg(issue_id),sqlc.arg(action),sqlc.narg(actor_account_id),sqlc.arg(created_at),sqlc.arg(details)) RETURNING *;
-- name: IssueAddResolution :exec
INSERT INTO issue_resolutions(issue_id,fixed_deployment_id,resolved_at,actor_account_id) VALUES(sqlc.arg(issue_id),sqlc.arg(deployment_id),sqlc.arg(resolved_at),sqlc.arg(actor_account_id));
-- name: IssueAssigneeAllowed :one
SELECT EXISTS(SELECT 1 FROM apps a LEFT JOIN org_memberships m ON m.org_id=a.org_id AND m.account_id=sqlc.arg(assignee) AND m.removed_at IS NULL
WHERE a.id=sqlc.arg(app_id) AND (a.account_id=sqlc.arg(assignee) OR m.account_id IS NOT NULL));
-- name: IssueInsertToken :one
INSERT INTO issue_ingest_tokens(account_id,app_id,deployment_id,environment,name,token_hash,expires_at)
VALUES(sqlc.arg(account_id),sqlc.arg(app_id),sqlc.arg(deployment_id),sqlc.arg(environment),sqlc.arg(name),sqlc.arg(token_hash),sqlc.arg(expires_at)) RETURNING *;
-- name: IssueFindToken :one
SELECT * FROM issue_ingest_tokens WHERE token_hash=sqlc.arg(token_hash) AND revoked_at IS NULL AND expires_at > sqlc.arg(now);
-- name: IssueListTokens :many
SELECT * FROM issue_ingest_tokens WHERE app_id=sqlc.arg(app_id) ORDER BY created_at DESC;
-- name: IssueRevokeToken :execrows
UPDATE issue_ingest_tokens SET revoked_at=now() WHERE app_id=sqlc.arg(app_id) AND id=sqlc.arg(id);
-- name: IssueCountTokens :one
SELECT count(*) FROM issue_ingest_tokens WHERE app_id=sqlc.arg(app_id) AND revoked_at IS NULL AND expires_at > now();
-- name: IssueAddTransition :exec
WITH recipients AS (
 SELECT array_agg(id) AS ids FROM app_webhooks
 WHERE app_id=sqlc.arg(app_id) AND account_id=sqlc.arg(account_id) AND scope='app' AND enabled
 AND (cardinality(event_filter)=0 OR (sqlc.arg(payload)::jsonb->>'type')=ANY(event_filter))
)
INSERT INTO app_webhook_event_outbox(id,account_id,app_id,event,source_id,payload,recipient_webhook_ids)
SELECT sqlc.arg(activity_id),sqlc.arg(account_id),sqlc.arg(app_id),sqlc.arg(payload)::jsonb->>'type',sqlc.arg(activity_id),sqlc.arg(payload),ids
FROM recipients WHERE cardinality(ids)>0 ON CONFLICT(event,source_id) DO NOTHING;

-- name: IssueRequestAttribution :many
SELECT DISTINCT consumer_key,platform_tenant_id FROM request_audit_events
WHERE account_id=sqlc.arg(account_id) AND app_id=sqlc.arg(app_id) AND deployment_id=sqlc.arg(deployment_id)
AND request_id=sqlc.arg(request_id) LIMIT 2;

-- name: IssueTokenStillValid :one
SELECT EXISTS(SELECT 1 FROM issue_ingest_tokens WHERE id=sqlc.arg(id) AND app_id=sqlc.arg(app_id) AND deployment_id=sqlc.arg(deployment_id) AND revoked_at IS NULL AND expires_at>sqlc.arg(now));
-- name: IssuePurgePlanEvents :execrows
WITH expired AS (
 SELECT e.app_id,e.deployment_id,e.event_id FROM issue_events e JOIN apps a ON a.id=e.app_id JOIN accounts c ON c.id=a.account_id
 WHERE c.plan=sqlc.arg(plan) AND e.received_at<sqlc.arg(before) ORDER BY e.received_at LIMIT sqlc.arg(batch_limit) FOR UPDATE OF e SKIP LOCKED
)
DELETE FROM issue_events e USING expired x WHERE e.app_id=x.app_id AND e.deployment_id=x.deployment_id AND e.event_id=x.event_id;
-- name: IssuePurgeExpiredTokens :execrows
DELETE FROM issue_ingest_tokens WHERE expires_at < sqlc.arg(now);
-- name: IssueExpiredIgnores :many
SELECT app_id,id FROM app_issues WHERE state='ignored' AND ignored_until <= sqlc.arg(now) ORDER BY ignored_until LIMIT sqlc.arg(batch_limit);

-- name: IssueDebugRequest :many
SELECT id FROM request_telemetry WHERE account_id=sqlc.arg(account_id) AND app_id=sqlc.arg(app_id) AND deployment_id=sqlc.arg(deployment_id)
AND count=1 AND received_at BETWEEN sqlc.arg(since) AND sqlc.arg(until)
AND (id=sqlc.narg(request_id)::uuid OR (sqlc.arg(trace_id)::text<>'' AND trace_id=sqlc.arg(trace_id))) LIMIT 2;

-- name: IssueUnattributedEvents :many
SELECT e.*,i.account_id,i.environment FROM issue_events e JOIN app_issues i ON i.id=e.issue_id
WHERE e.verified_consumer_id IS NULL AND e.verified_platform_tenant_id IS NULL
AND e.attribution_checked_at < sqlc.arg(before) ORDER BY e.attribution_checked_at LIMIT sqlc.arg(batch_limit);

-- name: IssueEnrichAttribution :execrows
UPDATE issue_events SET verified_consumer_id=sqlc.narg(consumer_id),verified_platform_tenant_id=sqlc.narg(tenant_id),attribution_checked_at=sqlc.arg(now)
WHERE app_id=sqlc.arg(app_id) AND deployment_id=sqlc.arg(deployment_id) AND event_id=sqlc.arg(event_id)
AND verified_consumer_id IS NULL AND verified_platform_tenant_id IS NULL;

-- name: CreateDevBridge :execrows
INSERT INTO dev_bridge_sessions
(id,account_id,target_app_id,environment_id,scope,attachment_digest,request_digest,expires_at)
SELECT sqlc.arg(id),sqlc.arg(account_id),sqlc.arg(target_app_id),sqlc.arg(environment_id),
       sqlc.arg(scope),sqlc.arg(attachment_digest),sqlc.arg(request_digest),sqlc.arg(expires_at)
FROM apps a JOIN project_environments e ON e.project_id=a.project_id AND e.account_id=a.account_id
WHERE a.id=sqlc.arg(target_app_id) AND a.account_id=sqlc.arg(account_id) AND a.status='active'
  AND e.id=sqlc.arg(environment_id) AND NOT e.protected AND e.slug NOT IN ('production','default')
  AND (sqlc.arg(scope)::jsonb->>'project_id')=e.project_id::text
  AND (SELECT count(*) FROM dev_bridge_sessions b WHERE b.account_id=a.account_id
       AND b.revoked_at IS NULL AND b.expires_at > now()) < sqlc.arg(max_sessions)::integer;

-- name: DevBridgeByID :one
SELECT id,scope,attachment_digest,request_digest,expires_at,revoked_at
FROM dev_bridge_sessions WHERE id=$1 AND account_id=$2;

-- name: ListDevBridges :many
SELECT id,scope,attachment_digest,request_digest,expires_at,revoked_at
FROM dev_bridge_sessions
WHERE account_id=sqlc.arg(account_id) AND revoked_at IS NULL AND expires_at > now()
ORDER BY expires_at DESC, id ASC LIMIT sqlc.arg(row_limit);

-- name: PruneDevBridgeSessions :exec
DELETE FROM dev_bridge_sessions WHERE account_id=$1 AND expires_at < $2;

-- name: RevokeDevBridge :execrows
UPDATE dev_bridge_sessions SET revoked_at=COALESCE(revoked_at,sqlc.arg(revoked_at))
WHERE id=sqlc.arg(id) AND account_id=sqlc.arg(account_id);

-- name: LockDevBridgeAccount :one
SELECT plan FROM accounts WHERE id=$1 FOR UPDATE;

-- name: LockDevBridgeReplaySession :one
SELECT id FROM dev_bridge_sessions WHERE id=$1 AND account_id=$2
  AND revoked_at IS NULL AND expires_at > now() FOR UPDATE;

-- name: CreateDevBridgeWebhookReplay :execrows
INSERT INTO dev_bridge_webhook_replays (id,session_id,account_id,invocation_id,idempotency_key)
SELECT sqlc.arg(id),sqlc.arg(session_id),sqlc.arg(account_id),sqlc.arg(invocation_id),sqlc.arg(idempotency_key)
WHERE (SELECT count(*) FROM dev_bridge_webhook_replays WHERE session_id=sqlc.arg(session_id)) < sqlc.arg(max_replays)::integer
ON CONFLICT (session_id,idempotency_key) DO NOTHING;

-- name: DevBridgeWebhookReplayByKey :one
SELECT * FROM dev_bridge_webhook_replays WHERE session_id=$1 AND account_id=$2 AND idempotency_key=$3;

-- name: FinishDevBridgeWebhookReplay :execrows
UPDATE dev_bridge_webhook_replays SET state=sqlc.arg(state),http_status=sqlc.arg(http_status),completed_at=now()
WHERE id=sqlc.arg(id) AND account_id=sqlc.arg(account_id) AND state='dispatching';

-- name: DevBridgeWebhookReplayByID :one
SELECT * FROM dev_bridge_webhook_replays WHERE id=$1 AND account_id=$2 AND session_id=$3;
-- name: LockFeatureFlagEnvironment :one
SELECT e.id FROM project_environments e
JOIN projects p ON p.id = e.project_id AND p.account_id = e.account_id
WHERE e.id = sqlc.arg(environment_id)::uuid AND e.project_id = sqlc.arg(project_id)::uuid
 AND e.account_id = sqlc.arg(account_id)::uuid
FOR UPDATE OF e;

-- name: GetFeatureFlagVersion :one
SELECT * FROM feature_flag_versions
WHERE environment_id = sqlc.arg(environment_id)::uuid
 AND account_id = sqlc.arg(account_id)::uuid AND project_id = sqlc.arg(project_id)::uuid
 AND (sqlc.arg(version)::bigint = 0 OR version = sqlc.arg(version)::bigint)
ORDER BY version DESC LIMIT 1;

-- name: ListFeatureFlagVersions :many
SELECT * FROM feature_flag_versions
WHERE environment_id = sqlc.arg(environment_id)::uuid
 AND account_id = sqlc.arg(account_id)::uuid AND project_id = sqlc.arg(project_id)::uuid
 AND version < sqlc.arg(before_version)::bigint
ORDER BY version DESC LIMIT 100;

-- name: InsertFeatureFlagVersion :one
INSERT INTO feature_flag_versions (account_id, project_id, environment_id, version, config, actor, restored_from)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *;

-- name: FeatureFlagCustomerOwned :one
SELECT EXISTS(SELECT 1 FROM platform_tenants
 WHERE account_id = sqlc.arg(account_id)::uuid AND id = sqlc.arg(tenant_id)::uuid) AS owned;

-- name: ListFeatureFlagAutoRolloutCandidates :many
SELECT pe.account_id, pe.project_id, pe.id AS environment_id,
       p.slug AS project_slug, pe.slug AS environment_slug
FROM project_environments pe
JOIN projects p ON p.id = pe.project_id AND p.account_id = pe.account_id
JOIN LATERAL (
 SELECT v.config
 FROM feature_flag_versions v
 WHERE v.account_id = pe.account_id
   AND v.project_id = pe.project_id
   AND v.environment_id = pe.id
 ORDER BY v.version DESC
 LIMIT 1
) current_config ON current_config.config @> '{"flags":[{"rules":[{"progression":{"auto_advance":true}}]}]}'::jsonb
WHERE sqlc.narg(after_environment_id)::uuid IS NULL
   OR pe.id > sqlc.narg(after_environment_id)::uuid
ORDER BY pe.id
LIMIT sqlc.arg(limit_rows)::int;

-- name: ListFeatureFlagRequestEvidence :many
SELECT t.id, t.app_id, t.deployment_id, t.platform_tenant_id, t.received_at, t.route, t.method,
 t.status, t.latency_ms, t.count, t.cold_boot, t.trace_id, t.flag_evidence
FROM request_telemetry t JOIN deployments d ON d.id = t.deployment_id
WHERE (d.scope = sqlc.arg(environment_slug)::text OR (d.scope = 'default' AND sqlc.arg(environment_slug)::text = 'production'))
 AND t.account_id = sqlc.arg(account_id)::uuid
 AND t.app_id = ANY(sqlc.arg(app_ids)::uuid[])
 AND t.received_at >= sqlc.arg(received_from)::timestamptz
 AND t.received_at < sqlc.arg(received_until)::timestamptz
 AND (sqlc.arg(customer_id)::text = '' OR platform_tenant_id::text = sqlc.arg(customer_id)::text)
 AND flag_evidence @> sqlc.arg(evidence_filter)::jsonb
 AND (sqlc.narg(cursor_at)::timestamptz IS NULL OR (t.received_at,t.id) < (sqlc.narg(cursor_at)::timestamptz,sqlc.narg(cursor_id)::uuid))
ORDER BY t.received_at DESC, t.id DESC LIMIT 101;

-- name: ListServiceRecoveryApps :many
SELECT a.id FROM apps a JOIN accounts ac ON ac.id = a.account_id
LEFT JOIN service_recovery r ON r.app_id = a.id
WHERE a.status = 'active' AND a.manifest->>'execution_mode' = 'service'
  AND ac.status IN ('active', 'past_due') AND ac.abuse_hold_at IS NULL
  AND (sqlc.arg(owner_node_id)::text = '' OR a.node_id IS NULL OR a.node_id::text = sqlc.arg(owner_node_id)::text)
  AND coalesce(r.lease_until, '1970-01-01'::timestamptz) <= sqlc.arg(sampled_at)::timestamptz
  AND coalesce(r.next_attempt_at, '1970-01-01'::timestamptz) <= sqlc.arg(sampled_at)::timestamptz
ORDER BY coalesce(r.next_attempt_at, '1970-01-01'::timestamptz), a.id
LIMIT sqlc.arg(batch_limit)::integer;

-- name: ClaimServiceRecovery :one
INSERT INTO service_recovery(app_id, revision, claim_token, lease_until, status, next_attempt_at, updated_at)
SELECT a.id, sqlc.arg(revision)::text, sqlc.arg(claim_token)::uuid, sqlc.arg(lease_until)::timestamptz,
       'reconciling', sqlc.arg(sampled_at)::timestamptz, sqlc.arg(sampled_at)::timestamptz
FROM apps a JOIN accounts ac ON ac.id = a.account_id
WHERE a.id = sqlc.arg(app_id)::uuid AND a.status = 'active' AND a.manifest->>'execution_mode' = 'service'
  AND ac.status IN ('active', 'past_due') AND ac.abuse_hold_at IS NULL
ON CONFLICT (app_id) DO UPDATE SET
 revision = EXCLUDED.revision, claim_token = EXCLUDED.claim_token, lease_until = EXCLUDED.lease_until,
 status = 'reconciling', updated_at = EXCLUDED.updated_at,
 failures = CASE WHEN service_recovery.revision <> EXCLUDED.revision THEN 0 ELSE service_recovery.failures END
WHERE service_recovery.lease_until <= EXCLUDED.updated_at
  AND (service_recovery.revision <> EXCLUDED.revision
    OR service_recovery.status NOT IN ('waiting_capacity', 'retrying_startup', 'waiting_dependency')
    OR service_recovery.next_attempt_at <= EXCLUDED.updated_at)
RETURNING *;

-- name: CompleteServiceRecovery :execrows
UPDATE service_recovery SET status = sqlc.arg(status)::text, failures = sqlc.arg(failures)::integer,
 next_attempt_at = sqlc.arg(next_attempt_at)::timestamptz, updated_at = sqlc.arg(updated_at)::timestamptz,
 claim_token = NULL, lease_until = '1970-01-01'::timestamptz
WHERE app_id = sqlc.arg(app_id)::uuid AND claim_token = sqlc.arg(claim_token)::uuid;

-- name: ServiceRecoveryByApp :one
SELECT * FROM service_recovery WHERE app_id = $1;

-- ADR-421: keyset paging advances even when a candidate cannot fit.
-- name: ListOrphanedAppsPage :many
SELECT a.id, a.node_id, a.ram_mb FROM apps a
WHERE a.node_id IS NOT NULL AND a.status IN ('active', 'evicted_cold')
  AND NOT EXISTS (SELECT 1 FROM compute_nodes n WHERE n.id=a.node_id AND n.active)
  AND (sqlc.narg(after_app_id)::uuid IS NULL OR a.id > sqlc.narg(after_app_id)::uuid)
  AND (sqlc.narg(dead_node_id)::uuid IS NULL OR a.node_id=sqlc.narg(dead_node_id)::uuid)
  AND (sqlc.arg(cooldown_seconds)::integer < 0 OR a.reassigned_at IS NULL
       OR a.reassigned_at < now() - make_interval(secs => sqlc.arg(cooldown_seconds)::integer))
ORDER BY a.id LIMIT sqlc.arg(batch_limit)::integer;

-- Lifecycle writers take incompatible locks, held until the transfer commits.
-- Stable node order avoids deadlocks between transfers in opposite directions.
-- name: LockOwnershipRecoveryNodes :many
SELECT id, lifecycle FROM compute_nodes
WHERE id IN (sqlc.arg(from_node_id)::uuid, sqlc.arg(to_node_id)::uuid)
ORDER BY id FOR SHARE;

-- name: ReassignOrphanedAppOwner :execrows
UPDATE apps SET node_id=sqlc.arg(to_node_id)::uuid, reassigned_at=now()
WHERE id=sqlc.arg(app_id)::uuid AND node_id=sqlc.arg(from_node_id)::uuid
  AND status IN ('active', 'evicted_cold')
  AND (sqlc.arg(cooldown_seconds)::integer < 0 OR reassigned_at IS NULL
       OR reassigned_at < now() - make_interval(secs => sqlc.arg(cooldown_seconds)::integer));

-- name: ServiceCapacityProtection :one
SELECT service_capacity_snapshot()::jsonb AS snapshot;

-- name: SetServiceCapacityProtection :one
SELECT set_service_capacity_protection(sqlc.arg(enabled)::boolean)::jsonb AS snapshot;

-- name: ServiceCapacityPlacement :one
SELECT CASE WHEN enabled THEN service_capacity_snapshot()
            ELSE jsonb_build_object('enabled', false) END::jsonb AS snapshot
FROM service_capacity_policy WHERE singleton;

-- name: FeatureFlagRequestOutcomes :many
WITH evidence AS MATERIALIZED (
 SELECT
  COALESCE(e->>'type', 'boolean')::text AS decision_type,
  (e->>'value')::text AS decision_value,
  e->>'used' = 'true' AS used,
  t.status,
  t.latency_ms,
  t.count::bigint AS request_count
 FROM request_telemetry t
 JOIN deployments d ON d.id = t.deployment_id
 CROSS JOIN LATERAL jsonb_array_elements(t.flag_evidence) AS decision(e)
 WHERE (d.scope = sqlc.arg(environment_slug)::text OR (d.scope = 'default' AND sqlc.arg(environment_slug)::text = 'production'))
  AND t.account_id = sqlc.arg(account_id)::uuid
  AND t.app_id = ANY(sqlc.arg(app_ids)::uuid[])
  AND t.received_at >= sqlc.arg(received_from)::timestamptz
  AND t.received_at < sqlc.arg(received_until)::timestamptz
  AND (sqlc.arg(customer_id)::text = '' OR t.platform_tenant_id::text = sqlc.arg(customer_id)::text)
  AND e->>'flag' = sqlc.arg(flag_key)::text
  AND (sqlc.arg(rule_id)::text = '' OR e->>'rule_id' = sqlc.arg(rule_id)::text)
  AND (sqlc.arg(config_version)::bigint = 0 OR (e->>'config_version')::bigint = sqlc.arg(config_version)::bigint)
  AND jsonb_typeof(e->'value') IN ('boolean', 'string')
  AND COALESCE(e->>'type', 'boolean') IN ('boolean', 'variant')
), totals AS (
 SELECT decision_type, decision_value,
  sum(request_count)::bigint AS request_count,
  COALESCE(sum(request_count) FILTER (WHERE used), 0)::bigint AS used_count,
  COALESCE(sum(request_count) FILTER (WHERE status >= 500), 0)::bigint AS error_count
 FROM evidence
 GROUP BY decision_type, decision_value
), latency_counts AS (
 SELECT decision_type, decision_value, latency_ms, sum(request_count)::bigint AS latency_count
 FROM evidence
 GROUP BY decision_type, decision_value, latency_ms
), latency_ranked AS (
 SELECT decision_type, decision_value, latency_ms,
  sum(latency_count) OVER (PARTITION BY decision_type, decision_value ORDER BY latency_ms) AS cumulative_count
 FROM latency_counts
)
SELECT totals.decision_type, totals.decision_value, totals.request_count, totals.used_count, totals.error_count,
 min(latency_ranked.latency_ms) FILTER (WHERE latency_ranked.cumulative_count >= ceil(totals.request_count * 0.50))::int AS p50_latency_ms,
 min(latency_ranked.latency_ms) FILTER (WHERE latency_ranked.cumulative_count >= ceil(totals.request_count * 0.95))::int AS p95_latency_ms
FROM totals
LEFT JOIN latency_ranked USING (decision_type, decision_value)
GROUP BY totals.decision_type, totals.decision_value, totals.request_count, totals.used_count, totals.error_count
-- Keep a sentinel row so the API can report when a busy flag has more than
-- the bounded response can display. Most flags have at most 16 live variants.
ORDER BY totals.request_count DESC, totals.decision_type, totals.decision_value
LIMIT 101;

-- name: SelectPendingFireNowRequestForNode :one
-- Hold placement stable while the caller changes the claimed request status.
SELECT r.id::text AS id, r.cron_id::text AS cron_id, r.account_id::text AS account_id, r.requested_at, r.status
FROM cron_fire_now_requests r
JOIN crons c ON c.id = r.cron_id
JOIN apps a ON a.id = c.app_id
WHERE r.status = 'pending'
  AND (sqlc.narg(node_id)::text IS NULL OR a.node_id IS NULL OR a.node_id::text = sqlc.narg(node_id))
ORDER BY r.requested_at ASC, r.id ASC
FOR UPDATE OF r, a SKIP LOCKED
LIMIT 1;

-- name: RequeueFireNowRequest :execrows
UPDATE cron_fire_now_requests
SET status = 'pending'
WHERE id = sqlc.arg(id)::uuid AND status = 'running';

-- name: ListAppSecretsWithBindingAccessInScope :many
SELECT s.account_id::text AS account_id, s.app_id::text AS app_id, s.scope, s.key, s.ciphertext,
 coalesce(s.kid, '')::text AS kid, coalesce(s.value_hash, '')::text AS value_hash,
 coalesce(s.managed_postgres_binding_id::text, '')::text AS managed_postgres_binding_id,
 coalesce(s.managed_credential_ref, '')::text AS managed_credential_ref,
 coalesce(s.managed_credential_generation, 0)::bigint AS managed_credential_generation,
 coalesce(s.managed_object_storage_credential_id::text, '')::text AS managed_object_storage_credential_id,
 coalesce(s.secret_version, 0)::bigint AS secret_version, s.delivery_version,
 coalesce(s.delivered_version, 0)::bigint AS delivered_version, s.delivery_status,
 s.last_delivery_attempt_at, s.last_delivered_at,
 coalesce(s.last_delivery_error_code, '')::text AS last_delivery_error_code,
 coalesce(s.last_delivered_wake_id, '')::text AS last_delivered_wake_id,
 coalesce(s.last_delivered_instance_id, '')::text AS last_delivered_instance_id,
 coalesce(s.last_runtime_reload_version, 0)::bigint AS last_runtime_reload_version,
 coalesce(s.last_runtime_reload_revision, '')::text AS last_runtime_reload_revision,
 coalesce(s.last_runtime_reload_projection, '')::text AS last_runtime_reload_projection,
 coalesce(s.last_runtime_reload_signal, '')::text AS last_runtime_reload_signal,
 s.last_runtime_reload_at, coalesce(s.last_runtime_reload_error_code, '')::text AS last_runtime_reload_error_code,
 coalesce(s.last_runtime_reload_instance_id, '')::text AS last_runtime_reload_instance_id,
 s.created_at, s.updated_at, coalesce(s.secret_class, 'persistent')::text AS secret_class,
 coalesce(b.access, '')::text AS managed_postgres_access
FROM app_secrets s
LEFT JOIN managed_postgres_bindings b ON b.id = s.managed_postgres_binding_id
 AND b.account_id = s.account_id AND b.app_id = s.app_id
 AND b.scope = s.scope AND b.environment_key = s.key AND b.state <> 'deleted'
WHERE s.account_id = sqlc.arg(account_id)::text::uuid
 AND s.app_id = sqlc.arg(app_id)::text::uuid AND s.scope = sqlc.arg(scope)::text
ORDER BY s.scope, s.key;

-- name: FinishManagedPostgresBindingProvision :one
UPDATE managed_postgres_bindings AS binding SET state = 'ready',
 provider_identity_id = sqlc.arg(provider_identity_id)::text,
 credential_ref = sqlc.arg(credential_ref)::text,
 rotation_cleanup_ready = rotation_cleanup_ready OR (access = 'migration' AND rotation_previous_generation IS NOT NULL),
 last_error_code = NULL, lease_token = NULL, lease_until = NULL,
 attempt_count = 0, retry_at = sqlc.arg(now)::timestamptz, updated_at = sqlc.arg(now)
WHERE binding.id = sqlc.arg(id)::uuid AND binding.state = 'provisioning'
 AND binding.lease_token = sqlc.arg(lease_token)::text AND binding.lease_until > sqlc.arg(now)
 AND EXISTS (SELECT 1 FROM app_secrets secret WHERE secret.managed_postgres_binding_id = binding.id
  AND secret.managed_credential_ref = sqlc.arg(credential_ref)
  AND secret.managed_credential_generation = binding.credential_generation)
RETURNING id::text AS id, account_id::text AS account_id, database_id::text AS database_id,
 app_id::text AS app_id, scope, environment_key, access,
 coalesce(provider_identity_id, '')::text AS provider_identity_id,
 coalesce(credential_ref, '')::text AS credential_ref, credential_generation,
 coalesce(rotation_previous_generation, 0)::bigint AS rotation_previous_generation,
 coalesce(rotation_wake_id::text, '')::text AS rotation_wake_id, rotation_cleanup_ready,
 state, coalesce(last_error_code, '')::text AS last_error_code,
 coalesce(lease_token, '')::text AS lease_token, lease_until,
 attempt_count, retry_at, created_at, updated_at, deleted_at;

-- name: ClaimManagedPostgresBindingRetirement :one
UPDATE managed_postgres_bindings AS binding SET state = 'retiring',
 lease_token = sqlc.arg(lease_token)::text, lease_until = sqlc.arg(lease_until)::timestamptz,
 updated_at = sqlc.arg(now)::timestamptz, retry_at = sqlc.arg(now),
 attempt_count = CASE WHEN state <> 'retiring' THEN 1 ELSE least(attempt_count + 1, 30) END,
 last_error_code = CASE WHEN state <> 'retiring' THEN NULL ELSE last_error_code END
WHERE binding.account_id = sqlc.arg(account_id)::uuid AND binding.id = sqlc.arg(id)::uuid
 AND binding.state IN ('ready','retiring') AND binding.rotation_cleanup_ready
 AND binding.rotation_previous_generation IS NOT NULL AND binding.retry_at <= sqlc.arg(now)
 AND (binding.lease_until IS NULL OR binding.lease_until <= sqlc.arg(now))
 AND (binding.access <> 'migration' OR NOT EXISTS (SELECT 1 FROM app_tasks task WHERE task.account_id = binding.account_id
 AND task.app_id = binding.app_id AND task.deployment_scope = binding.scope
 AND task.status IN ('restoring','running')))
RETURNING id::text AS id, account_id::text AS account_id, database_id::text AS database_id,
 app_id::text AS app_id, scope, environment_key, access,
 coalesce(provider_identity_id, '')::text AS provider_identity_id,
 coalesce(credential_ref, '')::text AS credential_ref, credential_generation,
 coalesce(rotation_previous_generation, 0)::bigint AS rotation_previous_generation,
 coalesce(rotation_wake_id::text, '')::text AS rotation_wake_id, rotation_cleanup_ready,
 state, coalesce(last_error_code, '')::text AS last_error_code,
 coalesce(lease_token, '')::text AS lease_token, lease_until,
 attempt_count, retry_at, created_at, updated_at, deleted_at;

-- name: ListDueManagedPostgresBindings :many
SELECT id::text AS id, account_id::text AS account_id, database_id::text AS database_id,
 app_id::text AS app_id, scope, environment_key, access,
 coalesce(provider_identity_id, '')::text AS provider_identity_id,
 coalesce(credential_ref, '')::text AS credential_ref, credential_generation,
 coalesce(rotation_previous_generation, 0)::bigint AS rotation_previous_generation,
 coalesce(rotation_wake_id::text, '')::text AS rotation_wake_id, rotation_cleanup_ready,
 state, coalesce(last_error_code, '')::text AS last_error_code,
 coalesce(lease_token, '')::text AS lease_token, lease_until,
 attempt_count, retry_at, created_at, updated_at, deleted_at FROM managed_postgres_bindings binding
WHERE (state = 'deleting' OR (sqlc.arg(include_provisioning)::boolean AND state IN ('provisioning','failed'))
 OR (rotation_cleanup_ready AND state IN ('ready','retiring')
  AND (access <> 'migration' OR NOT EXISTS (SELECT 1 FROM app_tasks task WHERE task.account_id = binding.account_id
 AND task.app_id = binding.app_id AND task.deployment_scope = binding.scope
 AND task.status IN ('restoring','running')))))
 AND retry_at <= sqlc.arg(now)::timestamptz AND (lease_until IS NULL OR lease_until <= sqlc.arg(now))
ORDER BY retry_at, id LIMIT sqlc.arg(batch_size)::int;

-- name: ClaimManagedPostgresHealthCheck :one
WITH candidate AS (
 SELECT d.* FROM managed_postgres_databases d
 LEFT JOIN managed_postgres_health h ON h.database_id = d.id
 WHERE d.state = 'ready' AND d.provider_resource_id IS NOT NULL
 AND (h.database_id IS NULL OR h.next_check_at <= sqlc.arg(now)::timestamptz
  OR h.provider_resource_id <> d.provider_resource_id OR h.backend_fingerprint <> d.backend_fingerprint
  OR h.backend_id <> d.backend_id OR h.desired_generation <> d.desired_generation)
 AND (h.lease_until IS NULL OR h.lease_until <= sqlc.arg(now))
 ORDER BY coalesce(h.next_check_at, d.created_at), d.id
 LIMIT 1 FOR UPDATE OF d SKIP LOCKED
), claimed AS (
 INSERT INTO managed_postgres_health AS h
 (database_id, account_id, backend_id, backend_fingerprint, provider_resource_id, desired_generation, next_check_at, lease_token, lease_until)
 SELECT id, account_id, backend_id, backend_fingerprint, provider_resource_id, desired_generation,
 sqlc.arg(now), sqlc.arg(lease_token)::text, sqlc.arg(lease_until)::timestamptz FROM candidate
 ON CONFLICT (database_id) DO UPDATE SET
 account_id = EXCLUDED.account_id, backend_id = EXCLUDED.backend_id,
 backend_fingerprint = EXCLUDED.backend_fingerprint, provider_resource_id = EXCLUDED.provider_resource_id,
 desired_generation = EXCLUDED.desired_generation, next_check_at = EXCLUDED.next_check_at,
 lease_token = EXCLUDED.lease_token, lease_until = EXCLUDED.lease_until,
 provider_status = CASE WHEN (h.backend_id,h.backend_fingerprint,h.provider_resource_id,h.desired_generation)
  IS DISTINCT FROM (EXCLUDED.backend_id,EXCLUDED.backend_fingerprint,EXCLUDED.provider_resource_id,EXCLUDED.desired_generation) THEN 'unknown' ELSE h.provider_status END,
 compute_state = CASE WHEN (h.backend_id,h.backend_fingerprint,h.provider_resource_id,h.desired_generation)
  IS DISTINCT FROM (EXCLUDED.backend_id,EXCLUDED.backend_fingerprint,EXCLUDED.provider_resource_id,EXCLUDED.desired_generation) THEN 'unknown' ELSE h.compute_state END,
 checked_at = CASE WHEN (h.backend_id,h.backend_fingerprint,h.provider_resource_id,h.desired_generation)
  IS DISTINCT FROM (EXCLUDED.backend_id,EXCLUDED.backend_fingerprint,EXCLUDED.provider_resource_id,EXCLUDED.desired_generation) THEN NULL ELSE h.checked_at END,
 last_success_at = CASE WHEN (h.backend_id,h.backend_fingerprint,h.provider_resource_id,h.desired_generation)
  IS DISTINCT FROM (EXCLUDED.backend_id,EXCLUDED.backend_fingerprint,EXCLUDED.provider_resource_id,EXCLUDED.desired_generation) THEN NULL ELSE h.last_success_at END,
 last_error_code = CASE WHEN (h.backend_id,h.backend_fingerprint,h.provider_resource_id,h.desired_generation)
  IS DISTINCT FROM (EXCLUDED.backend_id,EXCLUDED.backend_fingerprint,EXCLUDED.provider_resource_id,EXCLUDED.desired_generation) THEN NULL ELSE h.last_error_code END,
 attempt_count = CASE WHEN (h.backend_id,h.backend_fingerprint,h.provider_resource_id,h.desired_generation)
  IS DISTINCT FROM (EXCLUDED.backend_id,EXCLUDED.backend_fingerprint,EXCLUDED.provider_resource_id,EXCLUDED.desired_generation) THEN 0 ELSE h.attempt_count END
 WHERE h.lease_until IS NULL OR h.lease_until <= sqlc.arg(now)
 RETURNING database_id, attempt_count
)
SELECT d.id::text AS id, d.account_id::text AS account_id, d.backend_id, d.backend_fingerprint,
 d.provider_resource_id::text AS provider_resource_id, d.desired_generation,
 d.region, d.postgres_major, d.service_class, d.availability, d.scale_to_zero, d.storage_limit_bytes,
 d.restore_window_seconds, c.attempt_count
FROM candidate d JOIN claimed c ON c.database_id = d.id;

-- name: FinishManagedPostgresHealthCheck :execrows
WITH target AS (
 SELECT d.* FROM managed_postgres_databases d
 WHERE d.id = sqlc.arg(database_id)::text::uuid AND d.state = 'ready' FOR SHARE
)
UPDATE managed_postgres_health h SET
 provider_status = sqlc.arg(provider_status)::text, compute_state = sqlc.arg(compute_state)::text,
 checked_at = sqlc.arg(checked_at)::timestamptz,
 last_success_at = CASE WHEN sqlc.arg(succeeded)::boolean THEN sqlc.arg(checked_at) ELSE h.last_success_at END,
 last_error_code = nullif(sqlc.arg(error_code)::text, ''), next_check_at = sqlc.arg(next_check_at)::timestamptz,
 attempt_count = CASE WHEN sqlc.arg(succeeded) THEN 0 ELSE least(h.attempt_count + 1,20) END,
 lease_token = NULL, lease_until = NULL
FROM target d
WHERE h.database_id = sqlc.arg(database_id)::text::uuid AND h.database_id = d.id AND d.state = 'ready'
 AND h.lease_token = sqlc.arg(lease_token)::text AND h.lease_until > sqlc.arg(checked_at)
 AND (d.account_id,d.backend_id,d.backend_fingerprint,d.provider_resource_id,d.desired_generation)
 = (h.account_id,h.backend_id,h.backend_fingerprint,h.provider_resource_id,h.desired_generation);

-- name: ReadManagedPostgresHealthSnapshots :many
SELECT h.database_id::text AS database_id, h.provider_status, h.compute_state,
 h.checked_at, h.last_success_at, coalesce(h.last_error_code,'')::text AS last_error_code
FROM managed_postgres_health h JOIN managed_postgres_databases d ON d.id = h.database_id
WHERE d.account_id = sqlc.arg(account_id)::text::uuid AND h.account_id = d.account_id AND d.state = 'ready'
 AND h.database_id::text = ANY(sqlc.arg(database_ids)::text[])
 AND (d.backend_id,d.backend_fingerprint,d.provider_resource_id,d.desired_generation)
 = (h.backend_id,h.backend_fingerprint,h.provider_resource_id,h.desired_generation);

-- name: CountManagedPostgresHealth :one
WITH statuses AS (
 SELECT CASE
 WHEN h.checked_at IS NULL AND d.created_at >= sqlc.arg(cutoff)::timestamptz THEN 'unknown'
 WHEN h.checked_at IS NULL OR h.checked_at < sqlc.arg(cutoff) OR h.checked_at > sqlc.arg(now)::timestamptz THEN 'stale'
 WHEN h.last_error_code IS NOT NULL OR h.provider_status <> 'ready' OR h.compute_state = 'unknown' THEN 'degraded'
 ELSE 'healthy' END AS status
 FROM managed_postgres_databases d LEFT JOIN managed_postgres_health h ON h.database_id = d.id
 AND h.account_id = d.account_id
 AND (d.backend_id,d.backend_fingerprint,d.provider_resource_id,d.desired_generation)
 = (h.backend_id,h.backend_fingerprint,h.provider_resource_id,h.desired_generation)
 WHERE d.state = 'ready'
)
SELECT count(*) FILTER (WHERE status='healthy') AS healthy,
 count(*) FILTER (WHERE status='degraded') AS degraded,
 count(*) FILTER (WHERE status='unknown') AS unknown,
 count(*) FILTER (WHERE status='stale') AS stale FROM statuses;

-- name: LockManagedPostgresCutoverAccount :one
SELECT status FROM accounts WHERE id=sqlc.arg(account_id)::text::uuid FOR KEY SHARE;

-- name: LockManagedPostgresCutoverApp :one
SELECT account_id::text AS account_id, status FROM apps WHERE id=sqlc.arg(app_id)::text::uuid FOR KEY SHARE;

-- name: LockManagedPostgresCutoverAdmissionApp :one
SELECT account_id::text AS account_id,status,managed_postgres_admission_cutover_id,
 managed_postgres_admission_fenced_at FROM apps WHERE id=sqlc.arg(app_id)::text::uuid FOR UPDATE;

-- name: FenceManagedPostgresCutoverAdmission :one
UPDATE apps SET managed_postgres_admission_cutover_id=sqlc.arg(cutover_id)::text::uuid,
 managed_postgres_admission_fenced_at=clock_timestamp()
WHERE id=sqlc.arg(app_id)::text::uuid AND managed_postgres_admission_cutover_id IS NULL
RETURNING managed_postgres_admission_fenced_at;

-- name: UnfenceCancelledManagedPostgresCutover :exec
UPDATE apps SET managed_postgres_admission_cutover_id=NULL,managed_postgres_admission_fenced_at=NULL
WHERE managed_postgres_admission_cutover_id=sqlc.arg(cutover_id)::text::uuid;

-- name: ManagedPostgresAdmissionFenced :one
SELECT (managed_postgres_admission_cutover_id IS NOT NULL)::boolean AS fenced FROM apps WHERE id=sqlc.arg(app_id)::text::uuid;

-- name: CancelExpiredAppTasksForClaim :exec
UPDATE app_tasks task SET status='cancelled',retry_at=NULL,finished_at=sqlc.arg(claimed_at)::timestamptz,updated_at=sqlc.arg(claimed_at)
WHERE task.status='queued' AND task.start_deadline_at<sqlc.arg(claimed_at)
AND (task.occurrence_id IS NULL OR NOT EXISTS (SELECT 1 FROM schedule_occurrences occurrence
 WHERE occurrence.id=task.occurrence_id AND occurrence.started_at IS NOT NULL));

-- name: ClaimNextUnfencedAppTask :one
WITH candidate AS (
 SELECT task.id FROM app_tasks task JOIN apps app ON app.id=task.app_id
 WHERE task.status='queued' AND task.cancel_requested_at IS NULL
 AND app.managed_postgres_admission_cutover_id IS NULL
 AND task.created_at<=sqlc.arg(claimed_at)::timestamptz
 AND (task.retry_at IS NULL OR task.retry_at<=sqlc.arg(claimed_at))
 AND (task.start_deadline_at IS NULL OR task.start_deadline_at>=sqlc.arg(claimed_at)
  OR EXISTS (SELECT 1 FROM schedule_occurrences occurrence WHERE occurrence.id=task.occurrence_id AND occurrence.started_at IS NOT NULL))
 AND (task.exclusive_operation_id IS NULL OR EXISTS (
  SELECT 1 FROM exclusive_work_operations operation
  WHERE operation.id=task.exclusive_operation_id AND operation.account_id=task.account_id
  AND operation.app_id=task.app_id AND operation.state='running'
  AND operation.generation=task.exclusive_generation
  AND operation.lease_expires_at>clock_timestamp() AND operation.attempt_deadline>clock_timestamp()))
 ORDER BY coalesce(task.retry_at,task.created_at),task.created_at,task.id
 LIMIT 1 FOR UPDATE OF task SKIP LOCKED
)
UPDATE app_tasks task SET status='restoring',lease_token=gen_random_uuid(),lease_owner=sqlc.arg(owner)::text,
lease_expires_at=sqlc.arg(expires_at)::timestamptz,retry_at=NULL,stdout_tail='',stderr_tail='',output_truncated=false,
exit_code=NULL,failure_code=NULL,failure_message=NULL,updated_at=sqlc.arg(claimed_at)
FROM candidate WHERE task.id=candidate.id RETURNING task.*;

-- name: MarkUnfencedAppTaskRunning :one
UPDATE app_tasks SET status='running',started_at=sqlc.arg(started_at)::timestamptz,attempt_count=attempt_count+1,
retry_at=NULL,stdout_tail='',stderr_tail='',output_truncated=false,exit_code=NULL,failure_code=NULL,failure_message=NULL,updated_at=sqlc.arg(started_at)
WHERE id=sqlc.arg(id)::text::uuid AND status='restoring' AND lease_token=sqlc.arg(token)::text::uuid
AND cancel_requested_at IS NULL AND lease_expires_at>sqlc.arg(started_at)
AND (start_deadline_at IS NULL OR start_deadline_at>=sqlc.arg(started_at) OR EXISTS (
 SELECT 1 FROM schedule_occurrences occurrence WHERE occurrence.id=app_tasks.occurrence_id AND occurrence.started_at IS NOT NULL))
RETURNING *;

-- name: LockManagedPostgresCutoverDatabases :many
SELECT * FROM managed_postgres_databases
WHERE id::text=ANY(sqlc.arg(database_ids)::text[]) ORDER BY id FOR UPDATE;

-- name: LockManagedPostgresCutoverBindings :many
SELECT * FROM managed_postgres_bindings WHERE account_id=sqlc.arg(account_id)::text::uuid
AND database_id=sqlc.arg(database_id)::text::uuid AND app_id=sqlc.arg(app_id)::text::uuid
AND scope=sqlc.arg(scope)::text AND state<>'deleted' ORDER BY id FOR UPDATE;

-- name: GetManagedPostgresCutover :one
SELECT * FROM managed_postgres_cutovers WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(id)::text::uuid;

-- name: GetActiveManagedPostgresCutover :one
SELECT * FROM managed_postgres_cutovers WHERE account_id=sqlc.arg(account_id)::text::uuid
AND app_id=sqlc.arg(app_id)::text::uuid AND scope=sqlc.arg(scope)::text AND state<>'cancelled';

-- name: ListManagedPostgresCutoverCredentials :many
SELECT * FROM managed_postgres_cutover_credentials WHERE cutover_id=sqlc.arg(id)::text::uuid ORDER BY environment_key;

-- name: InsertManagedPostgresCutover :exec
INSERT INTO managed_postgres_cutovers(id,account_id,app_id,scope,source_database_id,target_database_id,
source_backend_id,source_backend_fingerprint,source_resource_id,source_generation,
target_backend_id,target_backend_fingerprint,target_resource_id,target_generation,state,retry_at,created_at,updated_at)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(app_id)::text::uuid,sqlc.arg(scope)::text,
sqlc.arg(source_id)::text::uuid,sqlc.arg(target_id)::text::uuid,
sqlc.arg(source_backend)::text,sqlc.arg(source_fingerprint)::text,sqlc.arg(source_resource)::text,sqlc.arg(source_generation)::bigint,
sqlc.arg(target_backend)::text,sqlc.arg(target_fingerprint)::text,sqlc.arg(target_resource)::text,sqlc.arg(target_generation)::bigint,
'preparing',sqlc.arg(now)::timestamptz,sqlc.arg(now),sqlc.arg(now));

-- name: PinManagedPostgresCutoverDatabases :execrows
UPDATE managed_postgres_databases SET cutover_id=sqlc.arg(id)::text::uuid
WHERE id::text=ANY(sqlc.arg(database_ids)::text[]) AND cutover_id IS NULL AND state='ready';

-- name: InsertManagedPostgresCutoverCredential :execrows
WITH pinned AS (UPDATE managed_postgres_bindings SET cutover_id=sqlc.arg(cutover_id)::text::uuid
WHERE id=sqlc.arg(binding_id)::text::uuid AND cutover_id IS NULL AND state='ready' AND coalesce(rotation_previous_generation,0)=0 RETURNING *)
INSERT INTO managed_postgres_cutover_credentials(id,cutover_id,source_binding_id,source_credential_generation,environment_key,access)
SELECT sqlc.arg(id)::text::uuid,cutover_id,id,credential_generation,environment_key,access FROM pinned;

-- name: ClaimManagedPostgresCutover :one
UPDATE managed_postgres_cutovers SET lease_token=sqlc.arg(token)::text,lease_until=sqlc.arg(until)::timestamptz,
attempt_count=least(attempt_count+1,30),updated_at=sqlc.arg(now)::timestamptz
WHERE id=sqlc.arg(id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
AND state IN ('preparing','verifying','cancelling') AND retry_at<=sqlc.arg(now) AND (lease_until IS NULL OR lease_until<=sqlc.arg(now)) RETURNING *;

-- name: LockManagedPostgresCutoverLease :one
SELECT * FROM managed_postgres_cutovers WHERE id=sqlc.arg(id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
AND lease_token=sqlc.arg(token)::text AND lease_until>sqlc.arg(now)::timestamptz FOR UPDATE;

-- name: SaveManagedPostgresCutoverCredential :execrows
UPDATE managed_postgres_cutover_credentials SET state='sealed',provider_identity_id=sqlc.arg(provider_identity)::text,
credential_ref=sqlc.arg(ref)::text,ciphertext=sqlc.arg(ciphertext)::bytea,kid=sqlc.arg(kid)::text,value_hash=sqlc.arg(value_hash)::text
WHERE id=sqlc.arg(id)::text::uuid AND cutover_id=sqlc.arg(cutover_id)::text::uuid AND state='pending'
AND EXISTS (SELECT 1 FROM managed_postgres_cutovers c WHERE c.id=sqlc.arg(cutover_id)::text::uuid
 AND c.state='preparing' AND c.lease_token=sqlc.arg(token)::text AND c.lease_until>clock_timestamp());

-- name: RevokeManagedPostgresCutoverCredential :execrows
UPDATE managed_postgres_cutover_credentials SET state='revoked',provider_identity_id=NULL,credential_ref=NULL,ciphertext=NULL,kid=NULL,value_hash=NULL,verified_at=NULL
WHERE id=sqlc.arg(id)::text::uuid AND cutover_id=sqlc.arg(cutover_id)::text::uuid AND state<>'revoked'
AND EXISTS (SELECT 1 FROM managed_postgres_cutovers c WHERE c.id=sqlc.arg(cutover_id)::text::uuid
 AND c.state='cancelling' AND c.lease_token=sqlc.arg(token)::text AND c.lease_until>clock_timestamp());

-- name: FinishManagedPostgresCutoverStep :exec
UPDATE managed_postgres_cutovers c SET
state=CASE WHEN c.state='preparing' AND NOT EXISTS(SELECT 1 FROM managed_postgres_cutover_credentials WHERE cutover_id=c.id AND state<>'sealed') THEN 'prepared'
WHEN c.state='cancelling' AND NOT EXISTS(SELECT 1 FROM managed_postgres_cutover_credentials WHERE cutover_id=c.id AND state<>'revoked') THEN 'cancelled' ELSE c.state END,
lease_token=NULL,lease_until=NULL,attempt_count=0,last_error_code=NULL,retry_at=sqlc.arg(now)::timestamptz,updated_at=sqlc.arg(now)
WHERE id=sqlc.arg(id)::text::uuid;

-- name: UnpinManagedPostgresCutoverDatabases :exec
UPDATE managed_postgres_databases SET cutover_id=NULL WHERE cutover_id=sqlc.arg(id)::text::uuid;

-- name: UnpinManagedPostgresCutoverBindings :exec
UPDATE managed_postgres_bindings SET cutover_id=NULL WHERE cutover_id=sqlc.arg(id)::text::uuid;

-- name: ReleaseManagedPostgresCutover :execrows
UPDATE managed_postgres_cutovers SET lease_token=NULL,lease_until=NULL,last_error_code=sqlc.arg(code)::text,
retry_at=sqlc.arg(retry_at)::timestamptz,updated_at=sqlc.arg(now)::timestamptz
WHERE id=sqlc.arg(id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
AND lease_token=sqlc.arg(token)::text AND lease_until>sqlc.arg(now);

-- name: CancelManagedPostgresCutover :one
UPDATE managed_postgres_cutovers SET state=CASE WHEN state='cancelled' THEN state ELSE 'cancelling' END,verified_at=NULL,
retry_at=sqlc.arg(now)::timestamptz,updated_at=sqlc.arg(now)
WHERE id=sqlc.arg(id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid RETURNING *;

-- name: CancelDeletedOwnerManagedPostgresCutovers :exec
UPDATE managed_postgres_cutovers c SET state='cancelling',verified_at=NULL,retry_at=sqlc.arg(now)::timestamptz,updated_at=sqlc.arg(now)
FROM accounts a, apps app WHERE a.id=c.account_id AND app.id=c.app_id
AND (a.status='deleted_pending' OR app.status='deleted') AND c.state IN ('preparing','prepared','verifying','verified');

-- name: ListDueManagedPostgresCutovers :many
SELECT * FROM managed_postgres_cutovers WHERE (state='cancelling' OR (state IN ('preparing','verifying') AND sqlc.arg(include_preparing)::boolean))
AND retry_at<=sqlc.arg(now)::timestamptz AND (lease_until IS NULL OR lease_until<=sqlc.arg(now))
ORDER BY retry_at,id LIMIT sqlc.arg(batch_size)::int;

-- name: CountManagedPostgresCutoverTargetBindings :one
SELECT count(*) FROM managed_postgres_bindings WHERE database_id=sqlc.arg(id)::text::uuid AND state<>'deleted';

-- name: LockManagedPostgresCutoverForVerification :one
SELECT * FROM managed_postgres_cutovers WHERE account_id=sqlc.arg(account_id)::text::uuid
AND id=sqlc.arg(id)::text::uuid FOR UPDATE;

-- name: RequestManagedPostgresCutoverVerification :exec
UPDATE managed_postgres_cutovers SET state='verifying',verified_at=NULL,lease_token=NULL,lease_until=NULL,
attempt_count=0,last_error_code=NULL,retry_at=sqlc.arg(now)::timestamptz,updated_at=sqlc.arg(now)
WHERE id=sqlc.arg(id)::text::uuid;

-- name: ResetManagedPostgresCutoverVerification :exec
UPDATE managed_postgres_cutover_credentials SET verified_at=NULL WHERE cutover_id=sqlc.arg(id)::text::uuid;

-- name: SaveManagedPostgresCutoverVerification :execrows
UPDATE managed_postgres_cutover_credentials SET verified_at=sqlc.arg(now)::timestamptz
WHERE cutover_id=sqlc.arg(cutover_id)::text::uuid AND id=sqlc.arg(id)::text::uuid AND state='sealed'
AND provider_identity_id=sqlc.arg(provider_identity)::text AND credential_ref=sqlc.arg(ref)::text
AND ciphertext=sqlc.arg(ciphertext)::bytea AND kid=sqlc.arg(kid)::text AND value_hash=sqlc.arg(value_hash)::text
AND EXISTS (SELECT 1 FROM managed_postgres_cutovers c WHERE c.id=sqlc.arg(cutover_id)::text::uuid
 AND c.state='verifying' AND c.lease_token=sqlc.arg(token)::text
 AND c.lease_until>sqlc.arg(now)::timestamptz AND c.lease_until>clock_timestamp());

-- name: FinishManagedPostgresCutoverVerification :exec
WITH evidence AS (SELECT count(*)>0 AND bool_and(state='sealed' AND verified_at IS NOT NULL
 AND verified_at>=sqlc.arg(cutoff)::timestamptz AND verified_at<=sqlc.arg(now)::timestamptz) AS complete
 FROM managed_postgres_cutover_credentials WHERE cutover_id=sqlc.arg(id)::text::uuid)
UPDATE managed_postgres_cutovers SET state=CASE WHEN evidence.complete THEN 'verified' ELSE state END,
verified_at=CASE WHEN evidence.complete THEN sqlc.arg(now) ELSE NULL END,
lease_token=CASE WHEN evidence.complete THEN NULL ELSE lease_token END,
lease_until=CASE WHEN evidence.complete THEN NULL ELSE lease_until END,
attempt_count=CASE WHEN evidence.complete THEN 0 ELSE attempt_count END,
last_error_code=NULL,updated_at=sqlc.arg(now),retry_at=sqlc.arg(now)
FROM evidence WHERE id=sqlc.arg(id)::text::uuid;

-- name: ProbeManagedPostgresCredential :one
SELECT session_user::text AS login, current_user::text AS effective_user,
 current_database()::text AS database_name, current_setting('server_version_num')::integer AS version_num,
 current_setting('transaction_read_only')::boolean AS read_only,
 current_setting('row_security')='on' AS row_security,
 has_schema_privilege(current_user,'public','USAGE') AS schema_usage,
 has_schema_privilege(current_user,'public','CREATE') AS schema_create,
 (r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls OR r.rolinherit
 OR e.rolsuper OR e.rolcreatedb OR e.rolcreaterole OR e.rolreplication OR e.rolbypassrls OR e.rolinherit) AS unsafe_role,
 (has_database_privilege(current_user,current_database(),'CREATE')
 OR has_database_privilege(current_user,current_database(),'TEMPORARY')
 OR EXISTS (SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=r.oid
 AND (sqlc.arg(access)::text<>'migration' OR roleid<>e.oid))
 OR EXISTS (SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=e.oid AND e.oid<>r.oid)) AS elevated_runtime,
 NOT EXISTS (SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='public' AND ((c.relkind IN ('r','p','v','m','f') AND NOT
 (has_table_privilege(current_user,c.oid,'SELECT') AND
 ((sqlc.arg(access)::text='read_only' AND NOT (has_table_privilege(current_user,c.oid,'INSERT')
 OR has_table_privilege(current_user,c.oid,'UPDATE') OR has_table_privilege(current_user,c.oid,'DELETE')))
 OR (sqlc.arg(access)::text<>'read_only' AND has_table_privilege(current_user,c.oid,'INSERT')
 AND has_table_privilege(current_user,c.oid,'UPDATE') AND has_table_privilege(current_user,c.oid,'DELETE')))
 AND (sqlc.arg(access)::text='migration' OR
 (c.relowner<>e.oid AND NOT has_table_privilege(current_user,c.oid,'TRUNCATE')))))
 OR (c.relkind='S' AND NOT (has_sequence_privilege(current_user,c.oid,'SELECT') AND
 ((sqlc.arg(access)::text='read_only' AND NOT (has_sequence_privilege(current_user,c.oid,'USAGE')
 OR has_sequence_privilege(current_user,c.oid,'UPDATE'))) OR (sqlc.arg(access)::text<>'read_only'
 AND has_sequence_privilege(current_user,c.oid,'USAGE'))))))) AS data_access
FROM pg_catalog.pg_roles r JOIN pg_catalog.pg_roles e ON e.rolname=current_user WHERE r.rolname=session_user;

-- name: LockUDPListenerAppOwner :one
SELECT account_id::text AS account_id FROM apps
WHERE id = sqlc.arg(app_id)::text::uuid AND status <> 'deleted'
FOR UPDATE;

-- name: CreateUDPListener :one
INSERT INTO app_udp_listeners
(id, app_id, account_id, listener_name, guest_port, public_port, protocol, enabled)
VALUES (sqlc.arg(id)::text::uuid, sqlc.arg(app_id)::text::uuid,
        sqlc.arg(account_id)::text::uuid, sqlc.arg(listener_name),
        sqlc.arg(guest_port), sqlc.arg(public_port), sqlc.arg(protocol), sqlc.arg(enabled))
RETURNING *;

-- name: UDPListenerByID :one
SELECT * FROM app_udp_listeners WHERE id = sqlc.arg(id)::text::uuid;

-- name: UDPListenerByAppAndName :one
SELECT * FROM app_udp_listeners
WHERE app_id = sqlc.arg(app_id)::text::uuid AND listener_name = sqlc.arg(listener_name);

-- name: UDPListenerByPublicPort :one
SELECT * FROM app_udp_listeners l
WHERE public_port = sqlc.arg(public_port) AND enabled
AND EXISTS (SELECT 1 FROM apps a WHERE a.id = l.app_id AND a.account_id = l.account_id AND a.status <> 'deleted');

-- name: ListUDPListenersForApp :many
SELECT * FROM app_udp_listeners
WHERE app_id = sqlc.arg(app_id)::text::uuid ORDER BY created_at DESC, id DESC;

-- name: ListEnabledUDPListeners :many
SELECT * FROM app_udp_listeners l
WHERE enabled AND EXISTS (SELECT 1 FROM apps a WHERE a.id = l.app_id AND a.account_id = l.account_id AND a.status <> 'deleted')
ORDER BY public_port ASC;

-- name: SetUDPListenerEnabled :one
UPDATE app_udp_listeners SET enabled = sqlc.arg(enabled), updated_at = now()
WHERE id = sqlc.arg(id)::text::uuid RETURNING *;

-- name: DeleteUDPListener :execrows
DELETE FROM app_udp_listeners WHERE id = sqlc.arg(id)::text::uuid;

-- name: CountUDPListenersForApp :one
SELECT count(*) FROM app_udp_listeners WHERE app_id = sqlc.arg(app_id)::text::uuid;

-- name: ActiveTCPListenerByPublicPort :one
SELECT l.* FROM app_tcp_listeners l JOIN apps a ON a.id = l.app_id
WHERE l.public_port = sqlc.arg(public_port) AND l.enabled
  AND a.status <> 'deleted' AND a.account_id = l.account_id;

-- name: ListActiveTCPListeners :many
SELECT l.* FROM app_tcp_listeners l JOIN apps a ON a.id = l.app_id
WHERE l.enabled AND a.status <> 'deleted' AND a.account_id = l.account_id
ORDER BY l.public_port;
-- name: LockExclusiveWorkAccount :one
SELECT id::text,plan FROM accounts WHERE id=sqlc.arg(account_id)::text::uuid
AND status='active' FOR UPDATE;

-- name: ExclusiveWorkAppScope :one
SELECT id::text, coalesce(project_id::text,'')::text AS project_id
FROM apps WHERE id=sqlc.arg(app_id)::text::uuid
AND account_id=sqlc.arg(account_id)::text::uuid AND status<>'deleted' FOR SHARE;

-- name: ExclusiveWorkTenantScope :one
SELECT id::text, status FROM platform_tenants
WHERE id=sqlc.arg(tenant_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
FOR SHARE;

-- name: ExclusiveWorkEnvironmentScope :one
SELECT id::text FROM project_environments
WHERE id=sqlc.arg(environment_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
AND project_id=sqlc.arg(project_id)::text::uuid FOR SHARE;

-- name: ReadExclusiveWorkPolicy :one
SELECT * FROM exclusive_work_policies
WHERE account_id=sqlc.arg(account_id)::text::uuid AND name=sqlc.arg(name)::text FOR UPDATE;

-- name: ListExclusiveWorkPolicies :many
SELECT * FROM exclusive_work_policies
WHERE account_id=sqlc.arg(account_id)::text::uuid ORDER BY name;

-- name: SaveExclusiveWorkPolicy :one
INSERT INTO exclusive_work_policies(id,account_id,name,configuration,retired)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(name)::text,sqlc.arg(configuration)::jsonb,sqlc.arg(retired)::boolean)
ON CONFLICT(account_id,name) DO UPDATE SET configuration=excluded.configuration,retired=excluded.retired,
revision=exclusive_work_policies.revision+1,updated_at=clock_timestamp()
RETURNING *;

-- name: ExclusiveWorkPolicyInUse :one
SELECT EXISTS (
    SELECT 1 FROM exclusive_work_operations o
    JOIN exclusive_work_keys k ON k.id=o.key_id
    WHERE k.policy_id=sqlc.arg(policy_id)::text::uuid AND o.state IN ('pending','running')
) OR EXISTS (
    SELECT 1 FROM exclusive_work_trigger_bindings
    WHERE policy_id=sqlc.arg(policy_id)::text::uuid
) OR EXISTS (
    SELECT 1 FROM commit_sources c JOIN exclusive_work_policies p
    ON p.account_id=c.account_id AND p.name=c.operation_policy
    WHERE p.id=sqlc.arg(policy_id)::text::uuid AND c.enabled
) AS in_use;

-- name: EnsureExclusiveWorkKey :one
INSERT INTO exclusive_work_keys(id,account_id,policy_id,scope_id,environment_id,key_digest)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(policy_id)::text::uuid,
sqlc.arg(scope_id)::text::uuid,sqlc.arg(environment_id)::text,sqlc.arg(key_digest)::bytea)
ON CONFLICT(policy_id,scope_id,environment_id,key_digest) DO UPDATE SET id=exclusive_work_keys.id
RETURNING *;

-- name: ReadExclusiveWorkKey :one
SELECT * FROM exclusive_work_keys
WHERE id=sqlc.arg(id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid FOR UPDATE;

-- name: SaveExclusiveWorkKey :exec
UPDATE exclusive_work_keys SET generation=sqlc.arg(generation)::bigint,
next_sequence=sqlc.arg(next_sequence)::bigint WHERE id=sqlc.arg(id)::text::uuid;

-- name: ReadExclusiveWorkOperation :one
SELECT * FROM exclusive_work_operations
WHERE id=sqlc.arg(id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid;

-- name: ReadExclusiveWorkReplay :one
SELECT o.* FROM exclusive_work_operations o JOIN exclusive_work_submissions s ON s.operation_id=o.id
WHERE s.key_id=sqlc.arg(key_id)::text::uuid AND s.idempotency_digest=sqlc.arg(digest)::bytea;

-- name: ListExclusiveWorkActive :many
SELECT * FROM exclusive_work_operations
WHERE key_id=sqlc.arg(key_id)::text::uuid AND state IN ('pending','running') ORDER BY sequence;

-- name: CountExclusiveWorkPending :one
SELECT count(*) FROM exclusive_work_operations
WHERE account_id=sqlc.arg(account_id)::text::uuid AND state IN ('pending','running');

-- name: InsertExclusiveWorkOperation :one
INSERT INTO exclusive_work_operations(id,account_id,key_id,app_id,job_id,platform_tenant_id,sequence,
policy_revision,configuration,request,request_digest,equivalence_digest,idempotency_digest)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(key_id)::text::uuid,
sqlc.narg(app_id)::text::uuid,sqlc.narg(job_id)::text::uuid,nullif(sqlc.arg(tenant_id)::text,'')::uuid,
sqlc.arg(sequence)::bigint,sqlc.arg(policy_revision)::bigint,sqlc.arg(configuration)::jsonb,
sqlc.arg(request)::jsonb,sqlc.arg(request_digest)::bytea,sqlc.narg(equivalence_digest)::bytea,
sqlc.narg(idempotency_digest)::bytea) RETURNING *;

-- name: SaveExclusiveWorkOperation :exec
UPDATE exclusive_work_operations SET state=sqlc.arg(state)::text,generation=sqlc.arg(generation)::bigint,
claim_token=nullif(sqlc.arg(claim_token)::text,'')::uuid,incarnation_id=sqlc.arg(incarnation_id)::text,
lease_expires_at=sqlc.narg(lease_expires_at)::timestamptz,
attempt_deadline=sqlc.narg(attempt_deadline)::timestamptz,result=sqlc.narg(result)::jsonb,
last_error=sqlc.arg(last_error)::text,completed_at=sqlc.narg(completed_at)::timestamptz,
due_at=sqlc.arg(due_at)::timestamptz,attempts=sqlc.arg(attempts)::integer,
quota_reserved=sqlc.arg(quota_reserved)::boolean
WHERE id=sqlc.arg(id)::text::uuid;

-- name: InsertExclusiveWorkEffect :exec
INSERT INTO exclusive_work_effects(id,operation_id,generation,name,payload)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(operation_id)::text::uuid,
sqlc.arg(generation)::bigint,sqlc.arg(name)::text,sqlc.arg(payload)::jsonb);

-- name: ExclusiveWorkClock :one
SELECT clock_timestamp()::timestamptz AS now;

-- name: BindExclusiveWorkSubmission :exec
INSERT INTO exclusive_work_submissions(key_id,idempotency_digest,operation_id)
VALUES(sqlc.arg(key_id)::text::uuid,sqlc.arg(digest)::bytea,sqlc.arg(operation_id)::text::uuid);

-- name: CheckExclusiveWorkRuntime :one
SELECT i.id::text FROM instances i JOIN apps a ON a.id=i.app_id
WHERE i.id=sqlc.arg(instance_id)::text::uuid AND i.wake_id=sqlc.arg(wake_id)::text::uuid
AND i.node_id=sqlc.arg(node_id)::text::uuid AND i.app_id=sqlc.arg(app_id)::text::uuid
AND a.account_id=sqlc.arg(account_id)::text::uuid AND i.state='running' AND NOT i.exclusive_capture_blocked
FOR SHARE OF i;

-- name: ListDueExclusiveWork :many
WITH heads AS (
 SELECT DISTINCT ON (o.key_id) o.* FROM exclusive_work_operations o
 JOIN accounts a ON a.id=o.account_id AND a.status='active'
 LEFT JOIN platform_tenants t ON t.id=o.platform_tenant_id
 WHERE o.state IN ('pending','running') AND (t.id IS NULL OR t.status='active')
 ORDER BY o.key_id,o.sequence
), ranked AS (
 SELECT id,row_number() OVER (PARTITION BY account_id ORDER BY due_at,id) AS account_rank,due_at
 FROM heads WHERE (state='pending' AND due_at<=clock_timestamp())
 OR (state='running' AND lease_expires_at<=clock_timestamp())
)
SELECT o.* FROM exclusive_work_operations o JOIN ranked r ON r.id=o.id
ORDER BY r.account_rank,r.due_at,o.id LIMIT sqlc.arg(row_limit)::integer;

-- name: EnsureExclusiveWorkQuota :exec
INSERT INTO account_async_quota(account_id,max_inflight)
VALUES(sqlc.arg(account_id)::text::uuid,sqlc.arg(max_inflight)::integer)
ON CONFLICT(account_id) DO UPDATE SET max_inflight=excluded.max_inflight;

-- name: ReserveExclusiveWorkQuota :one
UPDATE account_async_quota SET current_inflight=current_inflight+1,updated_at=clock_timestamp()
WHERE account_id=sqlc.arg(account_id)::text::uuid AND current_inflight<max_inflight
RETURNING current_inflight;

-- name: LockExclusiveSnapshotInstance :one
SELECT id::text,exclusive_capture_blocked FROM instances
WHERE id=sqlc.arg(instance_id)::text::uuid FOR UPDATE;

-- name: HasExclusiveSnapshotOwner :one
SELECT EXISTS(SELECT 1 FROM exclusive_work_operations o JOIN instances i
ON o.incarnation_id=i.id::text||'/'||i.wake_id::text||'/'||i.node_id::text
WHERE i.id=sqlc.arg(instance_id)::text::uuid AND o.state='running'
AND o.lease_expires_at>clock_timestamp() AND o.attempt_deadline>clock_timestamp());

-- name: SetExclusiveCaptureBarrier :exec
UPDATE instances SET exclusive_capture_blocked=sqlc.arg(blocked)::boolean
WHERE id=sqlc.arg(instance_id)::text::uuid;

-- name: RecordTriggerConsumerHealth :exec
INSERT INTO trigger_consumer_health (
    trigger_id, last_poll_at, last_success_at, last_error_at, last_error,
    lag_messages, lag_age_seconds
) VALUES (
    sqlc.arg(trigger_id)::uuid,
    sqlc.arg(polled_at)::timestamptz,
    CASE WHEN sqlc.arg(success)::boolean THEN sqlc.arg(polled_at)::timestamptz ELSE NULL END,
    CASE WHEN sqlc.arg(success)::boolean THEN NULL ELSE sqlc.arg(polled_at)::timestamptz END,
    CASE WHEN sqlc.arg(success)::boolean THEN NULL ELSE NULLIF(sqlc.arg(error_detail)::text, '') END,
    sqlc.narg(lag_messages)::bigint,
    sqlc.narg(lag_age_seconds)::double precision
)
ON CONFLICT (trigger_id) DO UPDATE SET
    last_poll_at = EXCLUDED.last_poll_at,
    last_success_at = CASE WHEN sqlc.arg(success)::boolean THEN EXCLUDED.last_poll_at ELSE trigger_consumer_health.last_success_at END,
    last_error_at = CASE WHEN sqlc.arg(success)::boolean THEN trigger_consumer_health.last_error_at ELSE EXCLUDED.last_poll_at END,
    last_error = CASE WHEN sqlc.arg(success)::boolean THEN trigger_consumer_health.last_error ELSE NULLIF(sqlc.arg(error_detail)::text, '') END,
    lag_messages = EXCLUDED.lag_messages,
    lag_age_seconds = EXCLUDED.lag_age_seconds,
    updated_at = NOW();

-- name: TriggerConsumerHealth :one
SELECT last_poll_at, last_success_at, last_error_at, last_error,
       lag_messages, lag_age_seconds
FROM trigger_consumer_health
WHERE trigger_id = sqlc.arg(trigger_id)::uuid;

-- name: CommitSourceForManagedAdmission :one
SELECT c.app_id::text, c.enabled, COALESCE(c.operation_policy,'')::text AS operation_policy,
 a.platform_tenant_required
FROM commit_sources c JOIN apps a ON a.id=c.app_id AND a.account_id=c.account_id
WHERE c.account_id=sqlc.arg(account_id)::text::uuid AND c.id=sqlc.arg(source_id)::text::uuid
FOR UPDATE OF c FOR SHARE OF a;

-- name: CommitManagedSource :one
INSERT INTO commit_sources(account_id,app_id,name,operation_policy)
SELECT sqlc.arg(account_id)::text::uuid,id,sqlc.arg(name)::text,sqlc.arg(operation_policy)::text
FROM apps WHERE id=sqlc.arg(app_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
 AND NOT platform_tenant_required AND status<>'deleted'
ON CONFLICT(account_id,name) DO UPDATE SET name=commit_sources.name
WHERE commit_sources.app_id=excluded.app_id AND commit_sources.operation_policy=excluded.operation_policy
RETURNING id::text, enabled;

-- name: CommitManagedReceiptReplay :one
SELECT id::text, source_id::text, event_id::text,
 COALESCE(invocation_id::text,'')::text AS invocation_id,
 COALESCE(operation_id::text,'')::text AS operation_id, accepted_at,
 event_type=sqlc.arg(event_type)::text AND payload=sqlc.arg(payload)::jsonb AS matches
FROM commit_receipts WHERE account_id=sqlc.arg(account_id)::text::uuid
 AND source_id=sqlc.arg(source_id)::text::uuid AND event_id=sqlc.arg(event_id)::text::uuid;

-- name: CommitManagedReceipt :one
INSERT INTO commit_receipts(id,account_id,source_id,event_id,event_type,payload,operation_id,operation_state,completed_at)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(source_id)::text::uuid,
 sqlc.arg(event_id)::text::uuid,sqlc.arg(event_type)::text,sqlc.arg(payload)::jsonb,
 sqlc.arg(operation_id)::text::uuid,sqlc.arg(operation_state)::text,sqlc.narg(completed_at)::timestamptz)
RETURNING id::text,source_id::text,event_id::text,operation_id::text,accepted_at;

-- name: CommitReceiptIdentity :one
SELECT id::text,source_id::text,event_id::text,
 COALESCE(invocation_id::text,'')::text AS invocation_id,
 COALESCE(operation_id::text,'')::text AS operation_id,accepted_at
FROM commit_receipts WHERE account_id=sqlc.arg(account_id)::text::uuid
 AND source_id=sqlc.arg(source_id)::text::uuid AND event_id=sqlc.arg(event_id)::text::uuid;

-- name: CommitOperationHistory :one
SELECT COALESCE(operation_id,invocation_id)::text AS operation_id,id::text AS receipt_id,
 source_id::text,event_id::text,operation_state,accepted_at,completed_at
FROM commit_receipts WHERE account_id=sqlc.arg(account_id)::text::uuid
 AND COALESCE(operation_id,invocation_id)=sqlc.arg(operation_id)::text::uuid;

-- name: CommitSourceIdentity :one
SELECT id::text,app_id::text,name,enabled,COALESCE(operation_policy,'')::text AS operation_policy,
 relay_status,last_checked_at,pending_events,blocked_events,oldest_pending_at
FROM commit_sources WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(source_id)::text::uuid;

-- name: SetCommitSourceEnabled :exec
UPDATE commit_sources SET
 credential_revision=CASE WHEN sqlc.arg(enabled)::boolean AND NOT enabled THEN credential_revision+1 ELSE credential_revision END,
 relay_status=CASE WHEN sqlc.arg(enabled)::boolean AND NOT enabled THEN 'unconfigured' ELSE relay_status END,
 last_checked_at=CASE WHEN sqlc.arg(enabled)::boolean AND NOT enabled THEN NULL ELSE last_checked_at END,
 pending_events=CASE WHEN sqlc.arg(enabled)::boolean AND NOT enabled THEN NULL ELSE pending_events END,
 blocked_events=CASE WHEN sqlc.arg(enabled)::boolean AND NOT enabled THEN NULL ELSE blocked_events END,
 oldest_pending_at=CASE WHEN sqlc.arg(enabled)::boolean AND NOT enabled THEN NULL ELSE oldest_pending_at END,
 enabled=sqlc.arg(enabled)::boolean
WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(source_id)::text::uuid;

-- name: CommitPolicyWouldInvalidateSource :one
SELECT EXISTS(SELECT 1 FROM commit_sources c
 WHERE c.account_id=sqlc.arg(account_id)::text::uuid AND c.operation_policy=sqlc.arg(name)::text AND c.enabled
 AND (sqlc.arg(retired)::boolean OR sqlc.arg(configuration)::jsonb->>'scope'<>'account'
 OR sqlc.arg(configuration)::jsonb->>'contention'<>'queue'
 OR NOT COALESCE(sqlc.arg(configuration)::jsonb->'member_app_ids' ? c.app_id::text,false))) AS incompatible;

-- name: SetCommitSourceConnection :execrows
UPDATE commit_sources SET sealed_connection=sqlc.arg(connection)::bytea,
 credential_revision=credential_revision+1,relay_status='unconfigured',
 last_checked_at=NULL,pending_events=NULL,blocked_events=NULL,oldest_pending_at=NULL
WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(source_id)::text::uuid;

-- name: CommitRelayObservationSummary :one
WITH observations AS (
 SELECT relay_status,pending_events,blocked_events,oldest_pending_at,COALESCE(last_checked_at>=sqlc.arg(fresh_after)::timestamptz
   AND last_checked_at<=now()+interval '1 minute',false) AS fresh
 FROM commit_sources WHERE enabled
), snapshots AS (
 SELECT *,fresh AND pending_events IS NOT NULL AND blocked_events IS NOT NULL AS known
 FROM observations
)
SELECT count(*)::bigint AS enabled_sources,
 count(*) FILTER(WHERE NOT known)::bigint AS unknown_sources,
 count(*) FILTER(WHERE fresh AND relay_status NOT IN ('healthy','blocked_events'))::bigint AS failing_sources,
 COALESCE(sum(pending_events) FILTER(WHERE known),0)::bigint AS pending_events,
 COALESCE(sum(blocked_events) FILTER(WHERE known),0)::bigint AS blocked_events,
 COALESCE(extract(epoch FROM min(oldest_pending_at) FILTER(WHERE known AND (pending_events>0 OR blocked_events>0))),0)::double precision AS oldest_pending_timestamp
FROM snapshots;
-- Route policy snapshots, locked batches, and receipts (ADR-438).
-- name: ReadRoutePolicyAccount :one
SELECT jsonb_build_object('ID', id, 'Plan', plan, 'Status', status, 'AbuseHoldAt', abuse_hold_at) AS snapshot FROM accounts WHERE id = sqlc.arg(account_id)::text::uuid;

-- name: LockRoutePolicyAccount :one
SELECT jsonb_build_object('ID', id, 'Plan', plan, 'Status', status, 'AbuseHoldAt', abuse_hold_at) AS snapshot FROM accounts WHERE id = sqlc.arg(account_id)::text::uuid FOR UPDATE;

-- name: ReadRoutePolicyApp :one
SELECT jsonb_build_object('ID', id, 'AccountID', account_id, 'Slug', slug, 'Type', type, 'Status', status, 'RAMMB', ram_mb, 'CPUMillicores', cpu_millicores, 'MaxConcurrency', max_concurrency, 'RequestRateLimitRPS', request_rate_limit_rps, 'RequestRateLimitBurst', request_rate_limit_burst, 'ConsumerAuthMode', consumer_auth_mode, 'MaintenanceMode', maintenance_mode, 'Manifest', manifest, 'ScalingPolicy', scaling_policy) AS snapshot FROM apps WHERE id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid AND status <> 'deleted';

-- name: LockRoutePolicyApp :one
SELECT jsonb_build_object('ID', id, 'AccountID', account_id, 'Slug', slug, 'Type', type, 'Status', status, 'RAMMB', ram_mb, 'CPUMillicores', cpu_millicores, 'MaxConcurrency', max_concurrency, 'RequestRateLimitRPS', request_rate_limit_rps, 'RequestRateLimitBurst', request_rate_limit_burst, 'ConsumerAuthMode', consumer_auth_mode, 'MaintenanceMode', maintenance_mode, 'Manifest', manifest, 'ScalingPolicy', scaling_policy) AS snapshot FROM apps WHERE id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid AND status <> 'deleted' FOR UPDATE;

-- name: ReadRoutePolicyRules :many
SELECT jsonb_build_object('id', id, 'account_id', account_id, 'app_id', app_id, 'match_host', match_host, 'match_path', match_path, 'match_methods', match_methods, 'match_headers', match_headers, 'priority', priority, 'enabled', enabled, 'kind', kind, 'validate_mode', validate_mode, 'action', action, 'created_at', created_at, 'updated_at', updated_at) AS snapshot FROM edge_rules WHERE app_id = sqlc.arg(app_id)::text::uuid ORDER BY id;

-- name: LockRoutePolicyRules :many
SELECT jsonb_build_object('id', id, 'account_id', account_id, 'app_id', app_id, 'match_host', match_host, 'match_path', match_path, 'match_methods', match_methods, 'match_headers', match_headers, 'priority', priority, 'enabled', enabled, 'kind', kind, 'validate_mode', validate_mode, 'action', action, 'created_at', created_at, 'updated_at', updated_at) AS snapshot FROM edge_rules WHERE app_id = sqlc.arg(app_id)::text::uuid ORDER BY id FOR UPDATE;

-- name: ReadRoutePolicyReceiptByKey :one
SELECT request_sha256, receipt FROM route_policy_receipts
WHERE account_id = sqlc.arg(account_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid AND idempotency_key = sqlc.arg(idempotency_key);

-- name: ReadRoutePolicyReceipt :one
SELECT receipt FROM route_policy_receipts WHERE account_id = sqlc.arg(account_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid AND id = sqlc.arg(id)::text::uuid;

-- name: InsertRoutePolicyReceipt :exec
INSERT INTO route_policy_receipts (id, account_id, app_id, idempotency_key, request_sha256, receipt)
VALUES (sqlc.arg(id)::text::uuid, sqlc.arg(account_id)::text::uuid, sqlc.arg(app_id)::text::uuid, sqlc.arg(idempotency_key), sqlc.arg(request_sha256), sqlc.arg(receipt));

-- name: CreateRoutePolicyRule :exec
INSERT INTO edge_rules (id, account_id, app_id, match_host, match_path, match_methods, priority, enabled, kind, action)
VALUES (sqlc.arg(id)::text::uuid, sqlc.arg(account_id)::text::uuid, sqlc.arg(app_id)::text::uuid, sqlc.arg(match_host), sqlc.arg(match_path), sqlc.arg(match_methods), sqlc.arg(priority), true, sqlc.arg(kind), sqlc.arg(action));

-- name: UpdateRoutePolicyRuleAction :execrows
UPDATE edge_rules SET action = sqlc.arg(action), updated_at = now()
WHERE id = sqlc.arg(id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid AND kind = sqlc.arg(kind);

-- Captured contract ownership and stability for group policy plans (ADR-446).
-- name: ReadRoutePolicyDeployment :one
SELECT id::text FROM deployments WHERE id = sqlc.arg(deployment_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid;

-- name: LockRoutePolicyDeployment :one
SELECT id::text FROM deployments WHERE id = sqlc.arg(deployment_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid FOR UPDATE;

-- name: ReadRoutePolicyContract :one
SELECT doc, doc_sha256, truncated FROM deployment_openapi_docs WHERE deployment_id = sqlc.arg(deployment_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid;

-- name: LockRoutePolicyContract :one
SELECT doc, doc_sha256, truncated FROM deployment_openapi_docs WHERE deployment_id = sqlc.arg(deployment_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid FOR UPDATE;

-- Saved route intent is read with the same app ownership filters (ADR-448).
-- name: ReadSavedRouteRequirements :one
SELECT jsonb_build_object('app_id', saved.app_id, 'revision', saved.revision, 'sha256', saved.sha256, 'requirements', saved.requirements, 'updated_at', saved.updated_at) AS saved
FROM saved_route_requirements AS saved JOIN apps AS a ON a.id = saved.app_id
WHERE saved.app_id = sqlc.arg(app_id)::text::uuid AND saved.account_id = sqlc.arg(account_id)::text::uuid AND a.account_id = saved.account_id AND a.status <> 'deleted';

-- Serialized by the owned app row lock, including the first insert.
-- name: WriteSavedRouteRequirements :exec
INSERT INTO saved_route_requirements (app_id, account_id, revision, sha256, requirements)
VALUES (sqlc.arg(app_id)::text::uuid, sqlc.arg(account_id)::text::uuid, sqlc.arg(revision), sqlc.arg(sha256), sqlc.arg(requirements))
ON CONFLICT (app_id) DO UPDATE SET revision = EXCLUDED.revision, sha256 = EXCLUDED.sha256, requirements = EXCLUDED.requirements, updated_at = now()
WHERE saved_route_requirements.account_id = EXCLUDED.account_id;

-- name: QueueAutomaticRouteCheck :exec
SELECT enqueue_automatic_route_check(sqlc.arg(app_id)::text::uuid, sqlc.arg(deployment_id)::text::uuid, false);

-- name: ReadCanaryRouteGate :one
SELECT jsonb_build_object('app_id', a.id, 'mode', coalesce(g.mode, 'report'), 'revision', coalesce(g.revision, 0), 'updated_at', g.updated_at) AS gate
FROM apps a LEFT JOIN canary_route_gates g ON g.app_id = a.id AND g.account_id = a.account_id
WHERE a.id = sqlc.arg(app_id)::text::uuid AND a.account_id = sqlc.arg(account_id)::text::uuid AND a.status <> 'deleted';

-- name: WriteCanaryRouteGate :exec
INSERT INTO canary_route_gates (app_id, account_id, mode, revision)
VALUES (sqlc.arg(app_id)::text::uuid, sqlc.arg(account_id)::text::uuid, sqlc.arg(mode), sqlc.arg(revision))
ON CONFLICT (app_id) DO UPDATE SET mode = EXCLUDED.mode, revision = EXCLUDED.revision, updated_at = now()
WHERE canary_route_gates.account_id = EXCLUDED.account_id;

-- name: ReadCanaryRouteGateOwner :one
SELECT a.id::text AS app_id, a.account_id::text AS account_id FROM apps a
JOIN deployments d ON d.app_id = a.id
WHERE d.id = sqlc.arg(deployment_id)::text::uuid AND a.status <> 'deleted';

-- name: LockCanaryRouteGateApp :one
SELECT account_id::text FROM apps WHERE id = sqlc.arg(app_id)::text::uuid FOR UPDATE;

-- name: ClaimAutomaticRouteCheck :one
WITH candidate AS (
    SELECT j.deployment_id FROM automatic_route_checks j
    JOIN apps a ON a.id = j.app_id AND a.account_id = j.account_id
    JOIN deployments d ON d.id = j.deployment_id AND d.app_id = a.id
    JOIN saved_route_requirements s ON s.app_id = a.id AND s.account_id = a.account_id
    WHERE a.status <> 'deleted' AND j.completed_request_id IS DISTINCT FROM j.request_id
      AND j.next_attempt_at <= now() AND (j.lease_until IS NULL OR j.lease_until <= now())
    ORDER BY j.next_attempt_at, j.queued_at, j.deployment_id
    FOR UPDATE OF j SKIP LOCKED LIMIT 1
), claimed AS (
    UPDATE automatic_route_checks j SET claimed_request_id = j.request_id,
        lease_token = sqlc.arg(lease_token)::text::uuid,
        lease_until = now() + sqlc.arg(lease_ms)::bigint * interval '1 millisecond',
        attempts = LEAST(j.attempts + 1, sqlc.arg(max_attempts)::integer)
    FROM candidate WHERE j.deployment_id = candidate.deployment_id
    RETURNING j.*
)
SELECT jsonb_build_object('deployment_id', deployment_id, 'app_id', app_id, 'account_id', account_id, 'request_id', request_id, 'lease_token', lease_token, 'lease_until', lease_until, 'attempts', attempts) AS claim FROM claimed;

-- name: CompleteAutomaticRouteCheck :execrows
UPDATE automatic_route_checks SET latest_check = sqlc.arg(latest_check)::jsonb,
    latest_changes = sqlc.arg(latest_changes)::jsonb, finding_baseline = sqlc.arg(finding_baseline)::jsonb,
    safety_state = CASE WHEN sqlc.arg(notification_allowed)::boolean
        AND sqlc.arg(latest_check)::jsonb->'report'->>'status' IN ('satisfied', 'violated')
        AND EXISTS (SELECT 1 FROM deployments d JOIN apps a ON a.id = d.app_id
            WHERE d.id = automatic_route_checks.deployment_id AND d.app_id = automatic_route_checks.app_id
                AND d.status = 'live' AND a.account_id = automatic_route_checks.account_id AND a.status <> 'deleted')
        THEN sqlc.arg(latest_check)::jsonb->'report'->>'status' ELSE safety_state END,
    capture_sha256 = sqlc.arg(capture_sha256), capture_truncated = sqlc.arg(capture_truncated),
    checked_at = sqlc.arg(checked_at)::timestamptz, completed_request_id = request_id,
    claimed_request_id = NULL, lease_token = NULL, lease_until = NULL,
    attempts = 0, last_error_code = ''
WHERE deployment_id = sqlc.arg(deployment_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid
    AND request_id = sqlc.arg(request_id)::text::uuid AND claimed_request_id = request_id
    AND lease_token = sqlc.arg(lease_token)::text::uuid AND lease_until > clock_timestamp();

-- Parent locks serialize configuration edits while remaining compatible with
-- the FK key-share locks taken by captures, rules and webhook event inserts.
-- name: LockRouteCheckCompletionAccount :one
SELECT jsonb_build_object('ID', id, 'Plan', plan, 'Status', status, 'AbuseHoldAt', abuse_hold_at) AS snapshot
FROM accounts WHERE id = sqlc.arg(account_id)::text::uuid FOR NO KEY UPDATE;

-- name: LockRouteCheckCompletionApp :one
SELECT id::text FROM apps WHERE id = sqlc.arg(app_id)::text::uuid
    AND account_id = sqlc.arg(account_id)::text::uuid AND status <> 'deleted' FOR NO KEY UPDATE;

-- name: FailAutomaticRouteCheck :execrows
UPDATE automatic_route_checks SET claimed_request_id = NULL, lease_token = NULL, lease_until = NULL,
    last_error_code = 'check_failed', next_attempt_at = now() + sqlc.arg(retry_ms)::bigint * interval '1 millisecond'
WHERE deployment_id = sqlc.arg(deployment_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid
    AND request_id = sqlc.arg(request_id)::text::uuid AND claimed_request_id = request_id
    AND lease_token = sqlc.arg(lease_token)::text::uuid AND lease_until > now();

-- name: ReadAutomaticRouteCheck :one
SELECT jsonb_build_object('version', 1, 'app', a.slug, 'app_id', j.app_id, 'deployment_id', j.deployment_id,
    'state', CASE WHEN j.completed_request_id = j.request_id THEN 'complete' WHEN j.lease_until > now() THEN 'running' WHEN j.last_error_code <> '' THEN 'retrying' ELSE 'pending' END,
    'freshness', 'unavailable', 'stale_reasons', '[]'::jsonb, 'attempts', j.attempts,
    'last_error_code', j.last_error_code, 'queued_at', j.queued_at,
    'next_attempt_at', CASE WHEN j.completed_request_id = j.request_id THEN NULL ELSE j.next_attempt_at END,
    'checked_at', j.checked_at, 'check', j.latest_check, 'check_id', j.completed_request_id, 'changes', j.latest_changes,
    'input_capture_sha256', j.capture_sha256, 'input_capture_truncated', j.capture_truncated) AS result
FROM automatic_route_checks j JOIN apps a ON a.id = j.app_id AND a.account_id = j.account_id
JOIN deployments d ON d.id = j.deployment_id AND d.app_id = a.id
WHERE j.deployment_id = sqlc.arg(deployment_id)::text::uuid AND j.app_id = sqlc.arg(app_id)::text::uuid AND j.account_id = sqlc.arg(account_id)::text::uuid AND a.status <> 'deleted';

-- name: LockRouteFindingBaseline :one
SELECT finding_baseline FROM automatic_route_checks
WHERE deployment_id = sqlc.arg(deployment_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid
    AND request_id = sqlc.arg(request_id)::text::uuid AND claimed_request_id = request_id
    AND lease_token = sqlc.arg(lease_token)::text::uuid AND lease_until > clock_timestamp()
FOR UPDATE;

-- name: InsertRouteCheckHistory :exec
INSERT INTO route_check_history(id, deployment_id, app_id, account_id, checked_at, encoded_bytes, entry)
VALUES (sqlc.arg(id)::text::uuid, sqlc.arg(deployment_id)::text::uuid, sqlc.arg(app_id)::text::uuid, sqlc.arg(account_id)::text::uuid, sqlc.arg(checked_at), sqlc.arg(encoded_bytes), sqlc.arg(entry)::jsonb);

-- name: PruneRouteCheckHistory :exec
DELETE FROM route_check_history WHERE id IN (
    SELECT id FROM (
        SELECT id, row_number() OVER (ORDER BY checked_at DESC, id DESC) AS position,
            sum(encoded_bytes) OVER (ORDER BY checked_at DESC, id DESC ROWS UNBOUNDED PRECEDING) AS total_bytes
        FROM route_check_history WHERE deployment_id = sqlc.arg(deployment_id)::text::uuid
    ) retained WHERE position > sqlc.arg(max_entries)::integer OR total_bytes > sqlc.arg(max_bytes)::bigint
);

-- name: ReadRouteCheckHistoryEntry :one
SELECT entry FROM route_check_history
WHERE id = sqlc.arg(id)::text::uuid AND deployment_id = sqlc.arg(deployment_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid;

-- name: ListRouteCheckHistory :many
SELECT jsonb_build_object('version', 1, 'id', h.id, 'checked_at', h.checked_at,
    'status', entry->'check'->'report'->>'status',
    'requirements_revision', entry->'check'->'requirements_revision',
    'requirements_sha256', entry->'check'->>'requirements_sha256',
    'comparison_status', entry->'changes'->>'status', 'summary', entry->'changes'->'summary')
FROM route_check_history h
WHERE deployment_id = sqlc.arg(deployment_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid
    AND (sqlc.arg(before_id)::text = '' OR (checked_at, id) < (SELECT c.checked_at, c.id FROM route_check_history c WHERE c.id = NULLIF(sqlc.arg(before_id)::text, '')::uuid AND c.deployment_id = h.deployment_id AND c.app_id = h.app_id AND c.account_id = h.account_id))
ORDER BY checked_at DESC, id DESC LIMIT sqlc.arg(page_limit)::integer;

-- name: ReadRouteHealthGate :one
SELECT jsonb_build_object('app_id', a.id, 'mode', coalesce(g.mode, 'report'), 'on_regression', coalesce(g.on_regression, 'hold'), 'revision', coalesce(g.revision, 0), 'routes', coalesce(g.routes, '[]'::jsonb), 'updated_at', g.updated_at) AS gate
FROM apps a LEFT JOIN route_health_gates g ON g.app_id = a.id AND g.account_id = a.account_id
WHERE a.id = sqlc.arg(app_id)::text::uuid AND a.account_id = sqlc.arg(account_id)::text::uuid AND a.status <> 'deleted';

-- name: WriteRouteHealthGate :exec
INSERT INTO route_health_gates (app_id, account_id, mode, on_regression, revision, routes, updated_at)
VALUES (sqlc.arg(app_id)::text::uuid, sqlc.arg(account_id)::text::uuid, sqlc.arg(mode), sqlc.arg(on_regression), sqlc.arg(revision), sqlc.arg(routes)::jsonb, clock_timestamp())
ON CONFLICT (app_id) DO UPDATE SET mode = EXCLUDED.mode, on_regression = EXCLUDED.on_regression, revision = EXCLUDED.revision, routes = EXCLUDED.routes, updated_at = clock_timestamp()
WHERE route_health_gates.account_id = EXCLUDED.account_id;

-- name: ReadRouteHealthDeployment :one
SELECT d.id::text AS id, d.app_id::text AS app_id, d.commit_sha, d.status::text AS status, d.traffic_percent,
 d.canary_step, d.canary_total_steps, d.canary_step_started_at, coalesce(d.scope, '')::text AS scope
FROM deployments d WHERE d.id = sqlc.arg(deployment_id)::text::uuid AND d.app_id = sqlc.arg(app_id)::text::uuid;

-- name: RouteHealthStableIDs :many
SELECT d.id::text FROM deployments d JOIN deployments c ON c.app_id = d.app_id
WHERE c.id = sqlc.arg(deployment_id)::text::uuid AND c.app_id = sqlc.arg(app_id)::text::uuid
 AND d.id <> c.id AND d.status = 'live' AND d.traffic_percent > 0 AND coalesce(nullif(d.scope, ''), 'default') = coalesce(nullif(c.scope, ''), 'default')
ORDER BY d.id LIMIT 2;

-- name: RouteHealthObservation :one
-- Exact selected labels and shared windows. Aggregate publisher weights without
-- expanding requests. Percentile ranks interpolate bucket representatives, like
-- RequestTelemetryBaselineP95ByRoute. Old selectors skip the percentile sort.
WITH selected AS (
 SELECT (value->>'method')::text AS method, (value->>'path')::text AS path,
  (coalesce((value->>'check_latency')::boolean, false) OR coalesce((value->>'max_p95_ms')::bigint, 0) > 0) AS latency_enabled
 FROM jsonb_array_elements(sqlc.arg(routes)::jsonb)
), windows AS (
 SELECT (value->>'start')::timestamptz AS start, (value->>'end')::timestamptz AS "end" FROM jsonb_array_elements(sqlc.arg(windows)::jsonb)
), observed AS MATERIALIZED (
 SELECT s.method, s.path, s.latency_enabled, w.start, w."end", t.deployment_id, t.latency_ms, t.status, t.count
 FROM selected s CROSS JOIN windows w LEFT JOIN request_telemetry t
 ON t.app_id = sqlc.arg(app_id)::text::uuid AND t.account_id = sqlc.arg(account_id)::text::uuid
 AND t.deployment_id IN (sqlc.arg(candidate_id)::text::uuid, sqlc.arg(stable_id)::text::uuid)
 AND t.method = s.method AND t.route = s.method || ' ' || s.path
 AND t.received_at >= w.start AND t.received_at < w."end"
 AND t.received_at >= sqlc.arg(since)::timestamptz AND t.received_at < sqlc.arg(until)::timestamptz
 AND (sqlc.arg(customer_id)::text = ''
  OR (sqlc.arg(customer_group_by)::text = 'tenant' AND t.platform_tenant_id = NULLIF(sqlc.arg(customer_id)::text, '')::uuid)
  OR (sqlc.arg(customer_group_by)::text = 'consumer' AND t.consumer_id = NULLIF(sqlc.arg(customer_id)::text, '')::uuid))
), counts AS (
 SELECT method, path, start, "end",
 coalesce(sum(count::bigint) FILTER (WHERE deployment_id = sqlc.arg(candidate_id)::text::uuid), 0)::bigint AS candidate_requests,
 coalesce(sum(count::bigint) FILTER (WHERE deployment_id = sqlc.arg(candidate_id)::text::uuid AND status BETWEEN 500 AND 599), 0)::bigint AS candidate_errors,
 coalesce(sum(count::bigint) FILTER (WHERE deployment_id = sqlc.arg(stable_id)::text::uuid), 0)::bigint AS stable_requests,
 coalesce(sum(count::bigint) FILTER (WHERE deployment_id = sqlc.arg(stable_id)::text::uuid AND status BETWEEN 500 AND 599), 0)::bigint AS stable_errors
 FROM observed GROUP BY method, path, start, "end"
), weighted AS (
 SELECT method, path, start, deployment_id, latency_ms, sum(count::bigint) AS weight
 FROM observed WHERE latency_enabled AND deployment_id IS NOT NULL
 GROUP BY method, path, start, deployment_id, latency_ms
), ranked AS (
 SELECT method, path, start, deployment_id, latency_ms,
 sum(weight) OVER (PARTITION BY method, path, start, deployment_id ORDER BY latency_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
 sum(weight) OVER (PARTITION BY method, path, start, deployment_id) AS total
 FROM weighted
), targets AS (
 SELECT method, path, start, deployment_id, latency_ms, cumulative,
 (total - 1)::numeric * sqlc.arg(latency_quantile)::double precision::numeric AS rank
 FROM ranked
), values_at_rank AS (
 SELECT method, path, start, deployment_id, rank,
 min(latency_ms) FILTER (WHERE cumulative > floor(rank)) AS low,
 min(latency_ms) FILTER (WHERE cumulative > ceil(rank)) AS high
 FROM targets GROUP BY method, path, start, deployment_id, rank
), percentiles AS (
 SELECT method, path, start, deployment_id,
 (low + (rank - floor(rank)) * (high - low))::double precision AS p95_ms
 FROM values_at_rank
)
SELECT coalesce(jsonb_agg(jsonb_build_object('method', c.method, 'path', c.path, 'start', c.start, 'end', c."end",
 'candidate', jsonb_build_object('requests', c.candidate_requests, 'server_errors', c.candidate_errors, 'p95_latency_ms', candidate.p95_ms),
 'stable', jsonb_build_object('requests', c.stable_requests, 'server_errors', c.stable_errors, 'p95_latency_ms', stable.p95_ms)) ORDER BY c.method, c.path, c.start), '[]'::jsonb)::jsonb AS observations
FROM counts c
LEFT JOIN percentiles candidate ON candidate.method = c.method AND candidate.path = c.path AND candidate.start = c.start AND candidate.deployment_id = sqlc.arg(candidate_id)::text::uuid
LEFT JOIN percentiles stable ON stable.method = c.method AND stable.path = c.path AND stable.start = c.start AND stable.deployment_id = sqlc.arg(stable_id)::text::uuid;

-- name: RouteHealthClock :one
SELECT clock_timestamp()::timestamptz AS checked_at;

-- name: InsertRouteHealthHistory :one
-- AdvanceCanary holds the same app lock for inserts and pruning. Retries do
-- not mutate the original evidence or its checked_at.
WITH inserted AS (
 INSERT INTO route_health_history(id, deployment_id, app_id, account_id, decision_key, checked_at, encoded_bytes, entry)
 VALUES (sqlc.arg(id)::text::uuid, sqlc.arg(deployment_id)::text::uuid, sqlc.arg(app_id)::text::uuid, sqlc.arg(account_id)::text::uuid,
  sqlc.arg(decision_key), sqlc.arg(checked_at), sqlc.arg(encoded_bytes), sqlc.arg(entry)::jsonb)
 ON CONFLICT (deployment_id, decision_key) DO NOTHING RETURNING id
)
SELECT id::text FROM inserted
UNION ALL
SELECT id::text FROM route_health_history
 WHERE deployment_id = sqlc.arg(deployment_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid
 AND decision_key = sqlc.arg(decision_key) AND NOT EXISTS (SELECT 1 FROM inserted)
LIMIT 1;

-- name: PruneRouteHealthHistory :exec
DELETE FROM route_health_history WHERE id IN (
 SELECT id FROM (
  SELECT id, row_number() OVER (ORDER BY checked_at DESC, id DESC) AS position,
   sum(encoded_bytes) OVER (ORDER BY checked_at DESC, id DESC ROWS UNBOUNDED PRECEDING) AS total_bytes
  FROM route_health_history WHERE deployment_id = sqlc.arg(deployment_id)::text::uuid
 ) retained WHERE position > sqlc.arg(max_entries)::integer OR total_bytes > sqlc.arg(max_bytes)::bigint
);

-- name: ReadRouteHealthHistoryEntry :one
SELECT entry FROM route_health_history
 WHERE id = sqlc.arg(id)::text::uuid AND deployment_id = sqlc.arg(deployment_id)::text::uuid
 AND app_id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid;

-- name: ListRouteHealthHistory :many
SELECT h.entry FROM route_health_history h
 WHERE deployment_id = sqlc.arg(deployment_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid
 AND (sqlc.arg(before_id)::text = '' OR (checked_at, id) < (SELECT c.checked_at, c.id FROM route_health_history c
  WHERE c.id = NULLIF(sqlc.arg(before_id)::text, '')::uuid AND c.deployment_id = h.deployment_id AND c.app_id = h.app_id AND c.account_id = h.account_id))
 ORDER BY checked_at DESC, id DESC LIMIT sqlc.arg(page_limit)::integer;

-- name: ReadRouteHealthNotificationState :one
SELECT context_key, status, COALESCE(blocked_decision_id::text, '')::text AS blocked_decision_id
FROM route_health_notification_state WHERE deployment_id = sqlc.arg(deployment_id)::text::uuid
 AND app_id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid;

-- name: WriteRouteHealthNotificationState :exec
-- Serialized by AdvanceCanary's owned parent app/deployment lock.
INSERT INTO route_health_notification_state(deployment_id, app_id, account_id, context_key, status, blocked_decision_id, updated_at)
VALUES (sqlc.arg(deployment_id)::text::uuid, sqlc.arg(app_id)::text::uuid, sqlc.arg(account_id)::text::uuid,
 sqlc.arg(context_key), sqlc.arg(status), NULLIF(sqlc.arg(blocked_decision_id)::text, '')::uuid, sqlc.arg(updated_at))
ON CONFLICT (deployment_id) DO UPDATE SET app_id = EXCLUDED.app_id, account_id = EXCLUDED.account_id,
 context_key = EXCLUDED.context_key, status = EXCLUDED.status, blocked_decision_id = EXCLUDED.blocked_decision_id, updated_at = EXCLUDED.updated_at;

-- name: EnqueueRouteHealthNotification :exec
WITH recipients AS (
 SELECT array_agg(id ORDER BY id) AS ids FROM app_webhooks
 WHERE app_id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid AND scope = 'app' AND enabled
 AND (cardinality(event_filter) = 0 OR sqlc.arg(event)::text = ANY(event_filter))
)
INSERT INTO app_webhook_event_outbox(account_id, app_id, event, source_id, payload, recipient_webhook_ids)
SELECT sqlc.arg(account_id)::text::uuid, sqlc.arg(app_id)::text::uuid, sqlc.arg(event), sqlc.arg(decision_id)::text::uuid, sqlc.arg(payload)::jsonb, ids
FROM recipients WHERE cardinality(ids) > 0 ON CONFLICT (event, source_id) DO NOTHING;


-- name: LockRouteHealthRecoveryLease :one
SELECT expires_at FROM safe_release_worker_lease WHERE singleton = true FOR UPDATE;

-- name: LockRouteHealthRecoveryCandidate :one
SELECT id::text AS id, app_id::text AS app_id, status::text AS status, commit_sha,
       traffic_percent, canary_step, canary_total_steps, canary_step_started_at,
       rollout_state, coalesce(scope, '')::text AS scope, created_at
FROM deployments WHERE id = sqlc.arg(deployment_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid FOR UPDATE;

-- name: LockRouteHealthRecoverySiblings :many
SELECT id::text AS id, created_at, canary_total_steps, rollout_state
FROM deployments WHERE app_id = sqlc.arg(app_id)::text::uuid AND id <> sqlc.arg(deployment_id)::text::uuid
 AND status = 'live' AND traffic_percent > 0
 AND coalesce(nullif(scope, ''), 'default') = coalesce(nullif(sqlc.arg(scope)::text, ''), 'default')
ORDER BY id FOR UPDATE;

-- name: NotifyRouteHealthRecovery :exec
SELECT pg_notify('deployment_changed', sqlc.arg(payload)::text);

-- name: LockImagePreparationDeployment :one
SELECT status, COALESCE(NULLIF(rootfs_path, ''),
       CASE WHEN kind = 'image' THEN COALESCE(NULLIF(image_digest, ''), NULLIF(source_path, '')) END, '')::text AS input_path, rootfs_key,
       COALESCE(rootfs_bytes, 0)::bigint AS input_bytes
FROM deployments WHERE id = sqlc.arg(deployment_id)::uuid FOR UPDATE;

-- name: GetImagePreparation :one
SELECT * FROM deployment_image_preparations
WHERE deployment_id = sqlc.arg(deployment_id)::uuid;

-- name: BeginImagePreparation :one
INSERT INTO deployment_image_preparations
    (deployment_id, node_name, input_path, input_key, input_bytes, claim_token, phase)
VALUES (sqlc.arg(deployment_id)::uuid, sqlc.arg(node_name)::text,
        sqlc.arg(input_path)::text, sqlc.arg(input_key)::text,
        sqlc.arg(input_bytes)::bigint, sqlc.arg(claim_token)::uuid, 'preparing')
ON CONFLICT (deployment_id) DO UPDATE
SET claim_token = EXCLUDED.claim_token, updated_at = now()
RETURNING *;

-- name: PublishImagePreparationLayer :execrows
WITH publication AS (
    UPDATE deployments d
    SET rootfs_path = sqlc.arg(path)::text, rootfs_key = sqlc.arg(key)::text,
        rootfs_bytes = sqlc.arg(bytes)::bigint
    WHERE d.id = sqlc.arg(deployment_id)::uuid AND d.status = 'imaging'
      AND EXISTS (SELECT 1 FROM deployment_image_preparations p
                  WHERE p.deployment_id = d.id AND p.claim_token = sqlc.arg(claim_token)::uuid
                    AND p.phase = 'preparing')
    RETURNING d.id
)
UPDATE deployment_image_preparations p
SET phase = 'layer_published', updated_at = now()
FROM publication WHERE p.deployment_id = publication.id;

-- name: AdvanceImagePreparation :execrows
UPDATE deployment_image_preparations
SET phase = sqlc.arg(next_phase)::text, updated_at = now()
WHERE deployment_id = sqlc.arg(deployment_id)::uuid
  AND claim_token = sqlc.arg(claim_token)::uuid AND phase = sqlc.arg(expected_phase)::text;

-- name: ListResumableImagePreparations :many
SELECT d.app_id, d.id AS deployment_id, p.node_name AS node_id
FROM deployment_image_preparations p JOIN deployments d ON d.id = p.deployment_id
WHERE p.phase <> 'handed_off' AND d.status IN ('pending', 'building', 'imaging', 'snapshotting')
  AND (sqlc.arg(node_id)::text = '' OR p.node_name = '' OR p.node_name = sqlc.arg(node_id)::text)
ORDER BY p.updated_at, p.deployment_id LIMIT sqlc.arg(batch_limit)::int;

-- name: ListBuildsAwaitingImage :many
SELECT d.app_id, d.id AS deployment_id, COALESCE(p.builder_node_id, '')::text AS node_id
FROM deployments d JOIN builds b ON b.deployment_id = d.id
JOIN build_provenance p ON p.build_id = b.id
WHERE d.status IN ('pending', 'building') AND b.status = 'succeeded'
  AND COALESCE(d.rootfs_path, '') <> ''
  AND NOT EXISTS (SELECT 1 FROM deployment_image_preparations i WHERE i.deployment_id = d.id)
  AND (sqlc.arg(node_id)::text = '' OR COALESCE(p.builder_node_id, '') = '' OR p.builder_node_id = sqlc.arg(node_id)::text)
ORDER BY b.finished_at, b.id LIMIT sqlc.arg(batch_limit)::int;

-- name: TransitionImagePreparation :execrows
UPDATE deployments d SET status = sqlc.arg(next_status)::text, error = NULL
FROM deployment_image_preparations p
WHERE d.id = sqlc.arg(deployment_id)::uuid AND p.deployment_id = d.id
  AND p.claim_token = sqlc.arg(claim_token)::uuid
  AND ((sqlc.arg(next_status)::text = 'imaging' AND
        ((p.phase = 'preparing' AND d.status IN ('pending', 'building', 'imaging')) OR
         (p.phase = 'layer_published' AND d.status = 'imaging'))) OR
       (sqlc.arg(next_status)::text = 'snapshotting' AND p.phase = 'scan_complete'
        AND d.status IN ('imaging', 'snapshotting')));

-- name: RouteCustomerHealthObservation :one
-- Advisory identity cohorts use the same exact routes, deployment pair and
-- closed windows as aggregate health. Rank before bounding output and sorting
-- weighted latency. Request-time attribution never follows today's tenant link.
WITH selected AS (
 SELECT value->>'method' AS method, value->>'path' AS path,
  (coalesce((value->>'check_latency')::boolean, false) OR coalesce((value->>'max_p95_ms')::bigint, 0) > 0) AS latency_enabled,
  ARRAY(SELECT code::integer FROM jsonb_array_elements_text(coalesce(value->'watch_statuses', '[]'::jsonb)) code) AS watch_statuses
 FROM jsonb_array_elements(sqlc.arg(routes)::jsonb)
), watched AS (
 SELECT method,path,unnest(watch_statuses) AS status_code FROM selected
), windows AS (
 SELECT (value->>'start')::timestamptz AS start, (value->>'end')::timestamptz AS "end"
 FROM jsonb_array_elements(sqlc.arg(windows)::jsonb)
), observed AS MATERIALIZED (
 SELECT s.method, s.path, s.latency_enabled, w.start, w."end", rt.deployment_id, rt.latency_ms, rt.status, rt.count::bigint AS requests, (rt.status=ANY(s.watch_statuses)) AS watched_status,
  CASE WHEN sqlc.arg(group_by)::text = 'tenant' THEN pt.id ELSE c.id END AS customer_id,
  CASE WHEN sqlc.arg(group_by)::text = 'tenant' THEN rt.platform_tenant_id IS NULL ELSE rt.consumer_id IS NULL END AS unattributed
 FROM selected s CROSS JOIN windows w JOIN request_telemetry rt
 ON rt.app_id = sqlc.arg(app_id)::text::uuid AND rt.account_id = sqlc.arg(account_id)::text::uuid
 AND rt.deployment_id IN (sqlc.arg(candidate_id)::text::uuid, sqlc.arg(stable_id)::text::uuid)
 AND rt.method = s.method AND rt.route = s.method || ' ' || s.path
 AND rt.received_at >= w.start AND rt.received_at < w."end"
 AND rt.received_at >= sqlc.arg(since)::timestamptz AND rt.received_at < sqlc.arg(until)::timestamptz
 LEFT JOIN api_consumers c ON c.id = rt.consumer_id AND c.account_id = rt.account_id AND c.app_id = rt.app_id
 LEFT JOIN platform_tenants pt ON pt.id = rt.platform_tenant_id AND pt.account_id = rt.account_id
), totals AS (
 SELECT method, path,
 count(DISTINCT customer_id)::bigint AS observed_customers,
 coalesce(sum(requests) FILTER (WHERE deployment_id = sqlc.arg(candidate_id)::text::uuid AND customer_id IS NOT NULL), 0)::bigint AS candidate_identified,
 coalesce(sum(requests) FILTER (WHERE deployment_id = sqlc.arg(candidate_id)::text::uuid AND unattributed), 0)::bigint AS candidate_unattributed,
 coalesce(sum(requests) FILTER (WHERE deployment_id = sqlc.arg(candidate_id)::text::uuid AND NOT unattributed AND customer_id IS NULL), 0)::bigint AS candidate_unresolved,
 coalesce(sum(requests) FILTER (WHERE deployment_id = sqlc.arg(stable_id)::text::uuid AND customer_id IS NOT NULL), 0)::bigint AS stable_identified,
 coalesce(sum(requests) FILTER (WHERE deployment_id = sqlc.arg(stable_id)::text::uuid AND unattributed), 0)::bigint AS stable_unattributed,
 coalesce(sum(requests) FILTER (WHERE deployment_id = sqlc.arg(stable_id)::text::uuid AND NOT unattributed AND customer_id IS NULL), 0)::bigint AS stable_unresolved
 FROM observed GROUP BY method, path
), cohort_totals AS (
 SELECT method, path, customer_id, sum(requests)::bigint AS requests,
 coalesce(sum(requests) FILTER (WHERE deployment_id = sqlc.arg(candidate_id)::text::uuid AND status BETWEEN 500 AND 599), 0)::bigint AS candidate_errors,
 coalesce(sum(requests) FILTER (WHERE deployment_id = sqlc.arg(candidate_id)::text::uuid AND watched_status),0)::bigint AS candidate_watched_responses,
 coalesce(sum(requests) FILTER (WHERE deployment_id = sqlc.arg(candidate_id)::text::uuid), 0)::bigint AS candidate_requests,
 coalesce(sum(requests) FILTER (WHERE deployment_id = sqlc.arg(stable_id)::text::uuid), 0)::bigint AS stable_requests
 FROM observed WHERE customer_id IS NOT NULL GROUP BY method, path, customer_id
), ranked_cohorts AS (
 SELECT *, row_number() OVER (PARTITION BY method, path ORDER BY candidate_errors DESC, candidate_watched_responses DESC, requests DESC, customer_id ASC) AS position
 FROM cohort_totals
), bounds AS (
 SELECT method, path,
 coalesce(sum(candidate_requests) FILTER (WHERE position > sqlc.arg(customer_limit)::integer), 0)::bigint AS candidate_other,
 coalesce(sum(stable_requests) FILTER (WHERE position > sqlc.arg(customer_limit)::integer), 0)::bigint AS stable_other
 FROM ranked_cohorts GROUP BY method, path
), bounded AS MATERIALIZED (
 SELECT * FROM ranked_cohorts WHERE position <= sqlc.arg(customer_limit)::integer
), counts AS (
 SELECT b.method, b.path, b.customer_id, b.position, w.start, w."end",
 coalesce(sum(o.requests) FILTER (WHERE o.deployment_id = sqlc.arg(candidate_id)::text::uuid), 0)::bigint AS candidate_requests,
 coalesce(sum(o.requests) FILTER (WHERE o.deployment_id = sqlc.arg(candidate_id)::text::uuid AND o.status BETWEEN 500 AND 599), 0)::bigint AS candidate_errors,
 coalesce(sum(o.requests) FILTER (WHERE o.deployment_id = sqlc.arg(stable_id)::text::uuid), 0)::bigint AS stable_requests,
 coalesce(sum(o.requests) FILTER (WHERE o.deployment_id = sqlc.arg(stable_id)::text::uuid AND o.status BETWEEN 500 AND 599), 0)::bigint AS stable_errors
 FROM bounded b CROSS JOIN windows w LEFT JOIN observed o
 ON o.method = b.method AND o.path = b.path AND o.customer_id = b.customer_id AND o.start = w.start
 GROUP BY b.method, b.path, b.customer_id, b.position, w.start, w."end"
), status_responses AS (
 SELECT o.method,o.path,o.customer_id,o.start,o.status AS status_code,
 coalesce(sum(o.requests) FILTER (WHERE o.deployment_id=sqlc.arg(candidate_id)::text::uuid),0)::bigint AS candidate_responses,
 coalesce(sum(o.requests) FILTER (WHERE o.deployment_id=sqlc.arg(stable_id)::text::uuid),0)::bigint AS stable_responses
 FROM observed o JOIN bounded b USING (method,path,customer_id) WHERE o.watched_status
 GROUP BY o.method,o.path,o.customer_id,o.start,o.status
), status_windows AS (
 SELECT c.method,c.path,c.customer_id,watch.status_code,
 jsonb_agg(jsonb_build_object('start',c.start,'end',c."end",
 'candidate',jsonb_build_object('requests',c.candidate_requests,'responses',coalesce(r.candidate_responses,0)),
 'stable',jsonb_build_object('requests',c.stable_requests,'responses',coalesce(r.stable_responses,0))) ORDER BY c.start) AS windows
 FROM counts c JOIN watched watch USING (method,path)
 LEFT JOIN status_responses r ON r.method=c.method AND r.path=c.path AND r.customer_id=c.customer_id AND r.start=c.start AND r.status_code=watch.status_code
 GROUP BY c.method,c.path,c.customer_id,watch.status_code
), status_findings AS (
 SELECT method,path,customer_id,jsonb_agg(jsonb_build_object('status_code',status_code,'windows',windows) ORDER BY status_code) AS statuses
 FROM status_windows GROUP BY method,path,customer_id
), weighted AS (
 SELECT o.method, o.path, o.customer_id, o.start, o.deployment_id, o.latency_ms, sum(o.requests) AS weight
 FROM observed o JOIN bounded b USING (method, path, customer_id) WHERE o.latency_enabled
 GROUP BY o.method, o.path, o.customer_id, o.start, o.deployment_id, o.latency_ms
), ranked AS (
 SELECT *, sum(weight) OVER (PARTITION BY method, path, customer_id, start, deployment_id ORDER BY latency_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
 sum(weight) OVER (PARTITION BY method, path, customer_id, start, deployment_id) AS total FROM weighted
), targets AS (
 SELECT *, (total - 1)::numeric * sqlc.arg(latency_quantile)::double precision::numeric AS rank FROM ranked
), values_at_rank AS (
 SELECT method, path, customer_id, start, deployment_id, rank,
 min(latency_ms) FILTER (WHERE cumulative > floor(rank)) AS low,
 min(latency_ms) FILTER (WHERE cumulative > ceil(rank)) AS high
 FROM targets GROUP BY method, path, customer_id, start, deployment_id, rank
), percentiles AS (
 SELECT method, path, customer_id, start, deployment_id,
 (low + (rank - floor(rank)) * (high - low))::double precision AS p95_ms FROM values_at_rank
), cohort_windows AS (
 SELECT c.method, c.path, c.customer_id, c.position,
 jsonb_agg(jsonb_build_object('start', c.start, 'end', c."end",
 'candidate', jsonb_build_object('requests', c.candidate_requests, 'server_errors', c.candidate_errors, 'p95_latency_ms', cp.p95_ms),
 'stable', jsonb_build_object('requests', c.stable_requests, 'server_errors', c.stable_errors, 'p95_latency_ms', sp.p95_ms)) ORDER BY c.start) AS windows
 FROM counts c
 LEFT JOIN percentiles cp ON cp.method = c.method AND cp.path = c.path AND cp.customer_id = c.customer_id AND cp.start = c.start AND cp.deployment_id = sqlc.arg(candidate_id)::text::uuid
 LEFT JOIN percentiles sp ON sp.method = c.method AND sp.path = c.path AND sp.customer_id = c.customer_id AND sp.start = c.start AND sp.deployment_id = sqlc.arg(stable_id)::text::uuid
 GROUP BY c.method, c.path, c.customer_id, c.position
), customers AS (
 SELECT cw.method, cw.path, jsonb_agg(jsonb_build_object('customer_id', cw.customer_id,
 'health', jsonb_build_object('method', cw.method, 'path', cw.path, 'windows', cw.windows) || CASE WHEN sf.statuses IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('client_errors',jsonb_build_object('statuses',sf.statuses)) END) ORDER BY cw.position) AS customers
 FROM cohort_windows cw LEFT JOIN status_findings sf USING (method,path,customer_id) GROUP BY cw.method, cw.path
)
SELECT coalesce(jsonb_agg(jsonb_build_object('method', s.method, 'path', s.path,
 'observed_customers', coalesce(t.observed_customers, 0), 'customers_truncated', coalesce(t.observed_customers, 0) > sqlc.arg(customer_limit)::integer,
 'candidate', jsonb_build_object('identified_requests', coalesce(t.candidate_identified, 0), 'unattributed_requests', coalesce(t.candidate_unattributed, 0), 'unresolved_identity_requests', coalesce(t.candidate_unresolved, 0), 'other_customer_requests', coalesce(b.candidate_other, 0)),
 'stable', jsonb_build_object('identified_requests', coalesce(t.stable_identified, 0), 'unattributed_requests', coalesce(t.stable_unattributed, 0), 'unresolved_identity_requests', coalesce(t.stable_unresolved, 0), 'other_customer_requests', coalesce(b.stable_other, 0)),
 'customers', coalesce(c.customers, '[]'::jsonb)) ORDER BY s.method, s.path), '[]'::jsonb)::jsonb AS observations
FROM selected s LEFT JOIN totals t USING (method, path) LEFT JOIN bounds b USING (method, path) LEFT JOIN customers c USING (method, path);

-- name: RouteHealthClientErrorObservation :one
-- Live advisory reads only. Each code has its own weighted numerator and the
-- entire deployment/route/window request count as denominator. No request expansion.
WITH selected AS (
 SELECT value->>'method' AS method, value->>'path' AS path,
  ARRAY(SELECT code::integer FROM jsonb_array_elements_text(coalesce(value->'watch_statuses', '[]'::jsonb)) code) AS watch_statuses
 FROM jsonb_array_elements(sqlc.arg(routes)::jsonb)
), watched AS (
 SELECT method, path, unnest(watch_statuses) AS status_code FROM selected
), windows AS (
 SELECT (value->>'start')::timestamptz AS start, (value->>'end')::timestamptz AS "end" FROM jsonb_array_elements(sqlc.arg(windows)::jsonb)
), counts AS (
 SELECT s.method,s.path,s.status_code,w.start,w."end",
 coalesce(sum(t.count::bigint) FILTER (WHERE t.deployment_id=sqlc.arg(candidate_id)::text::uuid),0)::bigint AS candidate_requests,
 coalesce(sum(t.count::bigint) FILTER (WHERE t.deployment_id=sqlc.arg(candidate_id)::text::uuid AND t.status=s.status_code),0)::bigint AS candidate_responses,
 coalesce(sum(t.count::bigint) FILTER (WHERE t.deployment_id=sqlc.arg(stable_id)::text::uuid),0)::bigint AS stable_requests,
 coalesce(sum(t.count::bigint) FILTER (WHERE t.deployment_id=sqlc.arg(stable_id)::text::uuid AND t.status=s.status_code),0)::bigint AS stable_responses
 FROM watched s CROSS JOIN windows w LEFT JOIN request_telemetry t
 ON t.app_id=sqlc.arg(app_id)::text::uuid AND t.account_id=sqlc.arg(account_id)::text::uuid
 AND t.deployment_id IN (sqlc.arg(candidate_id)::text::uuid,sqlc.arg(stable_id)::text::uuid)
 AND t.method=s.method AND t.route=s.method || ' ' || s.path
 AND t.received_at>=w.start AND t.received_at<w."end"
 AND t.received_at>=sqlc.arg(since)::timestamptz AND t.received_at<sqlc.arg(until)::timestamptz
 AND (sqlc.arg(customer_id)::text = ''
  OR (sqlc.arg(customer_group_by)::text = 'tenant' AND t.platform_tenant_id = NULLIF(sqlc.arg(customer_id)::text, '')::uuid)
  OR (sqlc.arg(customer_group_by)::text = 'consumer' AND t.consumer_id = NULLIF(sqlc.arg(customer_id)::text, '')::uuid))
 GROUP BY s.method,s.path,s.status_code,w.start,w."end"
), signals AS (
 SELECT method,path,status_code,jsonb_agg(jsonb_build_object('start',start,'end',"end",
 'candidate',jsonb_build_object('requests',candidate_requests,'responses',candidate_responses),
 'stable',jsonb_build_object('requests',stable_requests,'responses',stable_responses)) ORDER BY start) AS windows
 FROM counts GROUP BY method,path,status_code
), routes AS (
 SELECT method,path,jsonb_agg(jsonb_build_object('status_code',status_code,'windows',windows) ORDER BY status_code) AS statuses
 FROM signals GROUP BY method,path
)
SELECT coalesce(jsonb_agg(jsonb_build_object('method',method,'path',path,'client_errors',jsonb_build_object('statuses',statuses)) ORDER BY method,path),'[]'::jsonb)::jsonb AS observations
FROM routes;

-- name: RouteHealthInvestigationCustomerExists :one
SELECT CASE WHEN sqlc.arg(customer_group_by)::text = 'tenant' THEN
 EXISTS(SELECT 1 FROM platform_tenants WHERE id = sqlc.arg(customer_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid)
 ELSE EXISTS(SELECT 1 FROM api_consumers WHERE id = sqlc.arg(customer_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid) END::boolean AS owned;

-- name: RouteHealthInvestigationExamples :one
-- Metadata only. Count all matching rows/weights before independently bounding
-- each deployment/window. Error rows prefer trace links; latency rows prefer
-- slowest latency buckets. Both then use newest timestamps and UUID ties.
WITH windows AS (
 SELECT (value->>'start')::timestamptz AS start, (value->>'end')::timestamptz AS "end"
 FROM jsonb_array_elements(sqlc.arg(windows)::jsonb)
), observed AS MATERIALIZED (
 SELECT w.start,w."end",t.id,t.received_at,t.deployment_id,t.status,t.latency_ms,t.count,t.trace_id
 FROM windows w JOIN request_telemetry t
 ON t.app_id = sqlc.arg(app_id)::text::uuid AND t.account_id = sqlc.arg(account_id)::text::uuid
 AND t.method = sqlc.arg(method)::text AND t.route = sqlc.arg(method)::text || ' ' || sqlc.arg(path)::text
 AND t.deployment_id IN (sqlc.arg(candidate_id)::text::uuid,sqlc.arg(stable_id)::text::uuid)
 AND t.received_at >= w.start AND t.received_at < w."end"
 AND (sqlc.arg(latency)::boolean OR t.status BETWEEN sqlc.arg(status_min)::integer AND sqlc.arg(status_max)::integer)
 AND (sqlc.arg(customer_id)::text = ''
  OR (sqlc.arg(customer_group_by)::text = 'tenant' AND t.platform_tenant_id = NULLIF(sqlc.arg(customer_id)::text, '')::uuid)
  OR (sqlc.arg(customer_group_by)::text = 'consumer' AND t.consumer_id = NULLIF(sqlc.arg(customer_id)::text, '')::uuid))
), ranked AS (
 SELECT *,row_number() OVER (PARTITION BY start,deployment_id ORDER BY CASE WHEN sqlc.arg(latency)::boolean THEN latency_ms END DESC NULLS LAST,(nullif(trace_id,'') IS NOT NULL) DESC,received_at DESC,id DESC) AS position
 FROM observed
), totals AS (
 SELECT start,deployment_id,count(*)::bigint AS observed_rows,sum(count::bigint)::bigint AS matching_requests
 FROM observed GROUP BY start,deployment_id
), examples AS (
 SELECT start,deployment_id,jsonb_agg(jsonb_build_object('telemetry_id',id,'received_at',received_at,'status',status,'latency_ms',latency_ms,'represented_requests',count,'trace_id',trace_id) ORDER BY position) AS examples
 FROM ranked WHERE position <= sqlc.arg(example_limit)::integer GROUP BY start,deployment_id
), sides AS (
 SELECT sqlc.arg(candidate_id)::text::uuid AS deployment_id,'candidate'::text AS side
 UNION ALL SELECT sqlc.arg(stable_id)::text::uuid,'stable'::text
), summaries AS (
 SELECT w.start,w."end",jsonb_object_agg(s.side,jsonb_build_object(
 'matching_requests',coalesce(t.matching_requests,0),'observed_rows',coalesce(t.observed_rows,0),
 'examples_truncated',coalesce(t.observed_rows,0) > sqlc.arg(example_limit)::integer,'examples',coalesce(e.examples,'[]'::jsonb))) AS sides
 FROM windows w CROSS JOIN sides s LEFT JOIN totals t ON t.start=w.start AND t.deployment_id=s.deployment_id
 LEFT JOIN examples e ON e.start=w.start AND e.deployment_id=s.deployment_id GROUP BY w.start,w."end"
)
SELECT coalesce(jsonb_agg(jsonb_build_object('start',start,'end',"end",'candidate',sides->'candidate','stable',sides->'stable') ORDER BY start),'[]'::jsonb)::jsonb AS observations FROM summaries;


-- name: RouteHealthLatencyEvidence :many
-- Independently bound the newest rows in each exact deployment/window. Read
-- rows without spans too, so missing coverage is not hidden by selection.
WITH windows AS (
 SELECT (value->>'start')::timestamptz AS start, (value->>'end')::timestamptz AS "end"
 FROM jsonb_array_elements(sqlc.arg(windows)::jsonb)
), ranked AS (
 SELECT w.start,w."end",t.id,t.received_at,t.deployment_id,t.status,t.latency_ms,t.count,t.trace_id,
 t.spans_summary,t.cold_boot,t.guest_duration_ms,t.guest_outcome,t.wake_id,t.instance_id,
 row_number() OVER (PARTITION BY w.start,t.deployment_id ORDER BY t.received_at DESC,t.id DESC) AS position
 FROM windows w JOIN request_telemetry t
 ON t.app_id=sqlc.arg(app_id)::text::uuid AND t.account_id=sqlc.arg(account_id)::text::uuid
 AND t.method=sqlc.arg(method)::text AND t.route=sqlc.arg(method)::text || ' ' || sqlc.arg(path)::text
 AND t.deployment_id IN (sqlc.arg(candidate_id)::text::uuid,sqlc.arg(stable_id)::text::uuid)
 AND t.received_at>=w.start AND t.received_at<w."end"
 AND (sqlc.arg(customer_id)::text=''
  OR (sqlc.arg(customer_group_by)::text='tenant' AND t.platform_tenant_id=NULLIF(sqlc.arg(customer_id)::text,'')::uuid)
  OR (sqlc.arg(customer_group_by)::text='consumer' AND t.consumer_id=NULLIF(sqlc.arg(customer_id)::text,'')::uuid))
), sampled AS MATERIALIZED (
 SELECT * FROM ranked WHERE position<=sqlc.arg(rows_limit)::integer
)
SELECT s.start,s.deployment_id::text AS deployment_id,s.id::text AS telemetry_id,s.received_at,s.status,s.latency_ms,s.count,s.trace_id,
 s.spans_summary,s.cold_boot,s.guest_duration_ms,s.guest_outcome,s.wake_id,
 COALESCE((CASE WHEN wake.started IS NOT NULL AND wake.completed>=wake.started
 AND wake.completed-wake.started<=interval '24 hours'
 THEN (EXTRACT(EPOCH FROM (wake.completed-wake.started))*1000)::bigint END),-1)::bigint AS wake_boot_ms
FROM sampled s LEFT JOIN LATERAL (
 SELECT min(e.at) FILTER (WHERE e.kind='wake.boot_started') AS started,
 min(e.at) FILTER (WHERE e.kind='wake.boot_completed') AS completed
 FROM events e WHERE s.cold_boot AND s.wake_id IS NOT NULL AND nullif(s.instance_id,'') IS NOT NULL AND e.actor='schedd'
 AND e.kind IN ('wake.boot_started','wake.boot_completed')
 AND e.data->>'wake_id'=s.wake_id AND e.data->>'app_id'=sqlc.arg(app_id)::text
 AND e.data->>'instance_id'=s.instance_id
 AND e.at>=s.received_at-interval '24 hours' AND e.at<=s.received_at+interval '30 seconds'
) wake ON true
ORDER BY s.start,s.deployment_id,s.position;
