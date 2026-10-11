package environmentsync

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func definition() api.EnvironmentDefinition {
	return api.EnvironmentDefinition{
		APIVersion: APIVersion, Project: "shop", Environment: "production",
		Workloads: map[string]api.EnvironmentWorkload{
			"api": {App: "shop-api", Variables: map[string]string{"MODE": "production"}},
		},
	}
}

// adr: 568 — existing queue identities retain the catalog name contract.
func TestCompileQueueNamesUseCatalogContract(t *testing.T) {
	for _, tc := range []struct {
		name  string
		valid bool
	}{
		{"q", true}, {"tag-orders", true}, {strings.Repeat("q", 63), true},
		{"", false}, {"1queue", false}, {"Queue", false}, {"queue/name", false},
		{"queue_name", false}, {strings.Repeat("q", 64), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, bindingName := range []bool{true, false} {
				d := definition()
				disabled := false
				key, destination := "orders", tc.name
				if bindingName {
					key, destination = tc.name, "orders"
				}
				w := d.Workloads["api"]
				w.QueueBindings = map[string]api.EnvironmentQueueBinding{key: {QueueName: destination, WorkloadClass: "worker", Enabled: &disabled}}
				d.Workloads["api"] = w
				_, err := Compile(d)
				if (err == nil) != tc.valid {
					t.Fatalf("binding=%t name=%q valid=%t: %v", bindingName, tc.name, tc.valid, err)
				}
			}
		})
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
		{"missing workload membership", func(d *api.EnvironmentDefinition) { d.Workloads = nil }},
		{"secret variable", func(d *api.EnvironmentDefinition) { d.Workloads["api"].Variables["API_TOKEN"] = "must-not-leak" }},
		{"source traversal", setWorkload(func(w *api.EnvironmentWorkload) {
			w.Source = &api.EnvironmentWorkloadSource{Directory: "../../outside"}
		})},
		{"source backslash", setWorkload(func(w *api.EnvironmentWorkload) { w.Source = &api.EnvironmentWorkloadSource{Directory: `api\outside`} })},
		{"mutable image", setWorkload(func(w *api.EnvironmentWorkload) {
			w.Source = &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry/api:latest"}
		})},
		{"function missing runner", setWorkload(func(w *api.EnvironmentWorkload) {
			w.Source = &api.EnvironmentWorkloadSource{Kind: "function"}
		})},
		{"function unsupported runner", setWorkload(func(w *api.EnvironmentWorkload) {
			w.Source = &api.EnvironmentWorkloadSource{Kind: "function", Runtime: "node99"}
		})},
		{"function Dockerfile", setWorkload(func(w *api.EnvironmentWorkload) {
			w.Source = &api.EnvironmentWorkloadSource{Kind: "function", Runtime: "node22", Dockerfile: "Dockerfile"}
		})},
		{"source function runner", setWorkload(func(w *api.EnvironmentWorkload) {
			w.Source = &api.EnvironmentWorkloadSource{Kind: "source", Runtime: "node22"}
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

func TestCompileExplicitEmptyEnvironment(t *testing.T) {
	d := definition()
	d.Workloads = map[string]api.EnvironmentWorkload{}
	empty, err := Compile(d)
	if err != nil || len(empty.Fields) != 0 || empty.Definition.Workloads == nil {
		t.Fatalf("explicit empty environment: %+v %v", empty, err)
	}
	d.Configuration = map[string]json.RawMessage{"LOG_LEVEL": json.RawMessage(`"info"`)}
	configurationOnly, err := Compile(d)
	if err != nil || len(configurationOnly.Fields) != 1 || configurationOnly.Fields[0].Resource != "environment" {
		t.Fatalf("configuration-only environment: %+v %v", configurationOnly, err)
	}
	if empty.Digest == configurationOnly.Digest {
		t.Fatal("configuration-only revision reused the empty definition digest")
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
		QueueSmoke:      map[string]api.EnvironmentQueueSmoke{"orders": {Payload: json.RawMessage(`{"id":"qualification"}`)}},
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

func TestCompileQueueBindingContractLimitsAndDefaults(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cap   int
		retry *api.RetryPolicyDTO
		valid bool
	}{
		{"defaults", 0, &api.RetryPolicyDTO{}, true},
		{"ceiling", api.QueueBindingMaxConcurrency, &api.RetryPolicyDTO{MaxAttempts: 4, BaseSeconds: 1, MaxSeconds: 2}, true},
		{"above ceiling", api.QueueBindingMaxConcurrency + 1, nil, false},
		{"retry attempts", 1, &api.RetryPolicyDTO{MaxAttempts: api.DurableRetryMaxAttempts + 1}, false},
		{"retry base", 1, &api.RetryPolicyDTO{BaseSeconds: api.QueueBindingRetryMaxBaseSeconds + 1}, false},
		{"retry max", 1, &api.RetryPolicyDTO{MaxSeconds: api.QueueBindingRetryMaxSeconds + 1}, false},
		{"retry jitter", 1, &api.RetryPolicyDTO{JitterSeconds: 2}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := definition()
			w := d.Workloads["api"]
			w.QueueBindings = map[string]api.EnvironmentQueueBinding{"orders": {QueueName: "orders", WorkloadClass: "worker", MaxConcurrency: tc.cap, RetryPolicy: tc.retry}}
			w.QueueSmoke = map[string]api.EnvironmentQueueSmoke{"orders": {Payload: json.RawMessage(`{"id":"qualification"}`)}}
			d.Workloads["api"] = w
			desired, err := Compile(d)
			if (err == nil) != tc.valid {
				t.Fatalf("queue contract validity: %v", err)
			}
			if err != nil {
				return
			}
			value := desired.Definition.Workloads["api"].QueueBindings["orders"]
			if value.Enabled == nil || !*value.Enabled || value.Mode != "pull" || value.MaxConcurrency < 1 {
				t.Fatalf("queue defaults: %+v", value)
			}
			if tc.name == "defaults" && value.RetryPolicy != nil {
				t.Fatal("empty retry policy retained noncanonical ownership")
			}
			next, err := Compile(desired.Definition)
			if err != nil || next.Digest != desired.Digest {
				t.Fatalf("queue contract is not stable: %v", err)
			}
		})
	}
}

func TestCompileWorkerQueueSmokeRequiresAndCanonicalizesReviewedPayload(t *testing.T) {
	worker := definition()
	workload := worker.Workloads["api"]
	workload.Runtime = json.RawMessage(`{"execution_mode":"worker"}`)
	workload.QueueBindings = map[string]api.EnvironmentQueueBinding{
		"orders": {QueueName: "orders", Mode: "push", WorkloadClass: "worker"},
	}
	workload.QueueSmoke = map[string]api.EnvironmentQueueSmoke{
		"orders": {Payload: json.RawMessage(`{"z":2,"a":1}`)},
	}
	worker.Workloads["api"] = workload

	compiled, err := Compile(worker)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(compiled.Definition.Workloads["api"].QueueSmoke["orders"].Payload); got != `{"a":1,"z":2}` {
		t.Fatalf("queue smoke payload = %s, want canonical JSON", got)
	}
	second, err := Compile(compiled.Definition)
	if err != nil || compiled.Digest != second.Digest {
		t.Fatalf("queue smoke normalization was not stable: %v", err)
	}

	changedDefinition := compiled.Definition
	changedWorkload := changedDefinition.Workloads["api"]
	changedWorkload.QueueSmoke = map[string]api.EnvironmentQueueSmoke{"orders": {Payload: json.RawMessage(`{"a":2}`)}}
	changedDefinition.Workloads["api"] = changedWorkload
	changedCompiled, err := Compile(changedDefinition)
	if err != nil || changedCompiled.Digest == compiled.Digest {
		t.Fatalf("reviewed queue smoke payload did not affect definition digest: %v", err)
	}
}

func TestCompileFunctionQueueSmokeRequiresHTTPPushFunction(t *testing.T) {
	functionDefinition := func() api.EnvironmentDefinition {
		d := definition()
		w := d.Workloads["api"]
		w.Source = &api.EnvironmentWorkloadSource{Kind: "function", Runtime: "node22", Directory: "functions/api"}
		w.QueueBindings = map[string]api.EnvironmentQueueBinding{
			"orders": {QueueName: "orders", Mode: "push", WorkloadClass: "http"},
		}
		w.QueueSmoke = map[string]api.EnvironmentQueueSmoke{"orders": {Payload: json.RawMessage(`{"idempotency_key":"qualification"}`)}}
		d.Workloads["api"] = w
		return d
	}
	d := functionDefinition()
	compiled, err := Compile(d)
	if err != nil {
		t.Fatalf("compile function queue smoke: %v", err)
	}
	if got := compiled.Definition.Workloads["api"].QueueBindings["orders"].WorkloadClass; got != "http" {
		t.Fatalf("function queue workload class = %q", got)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*api.EnvironmentWorkload)
	}{
		{"HTTP pull", func(w *api.EnvironmentWorkload) {
			binding := w.QueueBindings["orders"]
			binding.Mode = "pull"
			w.QueueBindings["orders"] = binding
		}},
		{"HTTP image", func(w *api.EnvironmentWorkload) {
			w.Source = &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/api@sha256:" + strings.Repeat("a", 64)}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := functionDefinition()
			workload := candidate.Workloads["api"]
			tc.mutate(&workload)
			candidate.Workloads = map[string]api.EnvironmentWorkload{"api": workload}
			if _, err := Compile(candidate); err == nil {
				t.Fatal("unsupported HTTP queue binding was accepted")
			}
		})
	}
}

func TestCompileRejectsUnsafeWorkerQueueSmoke(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*api.EnvironmentWorkload)
	}{
		{"missing input", func(w *api.EnvironmentWorkload) {}},
		{"wrong workload class", func(w *api.EnvironmentWorkload) {
			w.QueueBindings["orders"] = api.EnvironmentQueueBinding{QueueName: "orders", Mode: "push", WorkloadClass: "job"}
			w.QueueSmoke = map[string]api.EnvironmentQueueSmoke{"orders": {Payload: json.RawMessage(`{}`)}}
		}},
		{"unknown binding", func(w *api.EnvironmentWorkload) {
			w.QueueBindings = nil
			w.QueueSmoke = map[string]api.EnvironmentQueueSmoke{"orders": {Payload: json.RawMessage(`{}`)}}
		}},
		{"invalid json", func(w *api.EnvironmentWorkload) {
			w.QueueSmoke = map[string]api.EnvironmentQueueSmoke{"orders": {Payload: json.RawMessage(`{"x":`)}}
		}},
		{"oversized input", func(w *api.EnvironmentWorkload) {
			w.QueueSmoke = map[string]api.EnvironmentQueueSmoke{"orders": {Payload: json.RawMessage(`"` + strings.Repeat("x", api.EnvironmentGitOpsMaxQueueSmokePayloadBytes) + `"`)}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := definition()
			workload := d.Workloads["api"]
			workload.Runtime = json.RawMessage(`{"execution_mode":"worker"}`)
			workload.QueueBindings = map[string]api.EnvironmentQueueBinding{
				"orders": {QueueName: "orders", Mode: "push", WorkloadClass: "worker"},
			}
			tc.change(&workload)
			d.Workloads["api"] = workload
			if _, err := Compile(d); err == nil {
				t.Fatal("unsafe worker queue smoke was accepted")
			}
		})
	}
}

func TestCompileWorkerPullQueueSmokeRequiresReviewedInput(t *testing.T) {
	d := definition()
	w := d.Workloads["api"]
	w.Runtime = json.RawMessage(`{"execution_mode":"worker"}`)
	w.QueueBindings = map[string]api.EnvironmentQueueBinding{
		"orders": {QueueName: "orders", Mode: "pull", WorkloadClass: "worker"},
	}
	w.QueueSmoke = map[string]api.EnvironmentQueueSmoke{"orders": {Payload: json.RawMessage(`{"idempotency_key":"qualification"}`)}}
	d.Workloads["api"] = w
	compiled, err := Compile(d)
	if err != nil {
		t.Fatalf("compile reviewed pull worker smoke: %v", err)
	}
	if got := compiled.Definition.Workloads["api"].QueueBindings["orders"].Mode; got != "pull" {
		t.Fatalf("pull worker mode = %q", got)
	}
	w = compiled.Definition.Workloads["api"]
	w.QueueSmoke = nil
	d.Workloads["api"] = w
	if _, err := Compile(d); err == nil {
		t.Fatal("enabled pull worker binding compiled without reviewed queue_smoke")
	}
}

func TestCompileJobSmokeFreezesBoundedArgvAndTimeout(t *testing.T) {
	d := definition()
	w := d.Workloads["api"]
	w.Runtime = json.RawMessage(`{"execution_mode":"job"}`)
	w.JobSmoke = &api.EnvironmentJobSmoke{Command: []string{"node", "scripts/smoke.js", "--once"}, TimeoutSeconds: 30}
	d.Workloads["api"] = w
	compiled, err := Compile(d)
	if err != nil {
		t.Fatal(err)
	}
	got := compiled.Definition.Workloads["api"].JobSmoke
	if got == nil || strings.Join(got.Command, " ") != "node scripts/smoke.js --once" || got.TimeoutSeconds != 30 {
		t.Fatalf("job smoke contract was not retained: %+v", got)
	}
	replayed, err := Compile(compiled.Definition)
	if err != nil || replayed.Digest != compiled.Digest {
		t.Fatalf("job smoke contract is not canonical across replay: %v", err)
	}
	changed := compiled.Definition
	changedWorkload := changed.Workloads["api"]
	changedWorkload.JobSmoke = &api.EnvironmentJobSmoke{Command: []string{"node", "scripts/other.js"}, TimeoutSeconds: 30}
	changed.Workloads["api"] = changedWorkload
	changedPlan, err := Compile(changed)
	if err != nil || changedPlan.Digest == compiled.Digest {
		t.Fatalf("job smoke command did not change the reviewed definition digest: %v", err)
	}
}

func TestCompileRejectsIncompleteJobSmokeContract(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runtime json.RawMessage
		smoke   *api.EnvironmentJobSmoke
	}{
		{name: "mode omitted", smoke: &api.EnvironmentJobSmoke{Command: []string{"node", "job.js"}, TimeoutSeconds: 30}},
		{name: "wrong mode", runtime: json.RawMessage(`{"execution_mode":"service"}`), smoke: &api.EnvironmentJobSmoke{Command: []string{"node", "job.js"}, TimeoutSeconds: 30}},
		{name: "empty command", runtime: json.RawMessage(`{"execution_mode":"job"}`), smoke: &api.EnvironmentJobSmoke{TimeoutSeconds: 30}},
		{name: "empty executable", runtime: json.RawMessage(`{"execution_mode":"job"}`), smoke: &api.EnvironmentJobSmoke{Command: []string{" "}, TimeoutSeconds: 30}},
		{name: "NUL argument", runtime: json.RawMessage(`{"execution_mode":"job"}`), smoke: &api.EnvironmentJobSmoke{Command: []string{"node", "bad\x00arg"}, TimeoutSeconds: 30}},
		{name: "timeout missing", runtime: json.RawMessage(`{"execution_mode":"job"}`), smoke: &api.EnvironmentJobSmoke{Command: []string{"node", "job.js"}}},
		{name: "timeout over preview cap", runtime: json.RawMessage(`{"execution_mode":"job"}`), smoke: &api.EnvironmentJobSmoke{Command: []string{"node", "job.js"}, TimeoutSeconds: api.EnvironmentGitOpsJobSmokeMaxTimeoutSeconds + 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := definition()
			w := d.Workloads["api"]
			w.Runtime = tc.runtime
			w.JobSmoke = tc.smoke
			d.Workloads["api"] = w
			if _, err := Compile(d); err == nil {
				t.Fatal("incomplete job smoke contract was accepted")
			}
		})
	}
}

func TestCompileJobScheduleUsesCanonicalDurableContract(t *testing.T) {
	d := definition()
	w := d.Workloads["api"]
	w.Runtime = json.RawMessage(`{"execution_mode":"job"}`)
	w.Schedule = &api.EnvironmentJobSchedule{
		Cron: " 0   3 * * * ", Timezone: "",
		SchedulePolicy: &workpolicy.SchedulePolicy{Version: workpolicy.Version, Overlap: "skip", MissedRuns: "coalesce_latest", StartDeadlineSeconds: 300},
		FailureRules:   &workpolicy.FailureRules{Version: workpolicy.Version, Rules: []workpolicy.FailureRule{}, UnmatchedFailure: "retry", UncertainOutcome: "hold"},
	}
	d.Workloads["api"] = w
	compiled, err := Compile(d)
	if err != nil {
		t.Fatal(err)
	}
	got := compiled.Definition.Workloads["api"].Schedule
	if got == nil || got.Cron != "0 3 * * *" || got.Timezone != "UTC" || got.SchedulePolicy == nil || got.SchedulePolicy.Overlap != "skip" {
		t.Fatalf("job schedule was not normalized: %+v", got)
	}
	managed := false
	for _, field := range compiled.Fields {
		managed = managed || field.Resource == "workload/api" && field.Path == "schedule"
	}
	if !managed {
		t.Fatal("schedule did not become an owned desired field")
	}
	replayed, err := Compile(compiled.Definition)
	if err != nil || replayed.Digest != compiled.Digest {
		t.Fatalf("job schedule is not canonical across replay: %v", err)
	}
	changed := compiled.Definition
	changedWorkload := changed.Workloads["api"]
	changedSchedule := *changedWorkload.Schedule
	changedSchedule.Timezone = "Europe/Istanbul"
	changedWorkload.Schedule = &changedSchedule
	changed.Workloads["api"] = changedWorkload
	changedPlan, err := Compile(changed)
	if err != nil || changedPlan.Digest == compiled.Digest {
		t.Fatalf("job schedule timezone did not change the reviewed digest: %v", err)
	}
}

func TestCompileRejectsIncompleteJobScheduleContract(t *testing.T) {
	for _, tc := range []struct {
		name     string
		runtime  json.RawMessage
		schedule *api.EnvironmentJobSchedule
	}{
		{name: "mode omitted", schedule: &api.EnvironmentJobSchedule{Cron: "0 3 * * *"}},
		{name: "wrong mode", runtime: json.RawMessage(`{"execution_mode":"service"}`), schedule: &api.EnvironmentJobSchedule{Cron: "0 3 * * *"}},
		{name: "empty cron", runtime: json.RawMessage(`{"execution_mode":"job"}`), schedule: &api.EnvironmentJobSchedule{}},
		{name: "invalid cron", runtime: json.RawMessage(`{"execution_mode":"job"}`), schedule: &api.EnvironmentJobSchedule{Cron: "not cron"}},
		{name: "invalid timezone", runtime: json.RawMessage(`{"execution_mode":"job"}`), schedule: &api.EnvironmentJobSchedule{Cron: "0 3 * * *", Timezone: "Mars/Olympus"}},
		{name: "invalid overlap policy", runtime: json.RawMessage(`{"execution_mode":"job"}`), schedule: &api.EnvironmentJobSchedule{Cron: "0 3 * * *", SchedulePolicy: &workpolicy.SchedulePolicy{Version: workpolicy.Version, Overlap: "surprise", MissedRuns: "skip"}}},
		{name: "invalid failure policy", runtime: json.RawMessage(`{"execution_mode":"job"}`), schedule: &api.EnvironmentJobSchedule{Cron: "0 3 * * *", FailureRules: &workpolicy.FailureRules{Version: workpolicy.Version, UnmatchedFailure: "retry", UncertainOutcome: "discard"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := definition()
			w := d.Workloads["api"]
			w.Runtime, w.Schedule = tc.runtime, tc.schedule
			d.Workloads["api"] = w
			if _, err := Compile(d); err == nil {
				t.Fatal("invalid job schedule contract was accepted")
			}
		})
	}
}
