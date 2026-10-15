-- +goose Up
-- Private scheduling metadata; checking consumer health does not change intent.
ALTER TABLE app_application_standards
 ADD COLUMN observation_checked_at timestamptz,
 ADD COLUMN observation_revision bigint NOT NULL DEFAULT 0
   CHECK (observation_revision >= 0 AND observation_revision <= desired_revision);

CREATE INDEX app_application_standards_observation_due
 ON app_application_standards (observation_checked_at NULLS FIRST,app_id)
 WHERE state IN ('persisted','observed') AND persisted_revision=desired_revision;

-- +goose Down
DROP INDEX app_application_standards_observation_due;
ALTER TABLE app_application_standards
 DROP COLUMN observation_checked_at, DROP COLUMN observation_revision;
