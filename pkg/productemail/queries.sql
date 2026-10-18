-- name: LockScope :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(scope)::text, 0));

-- name: Get :one
SELECT * FROM product_control_email_subscriptions WHERE id=$1;

-- name: CountSubscribers :one
SELECT count(*) FROM product_control_email_subscriptions WHERE account_id=$1 AND app_id=$2 AND tenant_id=$3;

-- name: Put :exec
INSERT INTO product_control_email_subscriptions(id,account_id,app_id,tenant_id,auth_app_id,organization_id,actor_id,app_slug,auth_slug,version,work_outcomes,help_replies,work_since,help_since,expires_at,grant_sealed,scan_cursor)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,'')
ON CONFLICT(id) DO UPDATE SET version=excluded.version,work_outcomes=excluded.work_outcomes,help_replies=excluded.help_replies,work_since=excluded.work_since,help_since=excluded.help_since,expires_at=excluded.expires_at,grant_sealed=excluded.grant_sealed,scan_cursor='';

-- name: CancelPending :exec
UPDATE product_control_email_deliveries SET status='cancelled',reason='preferences_changed',updated_at=$2 WHERE subscription_id=$1 AND status='queued';

-- name: List :many
SELECT * FROM product_control_email_subscriptions WHERE id>sqlc.arg(after_id)::uuid AND expires_at>sqlc.arg(observed_at) AND (work_outcomes OR help_replies) AND pause_reason='' ORDER BY id LIMIT sqlc.arg(page_limit);

-- name: Progress :execrows
UPDATE product_control_email_subscriptions SET scan_cursor=$3 WHERE id=$1 AND version=$2;

-- name: Prune :exec
WITH expired AS (SELECT candidate.id FROM product_control_email_deliveries candidate WHERE candidate.created_at<sqlc.arg(window_start) ORDER BY candidate.created_at,candidate.id LIMIT sqlc.arg(prune_limit) FOR UPDATE SKIP LOCKED)
DELETE FROM product_control_email_deliveries d USING expired WHERE d.id=expired.id;

-- name: CountDeliveries :one
SELECT count(*) FROM product_control_email_deliveries WHERE subscription_id=$1;

-- name: InsertDelivery :execrows
INSERT INTO product_control_email_deliveries(id,subscription_id,preference_version,source_revision,notification,status,reason,attempts,created_at,updated_at,next_at,deadline,recipient_generation)
VALUES($1,$2,$3,$4,$5,'queued','',0,$6,$6,$6,$7,$8) ON CONFLICT DO NOTHING;

-- name: DeliveryExists :one
SELECT EXISTS(SELECT 1 FROM product_control_email_deliveries WHERE id=$1);

-- name: Expire :exec
UPDATE product_control_email_deliveries SET status='failed',reason='retry_exhausted',updated_at=$1 WHERE status IN ('queued','leased') AND next_at<=$1 AND (deadline<=$1 OR attempts>=sqlc.arg(max_attempts));

-- name: Claim :many
WITH due AS (SELECT candidate.id FROM product_control_email_deliveries candidate WHERE candidate.status IN ('queued','leased') AND candidate.next_at<=sqlc.arg(observed_at) AND candidate.deadline>sqlc.arg(observed_at) AND candidate.attempts<sqlc.arg(max_attempts) ORDER BY candidate.next_at,candidate.id LIMIT sqlc.arg(page_limit) FOR UPDATE SKIP LOCKED)
UPDATE product_control_email_deliveries d SET status='leased',attempts=d.attempts+1,updated_at=sqlc.arg(observed_at),next_at=sqlc.arg(lease_until) FROM due WHERE d.id=due.id RETURNING d.*;

-- name: Ready :one
SELECT s.* FROM product_control_email_subscriptions s JOIN product_control_email_deliveries d ON d.subscription_id=s.id
WHERE d.id=$1 AND d.status='leased' AND d.attempts=$2 AND d.next_at>sqlc.arg(observed_at) AND d.deadline>sqlc.arg(observed_at) AND s.version=d.preference_version AND s.expires_at>sqlc.arg(observed_at);

-- name: Finish :execrows
UPDATE product_control_email_deliveries SET status=$3,reason=$4,next_at=$5,updated_at=$6 WHERE id=$1 AND status='leased' AND attempts=$2;

-- name: Recent :many
SELECT * FROM product_control_email_deliveries WHERE subscription_id=$1 AND created_at>sqlc.arg(window_start) ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_limit);

