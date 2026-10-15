-- Copy into a new numbered migration after reviewing the existing version trigger.
-- Declare that every successful update advances this database-managed integer.
COMMENT ON COLUMN api.notes.version IS '@gregale:version';
