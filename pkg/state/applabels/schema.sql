-- Offline projection of the migration-owned apps table for app labels
-- (ADR-20261031100025).
CREATE TABLE apps (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL,
    labels jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(labels) = 'object')
);
