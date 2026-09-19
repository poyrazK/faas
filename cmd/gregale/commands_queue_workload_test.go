package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type queueWorkloadFakeClient struct {
	req    api.QueueWorkloadProfileRequest
	calls  int
	result api.QueueWorkloadProfileResponse
}

func (f *queueWorkloadFakeClient) ConfigureQueueWorkload(_ context.Context, _ string, req api.QueueWorkloadProfileRequest) (api.QueueWorkloadProfileResponse, error) {
	f.req = req
	f.calls++
	return f.result, nil
}

func TestSetupQueueWorkloadUsesServerProfileDefaults(t *testing.T) {
	fake := &queueWorkloadFakeClient{}
	_, err := setupQueueWorkload(context.Background(), fake, "demo", "", defaultQueueWorkloadTargetDepth, defaultQueueWorkloadConcurrency, false)
	if err != nil {
		t.Fatalf("setupQueueWorkload() error = %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("configure calls = %d, want 1", fake.calls)
	}
	if fake.req.QueueName != defaultQueueWorkloadQueueName || fake.req.TargetDepth != defaultQueueWorkloadTargetDepth || fake.req.MaxConcurrency != defaultQueueWorkloadConcurrency || fake.req.Force {
		t.Fatalf("profile request = %+v", fake.req)
	}
}

func TestSetupQueueWorkloadRejectsInvalidProfileBeforeNetwork(t *testing.T) {
	fake := &queueWorkloadFakeClient{}
	if _, err := setupQueueWorkload(context.Background(), fake, "demo", "orders", 0, 1, false); err == nil {
		t.Fatal("target depth zero should fail before calling the API")
	}
	if _, err := setupQueueWorkload(context.Background(), fake, "demo", "orders", 10, 0, false); err == nil {
		t.Fatal("max concurrency zero should fail before calling the API")
	}
	if fake.calls != 0 {
		t.Fatalf("configure calls = %d, want 0", fake.calls)
	}
}
