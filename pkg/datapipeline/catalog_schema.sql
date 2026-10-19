-- Minimal projections of catalogs owned by the app and managed-binding services.
-- Used for atomic refresh intent only; never bootstrap these projections.
CREATE TABLE apps(id uuid PRIMARY KEY, account_id uuid NOT NULL, slug text NOT NULL, status text NOT NULL);
CREATE TABLE managed_postgres_bindings(id uuid PRIMARY KEY,account_id uuid NOT NULL,database_id uuid NOT NULL,app_id uuid NOT NULL,scope text NOT NULL,access text NOT NULL,state text NOT NULL);
CREATE TABLE deployments(id uuid PRIMARY KEY,app_id uuid NOT NULL,status text NOT NULL,scope text NOT NULL);
CREATE TABLE snapshots(id uuid PRIMARY KEY,deployment_id uuid NOT NULL,stale boolean NOT NULL);
CREATE TABLE app_runtime_config_changes(app_id uuid PRIMARY KEY,changed_at timestamptz NOT NULL);
