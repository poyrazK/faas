-- name: ProductControlOwnedApp :one
SELECT id FROM apps WHERE account_id=$1 AND id=$2 AND status<>'deleted' FOR SHARE;

-- name: ProductControlPutRevision :exec
INSERT INTO product_control_revisions(account_id,app_id,revision,spec)
VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING;

-- name: ProductControlSetHead :exec
INSERT INTO product_control_heads(account_id,app_id,revision) VALUES ($1,$2,$3)
ON CONFLICT(app_id) DO UPDATE SET revision=EXCLUDED.revision;

-- name: ProductControlGetHead :one
SELECT r.revision,r.spec FROM product_control_heads h
JOIN product_control_revisions r USING(app_id,revision)
WHERE h.account_id=$1 AND h.app_id=$2;

-- name: ProductControlGetRevision :one
SELECT spec FROM product_control_revisions WHERE account_id=$1 AND app_id=$2 AND revision=$3;

-- name: ProductControlManagedApp :one
SELECT EXISTS(SELECT 1 FROM product_control_heads WHERE account_id=$1 AND app_id=$2)::boolean;

-- name: ProductControlSetCustomer :exec
-- A change of revision or plan is appended to the plan history in the same
-- statement, so every writer records it (ADR-20261021154511). A customer
-- without a trial starts one when assigned a plan offering it, and keeps
-- any trial they had (ADR-20261021195901).
WITH prior AS (
  SELECT c.revision, c.plan FROM product_control_customers c
  WHERE c.app_id=sqlc.arg(app_id)::uuid AND c.tenant_id=sqlc.arg(tenant_id)::uuid
), offer AS (
  SELECT COALESCE((v.spec->'plans'->(sqlc.arg(plan)::text)->>'trial_days')::int, 0) AS days FROM product_control_revisions v
  WHERE v.app_id=sqlc.arg(app_id)::uuid AND v.revision=sqlc.arg(revision)::text
), assigned AS (
  INSERT INTO product_control_customers(account_id,app_id,tenant_id,revision,plan,suspended,auth_organization_id,trial_starts_at,trial_ends_at)
  SELECT sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(tenant_id)::uuid,sqlc.arg(revision)::text,sqlc.arg(plan)::text,
    sqlc.arg(suspended)::boolean,sqlc.narg(auth_organization_id)::uuid,
    CASE WHEN o.days > 0 THEN now() END, CASE WHEN o.days > 0 THEN now() + make_interval(days => o.days) END
  FROM (SELECT 1) AS one LEFT JOIN offer o ON true
  ON CONFLICT(app_id,tenant_id) DO UPDATE
  SET revision=EXCLUDED.revision,plan=EXCLUDED.plan,suspended=EXCLUDED.suspended,auth_organization_id=EXCLUDED.auth_organization_id,
    trial_starts_at=COALESCE(product_control_customers.trial_starts_at,EXCLUDED.trial_starts_at),
    trial_ends_at=CASE WHEN product_control_customers.trial_starts_at IS NULL THEN EXCLUDED.trial_ends_at ELSE product_control_customers.trial_ends_at END
)
INSERT INTO product_control_plan_history(account_id,app_id,tenant_id,revision,plan,first_assignment)
SELECT sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(tenant_id)::uuid,sqlc.arg(revision)::text,sqlc.arg(plan)::text,NOT EXISTS(SELECT 1 FROM prior)
WHERE NOT EXISTS(SELECT 1 FROM prior p WHERE p.revision=sqlc.arg(revision)::text AND p.plan=sqlc.arg(plan)::text);

-- name: ProductControlMonthPlans :many
-- The assignments a customer held during a month, each from since until the
-- next one (null while still held), and whether it was their first ever
-- (ADR-20261021154511, ADR-20261021195901).
SELECT held.revision, held.plan, held.first_assignment, held.since, held.held_until
FROM (
  SELECT h.revision, h.plan, h.first_assignment, h.effective_at AS since,
    (lead(h.effective_at) OVER (ORDER BY h.effective_at, h.id))::timestamptz AS held_until
  FROM product_control_plan_history h
  WHERE h.account_id=sqlc.arg(account_id)::uuid AND h.app_id=sqlc.arg(app_id)::uuid AND h.tenant_id=sqlc.arg(tenant_id)::uuid
    AND h.effective_at < sqlc.arg(month_end)::timestamptz
) held
WHERE held.held_until IS NULL OR held.held_until > sqlc.arg(month_start)::timestamptz
ORDER BY held.since
LIMIT sqlc.arg(page_limit)::int;