-- name: OwnerCounts :one
SELECT count(*) FILTER(WHERE d.status='queued') AS queued,
 count(*) FILTER(WHERE d.status='leased') AS leased,
 count(*) FILTER(WHERE d.status='captured') AS captured,
 count(*) FILTER(WHERE d.status='provider_accepted') AS provider_accepted,
 count(*) FILTER(WHERE d.status='failed') AS failed,
 count(*) FILTER(WHERE d.status='cancelled') AS cancelled,
 count(*) FILTER(WHERE d.status='delivered') AS delivered,
 count(*) FILTER(WHERE d.status='bounced') AS bounced,
 count(*) FILTER(WHERE d.status='complained') AS complained,
 (min(d.created_at) FILTER(WHERE d.status IN ('queued','leased')))::timestamptz AS oldest_pending_at
FROM product_control_email_subscriptions s JOIN product_control_email_deliveries d ON d.subscription_id=s.id
WHERE s.account_id=sqlc.arg(account_id) AND s.app_id=sqlc.arg(app_id)
 AND (NOT sqlc.arg(filter_tenant)::boolean OR s.tenant_id=sqlc.arg(tenant_id)) AND d.created_at>sqlc.arg(window_start);

-- name: OwnerDeliveries :many
SELECT sqlc.embed(d),s.tenant_id,s.version,s.work_outcomes,s.help_replies,s.work_since,s.help_since,s.expires_at,s.pause_reason,s.paused_at
FROM product_control_email_subscriptions s JOIN product_control_email_deliveries d ON d.subscription_id=s.id
WHERE s.account_id=sqlc.arg(account_id) AND s.app_id=sqlc.arg(app_id)
 AND (NOT sqlc.arg(filter_tenant)::boolean OR s.tenant_id=sqlc.arg(tenant_id)) AND d.created_at>sqlc.arg(window_start)
 AND (sqlc.arg(status)::text='all' OR d.status=sqlc.arg(status))
 AND (NOT sqlc.arg(has_cursor)::boolean OR (d.created_at,d.id)<(sqlc.arg(after_at)::timestamptz,sqlc.arg(after_id)::uuid))
ORDER BY d.created_at DESC,d.id DESC LIMIT sqlc.arg(page_limit);

-- name: OwnerGet :one
SELECT sqlc.embed(s),sqlc.embed(d)
FROM product_control_email_subscriptions s JOIN product_control_email_deliveries d ON d.subscription_id=s.id
WHERE s.account_id=sqlc.arg(account_id) AND s.app_id=sqlc.arg(app_id) AND s.tenant_id=sqlc.arg(tenant_id)
 AND d.id=sqlc.arg(delivery_id) AND d.created_at>sqlc.arg(window_start);

-- name: OwnerLock :one
SELECT sqlc.embed(s),sqlc.embed(d)
FROM product_control_email_subscriptions s JOIN product_control_email_deliveries d ON d.subscription_id=s.id
WHERE s.account_id=sqlc.arg(account_id) AND s.app_id=sqlc.arg(app_id) AND s.tenant_id=sqlc.arg(tenant_id)
 AND d.id=sqlc.arg(delivery_id) AND d.created_at>sqlc.arg(window_start) FOR UPDATE OF d;

-- name: OwnerRetryReceipt :one
SELECT r.*,s.tenant_id FROM product_control_email_retry_receipts r
JOIN product_control_email_deliveries d ON d.id=r.delivery_id
JOIN product_control_email_subscriptions s ON s.id=d.subscription_id
WHERE s.account_id=sqlc.arg(account_id) AND s.app_id=sqlc.arg(app_id) AND s.tenant_id=sqlc.arg(tenant_id)
 AND d.id=sqlc.arg(delivery_id) AND r.failed_attempt=sqlc.arg(failed_attempt) AND d.created_at>sqlc.arg(window_start);

-- name: OwnerRetry :execrows
UPDATE product_control_email_deliveries SET status='queued',reason='',next_at=sqlc.arg(observed_at),updated_at=sqlc.arg(observed_at)
WHERE id=sqlc.arg(delivery_id) AND status='failed' AND reason='provider_rejected' AND attempts=sqlc.arg(failed_attempt)
 AND attempts<sqlc.arg(max_attempts) AND deadline>sqlc.arg(observed_at) AND next_at<=sqlc.arg(observed_at);

-- name: InsertRetryReceipt :exec
INSERT INTO product_control_email_retry_receipts(delivery_id,failed_attempt,queued_at) VALUES($1,$2,$3);

