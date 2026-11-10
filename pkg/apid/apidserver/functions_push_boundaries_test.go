// A functions-project push applies its function's app-wide settings and
// default-scope env. A push that does not release to production traffic (a
// stage branch, or a release staged without traffic) rewrote production with
// them; the env bypassed config history (apps/projects review 2026-11-10,
// P11 and P12).
package apidserver_test

import (
	"reflect"
	"testing"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPushSettingsManifestKeepsFunctionSettingsOnlyForProductionReleases(t *testing.T) {
	spec := &gregalemanifest.FunctionSpec{RAMMB: 512, Env: map[string]string{"MODE": "x"}}
	manifest := &gregalemanifest.Manifest{FunctionSettings: spec}
	for _, tc := range []struct {
		name   string
		scope  string
		kind   state.DeploymentKind
		params apidsource.EnqueueParams
		keep   bool
	}{
		{"production push", "", state.DeploymentKindGitHub, apidsource.EnqueueParams{}, true},
		{"named production push", "production", state.DeploymentKindGitHub, apidsource.EnqueueParams{}, true},
		{"stage branch push", "staging", state.DeploymentKindGitHub, apidsource.EnqueueParams{}, false},
		{"staged without traffic", "", state.DeploymentKindGitHub, apidsource.EnqueueParams{TrafficPercentExplicit: true}, false},
		{"pull request preview", "", state.DeploymentKindPreview, apidsource.EnqueueParams{}, true},
	} {
		got := apidserver.PushSettingsManifest(manifest, &githubdpb.EnqueueBuildRequest{DeploymentScope: tc.scope}, tc.kind, tc.params)
		if kept := got.FunctionSettings != nil; kept != tc.keep {
			t.Errorf("%s: function settings kept = %v, want %v", tc.name, kept, tc.keep)
		}
	}
	if manifest.FunctionSettings == nil {
		t.Fatal("the caller's manifest must not be modified")
	}
}

func TestFunctionEnvBatchSetsChangesAndPrunesUndeclared(t *testing.T) {
	current := []state.AppEnv{{Key: "KEEP", Value: "same"}, {Key: "CHANGE", Value: "old"}, {Key: "EXTRA", Value: "manual"}}
	spec := gregalemanifest.FunctionSpec{Env: map[string]string{"KEEP": "same", "CHANGE": "new", "ADD": "1"}}
	request, changed := apidserver.FunctionEnvBatch(spec, current)
	if !changed || !reflect.DeepEqual(request.Values, map[string]string{"CHANGE": "new", "ADD": "1"}) || len(request.Delete) != 0 {
		t.Fatalf("without prune_env = %+v (changed %v)", request, changed)
	}
	spec.PruneEnv = true
	request, _ = apidserver.FunctionEnvBatch(spec, current)
	if !reflect.DeepEqual(request.Delete, []string{"EXTRA"}) {
		t.Fatalf("prune_env deletes = %v, want [EXTRA]", request.Delete)
	}
	if _, changed := apidserver.FunctionEnvBatch(gregalemanifest.FunctionSpec{Env: map[string]string{"KEEP": "same"}}, current); changed {
		t.Fatal("an unchanged env must not write a revision")
	}
}