-- name: ProductControlGetCustomer :one
SELECT revision,plan,suspended,auth_organization_id,trial_starts_at,trial_ends_at,payment_method_at,trial_resolved_at FROM product_control_customers WHERE account_id=$1 AND app_id=$2 AND tenant_id=$3;

-- name: ProductControlCustomerForUpdate :one
SELECT revision,plan,suspended,auth_organization_id,trial_starts_at,trial_ends_at,payment_method_at,trial_resolved_at FROM product_control_customers
WHERE account_id=$1 AND app_id=$2 AND tenant_id=$3 FOR UPDATE;

-- name: ProductControlSetTrialEnd :exec
-- Moves a running trial's end (ADR-20261021195901); the caller checked it
-- under the row lock.
UPDATE product_control_customers SET trial_ends_at=sqlc.arg(trial_ends_at)::timestamptz
WHERE account_id=sqlc.arg(account_id) AND app_id=sqlc.arg(app_id) AND tenant_id=sqlc.arg(tenant_id) AND trial_starts_at IS NOT NULL;

-- name: ProductControlSetPaymentMethod :exec
-- Records since when a payment method has been on file, or clears it
-- (ADR-20261022180921); the caller decided under the row lock.
UPDATE product_control_customers SET payment_method_at=sqlc.narg(payment_method_at)::timestamptz
WHERE account_id=sqlc.arg(account_id) AND app_id=sqlc.arg(app_id) AND tenant_id=sqlc.arg(tenant_id);

-- name: ProductControlUnresolvedTrials :many
-- Ended trials not resolved yet, oldest first, each with what its plan
-- requires in the customer's own revision and whether the customer was
-- assigned another plan after the trial ended (ADR-20261022180921). The
-- customers are locked; a concurrent step skips them.
SELECT c.account_id, c.app_id, c.tenant_id, c.revision, c.plan, c.trial_ends_at, c.payment_method_at,
  COALESCE((v.spec->'plans'->c.plan->>'requires_payment_method')::boolean, false)::boolean AS requires_payment_method,
  COALESCE(v.spec->'plans'->c.plan->>'fallback_plan', '')::text AS fallback_plan,
  EXISTS (SELECT 1 FROM product_control_plan_history h
    WHERE h.app_id=c.app_id AND h.tenant_id=c.tenant_id AND h.effective_at > c.trial_ends_at)::boolean AS changed_after_trial
FROM product_control_customers c
JOIN product_control_revisions v ON v.app_id=c.app_id AND v.revision=c.revision
JOIN apps a ON a.id=c.app_id AND a.account_id=c.account_id
WHERE c.trial_ends_at IS NOT NULL AND c.trial_resolved_at IS NULL AND c.trial_ends_at <= sqlc.arg(resolved_at)::timestamptz AND a.status<>'deleted'
ORDER BY c.trial_ends_at, c.tenant_id
LIMIT sqlc.arg(page_limit)::int
FOR UPDATE OF c SKIP LOCKED;

-- name: ProductControlResolveTrial :exec
-- Marks a customer's ended trial resolved, moving them to fallback_plan when
-- given (ADR-20261022180921).
UPDATE product_control_customers SET trial_resolved_at=sqlc.arg(resolved_at)::timestamptz, plan=COALESCE(sqlc.narg(fallback_plan)::text, plan)
WHERE account_id=sqlc.arg(account_id) AND app_id=sqlc.arg(app_id) AND tenant_id=sqlc.arg(tenant_id) AND trial_resolved_at IS NULL;

-- name: ProductControlAppendTrialFallback :exec
-- The fallback assignment takes effect at the trial's end, so no fee minute
-- after the trial is priced on the paid plan (ADR-20261022180921).
INSERT INTO product_control_plan_history(account_id,app_id,tenant_id,revision,plan,first_assignment,effective_at)
VALUES (sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(tenant_id)::uuid,sqlc.arg(revision)::text,sqlc.arg(plan)::text,false,sqlc.arg(effective_at)::timestamptz);

-- name: ProductControlCustomerByAuthOrganization :one
SELECT tenant_id,revision,plan,suspended,auth_organization_id,trial_starts_at,trial_ends_at,payment_method_at,trial_resolved_at FROM product_control_customers c
JOIN apps a ON a.id=c.app_id AND a.account_id=c.account_id
WHERE c.account_id=$1 AND c.app_id=$2 AND c.auth_organization_id=$3 AND a.status<>'deleted';

