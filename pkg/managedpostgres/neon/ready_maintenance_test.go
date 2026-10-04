// adr:531
package neon

import (
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectionfence"
)

func TestReadyMaintenanceConnectionPinsDatabaseAndCapturedMajor(t *testing.T) {
	for _, fault := range []string{"", "captured_major", "source_uri", "unknown_database"} {
		t.Run(fault, func(t *testing.T) {
			var calls, credentials atomic.Int32
			p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch r.URL.Path {
				case "/api/v2/projects/project-source":
					writeResponse(t, w, http.StatusOK, projectResponse{Project: project{ID: "project-source",
						OrganizationID: "org-gregale-12345678", RegionID: "aws-eu-central-1", PostgresMajor: 17}})
				case "/api/v2/projects/project-source/branches":
					writeResponse(t, w, http.StatusOK, branchesResponse{Branches: []branch{{ID: "br-source", ProjectID: "project-source", CurrentState: "ready"}}})
				case "/api/v2/projects/project-source/endpoints":
					disabled := false
					writeResponse(t, w, http.StatusOK, endpointsResponse{Endpoints: []endpoint{{ID: "ep-source", ProjectID: "project-source",
						RegionID: "aws-eu-central-1", BranchID: "br-source", Host: "ep-source.example.test", Type: "read_write", CurrentState: "active", Disabled: &disabled}}})
				case "/api/v2/projects/project-source/operations":
					writeResponse(t, w, http.StatusOK, operationsResponse{})
				case "/api/v2/projects/project-source/connection_uri":
					credentials.Add(1)
					q := r.URL.Query()
					if q.Get("branch_id") != "br-source" || q.Get("database_name") != connectionfence.MaintenanceDatabase ||
						q.Get("role_name") != maintenanceSourceRole || q.Get("pooled") != "false" {
						t.Error("ready recovery fetched source/default/pooled credentials")
					}
					database := connectionfence.MaintenanceDatabase
					if fault == "source_uri" {
						database = "gregale"
					}
					writeResponse(t, w, http.StatusOK, connectionURIResponse{URI: "postgres://gregale_owner:private-password@ep-source.example.test/" + database + "?sslmode=require&options=-c+role%3Devil"})
				default:
					t.Error("unexpected ready recovery API path")
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			major, database := 17, connectionfence.MaintenanceDatabase
			if fault == "captured_major" {
				major = 16
			}
			if fault == "unknown_database" {
				database = "unknown"
			}
			config, err := p.maintenanceConnectionConfigForDatabase(t.Context(), connectionfence.Identity{
				OwnerToken: uuid.NewString(), SourceResourceID: "project-source/br-source"}, database, major)
			if fault == "" {
				if err != nil || config == nil || config.Database != connectionfence.MaintenanceDatabase || config.PostgresMajor != 17 ||
					config.User != maintenanceSourceRole || config.TLSConfig == nil || config.TLSConfig.InsecureSkipVerify || len(config.RuntimeParams) != 1 {
					t.Fatalf("ready connection: %v", err)
				}
			} else if err == nil || config != nil {
				t.Fatalf("accepted %s: %v", fault, err)
			}
			if fault == "captured_major" && credentials.Load() != 0 || fault == "unknown_database" && calls.Load() != 0 {
				t.Fatal("rejected frozen definition fetched credentials")
			}
		})
	}
}

func TestCheckpointRecoveryRejectsInvalidAuthorityBeforeProviderIO(t *testing.T) {
	var calls atomic.Int32
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	spec := managedpostgres.Spec{Region: "eu-central-1", PostgresMajor: 16, Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone}
	m := managedpostgres.CheckpointMaintenance{OwnerToken: uuid.NewString(), SourceResourceID: "project/br-source", State: "ready", OwnerOID: 10101, DatabaseOID: 20202}
	i := managedpostgres.CheckpointConnectionIdentity{OwnerToken: uuid.NewString(), SourceResourceID: m.SourceResourceID}
	frozen := managedpostgres.RestoreSourceDefinition{Spec: spec, ProviderResourceID: "project", DataResourceID: m.SourceResourceID}
	for _, fault := range []string{"owner", "same_owner", "source", "missing_oid", "state", "region"} {
		maintenance, identity, definition := m, i, frozen
		switch fault {
		case "owner":
			identity.OwnerToken = uuid.Nil.String()
		case "same_owner":
			identity.OwnerToken = maintenance.OwnerToken
		case "source":
			identity.SourceResourceID += "/other"
		case "missing_oid":
			maintenance.DatabaseOID = 0
		case "state":
			maintenance.State = "reserved"
		case "region":
			definition.Spec.Region = "us-east-1"
		}
		if _, err := p.AbandonCheckpointConnections(t.Context(), definition, maintenance, identity); !errors.Is(err, managedpostgres.ErrInvalid) {
			t.Fatalf("invalid %s abandonment: %v", fault, err)
		}
		if _, err := p.ObserveCheckpointConnections(t.Context(), definition, maintenance, identity); !errors.Is(err, managedpostgres.ErrInvalid) {
			t.Fatalf("invalid %s observation: %v", fault, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid recovery authority reached provider")
	}
}

func TestCheckpointSourceDefinitionPinsLifecycleAndDatasetBeforeIO(t *testing.T) {
	var calls atomic.Int32
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	d := managedpostgres.RestoreSourceDefinition{Spec: managedpostgres.Spec{Region: "eu-central-1", PostgresMajor: 16,
		Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone},
		ProviderResourceID: "project-source", DataResourceID: "project-source/br-frozen"}
	for _, lifecycle := range []string{"project-source", "project-source/br-frozen"} {
		definition := d
		definition.ProviderResourceID = lifecycle
		if err := p.validateCheckpointSourceDefinition(definition, d.DataResourceID); err != nil {
			t.Fatalf("valid lifecycle: %v", err)
		}
	}
	for _, fault := range []string{"project", "branch", "default_alias", "data_pin", "region", "major", "phase"} {
		definition := d
		request := managedpostgres.CheckpointMaintenanceRequest{OwnerToken: uuid.NewString(), SourceResourceID: d.DataResourceID, Phase: "role"}
		switch fault {
		case "project":
			definition.ProviderResourceID = "project-other"
		case "branch":
			definition.ProviderResourceID += "/br-other"
		case "default_alias":
			definition.DataResourceID, request.SourceResourceID = "project-source", "project-source"
		case "data_pin":
			definition.DataResourceID += "-other"
		case "region":
			definition.Spec.Region = "us-east-1"
		case "major":
			definition.Spec.PostgresMajor = 15
		case "phase":
			request.Phase = "delete"
		}
		if _, err := p.ReconcileCheckpointMaintenance(t.Context(), definition, request); err == nil {
			t.Fatalf("accepted %s source", fault)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid frozen source reached provider")
	}
}
