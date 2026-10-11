// adr: 946
package durable

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
	s := &Service{ValidatorReleaseGateEnabled: true, RestoreIsolationEnabled: true, Host: fakeHost{execution: true}, Apps: map[string]bool{app.ID: true}, ValidatorBundles: map[string]validatorbundle.Bundle{}}
	if s.ValidatorReleaseProblem(t.Context(), app, deployment) == nil {
		t.Fatal("missing bundle passed")
	}
	b := validatorbundle.Bundle{AppID: app.ID, DeploymentID: deployment.ID, Runtime: api.ExecutionRuntimeNode22, Entrypoint: "validator.mjs", Files: []api.ExecutionFile{{Path: "validator.mjs", Content: []byte("export default () => ({protocol_version:1,valid:true})")}}}
	b.SHA256 = validatorBundleHash(b)
	s.ValidatorBundles[deployment.ID] = b
	if p := s.ValidatorReleaseProblem(t.Context(), app, deployment); p != nil {
		t.Fatal(p)
	}
	b.AppID = uuid.NewString()
	s.ValidatorBundles[deployment.ID] = b
	if s.ValidatorReleaseProblem(t.Context(), app, deployment) == nil {
		t.Fatal("foreign app passed")
	}
	s.ValidatorReleaseGateEnabled = false
	if s.ValidatorReleaseProblem(t.Context(), app, deployment) != nil {
		t.Fatal("disabled gate blocked release")
	}
	s.ValidatorReleaseGateEnabled = true
	s.Apps = nil
	if s.ValidatorReleaseProblem(t.Context(), app, deployment) != nil {
		t.Fatal("unrelated app blocked")
	}
}

func TestSharedValidatorArtifactsResolveWithoutRegistryReload(t *testing.T) {
	app := state.App{ID: uuid.NewString()}
	deployment := state.Deployment{ID: uuid.NewString(), AppID: app.ID}
	bucket := newTestBucket()
	shared := validatorbundle.NewArtifacts(bucket, map[string]bool{app.ID: true})
	b := validatorbundle.Bundle{AppID: app.ID, DeploymentID: deployment.ID, Runtime: api.ExecutionRuntimeNode22, Entrypoint: "validator.mjs", Files: []api.ExecutionFile{{Path: "validator.mjs", Content: []byte("export default () => ({protocol_version:1,valid:true})")}}}
	b.SHA256 = validatorBundleHash(b)
	s := &Service{ValidatorArtifacts: shared, ValidatorReleaseGateEnabled: true, RestoreIsolationEnabled: true, Host: fakeHost{execution: true}, Apps: map[string]bool{app.ID: true}, ValidatorBundles: map[string]validatorbundle.Bundle{deployment.ID: b}}
	if s.ValidatorReleaseProblem(t.Context(), app, deployment) == nil {
		t.Fatal("shared storage fell back to registry")
	}
	if err := shared.Publish(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if problem := s.ValidatorReleaseProblem(t.Context(), app, deployment); problem != nil {
		t.Fatal("new artifact not visible without restart", problem)
	}
}
