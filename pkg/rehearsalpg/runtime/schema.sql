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
