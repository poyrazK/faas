-- filename: 20261026154922613_object_storage_timeline_fence.sql
-- migration-compat: expand
-- ADR-20261025111015 amendment: a control-plane PostgreSQL restore brings
-- s3_enabled back as it was at the restore point, and with it every S3 key
-- and grant revoked since. The documented restores recover from the WAL
-- archive and promote, which starts a new PostgreSQL timeline. This row
-- holds the timeline an operator last accepted for serving object storage;
-- the S3 gateway and public reads serve only while the database runs on
-- it, so a restore fences them until `gregalectl object-storage accept`.
--
-- The previous release neither reads nor writes the table.

-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS object_storage_timeline_acceptance (
    singleton   boolean     PRIMARY KEY DEFAULT true CHECK (singleton),
    timeline    text        NOT NULL CHECK (timeline ~ '^[0-9A-F]{8}$'),
    accepted_at timestamptz NOT NULL DEFAULT now(),
    accepted_by text        NOT NULL CHECK (char_length(accepted_by) BETWEEN 1 AND 128)
);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS object_storage_timeline_acceptance;
