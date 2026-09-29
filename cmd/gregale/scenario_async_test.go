package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type testAsyncRouteFake struct {
	slug string
	req  api.CreateEdgeRuleRequest
}

func (f *testAsyncRouteFake) CreateEdgeRule(_ context.Context, slug string, req api.CreateEdgeRuleRequest) (api.EdgeRuleResponse, error) {
	f.slug, f.req = slug, req
	return api.EdgeRuleResponse{ID: "rule-1"}, nil
}

func TestCreateTestAsyncRoutesUsesIsolatedAppHost(t *testing.T) {
	fake := &testAsyncRouteFake{}
	ids, err := createTestAsyncRoutes(t.Context(), fake, "isolated-worker", "https://isolated-worker.apps.example/", []testAsyncRoute{{Path: "/process", Methods: []string{"POST"}, RetryPolicy: &testRetryPolicy{MaxAttempts: 3, BaseSeconds: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "rule-1" || fake.slug != "isolated-worker" ||
		fake.req.MatchHost != "isolated-worker.apps.example" || fake.req.MatchPath != "/process" ||
		fake.req.Kind != "async" || !strings.Contains(string(fake.req.Action), `"max_attempts":3`) {
		t.Fatalf("created = %v, request = %+v", ids, fake.req)
	}
}

type testInvocationFake struct {
	rows  []api.Invocation
	calls int
}

func (f *testInvocationFake) GetInvocation(_ context.Context, _ string) (api.Invocation, error) {
	row := f.rows[f.calls]
	if f.calls < len(f.rows)-1 {
		f.calls++
	}
	return row, nil
}

func TestWaitForTestInvocationRequiresTargetWorkloadCompletion(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	fake := &testInvocationFake{rows: []api.Invocation{
		{ID: id, AppID: "worker-app", State: "pending"},
		{ID: id, AppID: "worker-app", State: "completed", Attempts: 2},
	}}
	condition := testInvocationOutput{Service: "worker", MinAttempts: 2}
	evidence, err := waitForTestInvocation(ctx, fake, condition, "worker-app", id)
	if err != nil || evidence.State != "completed" || evidence.Attempts != 2 {
		t.Fatalf("wait = (%+v, %v)", evidence, err)
	}
	fake = &testInvocationFake{rows: []api.Invocation{{ID: id, AppID: "other-app", State: "completed"}}}
	if _, err := waitForTestInvocation(t.Context(), fake, condition, "worker-app", id); err == nil {
		t.Fatal("cross-workload invocation accepted")
	}
	fake = &testInvocationFake{rows: []api.Invocation{{ID: id, AppID: "worker-app", State: "dead_letter", Attempts: 3}}}
	if _, err := waitForTestInvocation(t.Context(), fake, condition, "worker-app", id); err == nil {
		t.Fatal("dead-letter invocation accepted")
	}
}

func TestReadTestTriggerOutputRequiresBoundedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trigger.json")
	if err := os.WriteFile(path, []byte(`{"worker_invocation_id":"11111111-1111-4111-8111-111111111111"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	values, err := readTestTriggerOutput(path)
	if err != nil || values["worker_invocation_id"] == "" {
		t.Fatalf("read trigger output = (%v, %v)", values, err)
	}
	if err := os.WriteFile(path, []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readTestTriggerOutput(path); err == nil {
		t.Fatal("non-object trigger output accepted")
	}
}

func TestCustomerExportManifestDeclaresAsyncCompletion(t *testing.T) {
	scenarios, _, err := readTestManifest("../../tests/scenario-acceptance/gregale-test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	export := scenarios["customer-export"]
	if len(export.Services["worker"].AsyncRoutes) != 1 ||
		export.Services["worker"].AsyncRoutes[0].Path != "/process" ||
		len(export.WaitFor.Invocations) != 1 ||
		export.WaitFor.Invocations[0].TriggerKey != "worker_invocation_id" {
		t.Fatalf("customer export async contract = %+v", export)
	}
}
