// Package connectionfence implements the SQL part of a managed PostgreSQL
// checkpoint barrier. It needs a private maintenance database on the exact
// source cluster and a role that owns the selected databases. It does not
// attest complete writer coverage, force session drainage or choose a point.
package connectionfence

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectionfence/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

type Identity struct {
	OwnerToken, SourceResourceID string
}

type Request struct {
	Identity
	DatabaseNames []string
}

type Database struct {
	OID, OwnerOID            uint32
	Name                     string
	OriginalAllowConnections bool
	Sessions                 int64
}

type Observation struct {
	Identity
	State                string
	ClosedAt, ReleasedAt time.Time
	Databases            []Database
	// True only for a closed, independently rechecked selected database set
	// with no observed sessions. The provider must also justify complete
	// database/background coverage and its immutable source identity.
	Drained bool
}

type Config struct {
	MaintenanceDatabase, MaintenanceRole string
	// A provider bootstrap supplies these independently authenticated OIDs.
	// Zero is reserved for callers that already own a private maintenance DB.
	MaintenanceDatabaseOID, MaintenanceOwnerOID uint32
}

type Controller struct {
	pool   *pgxpool.Pool
	config Config
}

// The caller owns the pool, source placement attestation and durable clone
// reservation. Errors never expose SQL/connection/provider credential text.
func New(ctx context.Context, pool *pgxpool.Pool, config Config) (*Controller, error) {
	if pool == nil || !validDatabaseName(config.MaintenanceDatabase) || config.MaintenanceRole == "" ||
		(config.MaintenanceDatabaseOID == 0) != (config.MaintenanceOwnerOID == 0) {
		return nil, pgerrors.ErrInvalid
	}
	c := &Controller{pool: pool, config: config}
	if err := c.checkMaintenance(ctx, pool); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Controller) checkMaintenance(ctx context.Context, db sqlc.DBTX) error {
	if c == nil || c.pool == nil {
		return pgerrors.ErrInvalid
	}
	row, err := sqlc.New().MaintenanceIdentity(ctx, db)
	if err != nil {
		return classifyError(err)
	}
	if row.DatabaseName != c.config.MaintenanceDatabase || row.RoleName != c.config.MaintenanceRole ||
		c.config.MaintenanceDatabaseOID != 0 && (!row.DatabaseOid.Valid || row.DatabaseOid.Uint32 != c.config.MaintenanceDatabaseOID) ||
		c.config.MaintenanceOwnerOID != 0 && (!row.OwnerOid.Valid || row.OwnerOid.Uint32 != c.config.MaintenanceOwnerOID) ||
		!row.OwnsDatabase || !row.AllowsConnections.Valid || !row.AllowsConnections.Bool ||
		!row.PrivateConnections || !row.PrivateRole || !row.PrivateSessions {
		return pgerrors.ErrUnsupported
	}
	return nil
}

func (c *Controller) checkInstallation(ctx context.Context, db sqlc.DBTX) error {
	if err := c.checkMaintenance(ctx, db); err != nil {
		return err
	}
	private, err := sqlc.New().FenceSchemaPrivate(ctx, db)
	if err != nil {
		return classifyError(err)
	}
	version, err := sqlc.New().FenceInstallationVersion(ctx, db)
	if err != nil {
		return classifyError(err)
	}
	if !private.Valid || !private.Bool || version != 1 {
		return pgerrors.ErrUnsupported
	}
	return nil
}

