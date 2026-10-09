package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type fakeExclusiveOperationsReconcileClient struct {
	apps        map[string]api.AppResponse
	jobs        []api.JobResponse
	crons       map[string][]api.CronResponse
	webhooks    map[string][]api.InboundWebhookEndpointResponse
	triggers    map[string][]api.Trigger
	policies    []api.ExclusivePolicyRequest
	bindings    []resolvedExclusiveManifestBinding
	appErr      error
	resourceErr error
}

func (f *fakeExclusiveOperationsReconcileClient) ListJobs(_ context.Context, pagination ...int) (api.ListJobsResponse, error) {
	return api.ListJobsResponse{Jobs: append([]api.JobResponse(nil), f.jobs...), Total: len(f.jobs), NextOffset: -1}, nil
}

func (f *fakeExclusiveOperationsReconcileClient) GetApp(_ context.Context, slug string) (api.AppResponse, error) {
	if f.appErr != nil {
		return api.AppResponse{}, f.appErr
	}
	app, ok := f.apps[slug]
	if !ok {
		return api.AppResponse{}, errors.New("app not found")
	}
	return app, nil
}

func (f *fakeExclusiveOperationsReconcileClient) UpsertExclusiveWorkPolicy(_ context.Context, _ string, req api.ExclusivePolicyRequest) (api.ExclusiveWorkPolicyRecord, error) {
	f.policies = append(f.policies, req)
	return api.ExclusiveWorkPolicyRecord{}, nil
}

func (f *fakeExclusiveOperationsReconcileClient) ListCrons(_ context.Context, slug string) ([]api.CronResponse, error) {
	if f.resourceErr != nil {
		return nil, f.resourceErr
	}
	return f.crons[slug], nil
}

func (f *fakeExclusiveOperationsReconcileClient) ListInboundWebhookEndpoints(_ context.Context, slug string) ([]api.InboundWebhookEndpointResponse, error) {
	if f.resourceErr != nil {
		return nil, f.resourceErr
	}
	return f.webhooks[slug], nil
}

func (f *fakeExclusiveOperationsReconcileClient) GetTriggers(_ context.Context, appID string, kind api.TriggerKind) ([]api.Trigger, error) {
	if f.resourceErr != nil {
		return nil, f.resourceErr
	}
	for _, trigger := range f.triggers[appID] {
		if trigger.Kind == kind {
			return []api.Trigger{trigger}, nil
		}
	}
	return nil, nil
}

func (f *fakeExclusiveOperationsReconcileClient) UpsertExclusiveTriggerBinding(_ context.Context, source, triggerID string, req api.ExclusiveTriggerBindingRequest) (api.ExclusiveTriggerBindingRecord, error) {
	f.bindings = append(f.bindings, resolvedExclusiveManifestBinding{source: source, trigger: triggerID, request: req})
	return api.ExclusiveTriggerBindingRecord{Source: source, TriggerID: triggerID, Policy: req.Policy}, nil
}

func writeOperationsManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestReconcileExclusiveOperationsResolvesDeclaredTriggersBeforeApplying(t *testing.T) {
	dir := writeOperationsManifest(t, `exclusive_operations:
  policies:
    - name: crm-sync
      scope: account
      member_apps: [crm-api]
      contention: queue
      lease_seconds: 30
      max_attempt_seconds: 600
  bindings:
    - source: cron
      app: crm-api
      schedule: "*/15 * * * *"
      path: /sync
      policy: crm-sync
      key: crm-sync
    - source: inbound_webhook
      app: crm-api
      name: stripe-events
      policy: crm-sync
      key: stripe-sync
    - source: broker
      app: crm-api
      kind: kafka
      name: crm-events
      policy: crm-sync
      key: 42
`)
	client := &fakeExclusiveOperationsReconcileClient{
		apps:     map[string]api.AppResponse{"crm-api": {ID: "app-1", Slug: "crm-api"}},
		crons:    map[string][]api.CronResponse{"crm-api": {{ID: "cron-1", Kind: "http", Schedule: "*/15 * * * *", Path: "/sync"}}},
		webhooks: map[string][]api.InboundWebhookEndpointResponse{"crm-api": {{ID: "webhook-1", Name: "stripe-events"}}},
		triggers: map[string][]api.Trigger{"app-1": {{ID: "trigger-1", AppID: "app-1", Kind: api.TriggerKindKafka, Slug: "crm-events"}}},
	}
	report, err := reconcileExclusiveOperations(context.Background(), client, dir)
	if err != nil {
		t.Fatal(err)
	}
	if report != (exclusiveOperationsReconcileReport{Policies: 1, Bindings: 3}) {
		t.Fatalf("report=%+v", report)
	}
	if len(client.policies) != 1 || len(client.policies[0].MemberAppIDs) != 1 || client.policies[0].MemberAppIDs[0] != "app-1" {
		t.Fatalf("policies=%+v", client.policies)
	}
	if len(client.bindings) != 3 {
		t.Fatalf("bindings=%+v", client.bindings)
	}
	want := map[string]string{"cron": "cron-1", "inbound_webhook": "webhook-1", "broker": "trigger-1"}
	for _, binding := range client.bindings {
		if want[binding.source] != binding.trigger {
			t.Errorf("%s trigger=%q, want %q", binding.source, binding.trigger, want[binding.source])
		}
		if binding.request.Policy != "crm-sync" || !json.Valid(binding.request.Key) {
			t.Errorf("binding request=%+v", binding.request)
		}
	}
	for _, binding := range client.bindings {
		if binding.trigger == "trigger-1" && string(binding.request.Key) != "42" {
			t.Fatalf("broker business key=%s, want 42", binding.request.Key)
		}
	}
}

