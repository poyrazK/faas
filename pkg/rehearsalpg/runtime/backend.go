package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
	"github.com/onebox-faas/faas/pkg/rehearsalpg/runtime/sqlc"
)

var ErrFixture = errors.New("isolated PostgreSQL fixture operation failed")
var ErrPending = errors.New("PostgreSQL has unfinished SQL at sealing")

type Backend interface {
	Identity() string
	Provision(context.Context, rehearsalpg.Resource, string, string) ([]rehearsalpg.Snapshot, error)
	FenceAndCapture(context.Context, rehearsalpg.Resource) ([]rehearsalpg.Snapshot, error)
	LockWait(context.Context, rehearsalpg.Resource) (int64, error)
	Drop(context.Context, rehearsalpg.Resource) error
}

type Postgres struct {
	config   *pgx.ConnConfig
	identity string
}

// NewPostgres accepts only an explicit local, dedicated administrative database.
// No inherited DATABASE_URL, service file or alternate host is consulted later.
func NewPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	if dsn == "" {
		return nil, ErrFixture
	}
	c, err := pgx.ParseConfig(dsn)
	if err != nil || c.Database != "gregale_rehearsal_admin" || !localHost(c.Host) || len(c.Fallbacks) > 0 {
		return nil, ErrFixture
	}
	c.ConnectTimeout = 3 * time.Second
	c.RuntimeParams = map[string]string{"application_name": "gregale_rehearsal_collector", "statement_timeout": "5000"}
	p := &Postgres{config: c}
	conn, err := p.connect(ctx, "")
	if err != nil {
		return nil, ErrFixture
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	ready, err := sqlc.New().ClusterReady(ctx, conn)
	if err != nil || !ready {
		return nil, ErrFixture
	}
	p.identity, err = p.connectionIdentity(ctx, conn)
	if err != nil {
		return nil, ErrFixture
	}
	return p, nil
}

func (p *Postgres) Identity() string { return p.identity }
func (p *Postgres) connectionIdentity(ctx context.Context, c *pgx.Conn) (string, error) {
	v, e := sqlc.New().BackendIdentity(ctx, c)
	if e != nil || v.SystemID == "" || v.AdminDatabaseID == "" {
		return "", ErrFixture
	}
	return rehearsalpg.HashBytes([]byte(p.config.Host + ":" + strconv.Itoa(int(p.config.Port)) + ":" + v.SystemID + ":" + v.AdminDatabaseID)), nil
}

