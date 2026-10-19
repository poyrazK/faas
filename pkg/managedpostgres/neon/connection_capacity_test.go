package neon

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestConnectionCapacityPGPinsDirectEndpointAndRejectsDrift(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL required for native capacity adapter contracts")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	root, err := pgx.ConnectConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close(context.Background()) })
	pgtest.LockCluster(t, root, "managed-postgres-checkpoint-fixtures")
	if _, err := root.Exec(t.Context(), `CREATE ROLE gregale_owner LOGIN NOSUPERUSER CONNECTION LIMIT 7 PASSWORD 'capacity-fixture-password'`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := root.Exec(context.Background(), `DROP ROLE gregale_owner`); err != nil {
			t.Error(err)
		}
	})
	var major int
	if err := root.QueryRow(t.Context(), `SELECT current_setting('server_version_num')::int/10000`).Scan(&major); err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"", "idle", "moving", "connect_failure", "endpoint_drift", "wrong_dataset"} {
		t.Run(fault, func(t *testing.T) {
			var drift atomic.Bool
			p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("capacity observation mutated provider")
				}
				switch r.URL.Path {
				case "/api/v2/projects/project-source":
					writeResponse(t, w, http.StatusOK, projectResponse{Project: project{ID: "project-source", OrganizationID: "org-gregale-12345678", RegionID: "aws-eu-central-1", PostgresMajor: major}})
				case "/api/v2/projects/project-source/branches":
					writeResponse(t, w, http.StatusOK, branchesResponse{Branches: []branch{{ID: "br-source", ProjectID: "project-source", CurrentState: "ready"}}})
				case "/api/v2/projects/project-source/endpoints":
					disabled := false
					value := endpoint{ID: "ep-source", Host: "ep-source.example.test", ProjectID: "project-source", RegionID: "aws-eu-central-1", BranchID: "br-source", Type: "read_write", CurrentState: "active", Disabled: &disabled}
					if fault == "idle" {
						value.CurrentState = "idle"
					}
					if fault == "moving" {
						value.PendingState = "idle"
					}
					if drift.Load() {
						value.ID, value.Host = "ep-replaced", "ep-replaced.example.test"
					}
					writeResponse(t, w, http.StatusOK, endpointsResponse{Endpoints: []endpoint{value}})
				case "/api/v2/projects/project-source/operations":
					writeResponse(t, w, http.StatusOK, operationsResponse{Operations: []operation{}})
				case "/api/v2/projects/project-source/connection_uri":
					q := r.URL.Query()
					if q.Get("branch_id") != "br-source" || q.Get("pooled") != "false" || q.Get("role_name") != maintenanceSourceRole || q.Get("database_name") != config.Database {
						t.Error("capacity observer used an alias, pool or incorrect database/role")
					}
					writeResponse(t, w, http.StatusOK, connectionURIResponse{URI: "postgres://gregale_owner:private-password@ep-source.example.test/" + config.Database + "?sslmode=require&options=-c+role%3Devil"})
				default:
					t.Error("unexpected capacity API path")
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			p.databaseName = config.Database
			d := managedpostgres.Database{ProviderResourceID: "project-source", DataResourceID: "project-source/br-source",
				Spec: managedpostgres.Spec{Region: p.logicalRegion, PostgresMajor: major, Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone}}
			if fault == "wrong_dataset" {
				d.DataResourceID = "project-other/br-source"
			}
			connections := 0
			observed, err := p.observeConnectionCapacity(t.Context(), d, func(ctx context.Context, requested *pgx.ConnConfig) (*pgx.Conn, error) {
				connections++
				if requested.Database != config.Database || requested.User != maintenanceSourceRole || requested.Host != "ep-source.example.test" ||
					requested.TLSConfig == nil || requested.TLSConfig.InsecureSkipVerify || len(requested.RuntimeParams) != 1 || requested.RuntimeParams["application_name"] != "gregale-capacity-observer" {
					t.Fatal("SQL connection was not pinned and sanitized")
				}
				if fault == "connect_failure" {
					return nil, errors.New("private-provider private-password")
				}
				if fault == "endpoint_drift" {
					drift.Store(true)
				}
				local := config.Copy()
				local.User, local.Password = maintenanceSourceRole, "capacity-fixture-password"
				return pgx.ConnectConfig(ctx, local)
			})
			if fault == "" {
				if err != nil || !observed.Valid() || observed.RoleLimit != 7 || connections != 1 {
					t.Fatal(observed, err, connections)
				}
			} else if err == nil || observed.Valid() || strings.Contains(err.Error(), "private") {
				t.Fatal("unsafe observation accepted or leaked", observed, err)
			}
			if (fault == "idle" || fault == "moving" || fault == "wrong_dataset") && connections != 0 {
				t.Fatal("unqualified compute reached SQL")
			}
		})
	}
}
