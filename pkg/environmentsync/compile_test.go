package environmentsync

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func definition() api.EnvironmentDefinition {
	return api.EnvironmentDefinition{
		APIVersion: APIVersion, Project: "shop", Environment: "production",
		Workloads: map[string]api.EnvironmentWorkload{
			"api": {App: "shop-api", Variables: map[string]string{"MODE": "production"}},
		},
	}
}

func TestCompileOwnsOnlyExplicitFieldsAndPreservesInput(t *testing.T) {
	d := definition()
	w := d.Workloads["api"]
	w.Source = &api.EnvironmentWorkloadSource{Directory: "./api"}
	w.Runtime = json.RawMessage(`{"execution_mode":"service","service_replicas":{"min":1,"max":2,"desired":1}}`)
	w.Policies = &[]api.EnvironmentPolicy{}
	d.Workloads["api"] = w
	before, _ := json.Marshal(d)
	compiled, err := Compile(d)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(d)
	if string(before) != string(after) {
		t.Fatal("compiler mutated caller's definition")
	}
	fields := map[string]bool{}
	for _, field := range compiled.Fields {
		fields[field.Key()] = true
	}
	for _, key := range []string{"workload/api#presence", "workload/api#source", "workload/api#variables/MODE", "workload/api#runtime/execution_mode", "workload/api#runtime/service_replicas", "workload/api#policies"} {
		if !fields[key] {
			t.Fatalf("missing owned field %s", key)
		}
	}
	if len(fields) != 6 || compiled.Definition.Workloads["api"].Source.Directory != "api" || compiled.Definition.Workloads["api"].Source.Kind != "source" {
		t.Fatalf("unexpected compilation %+v", compiled)
	}
	second, err := Compile(compiled.Definition)
	if err != nil || compiled.Digest != second.Digest {
		t.Fatalf("normalization is not idempotent: %v", err)
	}
}

func TestCompileRejectsUnsafeOrUnsupportedIntent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*api.EnvironmentDefinition)
	}{
		{"schema version", func(d *api.EnvironmentDefinition) { d.APIVersion = "v999" }},
		{"empty environment", func(d *api.EnvironmentDefinition) { d.Workloads = nil }},
		{"secret variable", func(d *api.EnvironmentDefinition) { d.Workloads["api"].Variables["API_TOKEN"] = "must-not-leak" }},
		{"source traversal", setWorkload(func(w *api.EnvironmentWorkload) {
			w.Source = &api.EnvironmentWorkloadSource{Directory: "../../outside"}
		})},
		{"source backslash", setWorkload(func(w *api.EnvironmentWorkload) { w.Source = &api.EnvironmentWorkloadSource{Directory: `api\outside`} })},
		{"mutable image", setWorkload(func(w *api.EnvironmentWorkload) {
			w.Source = &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry/api:latest"}
		})},
		{"image plus directory", setWorkload(func(w *api.EnvironmentWorkload) {
			w.Source = &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry/api@sha256:" + strings.Repeat("a", 64), Directory: "api"}
		})},
		{"unknown runtime", setWorkload(func(w *api.EnvironmentWorkload) { w.Runtime = json.RawMessage(`{"restart_polciy":"always"}`) })},
		{"runtime env", setWorkload(func(w *api.EnvironmentWorkload) { w.Runtime = json.RawMessage(`{"env":{"API_TOKEN":"must-not-leak"}}`) })},
		{"runtime null", setWorkload(func(w *api.EnvironmentWorkload) { w.Runtime = json.RawMessage(`{"after_restore":null}`) })},
		{"runtime invalid mode", setWorkload(func(w *api.EnvironmentWorkload) { w.Runtime = json.RawMessage(`{"execution_mode":"unknown"}`) })},
		{"plaintext secret", setWorkload(func(w *api.EnvironmentWorkload) { w.SecretRefs = map[string]string{"PASSWORD": "must-not-leak"} })},
		{"missing route list", setWorkload(func(w *api.EnvironmentWorkload) { w.Routes = &api.EnvironmentRouteContract{} })},
		{"empty enforced routes", setWorkload(func(w *api.EnvironmentWorkload) {
			w.Routes = &api.EnvironmentRouteContract{OnlyAllowDeclaredRoutes: true, DeclaredRoutes: []api.DeclaredRoute{}}
		})},
		{"duplicate route", setWorkload(func(w *api.EnvironmentWorkload) {
			w.Routes = &api.EnvironmentRouteContract{DeclaredRoutes: []api.DeclaredRoute{{Path: "/", Methods: []string{"GET"}}, {Path: "/", Methods: []string{"get"}}}}
		})},
		{"shared policy", setWorkload(func(w *api.EnvironmentWorkload) {
			w.Policies = &[]api.EnvironmentPolicy{{Name: "limit", Kind: "rate_limit", Action: json.RawMessage(`{}`)}}
		})},
		{"unknown action field", setWorkload(func(w *api.EnvironmentWorkload) {
			w.Policies = &[]api.EnvironmentPolicy{{Name: "headers", Kind: "headers", Action: json.RawMessage(`{"typo":true}`)}}
		})},
		{"unknown dependency", setWorkload(func(w *api.EnvironmentWorkload) {
			w.ServiceBindings = map[string]api.EnvironmentServiceBinding{"other": {Workload: "missing", EnvKey: "OTHER_URL"}}
		})},
		{"self dependency", setWorkload(func(w *api.EnvironmentWorkload) {
			w.ServiceBindings = map[string]api.EnvironmentServiceBinding{"api": {Workload: "api", EnvKey: "API_URL"}}
		})},
		{"invalid queue", setWorkload(func(w *api.EnvironmentWorkload) {
			w.QueueBindings = map[string]api.EnvironmentQueueBinding{"jobs": {QueueName: "jobs", WorkloadClass: "worker", MaxConcurrency: -1}}
		})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := definition()
			tc.change(&d)
			_, err := Compile(d)
			if err == nil {
				t.Fatal("invalid intent accepted")
			}
			if strings.Contains(err.Error(), "must-not-leak") {
				t.Fatal("validation error exposed sensitive value")
			}
		})
	}
}

func setWorkload(change func(*api.EnvironmentWorkload)) func(*api.EnvironmentDefinition) {
	return func(d *api.EnvironmentDefinition) {
		w := d.Workloads["api"]
		change(&w)
		d.Workloads["api"] = w
	}
}

func TestCompileGraphNormalizesAndRejectsDependencyCycles(t *testing.T) {
	d := definition()
	d.Workloads["worker"] = api.EnvironmentWorkload{
		Runtime:         json.RawMessage(`{"execution_mode":"worker"}`),
		QueueBindings:   map[string]api.EnvironmentQueueBinding{"orders": {QueueName: "orders", WorkloadClass: "worker"}},
		ServiceBindings: map[string]api.EnvironmentServiceBinding{"api": {Workload: "api", EnvKey: "API_URL"}},
	}
	compiled, err := Compile(d)
	if err != nil {
		t.Fatal(err)
	}
	queue := compiled.Definition.Workloads["worker"].QueueBindings["orders"]
	if queue.Mode != "pull" || queue.MaxConcurrency != 1 || queue.Enabled == nil || !*queue.Enabled {
		t.Fatalf("queue defaults not normalized: %+v", queue)
	}
	w := d.Workloads["api"]
	w.ServiceBindings = map[string]api.EnvironmentServiceBinding{"worker": {Workload: "worker", EnvKey: "WORKER_URL"}}
	d.Workloads["api"] = w
	if _, err := Compile(d); err == nil {
		t.Fatal("cyclic preparation graph accepted")
	}
}
