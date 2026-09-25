-- A deployment's compute shape is immutable revision input. Zero values are
-- retained only for legacy/direct-SQL rows; the application stores resolve
-- omitted values from the app before creating a deployment.
-- +goose Up
ALTER TABLE deployments
  ADD COLUMN IF NOT EXISTS ram_mb integer NOT NULL DEFAULT 0;

ALTER TABLE deployments
  ADD COLUMN IF NOT EXISTS cpu_millicores integer NOT NULL DEFAULT 0;

UPDATE deployments d
   SET ram_mb = CASE WHEN d.ram_mb = 0 THEN a.ram_mb ELSE d.ram_mb END,
       cpu_millicores = CASE WHEN d.cpu_millicores = 0
                             THEN COALESCE(NULLIF(a.cpu_millicores, 0), 1000)
                             ELSE d.cpu_millicores END
  FROM apps a
 WHERE a.id = d.app_id
   AND (d.ram_mb = 0 OR d.cpu_millicores = 0);

-- +goose StatementBegin
DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
     WHERE conname = 'deployments_compute_ram_chk'
       AND conrelid = 'deployments'::regclass
  ) THEN
    ALTER TABLE deployments ADD CONSTRAINT deployments_compute_ram_chk CHECK (ram_mb >= 0);
  END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
     WHERE conname = 'deployments_compute_cpu_chk'
       AND conrelid = 'deployments'::regclass
  ) THEN
    ALTER TABLE deployments ADD CONSTRAINT deployments_compute_cpu_chk
      CHECK (cpu_millicores IN (0, 250, 500, 1000));
  END IF;
END $$;
-- +goose StatementEnd

COMMENT ON COLUMN deployments.ram_mb IS
  'Immutable deployment RAM in MiB; zero is reserved for legacy/direct-SQL rows.';
COMMENT ON COLUMN deployments.cpu_millicores IS
  'Immutable deployment CPU in millicores; zero is reserved for legacy/direct-SQL rows.';

-- +goose Down
ALTER TABLE deployments DROP CONSTRAINT IF EXISTS deployments_compute_cpu_chk;
ALTER TABLE deployments DROP CONSTRAINT IF EXISTS deployments_compute_ram_chk;
ALTER TABLE deployments DROP COLUMN IF EXISTS cpu_millicores;
ALTER TABLE deployments DROP COLUMN IF EXISTS ram_mb;
