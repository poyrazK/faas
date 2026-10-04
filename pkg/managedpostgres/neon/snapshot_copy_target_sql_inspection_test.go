// adr: 569
package neon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestSnapshotCopyTargetSQLInspectionRejectsUnownedTopologyBeforeSQL(t *testing.T) {
	for _, fault := range []string{"unpinned", "source", "project", "parent", "passwordless", "missing_passwordless", "endpoint_id", "host", "endpoint_time", "future_endpoint", "endpoint_precision", "pending", "credential_host", "credential_database", "credential_role", "host_drift", "nil_connector"} {
		t.Run(fault, func(t *testing.T) {
			f := newTargetSQLFixture(t)
			r := f.request.Preparation
			x := &f.copy.endpoints[0]
			connects := 0
			connect := func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) {
				connects++
				return nil, errors.New("unqualified bootstrap connection")
			}
			switch fault {
			case "unpinned":
				r.ExpectedProviderResourceID, r.ExpectedCreatedAt = "", time.Time{}
			case "source":
				r.Capture.Snapshot.SourceResourceID = "project-other/br-root"
			case "project":
				f.copy.project.Name = "unowned-project"
			case "parent":
				f.copy.branches[0].ParentID = "br-source"
			case "passwordless":
				yes := true
				x.PasswordlessAccess = &yes
			case "missing_passwordless":
				x.PasswordlessAccess = nil
			case "endpoint_id":
				x.ID = "unknown-endpoint"
			case "host":
				x.Host = "ep-source.private.example"
			case "endpoint_time":
				x.CreatedAt = r.ExpectedCreatedAt.Add(-time.Second).Format(time.RFC3339Nano)
			case "future_endpoint":
				x.CreatedAt = time.Now().Add(time.Hour).Format(time.RFC3339Nano)
			case "endpoint_precision":
				x.CreatedAt = r.ExpectedCreatedAt.Add(time.Nanosecond).Format(time.RFC3339Nano)
			case "pending":
				x.PendingState = "updating"
			case "credential_host":
				f.uri = "postgres://gregale_owner:private-target-password@ep-source.private.example/gregale"
			case "credential_database":
				f.uri = "postgres://gregale_owner:private-target-password@" + x.Host + "/other"
			case "credential_role":
				f.uri = "postgres://other:private-target-password@" + x.Host + "/gregale"
			case "host_drift":
				f.hostDriftOnURI = true
			case "nil_connector":
				connect = nil
			}
			actual, err := f.copy.capture.p.inspectSnapshotCopyTargetSQL(t.Context(), f.copy.capture.definition, r, connect)
			if err == nil || actual != (managedpostgres.SnapshotCopyTargetSQLObservation{}) || connects != 0 || f.copy.posts+f.copy.capture.posts != 0 {
				t.Fatalf("unqualified bootstrap reached SQL or returned pins: %v", err)
			}
		})
	}
}

func localTargetSQLInspectionFixture(t *testing.T) (*targetSQLFixture, func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error), **pgx.Conn, *pgx.ConnConfig) {
	t.Helper()
	f, connect, opened, cfg := localTargetSQLFixtureForDatabase(t, "bootstrap_"+uuid.NewString()[:8])
	// The test provider's fixed provisioned database is the task-owned database.
	// No customer-selected name or OID enters the inspection request.
	f.copy.capture.p.databaseName = f.request.Target.DatabaseName
	u := url.URL{Scheme: "postgres", Host: f.copy.endpoints[0].Host, User: url.UserPassword(maintenanceSourceRole, "private-target-password"), Path: "/" + f.request.Target.DatabaseName,
		RawQuery: url.Values{"sslmode": {"require"}, "options": {"untrusted-startup"}, "hostaddr": {"203.0.113.9"}}.Encode()}
	f.uri = u.String()
	return f, connect, opened, cfg
}