// Install is explicit and atomic. A preexisting unknown/private-schema version
// is never overwritten. No source setting changes during installation.
func (c *Controller) Install(ctx context.Context) error {
	if c == nil {
		return pgerrors.ErrInvalid
	}
	if err := c.checkMaintenance(ctx, c.pool); err != nil {
		return err
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return classifyError(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	exists, err := q.FenceSchemaExists(ctx, tx)
	if err != nil {
		return classifyError(err)
	}
	if !exists {
		for _, step := range []func(context.Context, sqlc.DBTX) error{
			q.InstallFenceSchema, q.InstallFenceVersion, q.InstallFenceTable, q.InstallFenceActiveIndex,
			q.InstallFenceDatabaseTable, q.InstallFenceCloseFunction, q.RevokeFenceCloseFunction,
			q.InstallFenceReleaseFunction, q.RevokeFenceReleaseFunction,
			q.InstallFenceAbandonFunction, q.RevokeFenceAbandonFunction, q.RecordFenceVersion,
		} {
			if err := step(ctx, tx); err != nil {
				return classifyError(err)
			}
		}
	}
	if err := c.checkInstallation(ctx, tx); err != nil {
		return err
	}
	return classifyError(tx.Commit(ctx))
}

// Errors after SQL dispatch, including client cancellation, may have an
// unknown commit outcome. Retain the durable clone reservation and recover
// this exact owner; neither absence observed early nor a disconnected client
// is permission to drop recovery authority or start a different owner.
func (c *Controller) Close(ctx context.Context, request Request) (Observation, error) {
	if c == nil || !validIdentity(request.Identity) || len(request.DatabaseNames) == 0 {
		return Observation{}, pgerrors.ErrInvalid
	}
	names := append([]string(nil), request.DatabaseNames...)
	sort.Strings(names)
	for i, name := range names {
		if !validDatabaseName(name) || name == c.config.MaintenanceDatabase || i > 0 && names[i-1] == name {
			return Observation{}, pgerrors.ErrInvalid
		}
	}
	if err := c.checkInstallation(ctx, c.pool); err != nil {
		return Observation{}, err
	}
	_, err := sqlc.New().CloseConnections(ctx, c.pool, sqlc.CloseConnectionsParams{
		OwnerToken: tokenUUID(request.OwnerToken), SourceResourceID: request.SourceResourceID, DatabaseNames: names})
	if err != nil {
		return Observation{}, classifyError(err)
	}
	// A close acknowledgement says nothing about sessions already admitted.
	return c.Observe(ctx, request.Identity)
}

func (c *Controller) Observe(ctx context.Context, identity Identity) (Observation, error) {
	if c == nil || !validIdentity(identity) {
		return Observation{}, pgerrors.ErrInvalid
	}
	if err := c.checkInstallation(ctx, c.pool); err != nil {
		return Observation{}, err
	}
	q := sqlc.New()
	row, err := q.ReadFence(ctx, c.pool, sqlc.ReadFenceParams{OwnerToken: tokenUUID(identity.OwnerToken), SourceResourceID: identity.SourceResourceID})
	if err != nil {
		return Observation{}, classifyError(err)
	}
	dbs, err := q.ReadFenceDatabases(ctx, c.pool, tokenUUID(identity.OwnerToken))
	if err != nil {
		return Observation{}, classifyError(err)
	}
	if len(dbs) != len(row.DatabaseNames) || len(dbs) == 0 && row.State != "abandoned" {
		return Observation{}, pgerrors.ErrConflict
	}
	out := Observation{Identity: identity, State: row.State, ClosedAt: row.ClosedAt.Time, ReleasedAt: row.ReleasedAt.Time, Drained: row.State == "closed"}
	for i, db := range dbs {
		if !db.DatabaseOid.Valid || !db.OwnerOid.Valid || db.DatabaseName != row.DatabaseNames[i] ||
			row.State == "closed" && (!db.IdentityClosed.Valid || !db.IdentityClosed.Bool) {
			return Observation{}, pgerrors.ErrConflict
		}
		out.Databases = append(out.Databases, Database{OID: db.DatabaseOid.Uint32, OwnerOID: db.OwnerOid.Uint32, Name: db.DatabaseName,
			OriginalAllowConnections: db.OriginalAllowConnections, Sessions: db.Sessions})
		out.Drained = out.Drained && db.Sessions == 0
	}
	return out, nil
}

// Release restores only the recorded settings for these exact database OIDs,
// names and owners. The coordinated capture/abandonment owner authorizes this
// call; provider maintenance state alone is not permission to release writers.
func (c *Controller) Release(ctx context.Context, identity Identity) (Observation, error) {
	if c == nil || !validIdentity(identity) {
		return Observation{}, pgerrors.ErrInvalid
	}
	if err := c.checkInstallation(ctx, c.pool); err != nil {
		return Observation{}, err
	}
	_, err := sqlc.New().ReleaseConnections(ctx, c.pool, sqlc.ReleaseConnectionsParams{OwnerToken: tokenUUID(identity.OwnerToken), SourceResourceID: identity.SourceResourceID})
	if err != nil {
		return Observation{}, classifyError(err)
	}
	return c.Observe(ctx, identity)
}

// Abandon serializes against close dispatch and persists a terminal marker
// even when no close is visible yet. A delayed close from this operation
// cannot subsequently block admission. Existing closures restore their exact
// original settings; another operation's active fence is never released.
// Keep the durable reservation until this outcome is independently observed.
func (c *Controller) Abandon(ctx context.Context, identity Identity) (Observation, error) {
	if c == nil || !validIdentity(identity) {
		return Observation{}, pgerrors.ErrInvalid
	}
	if err := c.checkInstallation(ctx, c.pool); err != nil {
		return Observation{}, err
	}
	_, err := sqlc.New().AbandonConnections(ctx, c.pool, sqlc.AbandonConnectionsParams{
		OwnerToken: tokenUUID(identity.OwnerToken), SourceResourceID: identity.SourceResourceID})
	if err != nil {
		return Observation{}, classifyError(err)
	}
	return c.Observe(ctx, identity)
}

func validDatabaseName(name string) bool {
	return name != "" && len(name) <= 63 && utf8.ValidString(name) && !strings.ContainsAny(name, "\x00\r\n")
}

func validIdentity(id Identity) bool {
	token, err := uuid.Parse(id.OwnerToken)
	return err == nil && token != uuid.Nil && token.String() == id.OwnerToken && id.SourceResourceID != "" &&
		len(id.SourceResourceID) <= 255 && !strings.ContainsRune(id.SourceResourceID, '\x00')
}

func tokenUUID(value string) pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.MustParse(value), Valid: true}
}

func classifyError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return pgerrors.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "22023":
			return pgerrors.ErrInvalid
		case "55000", "42P06", "42P07", "23505":
			return pgerrors.ErrConflict
		case "42501":
			return pgerrors.ErrUnsupported
		}
	}
	return pgerrors.ErrUnavailable
}
