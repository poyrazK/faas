package state

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestEnvironmentQualificationSmokeEvidenceRequiresPassingNamedResult(t *testing.T) {
	request := EnvironmentWorkloadQualificationRequest{Resource: "workload/api", AppID: "app-api", ExecutionMode: "request", FrozenInputs: EnvironmentWorkloadRuntime{
		Resource: "workload/api", AppID: "app-api", Baseline: AppManifest{Port: 8087, Healthz: "/inherited-is-not-owned"},
		Runtime: map[string]json.RawMessage{"healthz": json.RawMessage(`"/reviewed-ready"`), "port": json.RawMessage("8087")},
	}}
	policy, policySHA256, err := EnvironmentQualificationSmokePolicyFor(request)
	if err != nil {
		t.Fatalf("derive reviewed policy: %v", err)
	}
	evidence := EnvironmentQualificationSmokeEvidence{
		Resource: "workload/api", InstanceID: "restored-instance", PolicyID: policy.ID,
		PolicySHA256: policySHA256, ResultSHA256: strings.Repeat("b", 64), Passed: true,
	}
	if err := evidence.ValidateFor(request, "restored-instance"); err != nil {
		t.Fatalf("valid evidence rejected: %v", err)
	}
	tests := []struct {
		name     string
		mutate   func(*EnvironmentQualificationSmokeEvidence)
		instance string
		want     error
	}{
		{name: "wrong resource", mutate: func(e *EnvironmentQualificationSmokeEvidence) { e.Resource = "workload/other" }, instance: "restored-instance", want: ErrConflict},
		{name: "wrong restored instance", mutate: func(e *EnvironmentQualificationSmokeEvidence) {}, instance: "another-instance", want: ErrConflict},
		{name: "failed check", mutate: func(e *EnvironmentQualificationSmokeEvidence) { e.Passed = false }, instance: "restored-instance", want: ErrConflict},
		{name: "missing policy", mutate: func(e *EnvironmentQualificationSmokeEvidence) { e.PolicyID = "" }, instance: "restored-instance", want: ErrInvalidArgument},
		{name: "malformed policy digest", mutate: func(e *EnvironmentQualificationSmokeEvidence) { e.PolicySHA256 = strings.Repeat("A", 64) }, instance: "restored-instance", want: ErrInvalidArgument},
		{name: "different approved policy", mutate: func(e *EnvironmentQualificationSmokeEvidence) { e.PolicyID = "http-healthcheck-v1" }, instance: "restored-instance", want: ErrConflict},
		{name: "substituted policy digest", mutate: func(e *EnvironmentQualificationSmokeEvidence) { e.PolicySHA256 = strings.Repeat("c", 64) }, instance: "restored-instance", want: ErrConflict},
		{name: "malformed result digest", mutate: func(e *EnvironmentQualificationSmokeEvidence) { e.ResultSHA256 = "redacted-output" }, instance: "restored-instance", want: ErrInvalidArgument},
		{name: "invalid resource", mutate: func(e *EnvironmentQualificationSmokeEvidence) { e.Resource = "workload/api-" }, instance: "restored-instance", want: ErrConflict},
		{name: "short resource", mutate: func(e *EnvironmentQualificationSmokeEvidence) { e.Resource = "workload/a" }, instance: "restored-instance", want: ErrConflict},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			candidate := evidence
			tc.mutate(&candidate)
			if err := candidate.ValidateFor(request, tc.instance); !errors.Is(err, tc.want) {
				t.Fatalf("ValidateFor() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestEnvironmentQualificationSmokePolicyRequiresReviewedAppHealthz(t *testing.T) {
	request := EnvironmentWorkloadQualificationRequest{Resource: "workload/api", AppID: "app-api", ExecutionMode: "request", FrozenInputs: EnvironmentWorkloadRuntime{
		Resource: "workload/api", AppID: "app-api", Baseline: AppManifest{Healthz: "/inherited"}, Runtime: map[string]json.RawMessage{},
	}}
	if _, _, err := EnvironmentQualificationSmokePolicyFor(request); !errors.Is(err, ErrEnvironmentWorkloadPreparationUnavailable) {
		t.Fatalf("inherited health path selected without explicit reviewed policy: %v", err)
	}
	request.FrozenInputs.Runtime["healthz"] = json.RawMessage(`"/reviewed-ready"`)
	if _, _, err := EnvironmentQualificationSmokePolicyFor(request); !errors.Is(err, ErrEnvironmentWorkloadPreparationUnavailable) {
		t.Fatalf("policy without explicitly owned port was accepted: %v", err)
	}
	request.FrozenInputs.Runtime["port"] = json.RawMessage("0")
	policy, first, err := EnvironmentQualificationSmokePolicyFor(request)
	if err != nil || policy.Path != "/reviewed-ready" || policy.Port != 8080 || policy.TimeoutMillis != 3000 {
		t.Fatalf("reviewed default-port HTTP policy: %+v %q %v", policy, first, err)
	}
	if _, second, err := EnvironmentQualificationSmokePolicyFor(request); err != nil || second != first {
		t.Fatalf("policy digest is not deterministic: %q %q %v", first, second, err)
	}
	request.FrozenInputs.Runtime["healthz"] = json.RawMessage(`"/changed-ready"`)
	if _, changed, err := EnvironmentQualificationSmokePolicyFor(request); err != nil || changed == first {
		t.Fatalf("reviewed path change did not change policy digest: %q %q %v", first, changed, err)
	}
	request.FrozenInputs.Runtime["healthz"] = json.RawMessage(`"/ready?debug=true"`)
	if _, _, err := EnvironmentQualificationSmokePolicyFor(request); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("query-bearing health path accepted: %v", err)
	}
	request.ExecutionMode = "worker"
	request.FrozenInputs.Runtime["healthz"] = json.RawMessage(`"/ready"`)
	request.FrozenInputs.Runtime["port"] = json.RawMessage("8087")
	if _, _, err := EnvironmentQualificationSmokePolicyFor(request); !errors.Is(err, ErrEnvironmentWorkloadPreparationUnavailable) {
		t.Fatalf("HTTP smoke policy accepted a worker execution mode: %v", err)
	}
}

func TestEnvironmentQualificationWorkerQueuePolicyPinsSyntheticPushInput(t *testing.T) {
	enabled := true
	request := EnvironmentWorkloadQualificationRequest{ID: "request", GraphID: "graph", Resource: "workload/worker", AppID: "app-worker",
		ExecutionMode: "worker", FrozenInputs: EnvironmentWorkloadRuntime{Resource: "workload/worker", AppID: "app-worker",
			WorkloadClass: WorkloadClassWorker, Runtime: map[string]json.RawMessage{}, Baseline: AppManifest{Port: 8087},
			QueueBindings: map[string]EnvironmentScopedQueueBinding{
				"orders": {BindingID: "11111111-1111-4111-8111-111111111111", TriggerID: "22222222-2222-4222-8222-222222222222",
					Contract: api.EnvironmentQueueBinding{QueueName: "orders", Mode: "push", WorkloadClass: "worker", Enabled: &enabled}},
			},
			QueueSmoke: map[string]api.EnvironmentQueueSmoke{"orders": {Payload: json.RawMessage(`{"idempotency_key":"qualification-1"}`)}},
		}}
	policy, first, err := EnvironmentQualificationSmokePolicyFor(request)
	if err != nil {
		t.Fatalf("derive worker queue policy: %v", err)
	}
	if policy.ID != environmentQualificationWorkerQueuePolicyID || policy.Port != 8087 || len(policy.QueueMessages) != 1 {
		t.Fatalf("worker queue policy = %+v", policy)
	}
	message := policy.QueueMessages[0]
	if message.BindingName != "orders" || message.BindingID != request.FrozenInputs.QueueBindings["orders"].BindingID || message.Mode != "push" ||
		message.TriggerID != request.FrozenInputs.QueueBindings["orders"].TriggerID || message.Method != "POST" ||
		message.Path != "/_triggers/esm/22222222-2222-4222-8222-222222222222" ||
		string(message.Payload) != `{"idempotency_key":"qualification-1"}` {
		t.Fatalf("worker queue test message = %+v", message)
	}
	if _, repeated, err := EnvironmentQualificationSmokePolicyFor(request); err != nil || repeated != first {
		t.Fatalf("worker queue policy digest is unstable: %q %q %v", first, repeated, err)
	}
	formatted := request
	formatted.FrozenInputs.QueueSmoke = map[string]api.EnvironmentQueueSmoke{"orders": {
		Payload: json.RawMessage(`{ "idempotency_key": "qualification-1" }`),
	}}
	formattedPolicy, formattedDigest, err := EnvironmentQualificationSmokePolicyFor(formatted)
	if err != nil || formattedDigest != first || string(formattedPolicy.QueueMessages[0].Payload) != string(message.Payload) {
		t.Fatalf("formatted frozen worker payload was not semantically canonicalized: policy=%+v digest=%q err=%v", formattedPolicy, formattedDigest, err)
	}
	request.FrozenInputs.QueueSmoke["orders"] = api.EnvironmentQueueSmoke{Payload: json.RawMessage(`{"idempotency_key":"qualification-2"}`)}
	if _, changed, err := EnvironmentQualificationSmokePolicyFor(request); err != nil || changed == first {
		t.Fatalf("changing reviewed test input did not change policy digest: %q %q %v", first, changed, err)
	}
}

func TestEnvironmentQualificationWorkerPullQueuePolicyUsesInvocationContract(t *testing.T) {
	enabled := true
	request := EnvironmentWorkloadQualificationRequest{ID: "request", GraphID: "graph", Resource: "workload/worker", AppID: "app-worker",
		ExecutionMode: api.ExecutionModeWorker, FrozenInputs: EnvironmentWorkloadRuntime{Resource: "workload/worker", AppID: "app-worker",
			WorkloadClass: WorkloadClassWorker, Runtime: map[string]json.RawMessage{}, Baseline: AppManifest{Port: 8087},
			QueueBindings: map[string]EnvironmentScopedQueueBinding{
				"orders": {BindingID: "11111111-1111-4111-8111-111111111111",
					Contract: api.EnvironmentQueueBinding{QueueName: "orders", Mode: "pull", WorkloadClass: "worker", Enabled: &enabled}},
			},
			QueueSmoke: map[string]api.EnvironmentQueueSmoke{"orders": {Payload: json.RawMessage(`{"idempotency_key":"qualification-1"}`)}},
		}}
	policy, digest, err := EnvironmentQualificationSmokePolicyFor(request)
	if err != nil {
		t.Fatalf("derive pull worker queue policy: %v", err)
	}
	if policy.ID != environmentQualificationWorkerQueuePolicyID || len(policy.QueueMessages) != 1 {
		t.Fatalf("pull worker queue policy = %+v", policy)
	}
	message := policy.QueueMessages[0]
	if message.Mode != "pull" || message.BindingID != request.FrozenInputs.QueueBindings["orders"].BindingID ||
		message.TriggerID != "" || message.Method != "POST" || message.Path != "/" ||
		string(message.Payload) != `{"idempotency_key":"qualification-1"}` {
		t.Fatalf("pull worker message contract = %+v", message)
	}
	if _, repeated, err := EnvironmentQualificationSmokePolicyFor(request); err != nil || repeated != digest {
		t.Fatalf("pull worker policy digest is unstable: %q %q %v", digest, repeated, err)
	}
}

func TestEnvironmentQualificationFunctionQueuePolicyUsesPrivateTriggerRoute(t *testing.T) {
	enabled := true
	request := EnvironmentWorkloadQualificationRequest{ID: "request", GraphID: "graph", Resource: "workload/function", AppID: "app-function",
		ExecutionMode: api.ExecutionModeRequest, FrozenInputs: EnvironmentWorkloadRuntime{Resource: "workload/function", AppID: "app-function",
			AppType: AppTypeFunction, RuntimeBase: "node22", WorkloadClass: WorkloadClassHTTP, Runtime: map[string]json.RawMessage{}, Baseline: AppManifest{Port: 8087},
			QueueBindings: map[string]EnvironmentScopedQueueBinding{
				"orders": {BindingID: "11111111-1111-4111-8111-111111111111", TriggerID: "22222222-2222-4222-8222-222222222222",
					Contract: api.EnvironmentQueueBinding{QueueName: "orders", Mode: "push", WorkloadClass: "http", Enabled: &enabled}},
			},
			QueueSmoke: map[string]api.EnvironmentQueueSmoke{"orders": {Payload: json.RawMessage(`{"idempotency_key":"qualification-1"}`)}},
		}}
	policy, _, err := EnvironmentQualificationSmokePolicyFor(request)
	if err != nil {
		t.Fatalf("derive function queue policy: %v", err)
	}
	if policy.ID != environmentQualificationFunctionQueuePolicyID || len(policy.QueueMessages) != 1 ||
		policy.QueueMessages[0].Path != "/_triggers/esm/22222222-2222-4222-8222-222222222222" {
		t.Fatalf("function queue policy = %+v", policy)
	}
}

func TestQualificationSmokeReceiptMatchingRechecksReviewedPolicy(t *testing.T) {
	request := EnvironmentWorkloadQualificationRequest{ID: "request", GraphID: "graph", ReservedInstanceID: "capture", Attempt: 2,
		Resource: "workload/api", AppID: "app-api", ExecutionMode: "request", FrozenInputs: EnvironmentWorkloadRuntime{Resource: "workload/api", AppID: "app-api",
			Runtime: map[string]json.RawMessage{"healthz": json.RawMessage(`"/ready"`), "port": json.RawMessage("8087")}}}
	policy, policySHA256, err := EnvironmentQualificationSmokePolicyFor(request)
	if err != nil {
		t.Fatal(err)
	}
	restore := EnvironmentQualificationRestoreReceipt{RequestID: request.ID, Attempt: request.Attempt,
		CaptureInstanceID: request.ReservedInstanceID, InstanceID: "restored"}
	receipt := EnvironmentQualificationSmokeReceipt{RequestID: request.ID, Attempt: request.Attempt, GraphID: request.GraphID,
		CaptureInstanceID: request.ReservedInstanceID, InstanceID: restore.InstanceID, Resource: request.Resource,
		PolicyID: policy.ID, PolicySHA256: policySHA256, ResultSHA256: strings.Repeat("b", 64)}
	if !qualificationSmokeReceiptMatchesRequest(receipt, request, restore) {
		t.Fatal("matching reviewed smoke receipt rejected")
	}
	receipt.PolicySHA256 = strings.Repeat("c", 64)
	if qualificationSmokeReceiptMatchesRequest(receipt, request, restore) {
		t.Fatal("receipt with substituted policy digest was counted")
	}
}
