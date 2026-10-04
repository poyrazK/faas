// adr: 583
package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func capturedDatabasePlanningFixture() (state.ProjectEnvironmentCloneOperation, []state.ProjectEnvironmentCloneWorkload, []state.ProjectEnvironmentCloneBindings) {
	op := state.ProjectEnvironmentCloneOperation{ID: "operation", AccountID: "account", ProjectID: "project", SourceEnvironment: "production", TargetEnvironment: "stage"}
	views := []state.ProjectEnvironmentCloneWorkload{
		{OperationID: op.ID, AppID: "app-a", SourceScope: "default", SourceBindingsHash: "hash-a"},
		{OperationID: op.ID, AppID: "app-b", SourceScope: "production", SourceBindingsHash: "hash-b"},
	}
	binding := state.ProjectEnvironmentClonePostgresBinding{ID: "binding-a", DatabaseID: "shared-db", DatabaseName: "orders", Region: "eu", PostgresMajor: 17,
		ServiceClass: string(managedpostgres.ClassDevelopment), Availability: string(managedpostgres.AvailabilitySingleZone), ScaleToZero: true,
		StorageLimitBytes: 1 << 30, RestoreWindowSeconds: 3600, BackendID: "backend", BackendFingerprint: strings.Repeat("a", 64), ProviderResourceID: "source-provider", DataResourceID: "source-provider/source-branch"}
	other := binding
	other.ID, other.EnvironmentKey, other.Access = "binding-b", "READ_DATABASE", "read_only"
	catalogue := []state.ProjectEnvironmentCloneBindings{
		{AppID: views[0].AppID, SourceScope: views[0].SourceScope, Hash: views[0].SourceBindingsHash, ProjectEnvironmentCloneBindingDefinitions: state.ProjectEnvironmentCloneBindingDefinitions{Postgres: []state.ProjectEnvironmentClonePostgresBinding{binding}}},
		{AppID: views[1].AppID, SourceScope: views[1].SourceScope, Hash: views[1].SourceBindingsHash, ProjectEnvironmentCloneBindingDefinitions: state.ProjectEnvironmentCloneBindingDefinitions{Postgres: []state.ProjectEnvironmentClonePostgresBinding{other}}},
	}
	return op, views, catalogue
}

func TestCapturedCloneDatabasePlansDeduplicateAndAuthenticateCatalogue(t *testing.T) {
	op, views, catalogue := capturedDatabasePlanningFixture()
	plans, err := buildCapturedProjectEnvironmentDatabasePlans(op, views, catalogue)
	if err != nil || len(plans) != 1 || plans[0].source.ID != "shared-db" {
		t.Fatalf("shared plans = %+v, %v", plans, err)
	}
	// Legacy receipts retain their hash, but are no longer cloneable sources.
	legacy := catalogue[0].Postgres[0]
	legacy.DataResourceID = ""
	legacyHash, err := state.ProjectEnvironmentCloneDatabaseSourceHash(legacy)
	if err != nil || plans[0].hash == legacyHash {
		t.Fatal("exact dataset identity is absent from the source hash")
	}
	if legacyHash != "a3210e328478e053a41dcc26ebe93cad5736506069ef314f78c7dbd22836fc7a" {
		t.Fatalf("database definition receipt encoding changed: %s", plans[0].hash)
	}
	otherOp := op
	otherOp.ID = "new-operation"
	otherViews := append([]state.ProjectEnvironmentCloneWorkload(nil), views...)
	for i := range otherViews {
		otherViews[i].OperationID = otherOp.ID
	}
	otherPlans, err := buildCapturedProjectEnvironmentDatabasePlans(otherOp, otherViews, catalogue)
	if err != nil || otherPlans[0].name == plans[0].name || otherPlans[0].hash != plans[0].hash {
		t.Fatalf("operation-specific target identity changed source definition: %+v, %v", otherPlans, err)
	}
	for _, fault := range []string{"missing_catalogue", "duplicate_workload", "duplicate_catalogue", "foreign_operation", "scope", "hash", "shared_database_spec", "shared_database_provider", "invalid_spec", "no_restore_window", "missing_data_identity", "shared_database_data"} {
		t.Run(fault, func(t *testing.T) {
			op, views, catalogue := capturedDatabasePlanningFixture()
			switch fault {
			case "missing_catalogue":
				catalogue = catalogue[:1]
			case "duplicate_workload":
				views[1] = views[0]
			case "duplicate_catalogue":
				catalogue[1] = catalogue[0]
			case "foreign_operation":
				views[0].OperationID = "other"
			case "scope":
				catalogue[0].SourceScope = "other"
			case "hash":
				catalogue[0].Hash = "changed"
			case "shared_database_spec":
				catalogue[1].Postgres[0].StorageLimitBytes *= 2
			case "shared_database_provider":
				catalogue[1].Postgres[0].ProviderResourceID = "replaced"
			case "missing_data_identity":
				catalogue[0].Postgres[0].DataResourceID = ""
			case "shared_database_data":
				catalogue[1].Postgres[0].DataResourceID += "/different"
			case "invalid_spec":
				catalogue[0].Postgres[0].PostgresMajor = 0
			case "no_restore_window":
				catalogue[0].Postgres[0].RestoreWindowSeconds = 0
			}
			if _, err := buildCapturedProjectEnvironmentDatabasePlans(op, views, catalogue); !errors.Is(err, state.ErrProjectEnvironmentCloneBindingCapture) {
				t.Fatalf("invalid catalogue accepted: %v", err)
			}
		})
	}
}

