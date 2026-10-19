-- name: OrgAccount :one
SELECT * FROM orgs WHERE id = $1 AND NOT personal_org;

-- name: ListOrgAccounts :many
SELECT * FROM orgs WHERE NOT personal_org ORDER BY id;

-- name: LockNamespace :one
SELECT id FROM resource_namespaces WHERE id = $1 FOR UPDATE;

-- name: ProjectByOrgSlug :one
SELECT * FROM projects WHERE org_id=$1 AND slug=$2;

-- name: ListOrgProjects :many
SELECT * FROM projects WHERE org_id=$1 ORDER BY created_at DESC,id;

-- name: ProjectOwner :one
SELECT account_id,org_id FROM projects WHERE id=$1;

-- name: PurgePersonalProjects :exec
DELETE FROM projects WHERE account_id=$1 AND org_id IN (SELECT id FROM orgs WHERE personal_org);

-- name: ErasureBlocked :one
SELECT org_app_identity_erasure_blocked($1::uuid)::boolean AS blocked;

-- name: UpdatePlan :execrows
UPDATE orgs SET plan=$2, updated_at=now() WHERE id=$1 AND NOT personal_org;

-- name: UpdateStatus :execrows
UPDATE orgs SET status=$2, deleted_pending=($2='deleted_pending'),
 past_due_at=CASE WHEN $2='past_due' THEN coalesce(past_due_at,now()) ELSE NULL END,
 suspended_reason=NULL, updated_at=now() WHERE id=$1 AND NOT personal_org;

-- name: UpdateCustomer :execrows
UPDATE orgs SET provider_customer_id=$2,stripe_subscription_item=$3, updated_at=now() WHERE id=$1 AND NOT personal_org;

-- name: UpdateSubscription :execrows
UPDATE orgs SET stripe_subscription_item=$2, updated_at=now() WHERE id=$1 AND NOT personal_org;

-- name: UpdateBillingInfo :execrows
UPDATE orgs SET business_name=$2,billing_address=$3,tax_id=$4,updated_at=now() WHERE id=$1 AND NOT personal_org;

-- name: UpdateBillingEmail :execrows
UPDATE orgs SET billing_email=$2,updated_at=now() WHERE id=$1 AND NOT personal_org;

-- name: ReadQuotaWarning :one
SELECT coalesce(last_quota_warning_at=$2,false)::boolean AS already FROM orgs
WHERE id=$1 AND NOT personal_org FOR UPDATE;

-- name: StampQuotaWarning :exec
UPDATE orgs SET last_quota_warning_at=$2 WHERE id=$1 AND NOT personal_org;

-- name: ClearQuotaWarning :execrows
UPDATE orgs SET last_quota_warning_at=NULL WHERE id=$1 AND NOT personal_org;

-- name: SuspendForQuota :execrows
UPDATE orgs SET status='suspended',suspended_reason='free_quota',updated_at=now()
WHERE id=$1 AND NOT personal_org AND status='active';

-- name: RestoreAfterQuota :execrows
UPDATE orgs SET status='active',suspended_reason=NULL,updated_at=now()
WHERE id=$1 AND NOT personal_org AND status='suspended' AND suspended_reason='free_quota';

-- name: OrgByBillingCustomer :one
SELECT o.* FROM orgs o JOIN billing_identities b ON b.account_id=o.id
WHERE NOT o.personal_org AND b.provider=$1 AND b.customer_id=$2;

-- name: OrgByLegacyCustomer :one
SELECT * FROM orgs WHERE NOT personal_org AND provider_customer_id=$1;

-- name: UsageSubject :one
SELECT account_id FROM usage_minutes WHERE instance_id=$1 AND minute=$2 AND app_id=$3;

-- name: DisableStreaming :exec
UPDATE apps SET streaming_enabled=false WHERE org_id=$1 AND streaming_enabled;

-- name: LockOrg :one
SELECT id FROM orgs WHERE id=$1 AND NOT personal_org FOR UPDATE;

-- name: MarkDunning :execrows
UPDATE orgs SET status=sqlc.arg(next_status)::text, deleted_pending=(sqlc.arg(next_status)::text='deleted_pending'),
 past_due_at=coalesce(past_due_at,now()),suspended_reason=NULL,updated_at=now()
WHERE id=sqlc.arg(id)::uuid AND NOT personal_org AND status=sqlc.arg(previous_status)::text;

