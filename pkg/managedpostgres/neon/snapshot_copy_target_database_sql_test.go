// adr:531
package neon

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
)

type targetSQLFixture struct {
	copy           *snapshotCopyFixture
	request        managedpostgres.SnapshotCopyTargetDatabaseSQLRequest
	uri            string
	uris           int
	hostDriftOnURI bool
}

func newTargetSQLFixture(t *testing.T) *targetSQLFixture {
	t.Helper()
	f := &targetSQLFixture{copy: newSnapshotCopyFixture(t)}
	c := f.copy
	c.exists = true
	c.request.ResourceID = uuid.NewString()
	c.project.Name = c.capture.p.snapshotCopyProjectName(c.request.ResourceID)
	created, err := time.Parse(time.RFC3339Nano, c.project.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	c.request.ExpectedProviderResourceID, c.request.ExpectedCreatedAt = c.project.ID, created
	d := &c.capture.definition
	d.BackendID, d.BackendFingerprint = "private-neon", strings.Repeat("e", 64)
	scope := copyinventory.Scope{PostgresMajor: d.Spec.PostgresMajor, OperationID: uuid.NewString(), AccountID: uuid.NewString(), ProjectID: uuid.NewString(), SourceDatabaseID: uuid.NewString(), CaptureDatabaseID: uuid.NewString(),
		SourceVersion: strings.Repeat("a", 64), BackendID: d.BackendID, BackendFingerprint: d.BackendFingerprint, SourceProviderResourceID: d.ProviderResourceID, SourceDataResourceID: d.DataResourceID,
		ProviderSnapshotID: c.request.Capture.ProviderSnapshotID, CaptureProviderResourceID: c.request.Capture.ExpectedTargetResourceID, CapturePoint: c.request.Capture.Snapshot.PointInTime,
		SnapshotCreatedAt: c.request.SnapshotCreatedAt, CaptureCreatedAt: c.request.CaptureCreatedAt}
	f.request = managedpostgres.SnapshotCopyTargetDatabaseSQLRequest{Preparation: c.request, Target: copyarchive.RestoreTarget{Scope: scope, OwnerID: c.request.ResourceID, ProviderResourceID: c.project.ID, ProviderCreatedAt: created,
		DataResourceID: c.project.ID + "/br-independent", EndpointID: "ep-independent", EndpointCreatedAt: created, DatabaseName: "private_target", DatabaseOID: 42, RoleName: maintenanceSourceRole, RoleOID: 43}}
	x := &c.endpoints[0]
	x.CreatedAt = created.Format(time.RFC3339Nano)
	x.Host = x.ID + ".private.example"
	no := false
	x.PasswordlessAccess = &no
	f.uri = "postgres://gregale_owner:private-target-password@" + x.Host + "/gregale?sslmode=require&options=untrusted-startup&hostaddr=203.0.113.9"
	c.capture.p = testProvider(t, http.HandlerFunc(f.serveHTTP))
	if err := f.request.Validate(*d); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *targetSQLFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/v2/projects/project-independent/connection_uri" {
		f.uris++
		t := f.copy.capture.t
		if r.Method != http.MethodGet || r.URL.Query().Get("branch_id") != "br-independent" || r.URL.Query().Get("endpoint_id") != "ep-independent" ||
			r.URL.Query().Get("database_name") != f.copy.capture.p.databaseName || r.URL.Query().Get("role_name") != maintenanceSourceRole || r.URL.Query().Get("pooled") != "false" {
			t.Error("target credentials lost fixed owned branch/endpoint/bootstrap selectors")
		}
		writeResponse(t, w, http.StatusOK, connectionURIResponse{URI: f.uri})
		if f.hostDriftOnURI {
			f.copy.endpoints[0].Host = "ep-independent.replacement.example"
		}
		return
	}
	f.copy.serveHTTP(w, r)
}

