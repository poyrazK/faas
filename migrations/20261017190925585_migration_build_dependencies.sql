-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE deployment_build_dependency_gates (
 deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE CHECK (deployment_id <> '00000000-0000-0000-0000-000000000000'),
 progress jsonb NOT NULL CHECK (
  jsonb_typeof(progress)='object'
  AND progress ?& ARRAY['dependencies','status','deadline_at','check_after']
  AND jsonb_typeof(progress->'dependencies')='array'
  AND jsonb_array_length(progress->'dependencies') BETWEEN 1 AND 10
  AND jsonb_typeof(progress->'status')='string'
  AND progress->>'status' IN ('waiting','ready','failed')
  AND jsonb_typeof(progress->'deadline_at')='string'
  AND jsonb_typeof(progress->'check_after')='string'
  AND octet_length(progress::text)<=16000
 ),
 check_after timestamptz NOT NULL DEFAULT '-infinity' CHECK (check_after < 'infinity'::timestamptz)
);
-- +goose Down
DROP TABLE deployment_build_dependency_gates;
