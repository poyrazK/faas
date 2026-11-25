-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE native_postgres_base_refreshes
    ADD COLUMN rolling_standby_node text,
    ADD COLUMN rolling_image text,
    ADD COLUMN phase text NOT NULL DEFAULT '',
    ADD COLUMN reason text NOT NULL DEFAULT '',
    ADD COLUMN standby_restarted boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT native_postgres_base_refreshes_rolling_check CHECK (
        (rolling_standby_node IS NULL AND rolling_image IS NULL AND phase = '' AND NOT standby_restarted)
        OR (rolling_standby_node IS NOT NULL AND rolling_standby_node <> node_id
            AND refresh_base AND resize_id IS NULL
            AND (phase = 'refreshing_standby' OR
                (phase IN ('switching_over', 'restoring_redundancy') AND COALESCE(rolling_image, '') <> '')))
    );
