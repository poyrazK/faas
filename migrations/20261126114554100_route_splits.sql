-- filename: 20261126114554100_route_splits.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-20261121170000 amendment 2: one durable row per applied route split.
-- apid writes it; its worker moves the phase forward. A split creates a
-- second app from the source's live build, routes one path to it and,
-- for a memory split, sizes the source app down.
CREATE TABLE IF NOT EXISTS route_splits (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id           uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    source_app_id        uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    -- target_slug is reserved before the app exists, so a crash between
    -- creating the app and recording it can still be rolled back by name.
    target_slug          text NOT NULL CHECK (target_slug ~ '^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$'),
    target_app_id        uuid REFERENCES apps(id) ON DELETE SET NULL,
    target_deployment_id uuid,
    kind                 text NOT NULL CHECK (kind IN ('memory', 'cpu')),
    route                text NOT NULL CHECK (char_length(route) BETWEEN 1 AND 512),
    method               text NOT NULL DEFAULT '' CHECK (char_length(method) <= 16),
    path_glob            text NOT NULL CHECK (path_glob LIKE '/%' AND char_length(path_glob) <= 512),
    route_ram_mb         integer NOT NULL CHECK (route_ram_mb > 0),
    main_ram_mb          integer NOT NULL CHECK (main_ram_mb > 0),
    -- previous_ram_mb is the source's size before the split, restored on undo.
    previous_ram_mb      integer NOT NULL CHECK (previous_ram_mb > 0),
    main_resized         boolean NOT NULL DEFAULT false,
    edge_rule_ids        uuid[] NOT NULL DEFAULT '{}',
    phase                text NOT NULL DEFAULT 'creating'
                         CHECK (phase IN ('creating', 'deploying', 'rule_live', 'done', 'rolled_back', 'undone')),
    warning              text NOT NULL DEFAULT '' CHECK (char_length(warning) <= 1024),
    error                text NOT NULL DEFAULT '' CHECK (char_length(error) <= 1024),
    -- lease_until claims the row for one worker pass, so two apid replicas
    -- never act on the same split at once.
    lease_until          timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
    -- target_app_id has no phase CHECK: deleting the route's app sets it to
    -- NULL whatever phase the split is in.
);

-- One split in flight per source app.
CREATE UNIQUE INDEX IF NOT EXISTS route_splits_one_active
    ON route_splits (source_app_id) WHERE phase IN ('creating', 'deploying', 'rule_live');

-- The worker's scan.
CREATE INDEX IF NOT EXISTS route_splits_in_flight
    ON route_splits (updated_at) WHERE phase IN ('creating', 'deploying', 'rule_live');

CREATE INDEX IF NOT EXISTS route_splits_by_source
    ON route_splits (source_app_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS route_splits;
-- +goose StatementEnd
