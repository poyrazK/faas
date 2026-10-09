// adr: 856
package main

import (
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity/validatorbundle"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestValidatorReleaseGateFailsClosedOnlyForAllowlistedApps(t *testing.T) {
	app := state.App{ID: uuid.NewString()}
	deployment := state.Deployment{ID: uuid.NewString(), AppID: app.ID}
	s := &server{durableEntityValidatorReleaseGateEnabled: true, durableEntityRestoreIsolationEnabled: true, executionAPIEnabled: true, durableEntityApps: map[string]bool{app.ID: true}, durableEntityValidatorBundles: map[string]durableEntityValidatorBundle{}}
	if s.durableEntityValidatorReleaseProblem(t.Context(), app, deployment) == nil {
		t.Fatal("missing bundle passed")
	}
	b := durableEntityValidatorBundle{AppID: app.ID, DeploymentID: deployment.ID, Runtime: api.ExecutionRuntimeNode22, Entrypoint: "validator.mjs", Files: []api.ExecutionFile{{Path: "validator.mjs", Content: []byte("export default () => ({protocol_version:1,valid:true})")}}}
	b.SHA256 = validatorBundleHash(b)
	s.durableEntityValidatorBundles[deployment.ID] = b
	if p := s.durableEntityValidatorReleaseProblem(t.Context(), app, deployment); p != nil {
		t.Fatal(p)
	}
	b.AppID = uuid.NewString()
	s.durableEntityValidatorBundles[deployment.ID] = b
	if s.durableEntityValidatorReleaseProblem(t.Context(), app, deployment) == nil {
		t.Fatal("foreign app passed")
	}
	s.durableEntityValidatorReleaseGateEnabled = false
	if s.durableEntityValidatorReleaseProblem(t.Context(), app, deployment) != nil {
		t.Fatal("disabled gate blocked release")
	}
	s.durableEntityValidatorReleaseGateEnabled = true
	s.durableEntityApps = nil
	if s.durableEntityValidatorReleaseProblem(t.Context(), app, deployment) != nil {
		t.Fatal("unrelated app blocked")
	}
}

func TestValidatorReleasePreflightRunsWithAPIContractDiffDisabled(t *testing.T) {
	t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "0")
	app := state.App{ID: uuid.NewString()}
	deployment := state.Deployment{ID: uuid.NewString(), AppID: app.ID, Scope: "default"}
	s := &server{durableEntityValidatorReleaseGateEnabled: true, durableEntityRestoreIsolationEnabled: true, executionAPIEnabled: true, durableEntityApps: map[string]bool{app.ID: true}}
	if _, problem := s.contractTrafficContext(t.Context(), app, deployment); problem == nil || problem.Code != "durable_entity_validator_release_required" {
		t.Fatal("missing bundle bypassed preflight", problem)
	}
}

func TestSharedValidatorArtifactsResolveWithoutRegistryReload(t *testing.T) {
	app := state.App{ID: uuid.NewString()}
	deployment := state.Deployment{ID: uuid.NewString(), AppID: app.ID}
	bucket := &entityTestBucket{objects: map[string]entityTestObject{}}
	shared := validatorbundle.NewArtifacts(bucket, map[string]bool{app.ID: true})
	b := durableEntityValidatorBundle{AppID: app.ID, DeploymentID: deployment.ID, Runtime: api.ExecutionRuntimeNode22, Entrypoint: "validator.mjs", Files: []api.ExecutionFile{{Path: "validator.mjs", Content: []byte("export default () => ({protocol_version:1,valid:true})")}}}
	b.SHA256 = validatorBundleHash(b)
	s := &server{durableEntityValidatorArtifacts: shared, durableEntityValidatorReleaseGateEnabled: true, durableEntityRestoreIsolationEnabled: true, executionAPIEnabled: true, durableEntityApps: map[string]bool{app.ID: true}, durableEntityValidatorBundles: map[string]durableEntityValidatorBundle{deployment.ID: b}}
	if s.durableEntityValidatorReleaseProblem(t.Context(), app, deployment) == nil {
		t.Fatal("shared storage fell back to registry")
	}
	if err := shared.Publish(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if problem := s.durableEntityValidatorReleaseProblem(t.Context(), app, deployment); problem != nil {
		t.Fatal("new artifact not visible without restart", problem)
	}
}
