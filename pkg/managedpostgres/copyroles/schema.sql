-- Offline declarations for SQLC only; installed privately on the independent
-- target by the transactional role seed protocol, never on a source cluster.
CREATE SCHEMA gregale_copy_roles;
CREATE TABLE gregale_copy_roles.receipt (
 singleton boolean PRIMARY KEY CHECK (singleton),
 version integer NOT NULL CHECK (version=1),
 plan jsonb NOT NULL, created_roles jsonb NOT NULL,
 seeded_at timestamptz NOT NULL
);
CREATE SCHEMA gregale_copy_memberships;
CREATE TABLE gregale_copy_memberships.receipt (
 singleton boolean PRIMARY KEY CHECK (singleton),
 version integer NOT NULL CHECK (version=1),
 plan jsonb NOT NULL, applied_at timestamptz NOT NULL
);
