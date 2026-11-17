-- filename: 20261117160000110_storage_object_deletions_generation.sql
-- migration-compat: expand
-- ADR-20261115180000 slice 2: exclusive-artifact retirement through the
-- object GC.

-- +goose Up
--
-- A native capture cohort retires its objects conditioned on the object
-- generation each publication acknowledged (storage.RetireExclusiveArtifact):
-- a replacement written under the same key must survive. In defer mode the
-- compute node records that generation here, and the control plane deletes
-- exactly that generation once nothing references the key. NULL is an
-- ordinary delete request.
SET LOCAL lock_timeout = '5s';

ALTER TABLE storage_object_deletions ADD COLUMN IF NOT EXISTS generation bigint;
ALTER TABLE storage_object_deletions DROP CONSTRAINT IF EXISTS storage_object_deletions_generation_check;
ALTER TABLE storage_object_deletions ADD CONSTRAINT storage_object_deletions_generation_check
    CHECK (generation IS NULL OR generation > 0) NOT VALID;
ALTER TABLE storage_object_deletions VALIDATE CONSTRAINT storage_object_deletions_generation_check;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE storage_object_deletions DROP CONSTRAINT IF EXISTS storage_object_deletions_generation_check;
ALTER TABLE storage_object_deletions DROP COLUMN IF EXISTS generation;
