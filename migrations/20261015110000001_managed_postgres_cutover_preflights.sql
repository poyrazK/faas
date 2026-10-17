-- +goose Up
CREATE TABLE IF NOT EXISTS managed_postgres_cutover_preflights (
 cutover_id uuid PRIMARY KEY REFERENCES managed_postgres_cutovers(id) ON DELETE CASCADE,
 state text NOT NULL CHECK (state IN ('pending','blocked')),
 revision bigint NOT NULL CHECK (revision > 0),
 binding_ids text[] NOT NULL DEFAULT '{}',
 blockers text[] NOT NULL DEFAULT '{}',
 verification_fresh boolean NOT NULL DEFAULT false,
 source_matches boolean NOT NULL DEFAULT false,
 target_matches boolean NOT NULL DEFAULT false,
 bindings_match boolean NOT NULL DEFAULT false,
 cutover_updated_at timestamptz,
 checked_at timestamptz,
 expires_at timestamptz,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 CHECK ((state='pending' AND checked_at IS NULL AND expires_at IS NULL AND cutover_updated_at IS NULL AND cardinality(blockers)=0)
     OR (state='blocked' AND checked_at IS NOT NULL AND expires_at IS NOT NULL AND cutover_updated_at IS NOT NULL AND cardinality(blockers)>0)),
 CHECK (state<>'blocked' OR 'activation_disabled'=ANY(blockers))
);
CREATE INDEX IF NOT EXISTS managed_postgres_cutover_preflights_pending
 ON managed_postgres_cutover_preflights(updated_at,cutover_id) WHERE state='pending';

-- +goose Down
DROP TABLE managed_postgres_cutover_preflights;
