-- filename: 20261119101645100_storage_grant_audit.sql
-- migration-compat: expand
-- ADR-20261117190000 slice 2: secretd signs per-object shared-store URLs for
-- compute nodes. Every grant of a tenant object is recorded with the node,
-- the subject that owns the object and whether it reads or writes, so a node
-- compromise becomes "these tenants' objects, these minutes". Platform reads
-- are only counted in metrics. Append-only for every runtime login.

-- +goose Up
SET LOCAL lock_timeout = '5s';

-- No foreign keys: the record outlives the node and the subject.
CREATE TABLE IF NOT EXISTS storage_grant_audit (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    node_id uuid NOT NULL,
    subject_class text NOT NULL CHECK (subject_class IN ('deployment', 'sandbox_account', 'job', 'build')),
    subject_id uuid NOT NULL,
    app_id uuid,
    access text NOT NULL CHECK (access IN ('read', 'write')),
    objects integer NOT NULL CHECK (objects > 0 AND objects <= 64),
    granted_at timestamp with time zone NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS storage_grant_audit_node_idx ON storage_grant_audit (node_id, granted_at);
CREATE INDEX IF NOT EXISTS storage_grant_audit_subject_idx ON storage_grant_audit (subject_id, granted_at);

-- Default privileges give faas_runtime DML on new tables. Compute must not
-- erase the record; runtime-roles.sql repeats this and grants secretd's own
-- login what it needs.
-- +goose StatementBegin
DO $migration$
BEGIN
    IF to_regrole('faas_runtime') IS NOT NULL THEN
        REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON storage_grant_audit FROM faas_runtime;
    END IF;
END;
$migration$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS storage_grant_audit;
