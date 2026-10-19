package state

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func workerServiceQueuePolicyFixture() EnvironmentWorkloadQualificationRequest {
	enabled := true
	return EnvironmentWorkloadQualificationRequest{ID: "request", GraphID: "graph", Resource: "workload/worker", AppID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		ExecutionMode: api.ExecutionModeWorker, FrozenInputs: EnvironmentWorkloadRuntime{Resource: "workload/worker", AppID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
			WorkloadClass: WorkloadClassWorker, Baseline: AppManifest{Port: 8080},
			Runtime:         map[string]json.RawMessage{"execution_mode": json.RawMessage(`"worker"`), "service_binding_transport": json.RawMessage(`"https"`)},
			ServiceBindings: map[string]EnvironmentScopedServiceBinding{"billing": {Workload: "billing", EnvKey: "CUSTOM_BILLING_URL", TargetAppID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}},
			QueueBindings: map[string]EnvironmentScopedQueueBinding{"orders": {BindingID: "11111111-1111-4111-8111-111111111111", TriggerID: "22222222-2222-4222-8222-222222222222",
				Contract: api.EnvironmentQueueBinding{QueueName: "orders", Mode: "push", WorkloadClass: "worker", Enabled: &enabled}}},
			QueueSmoke: map[string]api.EnvironmentQueueSmoke{"orders": {Payload: json.RawMessage(`{"id":"worker-service-qualification"}`)}},
		}}
}

func TestEnvironmentQualificationWorkerServicePolicyBindsReviewedDependencies(t *testing.T) {
	request := workerServiceQueuePolicyFixture()
	policy, digest, err := EnvironmentQualificationSmokePolicyFor(request)
	if err != nil || policy.ID != "worker-queue-v3" || policy.Version != 3 || len(policy.QueueMessages) != 1 || policy.ServiceBindingTransport != api.ServiceBindingTransportHTTPS || policy.ServiceBindings["billing"] != request.FrozenInputs.ServiceBindings["billing"] {
		t.Fatalf("service-bound worker policy: %+v %v", policy, err)
	}
	for _, field := range []string{"target", "workload", "env", "transport"} {
		t.Run(field, func(t *testing.T) {
			changed := workerServiceQueuePolicyFixture()
			binding := changed.FrozenInputs.ServiceBindings["billing"]
			switch field {
			case "target":
				binding.TargetAppID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
			case "workload":
				binding.Workload = "next-billing"
			case "env":
				binding.EnvKey = "NEXT_BILLING_URL"
			case "transport":
				changed.FrozenInputs.Runtime["service_binding_transport"] = json.RawMessage(`"http"`)
			}
			changed.FrozenInputs.ServiceBindings["billing"] = binding
			if _, next, err := EnvironmentQualificationSmokePolicyFor(changed); err != nil || next == digest {
				t.Fatalf("reviewed %s change retained policy digest: %v", field, err)
			}
		})
	}
	delete(request.FrozenInputs.ServiceBindings, "billing")
	if len(policy.ServiceBindings) != 1 {
		t.Fatal("returned policy borrowed mutable caller bindings")
	}
	legacy, _, err := EnvironmentQualificationSmokePolicyFor(request)
	if err != nil || legacy.ID != "worker-queue-v2" || legacy.Version != 1 || len(legacy.ServiceBindings) != 0 || legacy.ServiceBindingTransport != "" {
		t.Fatal("queue-only worker policy changed")
	}
}

func TestEnvironmentQualificationWorkerServicePolicyRejectsInvalidContext(t *testing.T) {
	for _, field := range []string{"self", "invalid-target", "duplicate-env", "variable-collision", "secret-collision", "function", "mode", "transport", "missing-smoke"} {
		t.Run(field, func(t *testing.T) {
			request := workerServiceQueuePolicyFixture()
			binding := request.FrozenInputs.ServiceBindings["billing"]
			switch field {
			case "self":
				binding.TargetAppID = request.AppID
			case "invalid-target":
				binding.TargetAppID = "unknown"
			case "duplicate-env":
				request.FrozenInputs.ServiceBindings["other"] = binding
			case "variable-collision":
				request.FrozenInputs.Variables = map[string]string{binding.EnvKey: "unreviewed-url"}
			case "secret-collision":
				request.FrozenInputs.SecretRefs = map[string]string{binding.EnvKey: "secret:billing"}
			case "function":
				request.ExecutionMode, request.FrozenInputs.AppType, request.FrozenInputs.WorkloadClass = api.ExecutionModeRequest, AppTypeFunction, WorkloadClassHTTP
			case "mode":
				request.FrozenInputs.Runtime["execution_mode"] = json.RawMessage(`"request"`)
			case "transport":
				request.FrozenInputs.Runtime["service_binding_transport"] = json.RawMessage(`"unknown"`)
			case "missing-smoke":
				request.FrozenInputs.QueueSmoke = nil
			}
			request.FrozenInputs.ServiceBindings["billing"] = binding
			if _, _, err := EnvironmentQualificationSmokePolicyFor(request); err == nil {
				t.Fatal("invalid worker service qualification policy accepted")
			}
		})
	}
}
