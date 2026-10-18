-- Customer work history is sourced from independent receipts, including old
-- features/plans. No price, input, result body, owner notes or document is read.
-- name: ProductWorkHistory :many
WITH history AS (
 SELECT r.operation_id,r.feature,r.plan,r.period,r.admitted_at,r.resolved_state,r.resolved_at,
 coalesce(o.state,'')::text AS live_state,
 coalesce(nullif(r.resolved_state,''),o.state,'unavailable')::text AS outcome_state,
 coalesce(o.state='succeeded' AND (coalesce(o.record->'result','null'::jsonb)<>'null'::jsonb OR coalesce(jsonb_array_length(o.record->'artifacts'),0)>0),false)::boolean AS result_available
 FROM product_control_receipts r
 LEFT JOIN customer_operations o ON o.id=r.operation_id AND o.account_id=r.account_id AND o.app_id=r.app_id AND o.platform_tenant_id=r.tenant_id
 AND (o.state IN ('accepted','running') OR o.expires_at>sqlc.arg(observed_at)::timestamptz)
 WHERE r.account_id=sqlc.arg(account_id) AND r.app_id=sqlc.arg(app_id) AND r.tenant_id=sqlc.arg(tenant_id)
 AND (sqlc.narg(operation_id)::uuid IS NULL OR r.operation_id=sqlc.narg(operation_id)::uuid)
 AND (sqlc.arg(feature)::text='' OR r.feature=sqlc.arg(feature)::text)
 AND (sqlc.narg(from_at)::timestamptz IS NULL OR r.admitted_at>=sqlc.narg(from_at)::timestamptz)
 AND (sqlc.narg(until_at)::timestamptz IS NULL OR r.admitted_at<sqlc.narg(until_at)::timestamptz)
 AND (sqlc.narg(after_uuid)::uuid IS NULL OR (r.admitted_at,r.operation_id)<(sqlc.narg(after_at)::timestamptz,sqlc.narg(after_uuid)::uuid))
)
SELECT operation_id,feature,plan,period,admitted_at,resolved_state,resolved_at,live_state,result_available FROM history
WHERE sqlc.arg(state_filter)::text='all' OR outcome_state=sqlc.arg(state_filter)::text
ORDER BY admitted_at DESC,operation_id DESC LIMIT sqlc.arg(page_limit);