func localHost(host string) bool {
	if strings.HasPrefix(host, "/") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
func (p *Postgres) connect(ctx context.Context, database string) (*pgx.Conn, error) {
	c := p.config.Copy()
	if database != "" {
		c.Database = database
	}
	conn, err := pgx.ConnectConfig(ctx, c)
	if err != nil {
		return nil, ErrFixture
	}
	if p.identity != "" {
		actual, e := p.connectionIdentity(ctx, conn)
		if e != nil || actual != p.identity {
			_ = conn.Close(context.WithoutCancel(ctx))
			return nil, ErrFixture
		}
	}
	return conn, nil
}
func (p *Postgres) resourceNames(r rehearsalpg.Resource) bool {
	u, e := uuid.Parse(r.ID)
	return e == nil && r.BackendSHA256 == p.identity && p.identity != "" && r.DatabaseName == "grpg_"+strings.ReplaceAll(u.String(), "-", "") && r.RoleName == r.DatabaseName
}

func execFormatted(ctx context.Context, c *pgx.Conn, command string, err error) error {
	if err != nil {
		return ErrFixture
	}
	if _, err = c.Exec(ctx, command); err != nil {
		return ErrFixture
	}
	return nil
}

func (p *Postgres) Provision(ctx context.Context, r rehearsalpg.Resource, fixture, password string) ([]rehearsalpg.Snapshot, error) {
	if !p.resourceNames(r) || r.Spec.Validate() != nil || len(fixture) == 0 || len(fixture) > rehearsalpg.MaxFixtureBytes || rehearsalpg.HashBytes([]byte(fixture)) != r.Spec.FixtureSHA256 || len(password) != 64 {
		return nil, ErrFixture
	}
	admin, e := p.connect(ctx, "")
	if e != nil {
		return nil, ErrFixture
	}
	defer func() { _ = admin.Close(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	command, e := q.FormatCreateRole(ctx, admin, sqlc.FormatCreateRoleParams{RoleName: r.RoleName, Password: password})
	if execFormatted(ctx, admin, command, e) != nil {
		return nil, ErrFixture
	}
	command, e = q.FormatCreateDatabase(ctx, admin, r.DatabaseName)
	if execFormatted(ctx, admin, command, e) != nil {
		return nil, ErrFixture
	}
	command, e = q.FormatConfigureDatabase(ctx, admin, sqlc.FormatConfigureDatabaseParams{DatabaseName: r.DatabaseName, RoleName: r.RoleName})
	if execFormatted(ctx, admin, command, e) != nil {
		return nil, ErrFixture
	}
	data, e := p.connect(ctx, r.DatabaseName)
	if e != nil {
		return nil, ErrFixture
	}
	defer func() { _ = data.Close(context.WithoutCancel(ctx)) }()
	command, e = q.FormatConfigureSchema(ctx, data, r.RoleName)
	if execFormatted(ctx, data, command, e) != nil {
		return nil, ErrFixture
	}
	c := p.config.Copy()
	c.Database = r.DatabaseName
	c.User = r.RoleName
	c.Password = password
	c.RuntimeParams = map[string]string{"application_name": "gregale_rehearsal_fixture", "statement_timeout": "15000", "lock_timeout": "10000"}
	wrong := c.Copy()
	wrong.Password = strings.Repeat("0", 64)
	probe, e := pgx.ConnectConfig(ctx, wrong)
	if e == nil {
		_ = probe.Close(ctx)
		return nil, ErrFixture
	}
	user, e := pgx.ConnectConfig(ctx, c)
	if e != nil {
		return nil, ErrFixture
	}
	_, e = user.PgConn().Exec(ctx, fixture).ReadAll()
	if e != nil {
		_ = user.Close(context.WithoutCancel(ctx))
		return nil, ErrFixture
	}
	defer func() { _ = user.Close(context.WithoutCancel(ctx)) }()
	if user.PgConn().TxStatus() != 'I' {
		return nil, ErrFixture
	}
	return capture(ctx, user, r.Spec)
}

func capture(ctx context.Context, c *pgx.Conn, spec rehearsalpg.Spec) ([]rehearsalpg.Snapshot, error) {
	out, _, err := captureInventory(ctx, c, spec, false)
	return out, err
}

// captureInventory reads the declared tables' selected columns and, with
// withSchema, the database's schema inventory, in one read-only snapshot.
// Undeclared relations and columns no longer make capture fail: the
// inventory and undeclared-table row counts carry them (ADR-20261020180000).
func captureInventory(ctx context.Context, c *pgx.Conn, spec rehearsalpg.Spec, withSchema bool) ([]rehearsalpg.Snapshot, []rehearsalpg.SchemaObject, error) {
	tx, e := c.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return nil, nil, ErrFixture
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	if q.ConfigureCapture(ctx, tx) != nil {
		return nil, nil, ErrFixture
	}
	extensions, e := q.SupportedExtensions(ctx, tx)
	if e != nil || !extensions.Valid || !extensions.Bool {
		return nil, nil, ErrFixture
	}
	rels, e := q.Relations(ctx, tx)
	if e != nil || len(rels) > rehearsalpg.MaxRelations {
		return nil, nil, ErrFixture
	}
	expected := map[string]rehearsalpg.Table{}
	for _, t := range spec.Tables {
		expected[t.Schema+"."+t.Name] = t
	}
	present := 0
	for _, rel := range rels {
		if _, declared := expected[rel.SchemaName+"."+rel.TableName]; !declared {
			continue
		}
		if rel.Kind != "r" || rel.Persistence != "p" {
			return nil, nil, ErrFixture
		}
		present++
	}
	if present != len(spec.Tables) {
		return nil, nil, ErrFixture
	}
	out := make([]rehearsalpg.Snapshot, 0, len(spec.Tables))
	// CanonicalSnapshots bounds the compacted inventory at a quarter of
	// MaxEvidenceBytes. Stop reading at the full bound so customer rows cannot
	// make apid buffer 16 tables x 1000 rows x 64 KiB before that check runs.
	scanned := 0
	for _, t := range spec.Tables {
		cols, e := q.Columns(ctx, tx, sqlc.ColumnsParams{SchemaName: t.Schema, TableName: t.Name})
		if e != nil {
			return nil, nil, ErrFixture
		}
		// Only the selected columns are read, so only they need a supported
		// type; any other column is part of the schema inventory.
		supported := map[string]bool{}
		for _, col := range cols {
			supported[col.Name] = col.Supported
		}
		for _, col := range t.Columns {
			if !supported[col] {
				return nil, nil, ErrFixture
			}
		}
		command, e := q.FormatSelect(ctx, tx, sqlc.FormatSelectParams{Columns: t.Columns, SchemaName: t.Schema, TableName: t.Name})
		if e != nil {
			return nil, nil, ErrFixture
		}
		rows, e := tx.Query(ctx, command)
		if e != nil {
			return nil, nil, ErrFixture
		}
		snapshot := rehearsalpg.Snapshot{Table: t.Schema + "." + t.Name, Rows: []json.RawMessage{}}
		for rows.Next() {
			var row []byte
			if rows.Scan(&row) != nil || len(row) == 0 || len(snapshot.Rows) >= rehearsalpg.MaxRows {
				rows.Close()
				return nil, nil, ErrFixture
			}
			if scanned += len(row); scanned > rehearsalpg.MaxEvidenceBytes {
				rows.Close()
				return nil, nil, rehearsalpg.ErrSnapshotBound
			}
			snapshot.Rows = append(snapshot.Rows, json.RawMessage(row))
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, nil, ErrFixture
		}
		out = append(out, snapshot)
	}
	var schema []rehearsalpg.SchemaObject
	if withSchema {
		if schema, e = captureSchema(ctx, tx, q, expected); e != nil {
			return nil, nil, e
		}
	}
	if tx.Commit(ctx) != nil {
		return nil, nil, ErrFixture
	}
	snapshots, e := rehearsalpg.CanonicalSnapshots(out)
	return snapshots, schema, e
}

// captureSchema returns the canonical schema inventory, with a bounded row
// count for every undeclared table so data written outside the declared
// tables still differs between runs.
func captureSchema(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, declared map[string]rehearsalpg.Table) ([]rehearsalpg.SchemaObject, error) {
	rows, e := q.SchemaInventory(ctx, tx)
	if e != nil || len(rows) > rehearsalpg.MaxSchemaObjects {
		return nil, ErrFixture
	}
	out := make([]rehearsalpg.SchemaObject, 0, len(rows))
	for _, row := range rows {
		o := rehearsalpg.SchemaObject{Kind: row.Kind, Name: row.Name, Definition: rehearsalpg.SchemaDefinition(row.Definition)}
		if _, isDeclared := declared[row.RelSchema+"."+row.RelName]; row.Kind == "table" && row.RelSchema != "" && !isDeclared {
			command, e := q.FormatCount(ctx, tx, sqlc.FormatCountParams{SchemaName: row.RelSchema, TableName: row.RelName})
			if e != nil {
				return nil, ErrFixture
			}
			var n int64
			if tx.QueryRow(ctx, command).Scan(&n) != nil {
				return nil, ErrFixture
			}
			o.Rows = &n
		}
		out = append(out, o)
	}
	// Byte order, not the database collation, is the canonical order.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// SchemaBackend is implemented by collectors that also seal the database's
// schema inventory (ADR-20261020180000).
type SchemaBackend interface {
	FenceAndCaptureSchema(context.Context, rehearsalpg.Resource) ([]rehearsalpg.Snapshot, []rehearsalpg.SchemaObject, error)
}

func (p *Postgres) FenceAndCapture(ctx context.Context, r rehearsalpg.Resource) ([]rehearsalpg.Snapshot, error) {
	out, _, err := p.fenceAndCapture(ctx, r, false)
	return out, err
}

func (p *Postgres) FenceAndCaptureSchema(ctx context.Context, r rehearsalpg.Resource) ([]rehearsalpg.Snapshot, []rehearsalpg.SchemaObject, error) {
	return p.fenceAndCapture(ctx, r, true)
}

func (p *Postgres) fenceAndCapture(ctx context.Context, r rehearsalpg.Resource, withSchema bool) ([]rehearsalpg.Snapshot, []rehearsalpg.SchemaObject, error) {
	if !p.resourceNames(r) {
		return nil, nil, ErrFixture
	}
	admin, e := p.connect(ctx, "")
	if e != nil {
		return nil, nil, ErrFixture
	}
	defer func() { _ = admin.Close(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	if q.LockFixtureDatabase(ctx, admin, r.DatabaseName) != nil {
		return nil, nil, ErrFixture
	}
	// Authenticate the reader as the restricted fixture login before disabling
	// all new connections. Customer objects never execute as an administrator.
	password := make([]byte, 32)
	if _, e = rand.Read(password); e != nil {
		return nil, nil, ErrFixture
	}
	secret := hex.EncodeToString(password)
	command, e := q.FormatRotateRole(ctx, admin, sqlc.FormatRotateRoleParams{RoleName: r.RoleName, Password: secret})
	if execFormatted(ctx, admin, command, e) != nil {
		return nil, nil, ErrFixture
	}
	reader := p.config.Copy()
	reader.Database, reader.User, reader.Password = r.DatabaseName, r.RoleName, secret
	data, e := pgx.ConnectConfig(ctx, reader)
	if e != nil {
		return nil, nil, ErrFixture
	}
	defer func() { _ = data.Close(context.WithoutCancel(ctx)) }()
	command, e = q.FormatFreezeRole(ctx, admin, r.RoleName)
	if execFormatted(ctx, admin, command, e) != nil {
		return nil, nil, ErrFixture
	}
	command, e = q.FormatFreezeDatabase(ctx, admin, r.DatabaseName)
	if execFormatted(ctx, admin, command, e) != nil {
		return nil, nil, ErrFixture
	}
	params := sqlc.PendingSQLParams{DatabaseName: r.DatabaseName, CollectorPid: int32(data.PgConn().PID())}
	pending, e := q.PendingSQL(ctx, admin, params)
	if e != nil {
		return nil, nil, ErrFixture
	}
	if _, e = q.TerminateSessions(ctx, admin, sqlc.TerminateSessionsParams(params)); e != nil {
		return nil, nil, ErrFixture
	}
	for {
		count, e := q.OtherSessions(ctx, admin, sqlc.OtherSessionsParams(params))
		if e != nil {
			return nil, nil, ErrFixture
		}
		if count == 0 {
			break
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, nil, ErrFixture
		case <-timer.C:
		}
	}
	if pending > 0 {
		return nil, nil, ErrPending
	}
	return captureInventory(ctx, data, r.Spec, withSchema)
}

func (p *Postgres) LockWait(ctx context.Context, r rehearsalpg.Resource) (int64, error) {
	if !p.resourceNames(r) {
		return 0, ErrFixture
	}
	c, e := p.connect(ctx, "")
	if e != nil {
		return 0, ErrFixture
	}
	defer func() { _ = c.Close(context.WithoutCancel(ctx)) }()
	v, e := sqlc.New().SampleLockWait(ctx, c, r.DatabaseName)
	if e != nil {
		return 0, ErrFixture
	}
	return v, nil
}

func (p *Postgres) Drop(ctx context.Context, r rehearsalpg.Resource) error {
	if !p.resourceNames(r) {
		return ErrFixture
	}
	c, e := p.connect(ctx, "")
	if e != nil {
		return ErrFixture
	}
	defer func() { _ = c.Close(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	if q.LockFixtureDatabase(ctx, c, r.DatabaseName) != nil {
		return ErrFixture
	}
	participants, e := q.ParticipantRoles(ctx, c, r.RoleName)
	if e != nil || len(participants) > 15 {
		return ErrFixture
	}
	command, e := q.FormatDropDatabase(ctx, c, r.DatabaseName)
	if execFormatted(ctx, c, command, e) != nil {
		return ErrFixture
	}
	for _, role := range participants {
		command, e = q.FormatDropRole(ctx, c, role)
		if execFormatted(ctx, c, command, e) != nil {
			return ErrFixture
		}
		exists, err := q.RoleExists(ctx, c, role)
		if err != nil || exists {
			return ErrFixture
		}
	}
	command, e = q.FormatDropRole(ctx, c, r.RoleName)
	if execFormatted(ctx, c, command, e) != nil {
		return ErrFixture
	}
	db, e := q.DatabaseExists(ctx, c, r.DatabaseName)
	if e != nil || db {
		return ErrFixture
	}
	role, e := q.RoleExists(ctx, c, r.RoleName)
	if e != nil || role {
		return ErrFixture
	}
	return nil
}
