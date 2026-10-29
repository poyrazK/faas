package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

type fakeRealtimeTriggerClient struct {
	triggers map[string]api.Trigger // by ID
	nextID   int
	calls    []string
}

func newFakeRealtimeTriggerClient(existing ...api.Trigger) *fakeRealtimeTriggerClient {
	f := &fakeRealtimeTriggerClient{triggers: map[string]api.Trigger{}}
	for _, t := range existing {
		f.triggers[t.ID] = t
	}
	return f
}

func (f *fakeRealtimeTriggerClient) GetApp(_ context.Context, slug string) (api.AppResponse, error) {
	return api.AppResponse{ID: "app-" + slug, Slug: slug}, nil
}

func (f *fakeRealtimeTriggerClient) GetTriggers(_ context.Context, appID string, kind api.TriggerKind) ([]api.Trigger, error) {
	var out []api.Trigger
	for _, t := range f.triggers {
		if t.AppID == appID && t.Kind == kind {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *fakeRealtimeTriggerClient) PostTriggers(_ context.Context, req api.CreateTriggerRequest) (api.Trigger, error) {
	f.nextID++
	t := api.Trigger{ID: fmt.Sprintf("t%d", f.nextID), AppID: req.AppID, Kind: req.Kind, Slug: req.Slug, Enabled: *req.Enabled, Config: req.Config}
	if req.Delivery != nil {
		t.Delivery = *req.Delivery
	}
	if req.BatchSizeMax != nil {
		t.BatchSizeMax = *req.BatchSizeMax
	}
	f.triggers[t.ID] = t
	f.calls = append(f.calls, "create "+req.Slug)
	return t, nil
}

func (f *fakeRealtimeTriggerClient) PatchTriggersId(_ context.Context, id string, req api.UpdateTriggerRequest) (api.Trigger, error) {
	t := f.triggers[id]
	var cfg api.RealtimeTriggerConfig
	_ = json.Unmarshal(t.Config, &cfg)
	_ = json.Unmarshal(req.Config, &cfg)
	t.Config, _ = json.Marshal(cfg)
	if req.Enabled != nil {
		t.Enabled = *req.Enabled
	}
	if req.Delivery != nil {
		t.Delivery = *req.Delivery
	}
	if req.BatchSizeMax != nil {
		t.BatchSizeMax = *req.BatchSizeMax
	}
	f.triggers[id] = t
	f.calls = append(f.calls, "update "+t.Slug)
	return t, nil
}

func (f *fakeRealtimeTriggerClient) DeleteTriggersId(_ context.Context, id string) error {
	f.calls = append(f.calls, "delete "+f.triggers[id].Slug)
	delete(f.triggers, id)
	return nil
}

func (f *fakeRealtimeTriggerClient) snapshot() map[string]api.RealtimeTriggerConfig {
	out := map[string]api.RealtimeTriggerConfig{}
	for _, t := range f.triggers {
		var cfg api.RealtimeTriggerConfig
		_ = json.Unmarshal(t.Config, &cfg)
		out[t.Slug] = cfg
	}
	return out
}

const testEndpoint = "6f1c2a4e-1d9b-4c7a-9a39-0b8d6f0f4c11"

func realtimeManifestTrigger(app, slug, group string, concurrency int) gregalemanifest.Trigger {
	cfg := map[string]any{"endpoint_id": testEndpoint, "channel": "orders", "group": group}
	if concurrency > 0 {
		cfg["max_concurrency"] = concurrency
	}
	return gregalemanifest.Trigger{Kind: gregalemanifest.TriggerKindRealtime, App: app, Slug: slug, Config: cfg}
}

func existingRealtimeTrigger(id, slug, group string, concurrency int) api.Trigger {
	raw, _ := json.Marshal(api.RealtimeTriggerConfig{EndpointID: testEndpoint, Channel: "orders", Group: group, MaxConcurrency: concurrency})
	return api.Trigger{ID: id, AppID: "app-orders-fn", Kind: api.TriggerKindRealtime, Slug: slug, Enabled: true, Config: raw}
}

func TestReconcileManifestRealtimeTriggers(t *testing.T) {
	cases := []struct {
		name      string
		existing  []api.Trigger
		triggers  []gregalemanifest.Trigger
		wantCalls []string
		want      map[string]string // slug -> group
	}{
		{
			name:      "creates a declared trigger",
			triggers:  []gregalemanifest.Trigger{realtimeManifestTrigger("orders-fn", "orders", "billing", 0)},
			wantCalls: []string{"create orders"},
			want:      map[string]string{"orders": "billing"},
		},
		{
			name:     "leaves a matching trigger alone",
			existing: []api.Trigger{existingRealtimeTrigger("e1", "orders", "billing", 0)},
			triggers: []gregalemanifest.Trigger{realtimeManifestTrigger("orders-fn", "orders", "billing", 1)},
			want:     map[string]string{"orders": "billing"},
		},
		{
			name:      "updates concurrency in place",
			existing:  []api.Trigger{existingRealtimeTrigger("e1", "orders", "billing", 0)},
			triggers:  []gregalemanifest.Trigger{realtimeManifestTrigger("orders-fn", "orders", "billing", 4)},
			wantCalls: []string{"update orders"},
			want:      map[string]string{"orders": "billing"},
		},
		{
			name:      "replaces a changed binding",
			existing:  []api.Trigger{existingRealtimeTrigger("e1", "orders", "billing", 0)},
			triggers:  []gregalemanifest.Trigger{realtimeManifestTrigger("orders-fn", "orders", "fulfilment", 0)},
			wantCalls: []string{"delete orders", "create orders"},
			want:      map[string]string{"orders": "fulfilment"},
		},
		{
			name:     "removes undeclared triggers",
			existing: []api.Trigger{existingRealtimeTrigger("e1", "orders", "billing", 0), existingRealtimeTrigger("e2", "audit", "audit", 0)},
			triggers: []gregalemanifest.Trigger{realtimeManifestTrigger("orders-fn", "orders", "billing", 0)},
			// The kept trigger is unchanged; only the stale one is removed.
			wantCalls: []string{"delete audit"},
			want:      map[string]string{"orders": "billing"},
		},
		{
			name:     "other apps' triggers do not opt this app in",
			existing: []api.Trigger{existingRealtimeTrigger("e1", "orders", "billing", 0)},
			triggers: []gregalemanifest.Trigger{realtimeManifestTrigger("other-fn", "orders", "billing", 0)},
			want:     map[string]string{"orders": "billing"},
		},
		{
			name:      "explicit empty list clears",
			existing:  []api.Trigger{existingRealtimeTrigger("e1", "orders", "billing", 0)},
			triggers:  []gregalemanifest.Trigger{},
			wantCalls: []string{"delete orders"},
			want:      map[string]string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newFakeRealtimeTriggerClient(tc.existing...)
			before := client.snapshot()
			txn := &manifestCronTransaction{}
			m := &gregalemanifest.Manifest{Triggers: tc.triggers}
			if err := reconcileManifestRealtimeTriggers(t.Context(), client, "orders-fn", m, txn, nil); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(client.calls, tc.wantCalls) {
				t.Fatalf("calls = %v, want %v", client.calls, tc.wantCalls)
			}
			got := map[string]string{}
			for slug, cfg := range client.snapshot() {
				got[slug] = cfg.Group
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("triggers = %v, want %v", got, tc.want)
			}
			// Rolling back restores the original set (IDs may differ).
			if err := txn.rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(client.snapshot()) != fmt.Sprint(before) {
				t.Fatalf("after rollback = %v, want %v", client.snapshot(), before)
			}
		})
	}
}

func TestFunctionRealtimeTriggersSelectsOneFunction(t *testing.T) {
	m := &gregalemanifest.Manifest{Triggers: []gregalemanifest.Trigger{
		realtimeManifestTrigger("notify", "orders", "billing", 0),
		realtimeManifestTrigger("thumbs", "images", "resize", 0),
		{Kind: gregalemanifest.TriggerKindCron, App: "notify", Schedule: "* * * * *", Path: "/"},
	}}
	fn := plannedFunction{spec: gregalemanifest.FunctionSpec{Name: "notify"}, slug: "shop-notify"}
	got := functionRealtimeTriggers(m, fn)
	if len(got.Triggers) != 1 || got.Triggers[0].App != "shop-notify" || got.Triggers[0].Slug != "orders" {
		t.Fatalf("selected = %+v, want the notify realtime trigger under its app slug", got.Triggers)
	}
	none := functionRealtimeTriggers(m, plannedFunction{spec: gregalemanifest.FunctionSpec{Name: "other"}, slug: "shop-other"})
	if none.Triggers != nil {
		t.Fatalf("a function without realtime triggers must not be managed: %+v", none.Triggers)
	}
	cleared := functionRealtimeTriggers(&gregalemanifest.Manifest{Triggers: []gregalemanifest.Trigger{}}, fn)
	if cleared.Triggers == nil || len(cleared.Triggers) != 0 {
		t.Fatalf("explicit empty triggers must clear: %+v", cleared.Triggers)
	}
}

func TestReconcileManifestRealtimeTriggerDelivery(t *testing.T) {
	client := newFakeRealtimeTriggerClient(existingRealtimeTrigger("e1", "orders", "billing", 0))
	want := realtimeManifestTrigger("orders-fn", "orders", "billing", 0)
	want.Delivery, want.BatchSizeMax = api.TriggerDeliveryBatch, 8
	txn := &manifestCronTransaction{}
	if err := reconcileManifestRealtimeTriggers(t.Context(), client, "orders-fn", &gregalemanifest.Manifest{Triggers: []gregalemanifest.Trigger{want}}, txn, nil); err != nil {
		t.Fatal(err)
	}
	got := client.triggers["e1"]
	if !slices.Equal(client.calls, []string{"update orders"}) || got.Delivery != api.TriggerDeliveryBatch || got.BatchSizeMax != 8 {
		t.Fatalf("calls = %v, trigger = %+v, want an in-place switch to batch of 8", client.calls, got)
	}
	// Rolling back restores record delivery of one message.
	if err := txn.rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := client.triggers["e1"]; got.Delivery != api.TriggerDeliveryRecord || got.BatchSizeMax != 1 {
		t.Fatalf("after rollback = %+v", got)
	}
	// Batch delivery without a size uses the server default.
	if got := realtimeTriggerBatchSize(api.TriggerDeliveryBatch, 0); got != api.RealtimeTriggerDefaultBatchSize {
		t.Fatalf("default batch = %d", got)
	}
}