func TestSnapshotCopyTargetDatabaseSQLRejectsPlacementAndCredentialDriftBeforeConnection(t *testing.T) {
	for _, fault := range []string{"scope", "unpinned", "data_branch", "endpoint_id", "endpoint_time", "readonly", "disabled", "passwordless", "missing_passwordless", "host", "pending", "project", "parent", "not_ready", "credential_host", "credential_role", "credential_database", "host_drift", "nil_callback", "connect_error"} {
		t.Run(fault, func(t *testing.T) {
			f := newTargetSQLFixture(t)
			r := f.request
			x := &f.copy.endpoints[0]
			run := managedpostgres.SnapshotCopyTargetSQLRun(func(context.Context, *pgx.Conn, managedpostgres.SnapshotCopyTargetSQLIdentity) error {
				t.Fatal("unqualified target write callback")
				return nil
			})
			switch fault {
			case "scope":
				r.Target.Scope.BackendFingerprint = strings.Repeat("f", 64)
			case "unpinned":
				r.Preparation.ExpectedProviderResourceID = ""
				r.Preparation.ExpectedCreatedAt = time.Time{}
			case "data_branch":
				r.Target.DataResourceID = "project-independent/br-other"
			case "endpoint_id":
				r.Target.EndpointID = "ep-other"
			case "endpoint_time":
				r.Target.EndpointCreatedAt = r.Target.EndpointCreatedAt.Add(time.Second)
			case "readonly":
				x.Type = "read_only"
			case "disabled":
				yes := true
				x.Disabled = &yes
			case "passwordless":
				yes := true
				x.PasswordlessAccess = &yes
			case "missing_passwordless":
				x.PasswordlessAccess = nil
			case "host":
				x.Host = "ep-source.private.example"
			case "pending":
				x.PendingState = "updating"
			case "project":
				f.copy.project.Name = "foreign-owner"
			case "parent":
				f.copy.branches[0].ParentID = "br-source"
			case "not_ready":
				f.copy.ops[0].Status = "running"
			case "credential_host":
				f.uri = "postgres://gregale_owner:private-target-password@ep-source.private.example/gregale?sslmode=require"
			case "credential_role":
				f.uri = "postgres://other:private-target-password@" + x.Host + "/gregale?sslmode=require"
			case "credential_database":
				f.uri = "postgres://gregale_owner:private-target-password@" + x.Host + "/other?sslmode=require"
			case "host_drift":
				f.hostDriftOnURI = true
			case "nil_callback":
				run = nil
			}
			connections := 0
			err := f.copy.capture.p.withSnapshotCopyTargetDatabaseSQL(t.Context(), f.copy.capture.definition, r, run, func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) {
				connections++
				return nil, errors.New("private-target-password")
			})
			if err == nil || strings.Contains(err.Error(), "private-target-password") || fault != "connect_error" && connections != 0 || f.copy.posts != 0 || f.copy.capture.posts != 0 {
				t.Fatalf("unqualified target connection/mutation: %v", err)
			}
		})
	}
}

func localTargetSQLFixture(t *testing.T) (*targetSQLFixture, func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error), **pgx.Conn, *pgx.ConnConfig) {
	return localTargetSQLFixtureForDatabase(t, "target /?%&数据库\n"+uuid.NewString()[:8])
}

func localTargetSQLFixtureForDatabase(t *testing.T, name string) (*targetSQLFixture, func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error), **pgx.Conn, *pgx.ConnConfig) {
	t.Helper()
	admin, cfg, major := localReaderSQLConnection(t)
	if _, err := admin.Exec(t.Context(), "SET default_transaction_read_only=off"); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := admin.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", maintenanceSourceRole).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		if _, err := admin.Exec(t.Context(), "CREATE ROLE "+pgx.Identifier{maintenanceSourceRole}.Sanitize()+" LOGIN PASSWORD 'private-target-password'"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := admin.Exec(context.Background(), "DROP ROLE "+pgx.Identifier{maintenanceSourceRole}.Sanitize()); err != nil {
				t.Error(err)
			}
		})
	}
	if _, err := admin.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" OWNER "+pgx.Identifier{maintenanceSourceRole}.Sanitize()+" TEMPLATE template0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	f := newTargetSQLFixture(t)
	f.copy.capture.definition.Spec.PostgresMajor = major
	f.copy.project.PostgresMajor = major
	f.request.Target.Scope.PostgresMajor = major
	f.request.Target.DatabaseName = name
	if err := admin.QueryRow(t.Context(), "SELECT d.oid,r.oid FROM pg_database d,pg_roles r WHERE d.datname=$1 AND r.rolname=$2", name, maintenanceSourceRole).Scan(&f.request.Target.DatabaseOID, &f.request.Target.RoleOID); err != nil {
		t.Fatal(err)
	}
	opened := new(*pgx.Conn)
	connect := func(ctx context.Context, actual *pgx.ConnConfig) (*pgx.Conn, error) {
		if actual.Database != name || actual.User != maintenanceSourceRole || actual.Host != f.copy.endpoints[0].Host || actual.Password != "private-target-password" ||
			actual.TLSConfig == nil || actual.TLSConfig.InsecureSkipVerify || actual.TLSConfig.ServerName != actual.Host || len(actual.Fallbacks) != 0 ||
			actual.RuntimeParams["default_transaction_read_only"] != "off" || actual.RuntimeParams["search_path"] != "pg_catalog" || len(actual.RuntimeParams) != 3 {
			t.Fatal("target connection changed literal selection, trust, credentials or controlled startup")
		}
		// Provider HTTP and credentials are mocked. SQL role/database/OID checks
		// run on an isolated local socket; remote Neon placement is not qualified.
		local := cfg.Copy()
		local.Database, local.User, local.Password = actual.Database, actual.User, actual.Password
		local.RuntimeParams = actual.RuntimeParams
		var err error
		*opened, err = pgx.ConnectConfig(ctx, local)
		return *opened, err
	}
	return f, connect, opened, cfg
}

