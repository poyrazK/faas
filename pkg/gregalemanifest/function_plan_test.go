package gregalemanifest_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

func writePlanFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPlanFunctions(t *testing.T) {
	dir := t.TempDir()
	writePlanFiles(t, dir, map[string]string{
		"functions/digest/handler.py": "def handler(event, ctx): pass\n",
		"src/api.mjs":                 "export default {}\n",
		"src/hook.py":                 "def handler(event, ctx): pass\n",
		"bin/handler.go":              "package main\n",
		"pinned/README":               "x",
	})
	m := &gregalemanifest.Manifest{Functions: []gregalemanifest.FunctionSpec{
		{Name: "digest", Root: "functions/digest"},
		{Name: "api", Handler: "src/api.mjs"},
		{Name: "hook", Handler: "src/hook.py", Runtime: "python313"},
		{Name: "tool", Root: "bin"},
		{Name: "pinned", Root: "pinned", Runtime: "node24"},
	}}
	got, err := m.PlanFunctions(dir, "shop")
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ slug, root, runtime, handler string }{
		{"shop-digest", "functions/digest", "python312", gregalemanifest.DefaultFunctionHandler},
		{"shop-api", ".", "node22", "src/api.mjs"},
		{"shop-hook", ".", "python313", "src/hook.py"},
		{"shop-tool", "bin", "go124", gregalemanifest.DefaultFunctionHandler},
		{"shop-pinned", "pinned", "node24", gregalemanifest.DefaultFunctionHandler},
	}
	if len(got) != len(want) {
		t.Fatalf("planned %d functions, want %d", len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.Slug != w.slug || g.Root != w.root || g.Runtime != w.runtime || g.Handler != w.handler {
			t.Errorf("function %d = %s %s %s %s, want %s %s %s %s", i, g.Slug, g.Root, g.Runtime, g.Handler, w.slug, w.root, w.runtime, w.handler)
		}
	}
}

func TestPlanFunctionsRefusals(t *testing.T) {
	dir := t.TempDir()
	writePlanFiles(t, dir, map[string]string{"empty/README": "x", "two/handler.js": "", "two/handler.py": ""})
	tests := []struct {
		name string
		fn   gregalemanifest.FunctionSpec
		want string
	}{
		{"missing root", gregalemanifest.FunctionSpec{Name: "a", Root: "nope"}, "is not a directory"},
		{"missing handler file", gregalemanifest.FunctionSpec{Name: "a", Handler: "src/x.mjs"}, "is not a file"},
		{"no handler to infer", gregalemanifest.FunctionSpec{Name: "a", Root: "empty"}, "could not infer a runtime"},
		{"ambiguous handlers", gregalemanifest.FunctionSpec{Name: "a", Root: "two"}, "could not infer a runtime"},
		{"slug too long", gregalemanifest.FunctionSpec{Name: strings.Repeat("x", 30), Root: "empty", Runtime: "node22"}, "app slug"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &gregalemanifest.Manifest{Functions: []gregalemanifest.FunctionSpec{tt.fn}}
			_, err := m.PlanFunctions(dir, "shop-with-a-long-name")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestFunctionCronTriggers(t *testing.T) {
	m := &gregalemanifest.Manifest{Triggers: []gregalemanifest.Trigger{
		{Kind: gregalemanifest.TriggerKindCron, App: "digest", Schedule: "0 8 * * *", Path: "/"},
		{Kind: gregalemanifest.TriggerKindCron, App: "other", Schedule: "* * * * *", Path: "/"},
	}}
	got := m.FunctionCronTriggers(gregalemanifest.FunctionPlan{Spec: gregalemanifest.FunctionSpec{Name: "digest"}, Slug: "shop-digest"})
	if len(got.Triggers) != 1 || got.Triggers[0].App != "shop-digest" || got.Triggers[0].Schedule != "0 8 * * *" {
		t.Fatalf("triggers = %+v, want the digest cron under its app slug", got.Triggers)
	}
	none := m.FunctionCronTriggers(gregalemanifest.FunctionPlan{Spec: gregalemanifest.FunctionSpec{Name: "x"}, Slug: "shop-x"})
	if none.Triggers == nil || len(none.Triggers) != 0 {
		t.Fatalf("a function without crons must reconcile to none: %+v", none.Triggers)
	}
}

func TestFunctionAppSettingsUpdate(t *testing.T) {
	app := api.AppResponse{RAMMB: 256, RequestTimeoutS: 30}
	if _, changed := (gregalemanifest.FunctionSpec{RAMMB: 256, RequestTimeoutS: 30, Concurrency: 1}).AppSettingsUpdate(app); changed {
		t.Fatal("matching settings must not produce an update")
	}
	update, changed := (gregalemanifest.FunctionSpec{RAMMB: 512, Concurrency: 4}).AppSettingsUpdate(app)
	if !changed || update.RAMMB == nil || *update.RAMMB != 512 || update.FunctionConcurrency == nil || *update.FunctionConcurrency != 4 || update.RequestTimeoutS != nil {
		t.Fatalf("update = %+v, want RAM 512 and concurrency 4 only", update)
	}
}

func TestRepositoryDeferredSteps(t *testing.T) {
	fn := gregalemanifest.FunctionSpec{
		Webhooks:       []gregalemanifest.FunctionWebhook{{Name: "stripe"}},
		BucketEvents:   []gregalemanifest.FunctionBucketEvent{{Bucket: "uploads"}},
		SecretsFromEnv: []string{"API_KEY"},
	}
	want := []gregalemanifest.RepositoryDeferredStep{
		{Kind: "webhook", Detail: "stripe"},
		{Kind: "bucket_events", Detail: "uploads"},
		{Kind: "secrets_from_env", Detail: "API_KEY"},
	}
	if got := fn.RepositoryDeferredSteps(); !reflect.DeepEqual(got, want) {
		t.Fatalf("steps = %+v, want %+v", got, want)
	}
	if got := (gregalemanifest.FunctionSpec{}).RepositoryDeferredSteps(); len(got) != 0 {
		t.Fatalf("a function without human steps has none: %+v", got)
	}
}
