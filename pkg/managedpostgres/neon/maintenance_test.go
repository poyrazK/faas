// adr:375
package neon

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectionfence"
)

func TestMaintenanceConnectionPinsExactProviderPlacement(t *testing.T) {
	for _, fault := range []string{"", "project", "organization", "region", "postgres_major", "unsupported_major", "branch_project", "endpoint_project", "endpoint_region", "endpoint_branch", "duplicate_endpoint", "disabled", "missing_disabled", "pending_operation", "operation_pagination", "uri_role", "uri_database", "uri_host", "uri_port", "uri_tls", "changed_host", "changed_endpoint", "changed_major", "cancelled"} {
		t.Run(fault, func(t *testing.T) {
			var uriRead atomic.Bool
			p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("placement qualification mutated provider: %s", r.Method)
				}
				switch r.URL.Path {
				case "/api/v2/projects/project-source":
					value := project{ID: "project-source", OrganizationID: "org-gregale-12345678", RegionID: "aws-eu-central-1", PostgresMajor: 16}
					switch fault {
					case "project":
						value.ID = "project-other"
					case "organization":
						value.OrganizationID = "org-other"
					case "region":
						value.RegionID = "aws-us-east-1"
					case "postgres_major":
						value.PostgresMajor = 15
					case "unsupported_major":
						value.PostgresMajor = 99
					case "changed_major":
						if uriRead.Load() {
							value.PostgresMajor = 17
						}
					}
					writeResponse(t, w, http.StatusOK, projectResponse{Project: value})
				case "/api/v2/projects/project-source/branches":
					value := branch{ID: "br-frozen-source", ProjectID: "project-source", CurrentState: "ready"}
					if fault == "branch_project" {
						value.ProjectID = "project-other"
					}
					writeResponse(t, w, http.StatusOK, branchesResponse{Branches: []branch{value,
						{ID: "br-new-default", ProjectID: "project-source", CurrentState: "ready", Default: true}}})
				case "/api/v2/projects/project-source/endpoints":
					disabled := false
					value := endpoint{ID: "ep-frozen-source", ProjectID: "project-source", RegionID: "aws-eu-central-1",
						BranchID: "br-frozen-source", Host: "ep-frozen-source.example.test", Type: "read_write", CurrentState: "idle", Disabled: &disabled}
					switch fault {
					case "endpoint_project":
						value.ProjectID = "project-other"
					case "endpoint_region":
						value.RegionID = "aws-us-east-1"
					case "endpoint_branch":
						value.BranchID = "br-new-default"
					case "disabled":
						disabled = true
					case "missing_disabled":
						value.Disabled = nil
					case "changed_host":
						if uriRead.Load() {
							value.Host = "ep-replaced.example.test"
						}
					case "changed_endpoint":
						if uriRead.Load() {
							value.ID = "ep-replaced-source"
						}
					}
					values := []endpoint{value}
					if fault == "duplicate_endpoint" {
						values = append(values, value)
					}
					writeResponse(t, w, http.StatusOK, endpointsResponse{Endpoints: values})
				case "/api/v2/projects/project-source/operations":
					value := operationsResponse{}
					if fault == "pending_operation" {
						value.Operations = []operation{{ID: "operation-pending", Status: "running"}}
					}
					if fault == "operation_pagination" {
						value.Pagination.Cursor = "more-operations"
					}
					writeResponse(t, w, http.StatusOK, value)
				case "/api/v2/projects/project-source/connection_uri":
					query := r.URL.Query()
					if query.Get("branch_id") != "br-frozen-source" || query.Get("role_name") != maintenanceSourceRole ||
						query.Get("database_name") != "gregale" || query.Get("pooled") != "false" {
						t.Errorf("maintenance requested an unfenced/default/pooled identity")
					}
					uri := "postgresql://gregale_owner:private-password@ep-frozen-source.example.test/gregale?sslmode=require&options=-c+role%3Devil&application_name=evil&replication=database"
					switch fault {
					case "uri_role":
						uri = strings.Replace(uri, "gregale_owner:", "application_role:", 1)
					case "uri_database":
						uri = strings.Replace(uri, "/gregale?", "/other?", 1)
					case "uri_host":
						uri = strings.Replace(uri, "ep-frozen-source.example.test", "ep-other.example.test", 1)
					case "uri_port":
						uri = strings.Replace(uri, "example.test/", "example.test:6432/", 1)
					case "uri_tls":
						uri = strings.Replace(uri, "sslmode=require", "sslmode=disable", 1)
					}
					uriRead.Store(true)
					writeResponse(t, w, http.StatusOK, connectionURIResponse{URI: uri})
				default:
					t.Errorf("unexpected maintenance API path: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			ctx := t.Context()
			if fault == "cancelled" {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			config, err := p.maintenanceConnectionConfig(ctx, connectionfence.Identity{OwnerToken: uuid.NewString(), SourceResourceID: "project-source/br-frozen-source"})
			if fault != "" {
				want := managedpostgres.ErrConflict
				if fault == "postgres_major" || fault == "unsupported_major" {
					want = managedpostgres.ErrUnsupported
				} else if fault == "cancelled" {
					want = context.Canceled
				}
				if !errors.Is(err, want) || config != nil {
					t.Fatalf("%s accepted substituted maintenance placement: %v", fault, err)
				}
				if strings.Contains(err.Error(), "private-password") || strings.Contains(err.Error(), "postgresql://") {
					t.Fatal("maintenance error exposed credentials")
				}
				return
			}
			if err != nil || config == nil {
				t.Fatalf("exact source connection: %v", err)
			}
			if config.PostgresMajor != 16 || config.Host != "ep-frozen-source.example.test" || config.Port != 5432 || config.Database != "gregale" || config.User != maintenanceSourceRole ||
				config.Password != "private-password" || config.TLSConfig == nil || config.TLSConfig.InsecureSkipVerify ||
				config.TLSConfig.ServerName != config.Host || len(config.Fallbacks) != 0 || len(config.RuntimeParams) != 1 ||
				config.RuntimeParams["application_name"] != "gregale-maintenance-bootstrap" {
				t.Fatal("maintenance connection lost exact direct identity, verified TLS or private startup options")
			}
		})
	}
}

func TestMaintenanceRejectsDefaultAliasesBeforeProviderIO(t *testing.T) {
	var calls atomic.Int32
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	for _, identity := range []connectionfence.Identity{
		{OwnerToken: uuid.NewString(), SourceResourceID: "project-source"},
		{OwnerToken: "invalid", SourceResourceID: "project-source/br-frozen-source"},
		{OwnerToken: uuid.Nil.String(), SourceResourceID: "project-source/br-frozen-source"},
	} {
		if _, err := p.ReserveMaintenance(t.Context(), identity); !errors.Is(err, managedpostgres.ErrInvalid) {
			t.Fatalf("invalid maintenance authority: %v", err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid authority reached provider IO")
	}
}
