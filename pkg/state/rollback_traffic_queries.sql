-- name: SynchronousRollbackRequiresBindingCheck :one
SELECT EXISTS(SELECT 1 FROM app_binding_release_policies
 WHERE app_id=sqlc.arg(app_id)::uuid AND scope=sqlc.arg(scope)::text AND mode='enforce');

-- name: ResetSynchronousRollbackTarget :exec
-- The app traffic lock and expected-current comparison precede this write.
-- Existing promotion checks and side effects run before the transaction commits.
UPDATE deployments SET status='snapshotting', error='', error_code='',
 traffic_percent=0, traffic_percent_explicit=false,
 canary_preset='none', canary_step=0, canary_total_steps=0, canary_stages=NULL,
 rollout_state='pending', rollout_started_at=NULL, rollout_completed_at=NULL,
 rollout_aborted_at=NULL, rollout_aborted_reason=''
WHERE id=sqlc.arg(target_id)::uuid;
