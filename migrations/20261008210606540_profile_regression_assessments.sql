-- +goose Up
ALTER TABLE profile_investigations ADD COLUMN IF NOT EXISTS assessment jsonb
    CHECK (assessment IS NULL OR (
        jsonb_typeof(assessment) = 'object'
        AND assessment->>'status' IS NOT NULL
        AND assessment->>'status' IN ('regressed', 'no_regression_detected', 'inconclusive')
        AND octet_length(assessment::text) <= 131072
    ));

-- +goose Down
ALTER TABLE profile_investigations DROP COLUMN assessment;