func TestSnapshotCopyTargetDatabaseSQLAuthenticatesLiteralSelectionWritesAndClosesBorrow(t *testing.T) {
	f, connect, opened, cfg := localTargetSQLFixture(t)
	calls := 0
	err := f.copy.capture.p.withSnapshotCopyTargetDatabaseSQL(t.Context(), f.copy.capture.definition, f.request, func(ctx context.Context, conn *pgx.Conn, i managedpostgres.SnapshotCopyTargetSQLIdentity) error {
		calls++
		if i.DatabaseName != f.request.Target.DatabaseName || i.RoleName != maintenanceSourceRole || i.DatabaseOID != f.request.Target.DatabaseOID || i.RoleOID != f.request.Target.RoleOID {
			t.Fatal("target SQL pins changed")
		}
		var ordinary bool
		if err := conn.QueryRow(ctx, "SELECT NOT rolsuper FROM pg_roles WHERE rolname=current_user").Scan(&ordinary); err != nil || !ordinary {
			t.Fatalf("borrow depends on superuser: %v", err)
		}
		_, err := conn.Exec(ctx, "CREATE TABLE public.stage_probe(id int PRIMARY KEY,payload jsonb); INSERT INTO public.stage_probe VALUES (1,'{\"copy\":\"isolated\"}'); SET search_path=public")
		return err
	}, connect)
	if err != nil || calls != 1 || *opened == nil || !(*opened).IsClosed() || f.uris != 1 || f.copy.posts+f.copy.capture.posts != 0 {
		t.Fatalf("target SQL write borrow: %v", err)
	}
	selected := cfg.Copy()
	selected.Database, selected.User = f.request.Target.DatabaseName, maintenanceSourceRole
	selected.Password = "private-target-password"
	selected.RuntimeParams = map[string]string{"default_transaction_read_only": "on"}
	conn, err := pgx.ConnectConfig(t.Context(), selected)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var copied bool
	if err := conn.QueryRow(t.Context(), "SELECT payload->>'copy'='isolated' FROM public.stage_probe WHERE id=1").Scan(&copied); err != nil || !copied {
		t.Fatalf("owned target write not retained: %v", err)
	}
}

func TestSnapshotCopyTargetDatabaseSQLRejectsIdentityAndPostWritePlacementDrift(t *testing.T) {
	for _, fault := range []string{"database_oid", "role_oid", "read_only", "transaction", "host", "endpoint_time", "callback", "closed", "post_connect_host", "partial_connect_error", "nil_connection"} {
		t.Run(fault, func(t *testing.T) {
			f, connect, opened, _ := localTargetSQLFixture(t)
			r := f.request
			want := error(managedpostgres.ErrConflict)
			calls := 0
			switch fault {
			case "database_oid":
				r.Target.DatabaseOID++
			case "role_oid":
				r.Target.RoleOID++
			case "callback":
				want = errors.New("private import callback unavailable")
			case "partial_connect_error":
				want = managedpostgres.ErrUnavailable
			}
			original := connect
			if fault == "post_connect_host" || fault == "partial_connect_error" || fault == "nil_connection" {
				connect = func(ctx context.Context, cfg *pgx.ConnConfig) (*pgx.Conn, error) {
					if fault == "nil_connection" {
						return nil, nil
					}
					conn, err := original(ctx, cfg)
					if err != nil {
						return conn, err
					}
					if fault == "post_connect_host" {
						f.copy.endpoints[0].Host = "ep-independent.replacement.example"
						return conn, nil
					}
					return conn, errors.New("private-target-password")
				}
			}
			err := f.copy.capture.p.withSnapshotCopyTargetDatabaseSQL(t.Context(), f.copy.capture.definition, r, func(ctx context.Context, conn *pgx.Conn, _ managedpostgres.SnapshotCopyTargetSQLIdentity) error {
				calls++
				switch fault {
				case "read_only":
					_, err := conn.Exec(ctx, "SET default_transaction_read_only=on")
					return err
				case "transaction":
					_, err := conn.Begin(ctx)
					return err
				case "host":
					f.copy.endpoints[0].Host = "ep-independent.replacement.example"
				case "endpoint_time":
					f.copy.endpoints[0].CreatedAt = r.Target.EndpointCreatedAt.Add(time.Second).Format(time.RFC3339Nano)
				case "callback":
					return want
				case "closed":
					return conn.Close(ctx)
				}
				return nil
			}, connect)
			pre := fault == "database_oid" || fault == "role_oid" || fault == "post_connect_host" || fault == "partial_connect_error" || fault == "nil_connection"
			if !errors.Is(err, want) || strings.Contains(err.Error(), "private-target-password") || fault != "nil_connection" && (*opened == nil || !(*opened).IsClosed()) || pre && calls != 0 || f.copy.posts+f.copy.capture.posts != 0 {
				t.Fatalf("target SQL identity/placement drift accepted: %v", err)
			}
		})
	}
}
