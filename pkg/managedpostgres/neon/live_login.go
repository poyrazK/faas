package neon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/livequery/schemalog"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Live-query logins (ADR-20261016151947). The owner login prepares the
// database and issues the login over SQL, like binding logins.
//
// Unverified on Neon: that the owner may grant REPLICATION, create event
// triggers and a publication FOR ALL TABLES, and terminate the live login's
// sessions. Stock PostgreSQL needs a superuser for each; the production test
// settles them.

// liveLoginSchemas are the schemas the live login reads: customer tables,
// the Data API schema, and the schema-change log. Auth schemas stay out.
var liveLoginSchemas = []string{"public", "api", "gregale_live"}

type liveLoginManager interface {
	Issue(ctx context.Context, owner managedpostgres.CredentialMaterial, role credentialRole, publication string) (string, error)
	Revoke(ctx context.Context, owner managedpostgres.CredentialMaterial, role credentialRole, slot string) error
}

type sqlLiveLogins struct {
	// Production always uses pgx.Connect. Tests supply an isolated database.
	connect func(context.Context, string) (*pgx.Conn, error)
}

// liveLogin is the database's live login, as a credential role: its name
// and marker derive from the database's credential scope.
func liveLogin(role credentialRole) credentialRole {
	role.name = "gregale_live_" + role.scope
	role.marker = "gregale:live:v1:" + role.scope
	role.access, role.legacy, role.bindingID = "", "", ""
	return role
}

// IssueLiveLogin implements managedpostgres.LiveLoginIssuer.
func (p *Provider) IssueLiveLogin(ctx context.Context, request managedpostgres.LiveLoginRequest) (managedpostgres.CredentialMaterial, error) {
	if request.Publication == "" {
		return managedpostgres.CredentialMaterial{}, managedpostgres.ErrInvalid
	}
	owner, role, err := p.liveOwner(ctx, request.ProviderResourceID)
	if err != nil {
		return managedpostgres.CredentialMaterial{}, err
	}
	password, err := p.liveLoginManager().Issue(ctx, owner, role, request.Publication)
	if err != nil {
		return managedpostgres.CredentialMaterial{}, credentialSQLError(err)
	}
	login := liveLogin(role)
	// A replication connection needs the compute itself, not the pooler.
	var direct []managedpostgres.Endpoint
	for _, endpoint := range owner.Endpoints {
		if endpoint.Role == managedpostgres.EndpointDirect {
			direct = append(direct, endpoint)
		}
	}
	return managedpostgres.CredentialMaterial{ProviderIdentityID: login.name, Username: login.name, Password: password,
		Database: owner.Database, TLSMode: owner.TLSMode, Endpoints: direct}, nil
}

// RevokeLiveLogin implements managedpostgres.LiveLoginIssuer.
func (p *Provider) RevokeLiveLogin(ctx context.Context, request managedpostgres.LiveLoginRequest) error {
	owner, role, err := p.liveOwner(ctx, request.ProviderResourceID)
	if errors.Is(err, managedpostgres.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return credentialSQLError(p.liveLoginManager().Revoke(ctx, owner, role, request.Slot))
}

func (p *Provider) liveLoginManager() liveLoginManager {
	if p.liveLogins == nil {
		return &sqlLiveLogins{}
	}
	return p.liveLogins
}

// liveOwner returns the owner credentials of the resource's branch and its
// credential scope.
func (p *Provider) liveOwner(ctx context.Context, resourceID string) (managedpostgres.CredentialMaterial, credentialRole, error) {
	ref, err := parseResourceRef(resourceID)
	if err != nil {
		return managedpostgres.CredentialMaterial{}, credentialRole{}, err
	}
	branchID := ref.branchID
	if branchID == "" {
		if branchID, err = p.defaultBranch(ctx, ref.projectID); err != nil {
			return managedpostgres.CredentialMaterial{}, credentialRole{}, err
		}
	}
	owner, err := p.ownerCredentials(ctx, ref.projectID, branchID)
	if err != nil {
		return managedpostgres.CredentialMaterial{}, credentialRole{}, err
	}
	role := p.credentialRole(managedpostgres.CredentialRequest{ProviderResourceID: resourceID, Access: managedpostgres.CredentialReadOnly})
	return owner, role, nil
}

// Issue prepares the database and creates the live login, or gives it a new
// password, returning the password.
func (m *sqlLiveLogins) Issue(ctx context.Context, owner managedpostgres.CredentialMaterial, role credentialRole, publication string) (string, error) {
	conn, err := (&sqlCredentialRoles{connect: m.connect}).connection(ctx, owner)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close(ctx) }()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Serialized with binding logins, which grant the live login SET on
	// themselves (grantLiveMembership).
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, role.schemaOwner); err != nil {
		return "", err
	}
	if err := initializeCredentialSchema(ctx, tx, role); err != nil {
		return "", err
	}
	if err := schemalog.Install(ctx, tx); err != nil {
		return "", err
	}
	if err := schemalog.EnsurePublication(ctx, tx, publication); err != nil {
		return "", err
	}
	login := liveLogin(role)
	name := roleIdentifier(login.name)
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	password := "Ga!" + hex.EncodeToString(entropy[:])
	info, err := credentialRoleInfo(ctx, tx, login.name)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if _, err := tx.Exec(ctx, "CREATE ROLE "+name+" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE REPLICATION NOBYPASSRLS PASSWORD '"+password+"'"); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, "COMMENT ON ROLE "+name+" IS '"+login.marker+"'"); err != nil {
			return "", err
		}
	case err != nil:
		return "", err
	case info.marker != login.marker || info.superuser || info.bypassRLS || info.createRole || info.createDB:
		return "", managedpostgres.ErrConflict
	default:
		if _, err := tx.Exec(ctx, "ALTER ROLE "+name+" LOGIN REPLICATION PASSWORD '"+password+"'"); err != nil {
			return "", err
		}
	}
	queries := []string{"GRANT CONNECT ON DATABASE " + roleIdentifier(role.database) + " TO " + name,
		"ALTER DEFAULT PRIVILEGES FOR ROLE " + roleIdentifier(role.schemaOwner) + " IN SCHEMA public GRANT SELECT ON TABLES TO " + name}
	for _, schema := range liveLoginSchemas {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, schema).Scan(&exists); err != nil {
			return "", err
		}
		if exists {
			queries = append(queries, "GRANT USAGE ON SCHEMA "+roleIdentifier(schema)+" TO "+name,
				"GRANT SELECT ON ALL TABLES IN SCHEMA "+roleIdentifier(schema)+" TO "+name)
		}
	}
	for _, query := range queries {
		if _, err := tx.Exec(ctx, query); err != nil {
			return "", err
		}
	}
	rows, err := tx.Query(ctx, `SELECT rolname FROM pg_roles WHERE shobj_description(oid, 'pg_authid') = ANY($1)`, bindingMarkers(role))
	if err != nil {
		return "", err
	}
	bindings, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return "", err
	}
	for _, binding := range bindings {
		if err := grantLiveSet(ctx, tx, binding, login.name); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return password, nil
}

