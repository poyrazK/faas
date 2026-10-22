package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-1321: batch delivery is opt-in per trigger and refused for kinds whose
// records cannot share one invocation.
func TestTriggerBatchDelivery(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	app, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "delivery-app"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	batch, record, bogus := api.TriggerDeliveryBatch, api.TriggerDeliveryRecord, "stream"
	kafka := json.RawMessage(`{"brokers":["b:9092"],"topic":"orders","group":"g"}`)

	// Batch delivery is refused until the preview is enabled.
	rec := e.Do(t, http.MethodPost, "/v1/triggers", api.CreateTriggerRequest{AppID: app.ID, Kind: api.TriggerKindKafka, Slug: "orders", Config: kafka, Delivery: &batch}, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("batch delivery without the preview = %d %s, want 422", rec.Code, rec.Body)
	}
	(*e.S()).WithTriggerBatchDeliveryEnabled(true)

	rec = e.Do(t, http.MethodPost, "/v1/triggers", api.CreateTriggerRequest{AppID: app.ID, Kind: api.TriggerKindKafka, Slug: "orders", Config: kafka, Delivery: &batch}, nil)
	var created api.Trigger
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &created) != nil {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	if created.Delivery != batch || !created.Enabled {
		t.Fatalf("created = %+v, want enabled batch delivery", created)
	}

	rec = e.Do(t, http.MethodPost, "/v1/triggers", api.CreateTriggerRequest{AppID: app.ID, Kind: api.TriggerKindKafka, Slug: "plain", Config: kafka}, nil)
	var plain api.Trigger
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &plain) != nil || plain.Delivery != record {
		t.Fatalf("default delivery = %d %s", rec.Code, rec.Body)
	}

	rec = e.Do(t, http.MethodPost, "/v1/triggers", api.CreateTriggerRequest{AppID: app.ID, Kind: api.TriggerKindQueue, Slug: "jobs",
		Config: json.RawMessage(`{"mode":"delayed_task"}`), Delivery: &batch}, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("queue batch delivery = %d %s, want 422", rec.Code, rec.Body)
	}

	rec = e.Do(t, http.MethodPatch, "/v1/triggers/"+created.ID, api.UpdateTriggerRequest{Delivery: &record}, nil)
	var updated api.Trigger
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &updated) != nil || updated.Delivery != record {
		t.Fatalf("switch to record = %d %s", rec.Code, rec.Body)
	}
	if rec = e.Do(t, http.MethodPatch, "/v1/triggers/"+created.ID, api.UpdateTriggerRequest{Delivery: &bogus}, nil); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown delivery = %d %s, want 422", rec.Code, rec.Body)
	}
}
