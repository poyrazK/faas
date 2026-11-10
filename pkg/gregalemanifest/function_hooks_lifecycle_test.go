package gregalemanifest

import (
	"slices"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
)

func TestPlanFunctionsReadsHooks(t *testing.T) {
	fsys := fstest.MapFS{
		"functions/api/handler.js": {Data: []byte(`export default { async fetch() {}, afterRestore() {}, beforeSnapshot() {} };`)},
		"functions/job/handler.py": {Data: []byte("def init():\n    pass\n\ndef handler(event, ctx):\n    return {}\n")},
		"functions/web/handler.js": {Data: []byte(`export async function handler() { return {}; }`)},
	}
	m := &Manifest{Functions: []FunctionSpec{
		{Name: "api", Root: "functions/api"},
		{Name: "job", Root: "functions/job"},
		{Name: "web", Root: "functions/web", Runtime: "node22"},
	}}
	plans, err := m.PlanFunctionsFS(fsys, "shop")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"api": {"before_snapshot", "after_restore"}, "job": {"init"}, "web": nil}
	for _, p := range plans {
		if !slices.Equal(p.Spec.Hooks, want[p.Spec.Name]) {
			t.Errorf("%s hooks = %v, want %v", p.Spec.Name, p.Spec.Hooks, want[p.Spec.Name])
		}
	}
	if !plans[0].Spec.HasPreDeploySettings() || plans[2].Spec.HasPreDeploySettings() {
		t.Error("declared hooks must count as pre-deploy settings, and only declared hooks")
	}
}

func TestHookLifecycleUpdate(t *testing.T) {
	reservedAfter := &api.AfterRestoreHook{Path: functionlifetime.FunctionAfterRestorePath}
	reservedBefore := &api.BeforeCheckpointHook{Path: functionlifetime.FunctionBeforeSnapshotPath}

	// Declared hooks point the app at the reserved paths.
	fn := FunctionSpec{Name: "api", Hooks: []string{"before_snapshot", "after_restore"}}
	after, before, changed, err := fn.HookLifecycleUpdate(nil, nil)
	if err != nil || !changed || after == nil || after.Path != functionlifetime.FunctionAfterRestorePath ||
		before == nil || before.Path != functionlifetime.FunctionBeforeSnapshotPath {
		t.Fatalf("new hooks = %+v %+v %v %v", after, before, changed, err)
	}
	// Already wired: nothing to change.
	if _, _, changed, err := fn.HookLifecycleUpdate(reservedAfter, reservedBefore); changed || err != nil {
		t.Fatalf("wired hooks changed = %v, %v", changed, err)
	}
	// init alone needs no lifecycle hook.
	if _, _, changed, _ := (FunctionSpec{Hooks: []string{"init"}}).HookLifecycleUpdate(nil, nil); changed {
		t.Fatal("init must not set a lifecycle hook")
	}
	// Hooks removed from code clear the reserved paths.
	after, before, changed, err = (FunctionSpec{Name: "api"}).HookLifecycleUpdate(reservedAfter, reservedBefore)
	if err != nil || !changed || after == nil || after.Path != "" || before == nil || before.Path != "" {
		t.Fatalf("removed hooks = %+v %+v %v %v", after, before, changed, err)
	}
	// A path of the app's own is left alone when the code declares nothing...
	if _, _, changed, err := (FunctionSpec{Name: "api"}).HookLifecycleUpdate(&api.AfterRestoreHook{Path: "/mine"}, nil); changed || err != nil {
		t.Fatalf("own hook changed = %v, %v", changed, err)
	}
	// ...and refused when the code declares the same hook.
	if _, _, _, err := fn.HookLifecycleUpdate(&api.AfterRestoreHook{Path: "/mine"}, nil); err == nil {
		t.Fatal("a hook declared in code and in the app must be refused")
	}
}