// Revoke stops and drops the live login, then drops its slot.
func (m *sqlLiveLogins) Revoke(ctx context.Context, owner managedpostgres.CredentialMaterial, role credentialRole, slot string) error {
	conn, err := (&sqlCredentialRoles{connect: m.connect}).connection(ctx, owner)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	if err := revokeCredentialRole(ctx, conn, liveLogin(role)); err != nil {
		return err
	}
	if slot == "" {
		return nil
	}
	// The walsender of a terminated session can hold the slot for a moment.
	deadline := time.Now().Add(15 * time.Second)
	for {
		var active *bool
		err := conn.QueryRow(ctx, `SELECT active FROM pg_replication_slots WHERE slot_name = $1`, slot).Scan(&active)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if active != nil && !*active {
			if _, err := conn.Exec(ctx, `SELECT pg_drop_replication_slot($1)`, slot); err == nil {
				return nil
			} else if time.Now().After(deadline) {
				return err
			}
		} else if time.Now().After(deadline) {
			return managedpostgres.ErrUnavailable
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// bindingMarkers are the markers of the binding logins a live query may read
// as in role's database.
func bindingMarkers(role credentialRole) []string {
	out := make([]string, 0, 3)
	for _, access := range []managedpostgres.CredentialAccess{managedpostgres.CredentialReadOnly, managedpostgres.CredentialReadWrite, managedpostgres.CredentialDataAPI} {
		out = append(out, "gregale:credential:v1:"+role.scope+":"+string(access))
	}
	return out
}

// grantLiveMembership lets the database's live login, when it has one, SET
// ROLE to a binding login created after live queries were enabled.
func grantLiveMembership(ctx context.Context, tx pgx.Tx, role credentialRole) error {
	switch role.access {
	case managedpostgres.CredentialReadOnly, managedpostgres.CredentialReadWrite, managedpostgres.CredentialDataAPI:
	default:
		return nil
	}
	login := liveLogin(role)
	info, err := credentialRoleInfo(ctx, tx, login.name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.marker != login.marker {
		return managedpostgres.ErrConflict
	}
	return grantLiveSet(ctx, tx, role.name, login.name)
}

// grantLiveSet grants the live login membership in a binding login that
// allows SET ROLE but inherits nothing (PostgreSQL 16 and later). Before 16
// a membership always allows SET ROLE, and the live login's NOINHERIT keeps
// it from inheriting.
func grantLiveSet(ctx context.Context, tx pgx.Tx, binding, live string) error {
	var version int
	if err := tx.QueryRow(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&version); err != nil {
		return err
	}
	grant := "GRANT " + roleIdentifier(binding) + " TO " + roleIdentifier(live)
	if version >= 160000 {
		grant += " WITH INHERIT FALSE, SET TRUE"
	}
	_, err := tx.Exec(ctx, grant)
	return err
}