-- name: ProductControlListCustomers :many
SELECT tenant_id,revision,plan,suspended,auth_organization_id,trial_starts_at,trial_ends_at,payment_method_at,trial_resolved_at FROM product_control_customers
WHERE account_id=$1 AND app_id=$2 AND tenant_id::text>sqlc.arg(cursor)::text
ORDER BY tenant_id::text LIMIT 101;

-- name: ProductControlCountStarts :one
SELECT count(*)::bigint FROM product_control_receipts
WHERE account_id=$1 AND app_id=$2 AND tenant_id=$3 AND feature=$4 AND period=$5;

-- name: ProductControlCountTrialStarts :one
-- A feature's starts admitted during the customer's trial, which count toward
-- no included starts (ADR-20261021195901).
SELECT count(*)::bigint FROM product_control_receipts
WHERE account_id=sqlc.arg(account_id) AND app_id=sqlc.arg(app_id) AND tenant_id=sqlc.arg(tenant_id) AND feature=sqlc.arg(feature) AND period=sqlc.arg(period)
  AND admitted_at >= sqlc.arg(trial_from)::timestamptz AND admitted_at < sqlc.arg(trial_until)::timestamptz;

-- name: ProductControlTrialStartsByFeature :many
SELECT feature, count(*)::bigint AS starts FROM product_control_receipts
WHERE account_id=sqlc.arg(account_id) AND app_id=sqlc.arg(app_id) AND tenant_id=sqlc.arg(tenant_id) AND period=sqlc.arg(period)
  AND admitted_at >= sqlc.arg(trial_from)::timestamptz AND admitted_at < sqlc.arg(trial_until)::timestamptz
GROUP BY feature ORDER BY feature;

-- name: ProductControlInsertReceipt :exec
INSERT INTO product_control_receipts(operation_id,account_id,app_id,tenant_id,revision,feature,plan,currency,period,unit_price_millicents,admitted_at,admission_evidence)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12);

-- name: ProductControlSettleReceipt :exec
UPDATE product_control_receipts SET resolved_state=sqlc.arg(resolved_state), resolved_at=sqlc.arg(resolved_at),
succeeded_at=CASE WHEN sqlc.arg(resolved_state)::text='succeeded' THEN sqlc.arg(resolved_at) ELSE succeeded_at END
WHERE operation_id=sqlc.arg(operation_id) AND resolved_state='';

-- name: ProductControlUsage :many
SELECT feature,revision,plan,count(*)::bigint AS starts,
count(succeeded_at)::bigint AS succeeded,
sum(CASE WHEN succeeded_at IS NOT NULL AND w.operation_id IS NULL THEN unit_price_millicents ELSE 0 END)::bigint AS amount_millicents
FROM product_control_receipts r LEFT JOIN product_control_waivers w USING(operation_id)
WHERE r.account_id=$1 AND r.app_id=$2 AND r.tenant_id=$3 AND r.period=$4
GROUP BY feature,revision,plan ORDER BY feature,revision,plan;

-- name: ProductControlLiveDefinition :one
SELECT od.id::text FROM customer_operation_definitions od
JOIN deployments d ON d.id=od.deployment_id AND d.app_id=od.app_id AND d.scope=od.scope
JOIN apps a ON a.id=d.app_id AND a.account_id=od.account_id
WHERE od.account_id=$1 AND od.app_id=$2 AND od.scope=$3 AND od.name=$4
AND a.status<>'deleted' AND d.status='live' AND d.traffic_percent=100
AND NOT EXISTS(SELECT 1 FROM deployments sibling WHERE sibling.app_id=d.app_id
 AND sibling.scope=d.scope AND sibling.id<>d.id AND sibling.status='live' AND sibling.traffic_percent>0);

-- name: ProductControlGetReceipt :one
SELECT r.*,w.reason AS waiver_reason,w.created_at AS waived_at,o.record AS operation_record FROM product_control_receipts r
JOIN apps a ON a.id=r.app_id AND a.account_id=r.account_id
LEFT JOIN product_control_waivers w USING(operation_id)
LEFT JOIN customer_operations o ON o.id=r.operation_id AND o.account_id=r.account_id
 AND o.app_id=r.app_id AND o.platform_tenant_id=r.tenant_id
WHERE r.account_id=$1 AND r.app_id=$2 AND r.operation_id=$3 AND a.status<>'deleted';