func TestReconcileExclusiveOperationsResolvesSelectedJobIDsBeforeApplying(t *testing.T) {
	const jobID = "5e57edce-03ad-4a1e-8a92-e2bd5e0a4253"
	dir := writeOperationsManifest(t, `exclusive_operations:
  policies:
    - name: nightly-import
      scope: account
      member_job_ids: [5e57edce-03ad-4a1e-8a92-e2bd5e0a4253]
      contention: queue
      lease_seconds: 30
      max_attempt_seconds: 600
  bindings:
    - source: job_schedule
      job: nightly-import
      policy: nightly-import
      key: scheduled-import
`)
	client := &fakeExclusiveOperationsReconcileClient{jobs: []api.JobResponse{{ID: jobID, Name: "nightly-import", Kind: "recurring", Schedule: "0 2 * * *"}}}
	report, err := reconcileExclusiveOperations(context.Background(), client, dir)
	if err != nil {
		t.Fatal(err)
	}
	if report.Policies != 1 || len(client.policies) != 1 || len(client.policies[0].MemberJobIDs) != 1 || client.policies[0].MemberJobIDs[0] != jobID {
		t.Fatalf("job member reconciliation report=%+v policy=%+v", report, client.policies)
	}
	if report.Bindings != 1 || len(client.bindings) != 1 || client.bindings[0].source != "job_schedule" || client.bindings[0].trigger != jobID {
		t.Fatalf("managed Job schedule binding=%+v report=%+v", client.bindings, report)
	}

	client = &fakeExclusiveOperationsReconcileClient{}
	if _, err := reconcileExclusiveOperations(context.Background(), client, dir); err == nil || !strings.Contains(err.Error(), "no Job with that ID") {
		t.Fatalf("missing selected Job error=%v", err)
	}
	if len(client.policies) != 0 {
		t.Fatalf("applied policies before resolving missing Job: %+v", client.policies)
	}
}

func TestReconcileExclusiveOperationsDoesNotWriteWhenASelectorIsMissing(t *testing.T) {
	dir := writeOperationsManifest(t, `exclusive_operations:
  policies:
    - name: crm-sync
      scope: account
      member_apps: [crm-api]
      contention: queue
      lease_seconds: 30
      max_attempt_seconds: 600
  bindings:
    - source: broker
      app: crm-api
      kind: kafka
      name: missing-trigger
      policy: crm-sync
      key: sync
`)
	client := &fakeExclusiveOperationsReconcileClient{
		apps:     map[string]api.AppResponse{"crm-api": {ID: "app-1", Slug: "crm-api"}},
		triggers: map[string][]api.Trigger{"app-1": {{ID: "trigger-1", AppID: "app-1", Kind: api.TriggerKindKafka, Slug: "other-trigger"}}},
	}
	if _, err := reconcileExclusiveOperations(context.Background(), client, dir); err == nil {
		t.Fatal("missing trigger selector succeeded")
	}
	if len(client.policies) != 0 || len(client.bindings) != 0 {
		t.Fatalf("reconcile performed writes before selector validation: policies=%d bindings=%d", len(client.policies), len(client.bindings))
	}
}

func TestOperationLastErrorLineLabelsRecoveredAttempts(t *testing.T) {
	for _, tc := range []struct{ state, lastError, want string }{
		{state: "completed", want: ""},
		{state: "completed", lastError: "worker is temporarily unavailable", want: "Recovered after: worker is temporarily unavailable"},
		{state: "failed", lastError: "boom", want: "Last error: boom"},
		{state: "running", lastError: "worker is temporarily unavailable", want: "Last error: worker is temporarily unavailable"},
	} {
		if got := operationLastErrorLine(tc.state, tc.lastError); got != tc.want {
			t.Fatalf("operationLastErrorLine(%q, %q) = %q, want %q", tc.state, tc.lastError, got, tc.want)
		}
	}
}
