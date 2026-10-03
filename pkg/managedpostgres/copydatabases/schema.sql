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
