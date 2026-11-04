package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RuntimeRoleEnv names the database role a daemon logs in as instead of the
// DSN's user (ADR-20261023104734). The shared control-plane DSN names the
// schema owner `faas`; a unit that sets this env var connects as its own
// member of the NOLOGIN faas_runtime group, which holds DML on the schema but
// owns nothing, so it cannot alter, drop or re-grant any object.
const RuntimeRoleEnv = "FAAS_DB_ROLE"

// runtimeRolePattern admits only the per-daemon roles the postgres Ansible
// role creates. The owner `faas` is not a runtime role.
var runtimeRolePattern = regexp.MustCompile(`^faas_[a-z][a-z0-9_]{0,40}$`)

// ErrMigrationsPendingForRuntimeRole means the binary carries migrations the
// database has not applied and the session cannot apply them: only the schema
// owner runs DDL. Apply them with cmd/migrate (as the owner) and restart.
var ErrMigrationsPendingForRuntimeRole = errors.New("db: migrations are pending and this database role does not own the schema; run migrate as the schema owner first")

// applyRuntimeRole switches cfg to the role named by FAAS_DB_ROLE. Peer
// authentication over the Unix socket needs no password, so only the user
// changes. A DSN that carries a password authenticates its own user; reusing
// that secret for another role cannot succeed, so it is refused with the fix.
func applyRuntimeRole(cfg *pgx.ConnConfig) error {
	role := strings.TrimSpace(os.Getenv(RuntimeRoleEnv))
	if role == "" {
		return nil
	}
	if !runtimeRolePattern.MatchString(role) {
		return fmt.Errorf("db: %s=%q is not a runtime role (want faas_<daemon>)", RuntimeRoleEnv, role)
	}
	if cfg.Password != "" && cfg.User != role {
		return fmt.Errorf("db: %s=%s cannot reuse the password of DSN user %q; put the role and its own password in the DSN instead", RuntimeRoleEnv, role, cfg.User)
	}
	cfg.User = role
	return nil
}

// sessionOwnsMigrationLedger reports whether the session's role is the one
// that runs migrations: it owns, or is a member of the owner of, the
// goose_db_version table its search_path resolves. The ledger's owner is the
// role that created the schema's objects; the schema's own owner is not a
// signal, since `public` belongs to `postgres` on clusters created before
// PostgreSQL 15. A database without a ledger has never been migrated, and its
// first migration must create one, so it counts as owned.
func sessionOwnsMigrationLedger(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var owns bool
	err := pool.QueryRow(ctx, `SELECT COALESCE((SELECT pg_has_role(current_user, c.relowner, 'USAGE')
FROM pg_catalog.pg_class c WHERE c.oid = to_regclass('goose_db_version')), true)`).Scan(&owns)
	if err != nil {
		return false, fmt.Errorf("db: read migration ledger owner: %w", err)
	}
	return owns, nil
}