-- name: PrunePendingFeedback :exec
WITH expired AS (SELECT candidate.provider_id,candidate.kind FROM product_control_email_pending_feedback candidate WHERE candidate.received_at<sqlc.arg(window_start) ORDER BY candidate.received_at,candidate.provider_id,candidate.kind LIMIT sqlc.arg(prune_limit) FOR UPDATE SKIP LOCKED)
DELETE FROM product_control_email_pending_feedback f USING expired WHERE f.provider_id=expired.provider_id AND f.kind=expired.kind;

-- name: PendingFeedback :many
SELECT * FROM product_control_email_pending_feedback WHERE provider_id=$1 AND received_at>=sqlc.arg(window_start) ORDER BY kind;

-- name: CountPendingFeedback :one
SELECT count(*) FROM product_control_email_pending_feedback;

-- name: PendingFeedbackExists :one
SELECT EXISTS(SELECT 1 FROM product_control_email_pending_feedback WHERE provider_id=$1 AND kind=$2);

-- name: PutPendingFeedback :exec
INSERT INTO product_control_email_pending_feedback(provider_id,kind,occurred_at,hard_bounce,received_at) VALUES($1,$2,$3,$4,$5)
ON CONFLICT(provider_id,kind) DO UPDATE SET occurred_at=LEAST(product_control_email_pending_feedback.occurred_at,excluded.occurred_at),hard_bounce=product_control_email_pending_feedback.hard_bounce OR excluded.hard_bounce;

-- name: DeletePendingFeedback :exec
DELETE FROM product_control_email_pending_feedback WHERE provider_id=$1;

-- name: ReceiptDelivery :one
SELECT * FROM product_control_email_deliveries WHERE id=$1 AND status='leased' AND attempts=$2 FOR UPDATE;

-- name: ProviderDelivery :one
SELECT * FROM product_control_email_deliveries WHERE provider_id=$1 AND created_at>sqlc.arg(window_start) FOR UPDATE;

-- name: AcceptReceipt :exec
UPDATE product_control_email_deliveries SET status='provider_accepted',reason='',provider_id=$2,updated_at=$3,recipient_sealed=$4 WHERE id=$1;

-- name: SaveFeedback :exec
UPDATE product_control_email_deliveries SET status=$2,delivered_at=$3,bounced_at=$4,complained_at=$5,hard_bounced=$6,updated_at=$7 WHERE id=$1;

-- name: PauseSubscriber :exec
UPDATE product_control_email_subscriptions SET pause_reason=$2,paused_at=$3,pause_recipient_sealed=$4 WHERE id=$1 AND (pause_reason='' OR pause_reason='hard_bounce' AND $2='complaint') AND ($2='complaint' OR recipient_generation=$5);

-- name: CancelSuppressedPending :exec
UPDATE product_control_email_deliveries SET status='cancelled',reason='recipient_suppressed',updated_at=$2 WHERE subscription_id=$1 AND status='queued' AND EXISTS(SELECT 1 FROM product_control_email_subscriptions s WHERE s.id=$1 AND s.pause_reason<>'');

-- name: RecoveryReceipt :one
SELECT r.*,s.tenant_id FROM product_control_email_recoveries r JOIN product_control_email_subscriptions s ON s.id=r.subscription_id
WHERE r.id=$1 AND r.subscription_id=$2 AND r.recovered_at>sqlc.arg(window_start);

-- name: PruneRecoveries :exec
WITH expired AS (SELECT candidate.id FROM product_control_email_recoveries candidate WHERE candidate.recovered_at<=sqlc.arg(window_start) ORDER BY candidate.recovered_at,candidate.id LIMIT sqlc.arg(prune_limit))
DELETE FROM product_control_email_recoveries r USING expired WHERE r.id=expired.id;

-- name: CountRecoveries :one
SELECT count(*) FROM product_control_email_recoveries WHERE subscription_id=$1 AND recovered_at>sqlc.arg(window_start);

-- name: InsertRecovery :exec
INSERT INTO product_control_email_recoveries(id,subscription_id,expected_version,previous_generation,version,generation,work_outcomes,help_replies,recovered_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9);

-- name: RecoverSubscription :exec
UPDATE product_control_email_subscriptions SET version=$2,recipient_generation=$3,work_outcomes=$4,help_replies=$5,work_since=$6,help_since=$6,expires_at=$7,grant_sealed=$8,scan_cursor='',pause_reason='',paused_at=NULL,pause_recipient_sealed='' WHERE id=$1;