func TestCapturedCloneDatabaseResourcesRequireExactRosterAndPoint(t *testing.T) {
	op, views, catalogue := capturedDatabasePlanningFixture()
	plans, err := buildCapturedProjectEnvironmentDatabasePlans(op, views, catalogue)
	if err != nil {
		t.Fatal(err)
	}
	point := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	for _, fault := range []string{"valid", "missing", "duplicate", "hash", "point_missing", "point_precision", "point_future", "point_noncanonical", "shared_target", "ready_without_target", "unsupported_status", "unexpected_database"} {
		t.Run(fault, func(t *testing.T) {
			resources, err := capturedProjectEnvironmentDatabaseResources(plans, point)
			if err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "missing":
				resources = nil
			case "duplicate":
				resources = append(resources, resources[0])
			case "hash":
				resources[0].SourceVersion = "changed"
			case "point_missing":
				resources[0].CapturePoint = ""
			case "point_precision":
				resources[0].CapturePoint = point.Add(time.Nanosecond).Format(time.RFC3339Nano)
			case "point_future":
				resources[0].CapturePoint = point.Add(time.Hour).Format(time.RFC3339Nano)
			case "point_noncanonical":
				resources[0].CapturePoint = point.In(time.FixedZone("offset", 3600)).Format(time.RFC3339Nano)
			case "shared_target":
				resources[0].TargetID = resources[0].SourceID
			case "ready_without_target":
				resources[0].Status = "ready"
			case "unsupported_status":
				resources[0].Status = "unsupported"
			case "unexpected_database":
				extra := resources[0]
				extra.Name, extra.SourceID = "other-db", "other-db"
				resources = append(resources, extra)
			}
			_, recorded, err := validateCapturedProjectEnvironmentDatabaseResources(plans, resources)
			if (err == nil) != (fault == "valid") || fault == "valid" && !recorded.Equal(point) {
				t.Fatalf("point = %v, error = %v", recorded, err)
			}
		})
	}
	// Independent databases must still use one recorded recovery time.
	op, views, catalogue = capturedDatabasePlanningFixture()
	catalogue[1].Postgres[0].DatabaseID = "other-db"
	catalogue[1].Postgres[0].DatabaseName = "other-orders"
	catalogue[1].Postgres[0].ProviderResourceID = "other-provider"
	plans, err = buildCapturedProjectEnvironmentDatabasePlans(op, views, catalogue)
	if err != nil || len(plans) != 2 {
		t.Fatalf("independent database plans = %+v, %v", plans, err)
	}
	resources, err := capturedProjectEnvironmentDatabaseResources(plans, point)
	if err != nil {
		t.Fatal(err)
	}
	resources[1].CapturePoint = point.Add(time.Second).Format(time.RFC3339Nano)
	if _, _, err := validateCapturedProjectEnvironmentDatabaseResources(plans, resources); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("different recovery times accepted: %v", err)
	}
}
