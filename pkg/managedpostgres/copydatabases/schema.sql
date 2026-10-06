-- Offline SQLC schema. The private protocol installs only its own journal.
CREATE SCHEMA gregale_copy_databases;
CREATE TABLE gregale_copy_databases.plan (
 singleton boolean PRIMARY KEY CHECK (singleton),
 version integer NOT NULL CHECK (version=1), body jsonb NOT NULL
);
CREATE TABLE gregale_copy_databases.databases (
 source_oid oid PRIMARY KEY, target_oid oid NOT NULL UNIQUE,
 state text NOT NULL CHECK (state IN ('reserved','creating','created','existing')),
 claimed_at timestamptz, created_at timestamptz,
 CHECK ((state='reserved' AND claimed_at IS NULL AND created_at IS NULL) OR
   (state='creating' AND claimed_at IS NOT NULL AND created_at IS NULL) OR
   (state='created' AND claimed_at IS NOT NULL AND created_at IS NOT NULL) OR
   (state='existing' AND claimed_at IS NULL AND created_at IS NOT NULL))
);

CREATE SCHEMA gregale_copy_database_maintenance;
CREATE TABLE gregale_copy_database_maintenance.windows (
 source_oid oid PRIMARY KEY CHECK (source_oid<>0),
 target_oid oid NOT NULL UNIQUE CHECK (target_oid<>0),
 owner_id uuid NOT NULL UNIQUE CHECK (owner_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 plan_fingerprint text NOT NULL CHECK (plan_fingerprint ~ '^[0-9a-f]{64}$'),
 preparation_created_at timestamptz NOT NULL,
 state text NOT NULL CHECK (state IN ('open','closing','closed')),
 opened_at timestamptz NOT NULL, closed_at timestamptz,
 CHECK (isfinite(preparation_created_at) AND isfinite(opened_at) AND opened_at>=preparation_created_at AND
  ((state IN ('open','closing') AND closed_at IS NULL) OR
   (state='closed' AND closed_at IS NOT NULL AND isfinite(closed_at) AND closed_at>=opened_at)))
);
CREATE UNIQUE INDEX windows_one_active ON gregale_copy_database_maintenance.windows ((1)) WHERE state IN ('open','closing');

CREATE SCHEMA gregale_copy_database_verification;
CREATE TABLE gregale_copy_database_verification.windows (
 source_oid oid PRIMARY KEY CHECK (source_oid<>0),
 target_oid oid NOT NULL UNIQUE CHECK (target_oid<>0),
 owner_id uuid NOT NULL UNIQUE CHECK (owner_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 plan_fingerprint text NOT NULL CHECK (plan_fingerprint ~ '^[0-9a-f]{64}$'),
 preparation_created_at timestamptz NOT NULL,
 state text NOT NULL CHECK (state IN ('open','closing','closed')),
 opened_at timestamptz NOT NULL, closed_at timestamptz,
 import_owner_id uuid NOT NULL CHECK (import_owner_id<>owner_id AND import_owner_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 import_opened_at timestamptz NOT NULL, import_closed_at timestamptz NOT NULL,
 CHECK (isfinite(preparation_created_at) AND isfinite(opened_at) AND
  isfinite(import_opened_at) AND isfinite(import_closed_at) AND import_opened_at>=preparation_created_at AND
  import_closed_at>=import_opened_at AND opened_at>=import_closed_at AND
  ((state IN ('open','closing') AND closed_at IS NULL) OR
   (state='closed' AND closed_at IS NOT NULL AND isfinite(closed_at) AND closed_at>=opened_at)))
);

CREATE UNIQUE INDEX windows_one_active ON gregale_copy_database_verification.windows ((1)) WHERE state IN ('open','closing');

CREATE SCHEMA gregale_copy_database_verification_retries;
CREATE TABLE gregale_copy_database_verification_retries.windows (
 source_oid oid NOT NULL CHECK (source_oid<>0),
 target_oid oid NOT NULL CHECK (target_oid<>0),
 owner_id uuid NOT NULL UNIQUE CHECK (owner_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 plan_fingerprint text NOT NULL CHECK (plan_fingerprint ~ '^[0-9a-f]{64}$'),
 preparation_created_at timestamptz NOT NULL,
 state text NOT NULL CHECK (state IN ('open','closing','closed')),
 opened_at timestamptz NOT NULL, closed_at timestamptz,
 import_owner_id uuid NOT NULL CHECK (import_owner_id<>owner_id AND import_owner_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 import_opened_at timestamptz NOT NULL, import_closed_at timestamptz NOT NULL,
 attempt integer NOT NULL CHECK (attempt BETWEEN 2 AND 3),
 previous_owner_id uuid NOT NULL UNIQUE CHECK (previous_owner_id<>owner_id AND previous_owner_id<>import_owner_id AND previous_owner_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 previous_opened_at timestamptz NOT NULL, previous_closed_at timestamptz NOT NULL,
 PRIMARY KEY (source_oid,attempt),
 CHECK (isfinite(previous_opened_at) AND isfinite(previous_closed_at) AND previous_opened_at>=import_closed_at AND previous_closed_at>=previous_opened_at AND opened_at>=previous_closed_at),
 CHECK (isfinite(preparation_created_at) AND isfinite(opened_at) AND
  isfinite(import_opened_at) AND isfinite(import_closed_at) AND import_opened_at>=preparation_created_at AND
  import_closed_at>=import_opened_at AND opened_at>=import_closed_at AND
  ((state IN ('open','closing') AND closed_at IS NULL) OR
   (state='closed' AND closed_at IS NOT NULL AND isfinite(closed_at) AND closed_at>=opened_at)))
);
CREATE UNIQUE INDEX windows_one_active ON gregale_copy_database_verification_retries.windows ((1)) WHERE state IN ('open','closing');
