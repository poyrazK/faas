-- filename: 20261019102120334_org_app_quota_fences.sql
-- ADR-570: organization app admissions run inside repeatable-read traffic
-- mutations. The organization advisory lock serialized them but could not
-- expose an admission that committed after the waiter's snapshot, so two
-- members could both take an organization's last app slot. Every admission
-- now bumps its organization's fence row: a snapshot older than the newest
-- admission fails with 40001 and retries, and one still in flight is waited on.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS org_app_quota_fences (
    org_id uuid PRIMARY KEY REFERENCES orgs(id) ON DELETE CASCADE,
    generation bigint NOT NULL DEFAULT 1 CHECK (generation > 0)
);

-- +goose Down
DROP TABLE IF EXISTS org_app_quota_fences;