-- name: SaveBillingIdentity :exec
INSERT INTO billing_identities(account_id,provider,customer_id,subscription_id)
SELECT id,sqlc.arg(provider)::text,coalesce(provider_customer_id,''),coalesce(stripe_subscription_item,'')
FROM orgs WHERE id=sqlc.arg(id)::uuid AND NOT personal_org AND provider_customer_id IS NOT NULL
ON CONFLICT(account_id,provider) DO UPDATE SET customer_id=EXCLUDED.customer_id,subscription_id=EXCLUDED.subscription_id;

-- name: PurgePersonalSecrets :exec
DELETE FROM app_secrets target WHERE target.account_id=$1 AND target.app_id IN
 (SELECT a.id FROM apps a LEFT JOIN orgs o ON o.id=a.org_id WHERE a.account_id=$1 AND (a.org_id IS NULL OR o.personal_org));

-- name: PurgePersonalSigners :exec
DELETE FROM app_trusted_signers target WHERE target.account_id=$1 AND target.app_id IN
 (SELECT a.id FROM apps a LEFT JOIN orgs o ON o.id=a.org_id WHERE a.account_id=$1 AND (a.org_id IS NULL OR o.personal_org));

-- name: PurgePersonalRegistry :exec
DELETE FROM app_registry_credentials target WHERE target.account_id=$1 AND target.app_id IN
 (SELECT a.id FROM apps a LEFT JOIN orgs o ON o.id=a.org_id WHERE a.account_id=$1 AND (a.org_id IS NULL OR o.personal_org));

-- name: PurgePersonalOperations :exec
-- Customer operations pin their invocations (ON DELETE RESTRICT), so they go
-- before PurgePersonalInvocations; their executions and events cascade.
DELETE FROM customer_operations target WHERE target.account_id=$1 AND target.app_id IN
 (SELECT a.id FROM apps a LEFT JOIN orgs o ON o.id=a.org_id WHERE a.account_id=$1 AND (a.org_id IS NULL OR o.personal_org));

-- name: PurgePersonalInvocations :exec
DELETE FROM invocations target WHERE target.account_id=$1 AND target.app_id IN
 (SELECT a.id FROM apps a LEFT JOIN orgs o ON o.id=a.org_id WHERE a.account_id=$1 AND (a.org_id IS NULL OR o.personal_org));

-- name: DeleteUnusedPersonalNamespace :exec
DELETE FROM resource_namespaces n WHERE n.id=$1
AND NOT EXISTS(SELECT 1 FROM apps a WHERE a.account_id=n.id)
AND NOT EXISTS(SELECT 1 FROM projects p WHERE p.account_id=n.id)
AND NOT EXISTS(SELECT 1 FROM managed_postgres_databases d WHERE d.account_id=n.id)
AND NOT EXISTS(SELECT 1 FROM object_buckets b WHERE b.account_id=n.id);

-- name: SetAbuseHold :execrows
UPDATE orgs SET abuse_hold_at=$3,abuse_hold_reason=$2,updated_at=now()
WHERE id=$1 AND NOT personal_org AND abuse_hold_at IS NULL;

-- name: ReleaseAbuseHold :execrows
UPDATE orgs SET abuse_hold_at=NULL,abuse_hold_reason=NULL,updated_at=now()
WHERE id=$1 AND NOT personal_org AND abuse_hold_at IS NOT NULL;

-- name: OwnedCustomerSubscription :one
SELECT subscription_id FROM billing_identities WHERE account_id=$1 AND provider=$2 AND customer_id=$3;

-- name: PurgePersonalDailyUsage :exec
DELETE FROM usage_daily target WHERE target.account_id=$1 AND
 (target.org_id IS NULL OR target.org_id IN(SELECT id FROM orgs WHERE personal_org));

-- name: PurgePersonalBuilderUsage :exec
DELETE FROM builder_usage target WHERE target.account_id=$1 AND
 (target.org_id IS NULL OR target.org_id IN(SELECT id FROM orgs WHERE personal_org));

-- name: PurgePersonalSnapshotStorage :exec
DELETE FROM snapshot_storage_daily target WHERE target.account_id=$1 AND target.app_id NOT IN
 (SELECT a.id FROM apps a JOIN orgs o ON o.id=a.org_id WHERE NOT o.personal_org);