-- name: Recoveries :many
SELECT r.*,s.tenant_id FROM product_control_email_recoveries r JOIN product_control_email_subscriptions s ON s.id=r.subscription_id
WHERE s.account_id=sqlc.arg(account_id) AND s.app_id=sqlc.arg(app_id)
 AND (NOT sqlc.arg(filter_tenant)::boolean OR s.tenant_id=sqlc.arg(tenant_id))
 AND (NOT sqlc.arg(filter_recipient)::boolean OR s.id=sqlc.arg(recipient_id))
 AND r.recovered_at>sqlc.arg(window_start)
 AND (NOT sqlc.arg(has_cursor)::boolean OR (r.recovered_at,r.id)<(sqlc.arg(after_at)::timestamptz,sqlc.arg(after_id)::uuid))
ORDER BY r.recovered_at DESC,r.id DESC LIMIT sqlc.arg(page_limit);

-- name: TryConsent :one
SELECT pg_try_advisory_xact_lock(hashtextextended(sqlc.arg(scope)::text,0));

-- name: PrepareUnsubscribe :one
UPDATE product_control_email_deliveries d
SET unsubscribe_hash=COALESCE(d.unsubscribe_hash,sqlc.arg(token_hash)::bytea),
 unsubscribe_sealed=CASE WHEN d.unsubscribe_hash IS NULL THEN sqlc.arg(token_sealed)::bytea ELSE d.unsubscribe_sealed END
FROM product_control_email_subscriptions s
WHERE d.id=sqlc.arg(delivery_id) AND d.subscription_id=s.id AND d.status='leased'
 AND d.attempts=sqlc.arg(attempts) AND d.next_at>sqlc.arg(observed_at) AND d.deadline>sqlc.arg(observed_at)
 AND s.version=d.preference_version AND s.recipient_generation=d.recipient_generation
 AND s.expires_at>sqlc.arg(observed_at) AND (s.work_outcomes OR s.help_replies) AND s.pause_reason=''
RETURNING d.unsubscribe_sealed;

-- name: UnsubscribeDelivery :one
SELECT sqlc.embed(s),sqlc.embed(d) FROM product_control_email_deliveries d
JOIN product_control_email_subscriptions s ON s.id=d.subscription_id
WHERE d.unsubscribe_hash=sqlc.arg(token_hash) AND d.created_at>sqlc.arg(window_start);

-- name: OptOutReceipt :one
SELECT r.*,s.tenant_id FROM product_control_email_opt_outs r
JOIN product_control_email_subscriptions s ON s.id=r.subscription_id
JOIN product_control_email_deliveries d ON d.id=r.delivery_id
WHERE r.subscription_id=$1 AND r.expected_version=$2 AND d.created_at>sqlc.arg(window_start);

-- name: InsertOptOut :exec
INSERT INTO product_control_email_opt_outs(id,subscription_id,delivery_id,expected_version,version,generation,unsubscribed_at) VALUES($1,$2,$3,$4,$5,$6,$7);

-- name: Unsubscribe :exec
UPDATE product_control_email_subscriptions SET version=$2,work_outcomes=false,help_replies=false,work_since=$3,help_since=$3,expires_at=NULL,grant_sealed='',scan_cursor='' WHERE id=$1;

-- name: CancelUnsubscribedPending :exec
UPDATE product_control_email_deliveries SET status='cancelled',reason='unsubscribed',updated_at=$2 WHERE subscription_id=$1 AND status='queued';

-- name: OptOuts :many
SELECT r.*,s.tenant_id FROM product_control_email_opt_outs r
JOIN product_control_email_subscriptions s ON s.id=r.subscription_id
JOIN product_control_email_deliveries d ON d.id=r.delivery_id
WHERE s.account_id=sqlc.arg(account_id) AND s.app_id=sqlc.arg(app_id)
 AND (NOT sqlc.arg(filter_tenant)::boolean OR s.tenant_id=sqlc.arg(tenant_id))
 AND (NOT sqlc.arg(filter_recipient)::boolean OR s.id=sqlc.arg(recipient_id))
 AND r.unsubscribed_at>sqlc.arg(window_start) AND d.created_at>sqlc.arg(window_start)
 AND (NOT sqlc.arg(has_cursor)::boolean OR (r.unsubscribed_at,r.id)<(sqlc.arg(after_at)::timestamptz,sqlc.arg(after_id)::uuid))
ORDER BY r.unsubscribed_at DESC,r.id DESC LIMIT sqlc.arg(page_limit);
