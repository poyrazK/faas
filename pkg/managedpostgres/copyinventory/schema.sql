-- ADR-531: offline SQLC declarations for read-only customer cluster catalogues.
-- These tables are never installed or mutated by Gregale.
CREATE TABLE pg_database (oid oid, datname name, datdba oid, encoding integer,
 datistemplate boolean, datallowconn boolean, datconnlimit integer, dattablespace oid,
 datcollate text, datctype text, datacl aclitem[]);
CREATE TABLE pg_roles (oid oid, rolname name, rolsuper boolean, rolinherit boolean,
 rolcreaterole boolean, rolcreatedb boolean, rolcanlogin boolean, rolreplication boolean,
 rolconnlimit integer, rolvaliduntil timestamptz, rolbypassrls boolean, rolconfig text[]);
CREATE TABLE pg_auth_members (roleid oid, member oid, grantor oid, admin_option boolean);
CREATE TABLE pg_db_role_setting (setdatabase oid, setrole oid, setconfig text[]);
CREATE TABLE pg_tablespace (oid oid, spcname name, spcowner oid, spcacl aclitem[], spcoptions text[]);
CREATE TABLE pg_prepared_xacts (transaction xid, gid text, prepared timestamptz, owner name, database name);
