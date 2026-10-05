package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestQueueCommandsForwardEnvironment(t *testing.T) {
	for _, command := range []string{"binding", "queue", "inbox"} {
		t.Run(command, func(t *testing.T) {
			resetJSONOut(t)
			f := authedFakeAPI(t, `{"id":"queue-one","environment":"staging"}`, http.StatusCreated)
			var code int
			switch command {
			case "binding":
				client, err := authedClient()
				if err != nil {
					t.Fatal(err)
				}
				code = cmdQueueBindingCreate(client, []string{"worker", "--name", "orders", "--queue-name", "orders", "--environment", "staging"})
			case "queue":
				code = cmdQueueSend([]string{"worker", "--payload", `{}`, "--queue-name", "orders", "--environment", "staging"})
			case "inbox":
				code = cmdSend([]string{"worker", "--type", "order.created", "--data", `{}`, "--queue-name", "orders", "--environment", "staging"})
			}
			var body map[string]any
			if err := json.Unmarshal(f.sawBody, &body); err != nil || code != 0 || body["environment"] != "staging" || body["queue_name"] != "orders" {
				t.Fatalf("%s environment transport: %d %s %v", command, code, f.sawBody, err)
			}
		})
	}
}

type manifestScopeQueueClient struct {
	rows             []api.QueueBindingResponse
	created          []api.CreateQueueBindingRequest
	updated, deleted []string
}

func (f *manifestScopeQueueClient) ListQueueBindings(context.Context, string) ([]api.QueueBindingResponse, error) {
	return f.rows, nil
}
func (f *manifestScopeQueueClient) CreateQueueBinding(_ context.Context, _ string, req api.CreateQueueBindingRequest) (api.QueueBindingResponse, error) {
	f.created = append(f.created, req)
	return api.QueueBindingResponse{}, nil
}
func (f *manifestScopeQueueClient) UpdateQueueBinding(_ context.Context, _, id string, _ api.UpdateQueueBindingRequest) (api.QueueBindingResponse, error) {
	f.updated = append(f.updated, id)
	return api.QueueBindingResponse{}, nil
}
func (f *manifestScopeQueueClient) DeleteQueueBinding(_ context.Context, _, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func TestDeployManifestQueueBindingsPreservesNamedEnvironments(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `queue_bindings:
  - name: orders
    queue_name: orders
    mode: push
    workload_class: worker
`)
	f := &manifestScopeQueueClient{rows: []api.QueueBindingResponse{
		{ID: "production-orders", Name: "orders", Environment: "production"},
		{ID: "staging-stale", Name: "stale", Environment: "staging"},
		{ID: "shared-stale", Name: "legacy"},
	}}
	if err := deployManifestQueueBindings(context.Background(), f, "worker", dir); err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 1 || f.created[0].Name != "orders" || f.created[0].Environment != "" || len(f.updated) != 0 || len(f.deleted) != 1 || f.deleted[0] != "shared-stale" {
		t.Fatalf("manifest crossed ownership: create=%+v update=%v delete=%v", f.created, f.updated, f.deleted)
	}
	f = &manifestScopeQueueClient{rows: []api.QueueBindingResponse{
		{ID: "shared-orders", Name: "orders", QueueName: "old", Mode: "push", WorkloadClass: "worker", Enabled: true, MaxConcurrency: 1},
		{ID: "production-orders", Name: "orders", QueueName: "old", Environment: "production"},
	}}
	if err := deployManifestQueueBindings(context.Background(), f, "worker", dir); err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 0 || len(f.deleted) != 0 || len(f.updated) != 1 || f.updated[0] != "shared-orders" {
		t.Fatalf("manifest updated named binding: %+v", f)
	}
}
