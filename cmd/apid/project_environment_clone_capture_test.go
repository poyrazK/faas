// adr: 567
package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestCloneConfigurationCaptureInventoryIsFrozenAndDeterministic(t *testing.T) {
	hash := strings.Repeat("a", 64)
	op := state.ProjectEnvironmentCloneOperation{ID: "operation", SourceEnvironment: "production", SourceRevisionHash: hash}
	capture := state.ProjectEnvironmentCloneConfigurationCapture{OperationID: op.ID, Version: 1, Hash: hash, WorkloadCount: 2}
	views := []state.ProjectEnvironmentCloneWorkload{}
	for _, name := range []string{"jobs", "api"} {
		views = append(views, state.ProjectEnvironmentCloneWorkload{OperationID: op.ID, AppID: name + "-app", WorkloadSlug: name,
			SourceDeploymentID: name + "-deployment", SourceHash: hash, SourceSettingsHash: hash, SourceProjectConfigHash: hash,
			SourceValuesHash: hash, SourceBindingsHash: hash, SourcePoliciesHash: hash})
	}
	resources, err := capturedCloneConfigurationResources(op, capture, views)
	if err != nil || len(resources) != 14 || views[0].WorkloadSlug != "jobs" {
		t.Fatalf("frozen inventory: %+v, %v", resources, err)
	}
	reversed := []state.ProjectEnvironmentCloneWorkload{views[1], views[0]}
	replayed, err := capturedCloneConfigurationResources(op, capture, reversed)
	if err != nil || !reflect.DeepEqual(replayed, resources) {
		t.Fatalf("reader order changed capture: %+v, %v", replayed, err)
	}
	for _, resource := range resources {
		if resource.CapturePoint != "" || resource.Kind == "managed_postgres" || resource.Kind == "object_storage" {
			t.Fatalf("configuration capture invented data evidence: %+v", resource)
		}
	}
	for _, fault := range []string{"legacy_root", "wrong_root", "wrong_count", "wrong_owner", "duplicate_workload", "missing_artifact", "missing_values", "missing_bindings", "missing_settings", "missing_config", "missing_policies", "mixed_config", "prepared_target"} {
		t.Run(fault, func(t *testing.T) {
			root := capture
			bad := append([]state.ProjectEnvironmentCloneWorkload(nil), views...)
			switch fault {
			case "legacy_root":
				root.Version = 0
			case "wrong_root":
				root.Hash = strings.Repeat("b", 64)
			case "wrong_count":
				root.WorkloadCount++
			case "wrong_owner":
				bad[0].OperationID = "foreign"
			case "duplicate_workload":
				bad[1] = bad[0]
			case "missing_artifact":
				bad[0].SourceDeploymentID = ""
			case "missing_values":
				bad[0].SourceValuesHash = ""
			case "missing_bindings":
				bad[0].SourceBindingsHash = ""
			case "missing_settings":
				bad[0].SourceSettingsHash = ""
			case "missing_config":
				bad[0].SourceProjectConfigHash = ""
			case "missing_policies":
				bad[0].SourcePoliciesHash = ""
			case "mixed_config":
				bad[0].SourceProjectConfigHash = strings.Repeat("b", 64)
			case "prepared_target":
				bad[0].TargetDeploymentID = "old-target"
			}
			if _, err := capturedCloneConfigurationResources(op, root, bad); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("accepted %s: %v", fault, err)
			}
		})
	}
}
