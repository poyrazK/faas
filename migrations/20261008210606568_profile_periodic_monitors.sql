-- +goose Up
CREATE TABLE IF NOT EXISTS profile_periodic_monitors (
 id uuid PRIMARY KEY,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 policy_revision bigint NOT NULL CHECK (policy_revision BETWEEN 1 AND 9007199254740991),
 route text NOT NULL CHECK (length(route)>0),
 data jsonb NOT NULL CHECK (jsonb_typeof(data)='object' AND octet_length(data::text)<=262144),
 next_attempt_at timestamptz NOT NULL,
 attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 6),
 lease_token uuid,
 lease_until timestamptz,
 updated_at timestamptz NOT NULL,
 UNIQUE(deployment_id,policy_revision,route),
 CHECK ((lease_token IS NULL)=(lease_until IS NULL))
);
CREATE INDEX IF NOT EXISTS profile_periodic_monitors_due_idx ON profile_periodic_monitors(next_attempt_at,id);
CREATE INDEX IF NOT EXISTS profile_periodic_monitors_app_idx ON profile_periodic_monitors(app_id,updated_at DESC,id);
-- +goose Down
-- Retain incident evidence during binary rollback.
SELECT 1;
