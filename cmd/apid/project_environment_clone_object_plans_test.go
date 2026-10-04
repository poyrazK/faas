// adr: 569
package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func capturedObjectPlanningFixture() (state.ProjectEnvironmentCloneOperation, []state.ProjectEnvironmentCloneWorkload, []state.ProjectEnvironmentCloneBindings) {
	op, views, catalogue := capturedDatabasePlanningFixture()
	for i := range catalogue {
		catalogue[i].Postgres = nil
		catalogue[i].Buckets = []state.ProjectEnvironmentCloneObjectBucket{{ID: "bucket-" + catalogue[i].AppID, Name: "assets", Region: "eu",
			BackendID: "storage", BackendFingerprint: strings.Repeat("b", 64), PhysicalName: "physical-" + catalogue[i].AppID, PublicRead: true, ServeAt: "/assets"}}
	}
	return op, views, catalogue
}

func TestCapturedCloneObjectPlansRequireCompleteOwnedCatalogue(t *testing.T) {
	for _, fault := range []string{"valid", "missing_catalogue", "duplicate_workload", "duplicate_catalogue", "foreign_operation", "scope", "hash", "duplicate_bucket", "no_provider"} {
		t.Run(fault, func(t *testing.T) {
			op, views, catalogue := capturedObjectPlanningFixture()
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
			case "duplicate_bucket":
				catalogue[1].Buckets[0] = catalogue[0].Buckets[0]
			case "no_provider":
				catalogue[0].Buckets[0].PhysicalName = ""
			}
			plans, err := buildCapturedProjectEnvironmentObjectPlans(op, views, catalogue)
			if fault == "valid" {
				if err != nil || len(plans) != 2 || plans[0].source.ID == plans[1].source.ID {
					t.Fatalf("omitted independent buckets: %+v, %v", plans, err)
				}
			} else if !errors.Is(err, state.ErrProjectEnvironmentCloneBindingCapture) {
				t.Fatalf("accepted invalid catalogue: %v", err)
			}
		})
	}
}

func TestCapturedCloneObjectResourcesRequirePinnedRosterAndSharedPoint(t *testing.T) {
	op, views, catalogue := capturedObjectPlanningFixture()
	plans, err := buildCapturedProjectEnvironmentObjectPlans(op, views, catalogue)
	if err != nil {
		t.Fatal(err)
	}
	point := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	for _, fault := range []string{"valid", "missing", "duplicate", "name", "point_precision", "point_future", "point_noncanonical", "point_changed", "database_point_changed", "shared_target", "copy_without_manifest", "hash", "ready_without_target"} {
		t.Run(fault, func(t *testing.T) {
			resources, err := capturedProjectEnvironmentObjectResources(plans, point)
			if err != nil {
				t.Fatal(err)
			}
			for i := range resources {
				resources[i].SourceVersion, resources[i].TargetID, resources[i].Status = strings.Repeat("a", 64), "target-"+resources[i].SourceID, "captured"
			}
			switch fault {
			case "missing":
				resources = resources[:1]
			case "duplicate":
				resources = append(resources, resources[0])
			case "name":
				resources[0].Name = "other"
			case "point_precision":
				resources[0].CapturePoint = point.Add(time.Nanosecond).Format(time.RFC3339Nano)
			case "point_future":
				resources[0].CapturePoint = point.Add(time.Hour).Format(time.RFC3339Nano)
			case "point_noncanonical":
				resources[0].CapturePoint = point.In(time.FixedZone("offset", 3600)).Format(time.RFC3339Nano)
			case "point_changed":
				resources[1].CapturePoint = point.Add(time.Microsecond).Format(time.RFC3339Nano)
			case "database_point_changed":
				resources = append(resources, state.ProjectEnvironmentCloneResource{Kind: "managed_postgres", Name: "db", SourceID: "db", CapturePoint: point.Add(time.Microsecond).Format(time.RFC3339Nano), Status: "captured"})
			case "shared_target":
				resources[0].TargetID = resources[0].SourceID
			case "copy_without_manifest":
				resources[0].SourceVersion = ""
			case "hash":
				resources[0].SourceVersion = strings.Repeat("G", 64)
			case "ready_without_target":
				resources[0].Status, resources[0].TargetID = "ready", ""
			}
			_, gotPoint, err := validateCapturedProjectEnvironmentObjectResources(plans, resources, false)
			if (err == nil) != (fault == "valid") || fault == "valid" && !gotPoint.Equal(point) {
				t.Fatalf("accepted changed resource plan: point %v, error %v", gotPoint, err)
			}
		})
	}
}
