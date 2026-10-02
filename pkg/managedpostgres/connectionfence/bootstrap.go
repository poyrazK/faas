package connectionfence

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectionfence/sqlc"
)

const MaintenanceDatabase = "gregale_checkpoint"

type BootstrapConfig struct {
	SourceDatabase, SourceRole string
	// Zero is for an already authenticated local caller. Provider bootstraps
	// supply the PostgreSQL major observed on the exact endpoint.
	SourcePostgresMajor int
}

// Maintenance is a private bootstrap receipt, not a checkpoint or a writer
// closure acknowledgement. The owner token must be durably reserved per exact
// provider dataset before IO; it must not come from a customer-supplied name.
type Maintenance struct {
	Identity
	OwnerRole             string
	OwnerOID, DatabaseOID uint32
	State                 string
}

type Bootstrap struct {
	conn   *pgx.Conn
	config BootstrapConfig
}

// NewBootstrap borrows a dedicated, direct source connection. The caller
// owns it. Do not put this connection in a pool or use it concurrently. An
// unsuccessful operation closes it, so an unknown dispatched SQL statement
// cannot leave session role changes or an advisory lock on a reused session.
// The caller separately authenticates the exact provider branch/endpoint.
func NewBootstrap(conn *pgx.Conn, config BootstrapConfig) (*Bootstrap, error) {
	if conn == nil || !validDatabaseName(config.SourceDatabase) || config.SourceDatabase == MaintenanceDatabase ||
		!validDatabaseName(config.SourceRole) || config.SourcePostgresMajor != 0 && config.SourcePostgresMajor < 16 {
		return nil, managedpostgres.ErrInvalid
	}
	return &Bootstrap{conn: conn, config: config}, nil
}

// AuthenticateReadyMaintenance recovers the installed owner through its private
// database, including while the source database rejects connections. It only
// observes a ready receipt: it cannot reserve, create, activate or retire an
// owner. The caller owns this dedicated connection and exact provider placement.
func AuthenticateReadyMaintenance(ctx context.Context, conn *pgx.Conn, config BootstrapConfig, receipt Maintenance) (Maintenance, error) {
	if conn == nil || !validDatabaseName(config.SourceDatabase) || config.SourceDatabase == MaintenanceDatabase ||
		!validDatabaseName(config.SourceRole) || config.SourcePostgresMajor != 0 && config.SourcePostgresMajor < 16 ||
		!validMaintenance(receipt, true) || receipt.State != "ready" {
		return Maintenance{}, managedpostgres.ErrInvalid
	}
	q := sqlc.New()
	row, err := q.MaintenanceBootstrapIdentity(ctx, conn)
	if err != nil {
		return Maintenance{}, classifyError(err)
	}
	if row.DatabaseName != MaintenanceDatabase || row.RoleName != config.SourceRole || row.SessionRole != row.RoleName ||
		!row.PrivateRole || row.ServerVersion < 160000 ||
		config.SourcePostgresMajor != 0 && int(row.ServerVersion/10000) != config.SourcePostgresMajor {
		return Maintenance{}, managedpostgres.ErrUnsupported
	}
	if err := q.InstallMaintenanceBootstrapFunction(ctx, conn); err != nil {
		return Maintenance{}, classifyError(err)
	}
	return (&Bootstrap{conn: conn, config: config}).action(ctx, receipt, "check")
}

// Reserve atomically creates the private NOLOGIN owner and records ownership.
// Repeating it can recover a lost acknowledgement, but cannot adopt a role
// created by a different administrator or a database merely sharing its name.
func (b *Bootstrap) Reserve(ctx context.Context, identity Identity) (Maintenance, error) {
	return b.run(ctx, Maintenance{Identity: identity}, "reserve", false)
}

// Create creates the reserved database with ALLOW_CONNECTIONS false in the
// CREATE statement itself. A recovered database with the exact private owner
// is returned without repeating CREATE. Persist its OID before activation.
// Durable first-dispatch authority remains the coordinator's responsibility.
func (b *Bootstrap) Create(ctx context.Context, receipt Maintenance) (Maintenance, error) {
	if !validMaintenance(receipt, false) || receipt.State != "reserved" {
		return Maintenance{}, managedpostgres.ErrInvalid
	}
	return b.run(ctx, receipt, "check", true)
}

func (b *Bootstrap) Observe(ctx context.Context, receipt Maintenance) (Maintenance, error) {
	if !validMaintenance(receipt, false) {
		return Maintenance{}, managedpostgres.ErrInvalid
	}
	return b.run(ctx, receipt, "check", false)
}

// Activate transactionally removes PUBLIC access before opening the database,
// pins its OID in the private owner marker and removes the owner's CREATEDB
// privilege. Retrying a lost reply rechecks the same ready identity and ACL.
func (b *Bootstrap) Activate(ctx context.Context, receipt Maintenance) (Maintenance, error) {
	if !validMaintenance(receipt, true) || receipt.State != "reserved" && receipt.State != "ready" {
		return Maintenance{}, managedpostgres.ErrInvalid
	}
	return b.run(ctx, receipt, "activate", false)
}

