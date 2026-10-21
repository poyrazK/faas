-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS deployment_cron_outcomes (
 deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
 session_id text NOT NULL CHECK (session_id ~ '^[0-9a-f]{32}$'),
 revision bigint NOT NULL CHECK (revision>0),
 status text NOT NULL CHECK (status IN ('prepared','activated','conflict','discarded')),
 reason text NOT NULL CHECK (reason IN ('','awaiting_readiness','awaiting_full_traffic','awaiting_rollout','competing_traffic','awaiting_activation','cron_changed','quota_changed','identity_conflict','plan_unavailable','intent_superseded','deployment_superseded','deployment_failed','deployment_canceled','rollout_aborted','non_production','cron_guard_conflict','activation_discarded')),
 updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO deployment_cron_outcomes(deployment_id,session_id,revision,status,reason)
SELECT d.id,c.session_id,c.revision,c.status,
 CASE WHEN c.status='conflict' THEN 'cron_guard_conflict' ELSE '' END
FROM deployment_cron_activations c JOIN apps a ON a.id=c.app_id
LEFT JOIN deployment_submission_receipts r ON r.app_id=c.app_id AND r.attempt_id=c.session_id
LEFT JOIN upload_sessions u ON u.id=c.upload_id AND u.app_slug=a.slug AND u.account_id=a.account_id AND u.status='committed'
JOIN deployments d ON d.app_id=c.app_id AND d.id::text=coalesce(r.deployment_id::text,u.deployment_id)
WHERE c.status<>'canceled'
ON CONFLICT (deployment_id) DO NOTHING;

-- +goose Down
DROP TABLE deployment_cron_outcomes;
