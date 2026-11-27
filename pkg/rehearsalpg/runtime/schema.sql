-- Offline PostgreSQL catalog projection; never installed in customer databases.
CREATE TABLE pg_database (oid oid,datname name,datallowconn boolean);
CREATE TABLE pg_roles (oid oid,rolname name,rolsuper boolean);
CREATE TABLE pg_namespace (oid oid,nspname name);
CREATE TABLE pg_class (oid oid,relnamespace oid,relname name,relkind "char",relpersistence "char");
CREATE TABLE pg_attribute (attrelid oid,attnum smallint,attname name,atttypid oid,attisdropped boolean);
CREATE TABLE pg_type (oid oid,typnamespace oid,typname name,typoutput oid);
CREATE TABLE pg_proc (oid oid,pronamespace oid);
CREATE TABLE pg_extension (extname name);
CREATE TABLE pg_stat_activity (pid integer,datname name,state text,xact_start timestamptz);
CREATE TABLE pg_locks (pid integer,granted boolean,waitstart timestamptz);
CREATE TABLE pg_prepared_xacts (database name);

-- Projection of the stock Node Task migration for the read-only observer.
CREATE TABLE gregale_mcp_tasks (
  namespace text, task_id uuid, tool_name text, handler_version text, status text, attempt_count integer,
  lease_expires_at timestamptz, lease_token uuid, expires_at timestamptz, cancel_requested_at timestamptz, result_encrypted bytea
);
CREATE TABLE gregale_mcp_task_workers (namespace text, worker_id uuid, expires_at timestamptz, draining boolean);
CREATE TABLE gregale_mcp_task_worker_handlers (
  namespace text, worker_id uuid, tool_name text, handler_version text, deployment_id text
);
