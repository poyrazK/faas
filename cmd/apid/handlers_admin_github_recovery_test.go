package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
)

const (
	testGithubDeliveryID   = "11111111-1111-1111-1111-111111111111"
	testGithubDeploymentID = "22222222-2222-2222-2222-222222222222"
)

type githubRecoveryClientFake struct {
	stubGithubdClient
	items             githubdgrpc.RecoveryQueueItems
	retriedDeliveryID string
	retriedCheckID    string
}

func (f *githubRecoveryClientFake) ListRecoveryQueueItems(context.Context, string, int) (githubdgrpc.RecoveryQueueItems, error) {
	return f.items, nil
}

func (f *githubRecoveryClientFake) RetryWebhookDelivery(_ context.Context, id string) (bool, error) {
	f.retriedDeliveryID = id
	return true, nil
}

func (f *githubRecoveryClientFake) RetryCheckUpdate(_ context.Context, id string) (bool, error) {
	f.retriedCheckID = id
	return true, nil
}

func TestGithubRecoveryStatusUsesGithubdProjection(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	fake := &githubRecoveryClientFake{items: githubdgrpc.RecoveryQueueItems{
		Deliveries:   []githubdgrpc.WebhookDeliveryRecord{{DeliveryID: testGithubDeliveryID, Status: "dead", UpdatedAt: time.Now()}},
		CheckUpdates: []githubdgrpc.CheckUpdateRecord{{DeploymentID: testGithubDeploymentID, Status: "dead", UpdatedAt: time.Now()}},
	}}
	e.s.githubd = fake
	e.h = e.s.handler()
	rec := e.do(t, http.MethodGet, "/v1/admin/ops/github/recovery?status=dead&limit=25", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var response api.GithubRecoveryStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Deliveries) != 1 || response.Deliveries[0].DeliveryID != testGithubDeliveryID || len(response.CheckUpdates) != 1 {
		t.Fatalf("response = %+v", response)
	}
}

func TestGithubRecoveryRetryIsAuditedWithTrace(t *testing.T) {
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	fake := &githubRecoveryClientFake{}
	e.s.githubd = fake
	e.h = e.s.handler()
	path := "/v1/admin/ops/github/deliveries/" + testGithubDeliveryID + "/retry?confirm=true&reason=github_incident_123"
	rec := e.doAdmin(t, http.MethodPost, path, nil, map[string]string{"X-Trace-Id": traceID})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if fake.retriedDeliveryID != testGithubDeliveryID || rec.Header().Get("X-Trace-Id") != traceID {
		t.Fatalf("retry id = %q trace = %q", fake.retriedDeliveryID, rec.Header().Get("X-Trace-Id"))
	}
	events, err := e.store.ListEvents(context.Background(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[0].Kind != "operator.action.github_delivery_retry" {
		t.Fatalf("events = %+v", events)
	}
	if events[0].TraceID == nil || *events[0].TraceID != traceID {
		t.Fatalf("audit trace = %v, want %s", events[0].TraceID, traceID)
	}
}

func TestGithubRecoveryRetryRequiresReasonAndConfirmation(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	e.s.githubd = &githubRecoveryClientFake{}
	e.h = e.s.handler()
	base := "/v1/admin/ops/github/check-updates/" + testGithubDeploymentID + "/retry"
	if rec := e.doAdmin(t, http.MethodPost, base+"?reason=incident_123", nil, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing confirm status = %d", rec.Code)
	}
	if rec := e.doAdmin(t, http.MethodPost, base+"?confirm=true", nil, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing reason status = %d", rec.Code)
	}
}
