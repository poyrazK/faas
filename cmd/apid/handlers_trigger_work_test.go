package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestTriggerWorkBindingAPIRequiresFreshDisabledTrigger(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID,
		Slug: "trigger-work-" + uuid.NewString(), RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.UpsertAppWorkPolicy(t.Context(), e.acct.ID, app.ID,
		workpolicy.Policy{Name: "orders", MaxRunningPerKey: 1}); err != nil {
		t.Fatal(err)
	}
	create := func(slug string, enabled bool) string {
		t.Helper()
		rec := e.do(t, http.MethodPost, "/v1/triggers", api.CreateTriggerRequest{
			AppID: app.ID, Kind: api.TriggerKindKafka, Slug: slug, Enabled: &enabled,
			Config: []byte(`{"brokers":["localhost:9092"],"topic":"orders","group":"workers"}`),
		}, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create trigger = %d: %s", rec.Code, rec.Body.String())
		}
		var trigger api.Trigger
		if err := json.Unmarshal(rec.Body.Bytes(), &trigger); err != nil {
			t.Fatal(err)
		}
		return trigger.ID
	}
	activeID := create("active", true)
	binding := api.TriggerWorkBinding{PolicyName: "orders", Key: "order_id"}
	put := func(id string, value api.TriggerWorkBinding) *http.Response {
		t.Helper()
		rec := e.do(t, http.MethodPut, "/v1/triggers/"+id+"/work-binding", value,
			map[string]string{"Idempotency-Key": uuid.NewString()})
		return rec.Result()
	}
	if resp := put(activeID, binding); resp.StatusCode != http.StatusConflict {
		t.Fatalf("binding enabled trigger = %d", resp.StatusCode)
	}
	id := create("disabled", false)
	if resp := put(id, api.TriggerWorkBinding{PolicyName: "orders", Key: "order_id.*"}); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid selector = %d", resp.StatusCode)
	}
	if resp := put(id, binding); resp.StatusCode != http.StatusOK {
		t.Fatalf("binding disabled trigger = %d", resp.StatusCode)
	}
	got := e.do(t, http.MethodGet, "/v1/triggers/"+id+"/work-binding", nil, nil)
	if got.Code != http.StatusOK {
		t.Fatalf("get binding = %d: %s", got.Code, got.Body.String())
	}
	var read api.TriggerWorkBinding
	if err := json.Unmarshal(got.Body.Bytes(), &read); err != nil || read != binding {
		t.Fatalf("get binding = %+v, err=%v", read, err)
	}
	removed := e.do(t, http.MethodDelete, "/v1/triggers/"+id+"/work-binding", nil, nil)
	if removed.Code != http.StatusNoContent {
		t.Fatalf("delete binding = %d: %s", removed.Code, removed.Body.String())
	}
}
