package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestTriggerWorkBindingAPIRequiresFreshDisabledTrigger(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	app, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID,
		Slug: "trigger-work-" + uuid.NewString(), RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).UpsertAppWorkPolicy(t.Context(), (*e.Acct()).ID, app.ID,
		workpolicy.Policy{Name: "orders", MaxRunningPerKey: 1}); err != nil {
		t.Fatal(err)
	}
	create := func(slug string, enabled bool) string {
		t.Helper()
		rec := e.Do(t, http.MethodPost, "/v1/triggers", api.CreateTriggerRequest{
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
	put := func(id string, value api.TriggerWorkBinding) int {
		t.Helper()
		rec := e.Do(t, http.MethodPut, "/v1/triggers/"+id+"/work-binding", value,
			map[string]string{"Idempotency-Key": uuid.NewString()})
		return rec.Code
	}
	if status := put(activeID, binding); status != http.StatusConflict {
		t.Fatalf("binding enabled trigger = %d", status)
	}
	id := create("disabled", false)
	if status := put(id, api.TriggerWorkBinding{PolicyName: "orders", Key: "order_id.*"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("invalid selector = %d", status)
	}
	if status := put(id, binding); status != http.StatusOK {
		t.Fatalf("binding disabled trigger = %d", status)
	}
	got := e.Do(t, http.MethodGet, "/v1/triggers/"+id+"/work-binding", nil, nil)
	if got.Code != http.StatusOK {
		t.Fatalf("get binding = %d: %s", got.Code, got.Body.String())
	}
	var read api.TriggerWorkBinding
	if err := json.Unmarshal(got.Body.Bytes(), &read); err != nil || read != binding {
		t.Fatalf("get binding = %+v, err=%v", read, err)
	}
	removed := e.Do(t, http.MethodDelete, "/v1/triggers/"+id+"/work-binding", nil, nil)
	if removed.Code != http.StatusNoContent {
		t.Fatalf("delete binding = %d: %s", removed.Code, removed.Body.String())
	}
}