func TestSnapshotCopyTargetSQLInspectionDiscoversRealOrdinaryOwnerPinsWithoutWrites(t *testing.T) {
	f, connect, opened, _ := localTargetSQLInspectionFixture(t)
	actual, err := f.copy.capture.p.inspectSnapshotCopyTargetSQL(t.Context(), f.copy.capture.definition, f.request.Preparation, connect)
	x := f.request.Target
	if err != nil || actual.ProviderResourceID != x.ProviderResourceID || actual.DataResourceID != x.DataResourceID || actual.EndpointID != x.EndpointID ||
		!actual.ProviderCreatedAt.Equal(x.ProviderCreatedAt) || !actual.EndpointCreatedAt.Equal(x.EndpointCreatedAt) || actual.Identity.DatabaseName != x.DatabaseName || actual.Identity.DatabaseOID != x.DatabaseOID ||
		actual.Identity.RoleName != x.RoleName || actual.Identity.RoleOID != x.RoleOID || *opened == nil || !(*opened).IsClosed() || f.uris != 1 || f.copy.posts+f.copy.capture.posts != 0 {
		t.Fatalf("owned bootstrap discovery: %v", err)
	}
	if err := actual.Validate(f.copy.capture.definition, f.request.Preparation); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(actual)
	for _, text := range []string{string(raw), fmt.Sprint(actual), fmt.Sprintf("%#v", actual)} {
		if strings.Contains(text, x.DatabaseName) || strings.Contains(text, x.RoleName) || strings.Contains(text, x.ProviderResourceID) {
			t.Fatal("bootstrap discovery exposed private pins")
		}
	}
}

func TestSnapshotCopyTargetSQLInspectionRejectsSQLAndPostConnectionPlacementDrift(t *testing.T) {
	for _, fault := range []string{"read_only", "transaction", "closed", "database", "role", "major", "host", "endpoint_time", "post_sql_read_only", "partial_connect_error", "nil_connection", "cancelled"} {
		t.Run(fault, func(t *testing.T) {
			f, original, opened, localCfg := localTargetSQLInspectionFixture(t)
			d := f.copy.capture.definition
			ctx := t.Context()
			want := error(managedpostgres.ErrConflict)
			if fault == "major" {
				d.Spec.PostgresMajor++
				f.copy.project.PostgresMajor = d.Spec.PostgresMajor
				f.copy.capture.definition = d
			}
			if fault == "partial_connect_error" {
				want = managedpostgres.ErrUnavailable
			}
			if fault == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			}
			if fault == "post_sql_read_only" {
				reads := 0
				p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/v2/projects/project-independent/endpoints" {
						reads++
						if reads == 3 {
							if *opened == nil {
								t.Error("post-SQL check preceded connection")
							} else if _, err := (*opened).Exec(r.Context(), "SET default_transaction_read_only=on"); err != nil {
								t.Error(err)
							}
						}
					}
					f.serveHTTP(w, r)
				}))
				p.databaseName = f.copy.capture.p.databaseName
				f.copy.capture.p = p
			}
			connect := func(ctx context.Context, cfg *pgx.ConnConfig) (*pgx.Conn, error) {
				if fault == "nil_connection" {
					return nil, nil
				}
				var conn *pgx.Conn
				var err error
				if fault == "database" || fault == "role" {
					local := localCfg.Copy()
					local.Database, local.User, local.Password = cfg.Database, cfg.User, cfg.Password
					local.RuntimeParams = cfg.RuntimeParams
					if fault == "database" {
						local.Database = localCfg.Database
					} else {
						local.User, local.Password = localCfg.User, localCfg.Password
					}
					conn, err = pgx.ConnectConfig(ctx, local)
					*opened = conn
				} else {
					conn, err = original(ctx, cfg)
				}
				if err != nil {
					return conn, err
				}
				switch fault {
				case "read_only":
					_, err = conn.Exec(ctx, "SET default_transaction_read_only=on")
				case "transaction":
					_, err = conn.Begin(ctx)
				case "closed":
					err = conn.Close(ctx)
				case "host":
					f.copy.endpoints[0].Host = "ep-independent.replacement.example"
				case "endpoint_time":
					f.copy.endpoints[0].CreatedAt = f.request.Target.EndpointCreatedAt.Add(time.Second).Format(time.RFC3339Nano)
				case "partial_connect_error":
					err = errors.New("private-target-password")
				}
				return conn, err
			}
			actual, err := f.copy.capture.p.inspectSnapshotCopyTargetSQL(ctx, d, f.request.Preparation, connect)
			if !errors.Is(err, want) || actual != (managedpostgres.SnapshotCopyTargetSQLObservation{}) || strings.Contains(err.Error(), "private-target-password") ||
				fault != "cancelled" && fault != "nil_connection" && (*opened == nil || !(*opened).IsClosed()) || f.copy.posts+f.copy.capture.posts != 0 {
				t.Fatalf("bootstrap SQL/placement drift accepted: %v", err)
			}
		})
	}
}
