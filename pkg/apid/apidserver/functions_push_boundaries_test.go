// A functions-project push applies its function's settings and env. A
// release staged without traffic changed what the live release runs with,
// and the env bypassed config history (apps/projects review 2026-11-10, P11
// and P12).
package apidserver_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPushSettingsManifestDropsFunctionSettingsFromStagedReleases(t *testing.T) {
	manifest := &gregalemanifest.Manifest{FunctionSettings: &gregalemanifest.FunctionSpec{RAMMB: 512}}
	if got := apidserver.PushSettingsManifest(manifest, apidsource.EnqueueParams{}); got.FunctionSettings == nil {
		t.Fatal("a release taking traffic keeps the function's settings")
	}
	if got := apidserver.PushSettingsManifest(manifest, apidsource.EnqueueParams{TrafficPercentExplicit: true}); got.FunctionSettings != nil {
		t.Fatal("a release staged without traffic must not apply the function's settings")
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

// The push's env lands as a config revision that env history and rollback see.
func TestPushedFunctionEnvRecordsAConfigRevision(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "fn-history@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "hist-api", Type: state.AppTypeFunction, Runtime: "node22",
		RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive, WorkloadClass: state.WorkloadClassHTTP,
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := gregalemanifest.FunctionSpec{Name: "api", Env: map[string]string{"MODE": "prod"}}
	if _, prob := apidserver.ApplySourceFunctionSettingsForTest(ctx, store, acct, app, spec, ""); prob != nil {
		t.Fatalf("apply: %+v", prob)
	}
	revisions, err := store.ListConfigRevisions(ctx, app.ID, state.DefaultEnvScope, 5)
	if err != nil || len(revisions) == 0 || revisions[0].Variables["MODE"] != "prod" || revisions[0].Source != state.ConfigSourceEnvBatch {
		t.Fatalf("revisions = %+v, %v; want the newest an env batch with MODE=prod", revisions, err)
	}
	// An unchanged env writes no further revision.
	if _, prob := apidserver.ApplySourceFunctionSettingsForTest(ctx, store, acct, app, spec, ""); prob != nil {
		t.Fatalf("re-apply: %+v", prob)
	}
	if again, _ := store.ListConfigRevisions(ctx, app.ID, state.DefaultEnvScope, 5); len(again) != len(revisions) {
		t.Fatalf("revisions after an unchanged push = %d, want %d", len(again), len(revisions))
	}
}
