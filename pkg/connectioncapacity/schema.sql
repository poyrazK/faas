CREATE TABLE postgres_capacity_resources (
 resource_key text PRIMARY KEY,
 placement text NOT NULL,
 generation bigint NOT NULL CHECK(generation>0),
 report_sha256 text NOT NULL,
 connections bigint NOT NULL CHECK(connections>=0),
 slots bigint NOT NULL CHECK(slots>=0),
 senders bigint NOT NULL CHECK(senders>=0),
 workers bigint NOT NULL CHECK(workers>=0),
 observed_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK(expires_at>observed_at AND expires_at<=observed_at+interval '30 seconds')
);
CREATE TABLE postgres_capacity_sources (
 connection_id uuid PRIMARY KEY, account_id uuid NOT NULL,
 connection_revision bigint NOT NULL CHECK(connection_revision>0),
 resource_key text NOT NULL REFERENCES postgres_capacity_resources(resource_key),
 placement text NOT NULL, generation bigint NOT NULL CHECK(generation>0)
);
CREATE TABLE postgres_capacity_targets (
 database_id uuid PRIMARY KEY, account_id uuid NOT NULL,
 database_generation bigint NOT NULL CHECK(database_generation>0),
 backend_fingerprint text NOT NULL, data_resource_id text NOT NULL,
 resource_key text NOT NULL REFERENCES postgres_capacity_resources(resource_key),
 placement text NOT NULL, generation bigint NOT NULL CHECK(generation>0)
);
CREATE TABLE postgres_capacity_reservations (
 owner_kind text NOT NULL CHECK(owner_kind IN ('pipeline','binding')),
 owner_id uuid NOT NULL,
 endpoint text NOT NULL CHECK(endpoint IN ('source','target')),
 resource_key text NOT NULL REFERENCES postgres_capacity_resources(resource_key),
 placement text NOT NULL, generation bigint NOT NULL CHECK(generation>0),
 connections bigint NOT NULL CHECK(connections>=0),
 slots bigint NOT NULL CHECK(slots>=0),
 senders bigint NOT NULL CHECK(senders>=0),
 workers bigint NOT NULL CHECK(workers>=0),
 PRIMARY KEY(owner_kind,owner_id,endpoint)
);
CREATE INDEX postgres_capacity_reservations_resource ON postgres_capacity_reservations(resource_key);

-- Minimal projections of migration-owned catalogs, used only for recovery.
CREATE TABLE data_pipelines(id uuid PRIMARY KEY, account_id uuid NOT NULL, source_connection_id uuid NOT NULL, database_id uuid NOT NULL, state text NOT NULL);
CREATE TABLE managed_postgres_bindings(id uuid PRIMARY KEY, database_id uuid NOT NULL, state text NOT NULL, connection_limit bigint NOT NULL);

CREATE TABLE managed_postgres_cutovers(id uuid PRIMARY KEY, source_database_id uuid NOT NULL, target_database_id uuid NOT NULL, state text NOT NULL);
CREATE TABLE managed_postgres_databases(id uuid PRIMARY KEY, account_id uuid NOT NULL);
