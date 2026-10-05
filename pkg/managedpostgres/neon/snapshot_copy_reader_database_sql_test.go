// adr: 590
package neon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
)

func selectedReaderSQLRequest(t *testing.T, f *snapshotReaderFixture) managedpostgres.SnapshotCopyReaderDatabaseSQLRequest {
	t.Helper()
	f.rows = []endpoint{f.owned()}
	f.request.ExpectedEndpointID, f.request.ExpectedCreatedAt = "ep-owned", f.request.CaptureCreatedAt.Add(time.Minute)
	d := &f.base.definition
	d.BackendID, d.BackendFingerprint = "private-neon", strings.Repeat("e", 64)
	scope := copyinventory.Scope{PostgresMajor: d.Spec.PostgresMajor, OperationID: uuid.NewString(), AccountID: uuid.NewString(), ProjectID: uuid.NewString(),
		SourceDatabaseID: uuid.NewString(), CaptureDatabaseID: uuid.NewString(), SourceVersion: strings.Repeat("a", 64), BackendID: d.BackendID, BackendFingerprint: d.BackendFingerprint,
		SourceProviderResourceID: d.ProviderResourceID, SourceDataResourceID: d.DataResourceID, ProviderSnapshotID: f.request.Capture.ProviderSnapshotID,
		CaptureProviderResourceID: f.request.Capture.ExpectedTargetResourceID, CapturePoint: f.request.Capture.Snapshot.PointInTime,
		SnapshotCreatedAt: f.request.SnapshotCreatedAt, CaptureCreatedAt: f.request.CaptureCreatedAt}
	x := copyinventory.DatabaseExport{Scope: scope, InventoryFingerprint: strings.Repeat("b", 64), Database: copyinventory.Database{OID: 42, Name: "private /?%数据库\n"},
		CapturedAllowConnections: true, AuthenticatedReaderRoleOID: 43}
	r := managedpostgres.SnapshotCopyReaderDatabaseSQLRequest{Reader: f.request, Database: x}
	if err := r.Validate(*d); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSnapshotCopyReaderDatabaseSQLRejectsSelectionAndPlacementBeforeConnecting(t *testing.T) {
	for _, fault := range []string{"scope", "closed", "unpinned", "host_drift", "credential_host", "connect_error"} {
		t.Run(fault, func(t *testing.T) {
			f := newSnapshotReaderFixture(t)
			r := selectedReaderSQLRequest(t, f)
			switch fault {
			case "scope":
				r.Database.Scope.CaptureProviderResourceID = "project-source/br-other"
			case "closed":
				r.Database.CapturedAllowConnections = false
			case "unpinned":
				r.Reader.ExpectedCreatedAt = time.Time{}
			case "host_drift":
				f.fault = "host_drift"
			case "credential_host":
				f.uri = "postgres://gregale_owner:reader-secret@ep-source.neon.tech/gregale_checkpoint?sslmode=require"
			}
			connections := 0
			err := f.base.p.withSnapshotCopyReaderDatabaseSQL(t.Context(), f.base.definition, r,
				func(context.Context, *pgx.Conn, managedpostgres.SnapshotCopyReaderSQLIdentity) error {
					t.Fatal("unqualified selected callback")
					return nil
				},
				func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) {
					connections++
					return nil, errors.New("reader-secret")
				})
			if err == nil || strings.Contains(err.Error(), "reader-secret") || fault != "connect_error" && connections != 0 || f.posts != 0 {
				t.Fatalf("selected placement/connection boundary: %v", err)
			}
		})
	}
}

