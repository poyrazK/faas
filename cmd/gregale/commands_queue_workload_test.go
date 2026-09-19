package main

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type queueWorkloadFakeClient struct {
	app             api.AppResponse
	bindings        []api.QueueBindingResponse
	created         []api.CreateQueueBindingRequest
	updatedBindings []api.UpdateQueueBindingRequest
	updatedApps     []api.UpdateAppRequest
}

func (f *queueWorkloadFakeClient) GetApp(context.Context, string) (api.AppResponse, error) {
	return f.app, nil
}

func (f *queueWorkloadFakeClient) UpdateApp(_ context.Context, _ string, req api.UpdateAppRequest) (api.AppResponse, error) {
	f.updatedApps = append(f.updatedApps, req)
	f.app.ScalingPolicy = req.ScalingPolicy
	if req.ScalingPolicy != nil {
		f.app.MinInstances = req.ScalingPolicy.MinInstances
	}
	return f.app, nil
}

func (f *queueWorkloadFakeClient) ListQueueBindings(context.Context, string) ([]api.QueueBindingResponse, error) {
	return f.bindings, nil
}

func (f *queueWorkloadFakeClient) CreateQueueBinding(_ context.Context, _ string, req api.CreateQueueBindingRequest) (api.QueueBindingResponse, error) {
	f.created = append(f.created, req)
	row := api.QueueBindingResponse{ID: "binding-1", Name: req.Name, QueueName: req.QueueName, Mode: req.Mode, WorkloadClass: req.WorkloadClass, Enabled: true, MaxConcurrency: req.MaxConcurrency}
	f.bindings = append(f.bindings, row)
	return row, nil
}

func (f *queueWorkloadFakeClient) UpdateQueueBinding(_ context.Context, _ string, id string, req api.UpdateQueueBindingRequest) (api.QueueBindingResponse, error) {
	f.updatedBindings = append(f.updatedBindings, req)
	for i := range f.bindings {
		if f.bindings[i].ID != id {
			continue
		}
		if req.QueueName != nil {
			f.bindings[i].QueueName = *req.QueueName
		}
		if req.Mode != nil {
			f.bindings[i].Mode = *req.Mode
		}
		if req.WorkloadClass != nil {
			f.bindings[i].WorkloadClass = *req.WorkloadClass
		}
		if req.Enabled != nil {
			f.bindings[i].Enabled = *req.Enabled
		}
		if req.MaxConcurrency != nil {
			f.bindings[i].MaxConcurrency = *req.MaxConcurrency
		}
		return f.bindings[i], nil
	}
	return api.QueueBindingResponse{}, errors.New("binding not found")
}

func TestSetupQueueWorkloadCreatesPushBindingAndScaling(t *testing.T) {
	fake := &queueWorkloadFakeClient{app: api.AppResponse{Slug: "demo", WorkloadClass: "worker"}}
	result, err := setupQueueWorkload(context.Background(), fake, "demo", "", 10, 2, false)
	if err != nil {
		t.Fatalf("setupQueueWorkload() error = %v", err)
	}
	if !result.Created || len(fake.created) != 1 {
		t.Fatalf("created = %t, create calls = %d; want one create", result.Created, len(fake.created))
	}
	request := fake.created[0]
	if request.Name != "default" || request.QueueName != "default" || request.Mode != "push" || request.WorkloadClass != "worker" || request.MaxConcurrency != 2 {
		t.Fatalf("create request = %+v", request)
	}
	if len(fake.updatedApps) != 1 || fake.updatedApps[0].ScalingPolicy == nil {
		t.Fatalf("scaling updates = %+v; want one policy update", fake.updatedApps)
	}
	policy := fake.updatedApps[0].ScalingPolicy
	if policy.Target == nil || policy.Target.Metric != "queue_depth" || policy.Target.Value != 10 || policy.MinInstances != 0 {
		t.Fatalf("scaling policy = %+v", policy)
	}
}

func TestSetupQueueWorkloadUpdatesExistingBindingIdempotently(t *testing.T) {
	fake := &queueWorkloadFakeClient{
		app: api.AppResponse{Slug: "demo", WorkloadClass: "worker", ScalingPolicy: &api.ScalingPolicy{
			MinInstances: 1, MaxInstances: 4, ScaleOutCooldownS: 9, ScaleInCooldownS: 90,
			Target: &api.ScalingTarget{Metric: "queue_depth", Value: 10},
		}},
		bindings: []api.QueueBindingResponse{{ID: "binding-1", Name: "default", QueueName: "orders", Mode: "pull", WorkloadClass: "worker", Enabled: false, MaxConcurrency: 1}},
	}
	result, err := setupQueueWorkload(context.Background(), fake, "demo", "orders", 10, 3, false)
	if err != nil {
		t.Fatalf("setupQueueWorkload() error = %v", err)
	}
	if result.Created || len(fake.created) != 0 || len(fake.updatedBindings) != 1 {
		t.Fatalf("created = %t, creates = %d, updates = %d; want update only", result.Created, len(fake.created), len(fake.updatedBindings))
	}
	if len(fake.updatedApps) != 1 {
		t.Fatalf("scaling updates = %d, want one to clear min_instances", len(fake.updatedApps))
	}
	policy := fake.updatedApps[0].ScalingPolicy
	if policy == nil || policy.MaxInstances != 4 || policy.ScaleOutCooldownS != 9 || policy.ScaleInCooldownS != 90 {
		t.Fatalf("existing scaling settings were not preserved: %+v", policy)
	}
}

func TestSetupQueueWorkloadRejectsUnexpectedDefaultBinding(t *testing.T) {
	fake := &queueWorkloadFakeClient{
		app:      api.AppResponse{Slug: "demo", WorkloadClass: "worker"},
		bindings: []api.QueueBindingResponse{{ID: "binding-1", Name: "default", QueueName: "payments"}},
	}
	_, err := setupQueueWorkload(context.Background(), fake, "demo", "orders", 10, 1, false)
	if !errors.Is(err, errQueueWorkloadBindingConflict) {
		t.Fatalf("error = %v, want default-binding conflict", err)
	}
	if len(fake.created) != 0 || len(fake.updatedBindings) != 0 || len(fake.updatedApps) != 0 {
		t.Fatalf("conflict should not mutate state: creates=%d binding_updates=%d app_updates=%d", len(fake.created), len(fake.updatedBindings), len(fake.updatedApps))
	}
}