// RetireReserved serializes with creation, recording a permanent tombstone
// even if reserve has not arrived. Delayed reserve/create cannot resurrect it.
// It does not remove databases, roles or an activated maintenance installation.
// Keep control-plane recovery authority until this exact state is observed.
func (b *Bootstrap) RetireReserved(ctx context.Context, receipt Maintenance) (Maintenance, error) {
	if receipt.OwnerOID != 0 && !validMaintenance(receipt, false) {
		return Maintenance{}, managedpostgres.ErrInvalid
	}
	return b.run(ctx, receipt, "retire", false)
}

func (b *Bootstrap) run(ctx context.Context, receipt Maintenance, action string, create bool) (out Maintenance, err error) {
	if b == nil || b.conn == nil || !validIdentity(receipt.Identity) {
		return Maintenance{}, managedpostgres.ErrInvalid
	}
	q := sqlc.New()
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if err != nil {
			_ = b.conn.Close(cleanup)
			return
		}
		if resetErr := q.ResetMaintenanceOwnerRole(cleanup, b.conn); resetErr != nil {
			err = classifyError(resetErr)
		} else if unlocked, unlockErr := q.UnlockMaintenanceBootstrap(cleanup, b.conn); unlockErr != nil || !unlocked {
			if unlockErr != nil {
				err = classifyError(unlockErr)
			} else {
				err = managedpostgres.ErrConflict
			}
		}
		if err != nil {
			out = Maintenance{}
			_ = b.conn.Close(cleanup)
		}
	}()
	if err = b.checkIdentity(ctx); err != nil {
		return Maintenance{}, err
	}
	if err = q.LockMaintenanceBootstrap(ctx, b.conn); err != nil {
		return Maintenance{}, classifyError(err)
	}
	if err = b.checkIdentity(ctx); err != nil {
		return Maintenance{}, err
	}
	if err = q.InstallMaintenanceBootstrapFunction(ctx, b.conn); err != nil {
		return Maintenance{}, classifyError(err)
	}
	out, err = b.action(ctx, receipt, action)
	if err != nil || !create {
		return out, err
	}
	if out.State != "reserved" {
		return Maintenance{}, managedpostgres.ErrConflict
	}
	if out.DatabaseOID != 0 {
		return out, nil
	}
	if _, err = q.SetMaintenanceOwnerRole(ctx, b.conn, out.OwnerRole); err != nil {
		return Maintenance{}, classifyError(err)
	}
	if err = q.CreateMaintenanceDatabase(ctx, b.conn); err != nil {
		return Maintenance{}, classifyError(err)
	}
	if err = q.ResetMaintenanceOwnerRole(ctx, b.conn); err != nil {
		return Maintenance{}, classifyError(err)
	}
	return b.action(ctx, receipt, "check")
}

func (b *Bootstrap) checkIdentity(ctx context.Context) error {
	row, err := sqlc.New().MaintenanceBootstrapIdentity(ctx, b.conn)
	if err != nil {
		return classifyError(err)
	}
	if row.DatabaseName != b.config.SourceDatabase || row.RoleName != b.config.SourceRole || row.SessionRole != row.RoleName ||
		!row.OwnsDatabase || !row.PrivateRole || row.ServerVersion < 160000 ||
		b.config.SourcePostgresMajor != 0 && int(row.ServerVersion/10000) != b.config.SourcePostgresMajor {
		return managedpostgres.ErrUnsupported
	}
	return nil
}

func (b *Bootstrap) action(ctx context.Context, receipt Maintenance, action string) (Maintenance, error) {
	q := sqlc.New()
	state, err := q.MaintenanceBootstrapAction(ctx, b.conn, sqlc.MaintenanceBootstrapActionParams{
		OwnerToken: tokenUUID(receipt.OwnerToken), SourceResourceID: receipt.SourceResourceID, Action: action,
		ExpectedOwner: pgtype.Uint32{Uint32: receipt.OwnerOID, Valid: true}, ExpectedDatabase: pgtype.Uint32{Uint32: receipt.DatabaseOID, Valid: true}})
	if err != nil {
		return Maintenance{}, classifyError(err)
	}
	role := maintenanceOwnerRole(receipt.Identity)
	row, err := q.ReadMaintenanceBootstrap(ctx, b.conn, role)
	if err != nil {
		return Maintenance{}, classifyError(err)
	}
	if !row.OwnerOid.Valid || row.OwnerOid.Uint32 == 0 || !row.DatabaseOid.Valid || state == "ready" && row.DatabaseOid.Uint32 == 0 {
		return Maintenance{}, managedpostgres.ErrConflict
	}
	return Maintenance{Identity: receipt.Identity, OwnerRole: role, OwnerOID: row.OwnerOid.Uint32, DatabaseOID: row.DatabaseOid.Uint32, State: state}, nil
}

func maintenanceOwnerRole(identity Identity) string {
	return "grg_ckpt_" + strings.ReplaceAll(identity.OwnerToken, "-", "")
}

func validMaintenance(receipt Maintenance, databaseRequired bool) bool {
	return validIdentity(receipt.Identity) && receipt.OwnerOID != 0 && receipt.OwnerRole == maintenanceOwnerRole(receipt.Identity) &&
		(!databaseRequired || receipt.DatabaseOID != 0)
}
