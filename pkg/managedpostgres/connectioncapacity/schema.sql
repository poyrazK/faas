-- Offline SQLC declarations; never installed on customer databases.
CREATE TABLE pg_database (datname name, datconnlimit integer);
CREATE TABLE pg_roles (rolname name, rolconnlimit integer);
