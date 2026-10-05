// adr: 590
package neon

import (
	"context"
	"errors"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectionfence"
)

type nativeConnectionClosureFixture struct {
	p           *Provider
	root        *pgx.Conn
	config      *pgx.ConnConfig
	definition  managedpostgres.RestoreSourceDefinition
	maintenance managedpostgres.CheckpointMaintenance
	request     managedpostgres.CheckpointConnectionRequest
	drift       atomic.Bool
	apiCalls    atomic.Int32
	poolCalls   int
}

func newNativeConnectionClosureFixture(t *testing.T) *nativeConnectionClosureFixture {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL required for native checkpoint adapter contracts")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	root, err := pgx.ConnectConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close(context.Background()) })
	pgtest.LockCluster(t, root, "managed-postgres-checkpoint-fixtures")
	var occupied bool
	if err := root.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname=$1) OR EXISTS (SELECT 1 FROM pg_database WHERE datname=$2)", maintenanceSourceRole, connectionfence.MaintenanceDatabase).Scan(&occupied); err != nil || occupied {
		t.Fatalf("native adapter contract needs unoccupied owned names: %v occupied=%v", err, occupied)
	}
	var major int
	if err := root.QueryRow(t.Context(), "SELECT current_setting('server_version_num')::int/10000").Scan(&major); err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	f := &nativeConnectionClosureFixture{root: root, config: cfg}
	f.request = managedpostgres.CheckpointConnectionRequest{CheckpointConnectionIdentity: managedpostgres.CheckpointConnectionIdentity{
		OwnerToken: uuid.NewString(), SourceResourceID: "project-source/br-source"}, DatabaseNames: []string{"cc_source_" + suffix, "cc_closed_" + suffix + "\";ü%"}}
	f.definition = managedpostgres.RestoreSourceDefinition{Spec: managedpostgres.Spec{Region: "eu-central-1", PostgresMajor: major,
		Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone},
		ProviderResourceID: "project-source", DataResourceID: f.request.SourceResourceID}
	createdDBs, createdRoles := []string{}, []string{}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, name := range createdDBs {
			if _, err := root.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
				t.Errorf("drop owned database: %v", err)
			}
		}
		for _, name := range createdRoles {
			if _, err := root.Exec(ctx, "DROP ROLE "+pgx.Identifier{name}.Sanitize()); err != nil {
				t.Errorf("drop owned role: %v", err)
			}
		}
	})
	if _, err := root.Exec(t.Context(), "CREATE ROLE gregale_owner LOGIN NOSUPERUSER CREATEDB CREATEROLE PASSWORD 'private-fixture-password'"); err != nil {
		t.Fatal(err)
	}
	createdRoles = append(createdRoles, maintenanceSourceRole)
	for _, name := range f.request.DatabaseNames {
		if _, err := root.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" OWNER gregale_owner TEMPLATE template0"); err != nil {
			t.Fatal(err)
		}
		createdDBs = append(createdDBs, name)
	}
	if _, err := root.Exec(t.Context(), "ALTER DATABASE "+pgx.Identifier{f.request.DatabaseNames[1]}.Sanitize()+" ALLOW_CONNECTIONS false"); err != nil {
		t.Fatal(err)
	}
	source := cfg.Copy()
	source.Database, source.User = f.request.DatabaseNames[0], maintenanceSourceRole
	source.Password = "private-fixture-password"
	conn, err := pgx.ConnectConfig(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	b, err := connectionfence.NewBootstrap(conn, connectionfence.BootstrapConfig{SourceDatabase: source.Database, SourceRole: source.User, SourcePostgresMajor: major})
	if err != nil {
		t.Fatal(err)
	}
	identity := connectionfence.Identity{OwnerToken: uuid.NewString(), SourceResourceID: f.request.SourceResourceID}
	receipt, err := b.Reserve(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	createdRoles = append(createdRoles, receipt.OwnerRole)
	receipt, err = b.Create(t.Context(), receipt)
	if err != nil {
		t.Fatal(err)
	}
	createdDBs = append(createdDBs, connectionfence.MaintenanceDatabase)
	receipt, err = b.Activate(t.Context(), receipt)
	if err != nil {
		t.Fatal(err)
	}
	f.maintenance = managedpostgres.CheckpointMaintenance{OwnerToken: receipt.OwnerToken, SourceResourceID: receipt.SourceResourceID,
		State: receipt.State, OwnerOID: receipt.OwnerOID, DatabaseOID: receipt.DatabaseOID}
	f.p = testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.apiCalls.Add(1)
		switch r.URL.Path {
		case "/api/v2/projects/project-source":
			writeResponse(t, w, http.StatusOK, projectResponse{Project: project{ID: "project-source",
				OrganizationID: "org-gregale-12345678", RegionID: "aws-eu-central-1", PostgresMajor: major}})
		case "/api/v2/projects/project-source/branches":
			writeResponse(t, w, http.StatusOK, branchesResponse{Branches: []branch{{ID: "br-source", ProjectID: "project-source", CurrentState: "ready"}}})
		case "/api/v2/projects/project-source/endpoints":
			disabled, host := false, "ep-source.example.test"
			if f.drift.Load() {
				host = "ep-source.changed.test"
			}
			writeResponse(t, w, http.StatusOK, endpointsResponse{Endpoints: []endpoint{{ID: "ep-source", ProjectID: "project-source",
				RegionID: "aws-eu-central-1", BranchID: "br-source", Host: host, Type: "read_write", CurrentState: "active", Disabled: &disabled}}})
		case "/api/v2/projects/project-source/operations":
			writeResponse(t, w, http.StatusOK, operationsResponse{})
		case "/api/v2/projects/project-source/connection_uri":
			q := r.URL.Query()
			if q.Get("branch_id") != "br-source" || q.Get("database_name") != connectionfence.MaintenanceDatabase || q.Get("role_name") != maintenanceSourceRole || q.Get("pooled") != "false" {
				t.Error("barrier fetched source/default/pooled credentials")
			}
			writeResponse(t, w, http.StatusOK, connectionURIResponse{URI: "postgres://gregale_owner:private-password@ep-source.example.test/gregale_checkpoint?sslmode=require&options=-c+role%3Devil"})
		default:
			t.Error("unexpected barrier API path")
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	f.p.databaseName = source.Database
	return f
}

func (f *nativeConnectionClosureFixture) connectPool(t *testing.T) func(context.Context, *pgxpool.Config) (*pgxpool.Pool, error) {
	t.Helper()
	return func(ctx context.Context, requested *pgxpool.Config) (*pgxpool.Pool, error) {
		f.poolCalls++
		if requested.ConnConfig.Database != connectionfence.MaintenanceDatabase || requested.ConnConfig.User != maintenanceSourceRole ||
			requested.ConnConfig.Host != "ep-source.example.test" || requested.ConnConfig.TLSConfig == nil || requested.ConnConfig.TLSConfig.InsecureSkipVerify ||
			len(requested.ConnConfig.RuntimeParams) != 1 || requested.MaxConns != 1 || requested.MinConns != 0 {
			t.Fatal("native barrier pool was not direct/private/bounded")
		}
		local := requested.Copy()
		local.ConnConfig = f.config.Copy()
		local.ConnConfig.Database, local.ConnConfig.User = connectionfence.MaintenanceDatabase, maintenanceSourceRole
		local.ConnConfig.Password = "private-fixture-password"
		return pgxpool.NewWithConfig(ctx, local)
	}
}

func (f *nativeConnectionClosureFixture) selectedFlags(t *testing.T, open bool) {
	t.Helper()
	for _, name := range f.request.DatabaseNames {
		var allows bool
		if err := f.root.QueryRow(t.Context(), "SELECT datallowconn FROM pg_database WHERE datname=$1", name).Scan(&allows); err != nil || allows != (open && name == f.request.DatabaseNames[0]) {
			t.Fatalf("selected original flag changed: %v allows=%v", err, allows)
		}
	}
}

func TestCheckpointConnectionClosureNativePipelineRecoversAndObservesDrain(t *testing.T) {
	f := newNativeConnectionClosureFixture(t)
	ctx := t.Context()
	clientConfig := f.config.Copy()
	clientConfig.Database, clientConfig.User = f.request.DatabaseNames[0], maintenanceSourceRole
	clientConfig.Password = "private-fixture-password"
	client, err := pgx.ConnectConfig(ctx, clientConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })
	if _, err := client.Exec(ctx, "CREATE TABLE continuing_writer (value integer)"); err != nil {
		t.Fatal(err)
	}
	closed, err := f.p.checkpointConnectionClosure(ctx, f.definition, f.maintenance, f.request, true, f.connectPool(t))
	if err != nil || closed.Validate(f.request) != nil || closed.Drained || len(closed.Databases) != 2 {
		t.Fatalf("admitted writer was reported drained: %+v %v", closed, err)
	}
	f.selectedFlags(t, false)
	if _, err := client.Exec(ctx, "INSERT INTO continuing_writer VALUES (1)"); err != nil {
		t.Fatalf("existing session could not write after admission closure: %v", err)
	}
	if fresh, err := pgx.ConnectConfig(ctx, clientConfig); err == nil {
		_ = fresh.Close(ctx)
		t.Fatal("new source session bypassed closure")
	}
	for _, closeAdmission := range []bool{false, true} {
		actual, err := f.p.checkpointConnectionClosure(ctx, f.definition, f.maintenance, f.request, closeAdmission, f.connectPool(t))
		if err != nil || actual.Drained || !actual.ClosedAt.Equal(closed.ClosedAt) || !reflect.DeepEqual(actual.Databases, closed.Databases) {
			t.Fatalf("replacement worker changed original closure: %+v %v", actual, err)
		}
	}
	if err := client.Close(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		actual, err := f.p.checkpointConnectionClosure(ctx, f.definition, f.maintenance, f.request, false, f.connectPool(t))
		if err != nil {
			t.Fatal(err)
		}
		if actual.Drained {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("closed client failed to drain")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, fault := range []string{"owner", "set"} {
		request := f.request
		if fault == "owner" {
			request.OwnerToken = uuid.NewString()
		} else {
			request.DatabaseNames = request.DatabaseNames[:1]
		}
		if actual, err := f.p.checkpointConnectionClosure(ctx, f.definition, f.maintenance, request, true, f.connectPool(t)); !errors.Is(err, managedpostgres.ErrConflict) || !reflect.DeepEqual(actual, managedpostgres.CheckpointConnectionClosure{}) {
			t.Fatalf("%s adopted original closure: %+v %v", fault, actual, err)
		}
	}
	_, err = f.p.withCheckpointConnectionController(ctx, f.definition, f.maintenance, f.request.CheckpointConnectionIdentity, f.connectPool(t),
		func(c *connectionfence.Controller) (connectionfence.Observation, error) {
			return c.Abandon(ctx, connectionfence.Identity{OwnerToken: f.request.OwnerToken, SourceResourceID: f.request.SourceResourceID})
		})
	if err != nil {
		t.Fatal(err)
	}
	f.selectedFlags(t, true)
	if actual, err := f.p.checkpointConnectionClosure(ctx, f.definition, f.maintenance, f.request, false, f.connectPool(t)); !errors.Is(err, managedpostgres.ErrConflict) || !reflect.DeepEqual(actual, managedpostgres.CheckpointConnectionClosure{}) {
		t.Fatalf("released admission supplied closed proof: %+v %v", actual, err)
	}
}

func TestCheckpointConnectionClosureNativeObservationCannotCreateOrRebaseBarrier(t *testing.T) {
	f := newNativeConnectionClosureFixture(t)
	ctx := t.Context()
	if actual, err := f.p.checkpointConnectionClosure(ctx, f.definition, f.maintenance, f.request, false, f.connectPool(t)); err == nil || !reflect.DeepEqual(actual, managedpostgres.CheckpointConnectionClosure{}) {
		t.Fatalf("missing ledger supplied closure: %+v %v", actual, err)
	}
	f.selectedFlags(t, true)
	config := f.config.Copy()
	config.Database = connectionfence.MaintenanceDatabase
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	var installed bool
	err = conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname='gregale_checkpoint')").Scan(&installed)
	_ = conn.Close(ctx)
	if err != nil || installed {
		t.Fatalf("read-only observation installed ledger: %v installed=%v", err, installed)
	}
	if _, err := f.p.checkpointConnectionClosure(ctx, f.definition, f.maintenance, f.request, true, f.connectPool(t)); err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"set", "owner"} {
		request := f.request
		want := managedpostgres.ErrConflict
		if fault == "set" {
			request.DatabaseNames = slices.Clone(request.DatabaseNames)
			request.DatabaseNames[0] = "not-selected"
		} else {
			request.OwnerToken = uuid.NewString()
			want = managedpostgres.ErrNotFound
		}
		if actual, err := f.p.checkpointConnectionClosure(ctx, f.definition, f.maintenance, request, false, f.connectPool(t)); !errors.Is(err, want) || !reflect.DeepEqual(actual, managedpostgres.CheckpointConnectionClosure{}) {
			t.Fatalf("read-only %s adopted ledger: %+v %v", fault, actual, err)
		}
		f.selectedFlags(t, false)
	}
}

func TestCheckpointConnectionControllerNativePostcheckFailuresRetainOriginalClosure(t *testing.T) {
	for _, fault := range []string{"placement", "maintenance", "cancel"} {
		t.Run(fault, func(t *testing.T) {
			f := newNativeConnectionClosureFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			ownerRole := "grg_ckpt_" + strings.ReplaceAll(f.maintenance.OwnerToken, "-", "")
			actual, err := f.p.withCheckpointConnectionController(ctx, f.definition, f.maintenance, f.request.CheckpointConnectionIdentity, f.connectPool(t),
				func(c *connectionfence.Controller) (connectionfence.Observation, error) {
					if err := c.Install(ctx); err != nil {
						return connectionfence.Observation{}, err
					}
					closed, err := c.Close(ctx, connectionfence.Request{Identity: connectionfence.Identity{OwnerToken: f.request.OwnerToken, SourceResourceID: f.request.SourceResourceID}, DatabaseNames: f.request.DatabaseNames})
					if err != nil {
						return connectionfence.Observation{}, err
					}
					switch fault {
					case "placement":
						f.drift.Store(true)
					case "maintenance":
						_, err = f.root.Exec(ctx, "ALTER ROLE "+pgx.Identifier{ownerRole}.Sanitize()+" LOGIN")
					case "cancel":
						cancel()
					}
					return closed, err
				})
			if err == nil || !reflect.DeepEqual(actual, connectionfence.Observation{}) {
				t.Fatalf("%s postcheck returned usable evidence: %+v %v", fault, actual, err)
			}
			f.selectedFlags(t, false)
			f.drift.Store(false)
			if fault == "maintenance" {
				if _, err := f.root.Exec(t.Context(), "ALTER ROLE "+pgx.Identifier{ownerRole}.Sanitize()+" NOLOGIN"); err != nil {
					t.Fatal(err)
				}
			}
			observed, err := f.p.checkpointConnectionClosure(t.Context(), f.definition, f.maintenance, f.request, false, f.connectPool(t))
			if err != nil || !observed.Drained || observed.Validate(f.request) != nil {
				t.Fatalf("lost reply could not recover same closed owner: %+v %v", observed, err)
			}
		})
	}
}

func TestCheckpointConnectionClosureRejectsInvalidAuthorityBeforeProviderIO(t *testing.T) {
	var calls atomic.Int32
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	d := managedpostgres.RestoreSourceDefinition{Spec: managedpostgres.Spec{Region: "eu-central-1", PostgresMajor: 16,
		Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone}, ProviderResourceID: "project-source", DataResourceID: "project-source/br-source"}
	m := managedpostgres.CheckpointMaintenance{OwnerToken: uuid.NewString(), SourceResourceID: d.DataResourceID, State: "ready", OwnerOID: 10001, DatabaseOID: 20002}
	r := managedpostgres.CheckpointConnectionRequest{CheckpointConnectionIdentity: managedpostgres.CheckpointConnectionIdentity{OwnerToken: uuid.NewString(), SourceResourceID: d.DataResourceID}, DatabaseNames: []string{"gregale"}}
	for _, fault := range []string{"owner", "same_owner", "source", "missing_oid", "state", "region", "lifecycle", "major", "empty", "duplicate", "private_database", "oversize_set", "nil_connector", "canceled"} {
		maintenance, request, definition := m, r, d
		request.DatabaseNames = slices.Clone(r.DatabaseNames)
		connect := pgxpool.NewWithConfig
		ctx, cancel := context.WithCancel(t.Context())
		switch fault {
		case "owner":
			request.OwnerToken = uuid.Nil.String()
		case "same_owner":
			request.OwnerToken = maintenance.OwnerToken
		case "source":
			request.SourceResourceID += "/other"
		case "missing_oid":
			maintenance.DatabaseOID = 0
		case "state":
			maintenance.State = "reserved"
		case "region":
			definition.Spec.Region = "us-east-1"
		case "lifecycle":
			definition.ProviderResourceID = "other-project"
		case "major":
			definition.Spec.PostgresMajor = 15
		case "empty":
			request.DatabaseNames = nil
		case "duplicate":
			request.DatabaseNames = []string{"gregale", "gregale"}
		case "private_database":
			request.DatabaseNames = []string{connectionfence.MaintenanceDatabase}
		case "oversize_set":
			request.DatabaseNames = make([]string, api.PostgresCheckpointDatabasesMax+1)
		case "nil_connector":
			connect = nil
		case "canceled":
			cancel()
		}
		for _, closeAdmission := range []bool{false, true} {
			if actual, err := p.checkpointConnectionClosure(ctx, definition, maintenance, request, closeAdmission, connect); err == nil || !reflect.DeepEqual(actual, managedpostgres.CheckpointConnectionClosure{}) {
				t.Fatalf("invalid %s produced evidence: %+v %v", fault, actual, err)
			}
		}
		cancel()
	}
	if calls.Load() != 0 {
		t.Fatal("invalid authority reached provider")
	}
}

func TestCheckpointConnectionControllerAuthenticatesNativePinsBeforeDispatch(t *testing.T) {
	f := newNativeConnectionClosureFixture(t)
	for _, fault := range []string{"owner_oid", "database_oid", "maintenance_owner", "major", "placement", "pool_error", "nil_pool"} {
		maintenance, definition := f.maintenance, f.definition
		connect := f.connectPool(t)
		var opened *pgxpool.Pool
		switch fault {
		case "owner_oid":
			maintenance.OwnerOID++
		case "database_oid":
			maintenance.DatabaseOID++
		case "maintenance_owner":
			maintenance.OwnerToken = uuid.NewString()
		case "major":
			definition.Spec.PostgresMajor++
		case "placement":
			original := connect
			connect = func(ctx context.Context, cfg *pgxpool.Config) (*pgxpool.Pool, error) {
				pool, err := original(ctx, cfg)
				f.drift.Store(true)
				return pool, err
			}
		case "pool_error":
			original := connect
			connect = func(ctx context.Context, cfg *pgxpool.Config) (*pgxpool.Pool, error) {
				var err error
				opened, err = original(ctx, cfg)
				if err != nil {
					return opened, err
				}
				return opened, errors.New("private credential should not escape")
			}
		case "nil_pool":
			connect = func(context.Context, *pgxpool.Config) (*pgxpool.Pool, error) { return nil, nil }
		}
		called := false
		actual, err := f.p.withCheckpointConnectionController(t.Context(), definition, maintenance, f.request.CheckpointConnectionIdentity, connect,
			func(*connectionfence.Controller) (connectionfence.Observation, error) {
				called = true
				return connectionfence.Observation{}, nil
			})
		f.drift.Store(false)
		if err == nil || called || !reflect.DeepEqual(actual, connectionfence.Observation{}) || strings.Contains(err.Error(), "private credential") {
			t.Fatalf("%s reached dispatch or leaked proof/credential: %+v %v", fault, actual, err)
		}
		if opened != nil && opened.Stat().TotalConns() != 0 {
			t.Fatal("failed connector retained private pool")
		}
		f.selectedFlags(t, true)
	}
}
