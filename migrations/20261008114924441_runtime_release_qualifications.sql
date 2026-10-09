-- filename: 20261008114924441_runtime_release_qualifications.sql

-- +goose Up
CREATE TABLE IF NOT EXISTS runtime_release_qualifications (
 release_id text PRIMARY KEY REFERENCES runtime_releases(id),
 profile text NOT NULL CHECK (profile = 'runtime-upgrade-native-v1'),
 architecture text NOT NULL CHECK (architecture IN ('amd64','arm64')),
 host_id uuid NOT NULL CHECK (host_id <> '00000000-0000-0000-0000-000000000000'),
 kernel_boot_id uuid NOT NULL CHECK (kernel_boot_id <> '00000000-0000-0000-0000-000000000000'),
 source_commit text NOT NULL CHECK (source_commit ~ '^([a-f0-9]{40}|[a-f0-9]{64})$'),
 kernel_sha256 text NOT NULL CHECK (kernel_sha256 ~ '^[a-f0-9]{64}$'),
 firecracker_sha256 text NOT NULL CHECK (firecracker_sha256 ~ '^[a-f0-9]{64}$'),
 report_sha256 text NOT NULL CHECK (report_sha256 ~ '^[a-f0-9]{64}$'),
 test_metal_sha256 text NOT NULL CHECK (test_metal_sha256 ~ '^[a-f0-9]{64}$'),
 leakcheck_sha256 text NOT NULL CHECK (leakcheck_sha256 ~ '^[a-f0-9]{64}$'),
 started_at timestamptz NOT NULL CHECK (isfinite(started_at)),
 completed_at timestamptz NOT NULL CHECK (isfinite(completed_at) AND completed_at > started_at),
 recorded_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(recorded_at) AND recorded_at >= completed_at),
 revoked_at timestamptz CHECK (revoked_at IS NULL OR (isfinite(revoked_at) AND revoked_at >= recorded_at)),
 revocation_sha256 text CHECK (revocation_sha256 IS NULL OR revocation_sha256 ~ '^[a-f0-9]{64}$'),
 CHECK ((revoked_at IS NULL) = (revocation_sha256 IS NULL))
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_runtime_release_qualification() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP = 'INSERT' THEN
  IF NOT EXISTS (SELECT 1 FROM runtime_releases WHERE id=NEW.release_id AND architecture=NEW.architecture) THEN
   RAISE EXCEPTION 'runtime qualification architecture differs from release' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
 END IF;
 IF TG_OP = 'DELETE' THEN
  RAISE EXCEPTION 'runtime qualification evidence is retained' USING ERRCODE = '23514';
 END IF;
 IF ROW(NEW.release_id,NEW.profile,NEW.architecture,NEW.host_id,NEW.kernel_boot_id,NEW.source_commit,
  NEW.kernel_sha256,NEW.firecracker_sha256,NEW.report_sha256,NEW.test_metal_sha256,NEW.leakcheck_sha256,
  NEW.started_at,NEW.completed_at,NEW.recorded_at) IS DISTINCT FROM
  ROW(OLD.release_id,OLD.profile,OLD.architecture,OLD.host_id,OLD.kernel_boot_id,OLD.source_commit,
  OLD.kernel_sha256,OLD.firecracker_sha256,OLD.report_sha256,OLD.test_metal_sha256,OLD.leakcheck_sha256,
  OLD.started_at,OLD.completed_at,OLD.recorded_at) OR
  (OLD.revoked_at IS NOT NULL AND ROW(NEW.revoked_at,NEW.revocation_sha256) IS DISTINCT FROM ROW(OLD.revoked_at,OLD.revocation_sha256)) THEN
  RAISE EXCEPTION 'runtime qualification evidence and revocation are immutable' USING ERRCODE = '23514';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS runtime_release_qualification_immutable ON runtime_release_qualifications;
CREATE TRIGGER runtime_release_qualification_immutable BEFORE INSERT OR UPDATE OR DELETE ON runtime_release_qualifications
FOR EACH ROW EXECUTE FUNCTION guard_runtime_release_qualification();

-- +goose Down
-- Forward-only: retain qualification and permanent revocation evidence.
SELECT 1;
