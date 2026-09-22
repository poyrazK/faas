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

func TestSetupQueueWorkloadPassesRetryPolicy(t *testing.T) {
	fake := &queueWorkloadFakeClient{}
	policy := &api.RetryPolicyDTO{MaxAttempts: 7, BaseSeconds: 2, MaxSeconds: 30, JitterSeconds: 0.5}
	_, err := setupQueueWorkloadWithRetry(context.Background(), fake, "demo", "orders", 5, 3, true, policy)
	if err != nil {
		t.Fatalf("setupQueueWorkloadWithRetry() error = %v", err)
	}
	if fake.req.RetryPolicy == nil || *fake.req.RetryPolicy != *policy {
		t.Fatalf("retry policy = %+v, want %+v", fake.req.RetryPolicy, policy)
	}
	if !fake.req.Force || fake.req.QueueName != "orders" || fake.req.TargetDepth != 5 || fake.req.MaxConcurrency != 3 {
		t.Fatalf("profile request = %+v", fake.req)
	}
}

func TestValidateQueueRetryPolicy(t *testing.T) {
	tests := []struct {
		name string
		p    *api.RetryPolicyDTO
		want bool
	}{
		{name: "nil", p: nil},
		{name: "valid", p: &api.RetryPolicyDTO{MaxAttempts: 3, BaseSeconds: 1, MaxSeconds: 10, JitterSeconds: 0.25}},
		{name: "attempts too high", p: &api.RetryPolicyDTO{MaxAttempts: 26}, want: true},
		{name: "base above max", p: &api.RetryPolicyDTO{BaseSeconds: 11, MaxSeconds: 10}, want: true},
		{name: "jitter above one", p: &api.RetryPolicyDTO{JitterSeconds: 1.1}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateQueueRetryPolicy(tt.p); (err != nil) != tt.want {
				t.Fatalf("validateQueueRetryPolicy(%+v) error = %v, want error=%t", tt.p, err, tt.want)
			}
		})
	}
}
