-- ADR-375: this schema is installed only in a private customer-cluster
-- maintenance database. It is not a control-plane migration or clone input.
-- Read-only PostgreSQL catalogue declarations for SQLC's offline analyser.
-- The installer never creates or writes these relations.
CREATE TABLE pg_database (oid oid, datname name, datdba oid, datallowconn boolean, datacl aclitem[]);
CREATE TABLE pg_roles (oid oid, rolname name, rolcanlogin boolean, rolsuper boolean, rolcreatedb boolean, rolcreaterole boolean, rolreplication boolean, rolbypassrls boolean);
CREATE TABLE pg_auth_members (roleid oid, member oid, grantor oid, admin_option boolean);
CREATE TABLE pg_namespace (oid oid, nspname name, nspowner oid, nspacl aclitem[]);
CREATE TABLE pg_class (relnamespace oid, relowner oid, relacl aclitem[]);
CREATE TABLE pg_proc (pronamespace oid, proowner oid, prosecdef boolean, proacl aclitem[]);
CREATE TABLE pg_stat_activity (datid oid, usename name);
CREATE SCHEMA gregale_checkpoint;
CREATE TABLE gregale_checkpoint.installation (
    singleton boolean PRIMARY KEY CHECK (singleton),
    version integer NOT NULL CHECK (version = 1)
);
CREATE TABLE gregale_checkpoint.connection_fences (
    owner_token uuid PRIMARY KEY,
    source_resource_id text NOT NULL CHECK (source_resource_id <> ''),
    state text NOT NULL CHECK (state IN ('closed', 'released', 'abandoned')),
    database_names text[] NOT NULL CHECK (cardinality(database_names) > 0 OR state = 'abandoned'),
    closed_at timestamptz,
    released_at timestamptz,
    CHECK ((state = 'closed' AND closed_at IS NOT NULL AND released_at IS NULL) OR
           (state = 'released' AND closed_at IS NOT NULL AND released_at IS NOT NULL) OR
           (state = 'abandoned' AND closed_at IS NULL AND released_at IS NOT NULL AND cardinality(database_names) = 0))
);
CREATE UNIQUE INDEX connection_fences_one_active ON gregale_checkpoint.connection_fences ((1)) WHERE state = 'closed';
CREATE TABLE gregale_checkpoint.connection_fence_databases (
    owner_token uuid NOT NULL REFERENCES gregale_checkpoint.connection_fences(owner_token) ON DELETE RESTRICT,
    database_oid oid NOT NULL,
    database_name text NOT NULL CHECK (database_name <> ''),
    owner_oid oid NOT NULL,
    original_allow_connections boolean NOT NULL,
    PRIMARY KEY (owner_token, database_oid),
    UNIQUE (owner_token, database_name)
);
