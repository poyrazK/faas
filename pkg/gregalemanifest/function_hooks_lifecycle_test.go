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

// Per-function hook timeouts (ADR-20261110190000 §7) come from the entry or
// config in code, reach the lifecycle, and apply only to declared hooks.
func TestFunctionHookTimeouts(t *testing.T) {
	fsys := fstest.MapFS{
		"functions/api/handler.js": {Data: []byte("export const config = { afterRestoreTimeoutMs: 1500 };\nexport default { async fetch() {}, afterRestore() {}, beforeSnapshot() {} };\n")},
		"functions/web/handler.js": {Data: []byte("export async function handler() { return {}; }\n")},
	}
	m, _, err := ApplyInlineConfigFS(&Manifest{Functions: []FunctionSpec{
		{Name: "api", Root: "functions/api", BeforeSnapshotTimeoutMS: 800},
	}}, fsys)
	if err != nil {
		t.Fatal(err)
	}
	plans, err := m.PlanFunctionsFS(fsys, "shop")
	if err != nil {
		t.Fatal(err)
	}
	spec := plans[0].Spec
	if spec.AfterRestoreTimeoutMS != 1500 || spec.BeforeSnapshotTimeoutMS != 800 {
		t.Fatalf("timeouts = before %d after %d, want 800 from yaml and 1500 from code", spec.BeforeSnapshotTimeoutMS, spec.AfterRestoreTimeoutMS)
	}
	after, before, changed, err := spec.HookLifecycleUpdate(nil, nil)
	if err != nil || !changed || after.TimeoutMS != 1500 || before.TimeoutMS != 800 {
		t.Fatalf("lifecycle = after %+v before %+v changed %v err %v", after, before, changed, err)
	}
	// A timeout change alone re-applies the hook.
	_, before, changed, _ = spec.HookLifecycleUpdate(after, &api.BeforeCheckpointHook{Path: functionlifetime.FunctionBeforeSnapshotPath, TimeoutMS: 500})
	if !changed || before == nil || before.TimeoutMS != 800 {
		t.Fatalf("timeout change: before %+v changed %v", before, changed)
	}
	if _, _, changed, _ = spec.HookLifecycleUpdate(after, &api.BeforeCheckpointHook{Path: functionlifetime.FunctionBeforeSnapshotPath, TimeoutMS: 800}); changed {
		t.Fatal("matching lifecycle must not change")
	}

	// A timeout for a hook the code does not declare is refused.
	bad := &Manifest{Functions: []FunctionSpec{{Name: "web", Root: "functions/web", AfterRestoreTimeoutMS: 900}}}
	if _, err := bad.PlanFunctionsFS(fsys, "shop"); err == nil {
		t.Fatal("after_restore_timeout_ms without afterRestore must be refused")
	}
	// Above the lifecycle hook limit is refused.
	if err := validateFunctionSettings("functions[0]", FunctionSpec{AfterRestoreTimeoutMS: 5000}, api.MustLimitsFor(api.PlanPro)); err == nil {
		t.Fatal("after_restore_timeout_ms above 2000 must be refused")
	}
}