func localSelectedReaderSQLFixture(t *testing.T) (*snapshotReaderFixture, managedpostgres.SnapshotCopyReaderDatabaseSQLRequest, func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error), **pgx.Conn) {
	t.Helper()
	admin, cfg, major := localReaderSQLConnection(t)
	// Only the task-owned fixture administrator creates disposable resources.
	// The selected borrowed connection retains readonly startup settings.
	if _, err := admin.Exec(t.Context(), "set default_transaction_read_only=off"); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := admin.QueryRow(t.Context(), "select exists(select 1 from pg_catalog.pg_roles where rolname=$1)", maintenanceSourceRole).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		if _, err := admin.Exec(t.Context(), "create role "+pgx.Identifier{maintenanceSourceRole}.Sanitize()+" login password 'reader-private-password'"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := admin.Exec(context.Background(), "drop role "+pgx.Identifier{maintenanceSourceRole}.Sanitize()); err != nil {
				t.Error(err)
			}
		})
	}
	name := "copy /?%&数据库\n" + uuid.NewString()[:8]
	if _, err := admin.Exec(t.Context(), "create database "+pgx.Identifier{name}.Sanitize()+" owner "+pgx.Identifier{maintenanceSourceRole}.Sanitize()+" template template0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "drop database "+pgx.Identifier{name}.Sanitize()+" with (force)"); err != nil {
			t.Error(err)
		}
	})
	f := newSnapshotReaderFixture(t)
	f.base.definition.Spec.PostgresMajor = major
	r := selectedReaderSQLRequest(t, f)
	r.Database.Database.Name = name
	var databaseOID, roleOID int64
	if err := admin.QueryRow(t.Context(), "select oid::bigint from pg_catalog.pg_database where datname=$1", name).Scan(&databaseOID); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(t.Context(), "select oid::bigint from pg_catalog.pg_roles where rolname=$1", maintenanceSourceRole).Scan(&roleOID); err != nil {
		t.Fatal(err)
	}
	r.Database.Database.OID, r.Database.AuthenticatedReaderRoleOID = uint32(databaseOID), uint32(roleOID)
	opened := new(*pgx.Conn)
	connect := func(ctx context.Context, actual *pgx.ConnConfig) (*pgx.Conn, error) {
		if actual.Database != name || actual.User != maintenanceSourceRole || actual.Host != f.owned().Host || actual.TLSConfig == nil || actual.TLSConfig.InsecureSkipVerify ||
			actual.RuntimeParams["default_transaction_read_only"] != "on" || actual.RuntimeParams["search_path"] != "pg_catalog" {
			t.Fatal("selected SQL changed literal name, endpoint, role, trust or readonly startup")
		}
		// HTTP metadata/credentials are mocked; actual SQL authentication runs
		// against the fixture server using the requested role, password and database.
		local := cfg.Copy()
		local.Database, local.User, local.Password = actual.Database, actual.User, actual.Password
		local.RuntimeParams = actual.RuntimeParams
		var err error
		*opened, err = pgx.ConnectConfig(ctx, local)
		return *opened, err
	}
	return f, r, connect, opened
}

func TestSnapshotCopyReaderDatabaseSQLAuthenticatesLiteralLocalSelectionAndClosesBorrow(t *testing.T) {
	f, r, connect, opened := localSelectedReaderSQLFixture(t)
	calls := 0
	err := f.base.p.withSnapshotCopyReaderDatabaseSQL(t.Context(), f.base.definition, r,
		func(ctx context.Context, conn *pgx.Conn, id managedpostgres.SnapshotCopyReaderSQLIdentity) error {
			calls++
			if id.DatabaseName != r.Database.Database.Name || id.DatabaseOID != r.Database.Database.OID || id.RoleOID != r.Database.AuthenticatedReaderRoleOID {
				t.Fatal("selected SQL identity differs from retained pins")
			}
			_, err := conn.Exec(ctx, "set search_path=public")
			return err
		}, connect)
	if err != nil || calls != 1 || *opened == nil || !(*opened).IsClosed() || f.posts != 0 || f.uris != 1 {
		t.Fatalf("literal selected SQL borrow: %v", err)
	}
}

func TestSnapshotCopyReaderDatabaseSQLRejectsOIDSubstitutionAndPostReadDrift(t *testing.T) {
	for _, fault := range []string{"database_oid", "role_oid", "read_write", "transaction", "host", "callback"} {
		t.Run(fault, func(t *testing.T) {
			f, r, connect, opened := localSelectedReaderSQLFixture(t)
			want := managedpostgres.ErrConflict
			switch fault {
			case "database_oid":
				r.Database.Database.OID++
			case "role_oid":
				r.Database.AuthenticatedReaderRoleOID++
			case "callback":
				want = errors.New("private dump failure")
			}
			calls := 0
			err := f.base.p.withSnapshotCopyReaderDatabaseSQL(t.Context(), f.base.definition, r,
				func(ctx context.Context, conn *pgx.Conn, _ managedpostgres.SnapshotCopyReaderSQLIdentity) error {
					calls++
					switch fault {
					case "read_write":
						_, err := conn.Exec(ctx, "set default_transaction_read_only=off")
						return err
					case "transaction":
						_, err := conn.Begin(ctx)
						return err
					case "host":
						f.rows[0].Host = "ep-owned.replacement.neon.tech"
					case "callback":
						return want
					}
					return nil
				}, connect)
			if !errors.Is(err, want) || *opened == nil || !(*opened).IsClosed() || f.posts != 0 ||
				(fault == "database_oid" || fault == "role_oid") && calls != 0 {
				t.Fatalf("selected SQL drift accepted or connection leaked: %v", err)
			}
		})
	}
}
