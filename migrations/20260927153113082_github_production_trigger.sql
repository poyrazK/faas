-- +goose Up
alter table github_deploy_policies
    add column if not exists production_trigger text not null default 'webhook';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'github_deploy_policies_production_trigger_chk'
          AND conrelid = 'github_deploy_policies'::regclass
    ) THEN
        ALTER TABLE github_deploy_policies
            ADD CONSTRAINT github_deploy_policies_production_trigger_chk
            CHECK (production_trigger IN ('webhook', 'actions'));
    END IF;
END$$;

-- +goose Down
alter table github_deploy_policies drop column if exists production_trigger;
