-- Offline catalogue declarations; never installed on a customer database.
CREATE TABLE pg_database (oid oid, datname name, encoding integer);
CREATE TABLE pg_roles (oid oid, rolname name);
CREATE TABLE pg_namespace (oid oid, nspname name);
CREATE TABLE pg_class (oid oid, relnamespace oid, relname name, relkind "char", relpersistence "char", relispopulated boolean);
CREATE TABLE pg_attribute (attrelid oid, attnum smallint, attname name, atttypid oid, atttypmod integer, attisdropped boolean);
CREATE TABLE pg_type (oid oid, typnamespace oid, typname name, typtype "char", typcategory "char", typelem oid, typbasetype oid, typrelid oid, typoutput oid);
CREATE TABLE pg_proc (oid oid, pronamespace oid, proname name);
CREATE TABLE pg_enum (enumtypid oid, enumsortorder real, enumlabel name);
CREATE TABLE pg_range (rngtypid oid, rngsubtype oid, rngmultitypid oid);
CREATE TABLE pg_largeobject_metadata (oid oid);
