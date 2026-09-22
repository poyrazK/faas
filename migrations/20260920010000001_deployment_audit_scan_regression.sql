-- filename: 20260920010000001_deployment_audit_scan_regression.sql
-- +goose Up
-- +goose StatementBegin

-- Continuous image security re-evaluation (scan freshness follow-up).
-- imaged records a durable transition when a live enforce-mode deployment
-- changes from clean scan evidence to blocking findings or unavailable scan
-- evidence. The row is the durable trigger for the runtime quarantine path.
ALTER TABLE deployment_audit
    DROP CONSTRAINT IF EXISTS deployment_audit_kind_chk;
ALTER TABLE deployment_audit
    ADD CONSTRAINT deployment_audit_kind_chk
        CHECK (kind IN (
            'deploy.created',
            'deploy.source_ref',
            'deploy.local_tarball',
            'deploy.traffic_changed',
            'deploy.health_probe_failed',
            'deploy.health_recovered',
            'deploy.rolled_back',
            'deploy.removed',
            'deploy.rollout_started',
            'deploy.rollout_completed',
            'deploy.rollout_aborted',
            'deploy.canary_step_advanced',
            'deploy.alert_rule_fired',
            'deploy.scan_regressed'
        ));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE IF EXISTS deployment_audit
    DROP CONSTRAINT IF EXISTS deployment_audit_kind_chk;
ALTER TABLE IF EXISTS deployment_audit
    ADD CONSTRAINT deployment_audit_kind_chk
        CHECK (kind IN (
            'deploy.created',
            'deploy.source_ref',
            'deploy.local_tarball',
            'deploy.traffic_changed',
            'deploy.health_probe_failed',
            'deploy.health_recovered',
            'deploy.rolled_back',
            'deploy.removed',
            'deploy.rollout_started',
            'deploy.rollout_completed',
            'deploy.rollout_aborted',
            'deploy.canary_step_advanced',
            'deploy.alert_rule_fired'
        ));

-- +goose StatementEnd
