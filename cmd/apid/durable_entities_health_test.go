// adr: 937
package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/durable"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/durableentity"
)

func TestHealthPollRetainsLastObservationOnCorruptState(t *testing.T) {
	e, app, _, bucket := entityAPIFixture(t, false)
	hook := entityOutboxHook(t, e, app)
	seedAPIOutbox(t, e, app, entityRequest("health"), hook.ID)
	registry := prometheus.NewRegistry()
	e.s.durable.Metrics = durable.NewMetrics(registry, "apid")
	r := durable.HealthRotation{}
	e.s.durable.PollHealth(t.Context(), &r)
	if durableMetric(t, registry, "apid_durable_entity_health_pending_work", "target", "outbox") != 1 || durableMetric(t, registry, "apid_durable_entity_health_last_success_timestamp_seconds") == 0 {
		t.Fatal("completed rotation lost pending work")
	}
	previous := durableMetric(t, registry, "apid_durable_entity_health_last_success_timestamp_seconds")
	bucket.mu.Lock()
	for key, value := range bucket.objects {
		if strings.HasSuffix(key, "/manifest.json") {
			value.body = []byte(`{`)
			bucket.objects[key] = value
		}
	}
	bucket.mu.Unlock()
	e.s.durable.PollHealth(t.Context(), &r)
	if durableMetric(t, registry, "apid_durable_entity_health_poll_success") != 0 || durableMetric(t, registry, "apid_durable_entity_health_pending_work", "target", "outbox") != 1 || durableMetric(t, registry, "apid_durable_entity_health_last_success_timestamp_seconds") != previous {
		t.Fatal("failed rotation published healthy empty backlog")
	}
}

func TestHealthTracksRecoveryAndDeduplicatedHandoff(t *testing.T) {
	e, app, dispatch, request := apiRecoveryFixture(t, "outbox")
	registry := prometheus.NewRegistry()
	e.s.durable.Metrics = durable.NewMetrics(registry, "apid")
	r := durable.HealthRotation{}
	e.s.durable.PollHealth(t.Context(), &r)
	if durableMetric(t, registry, "apid_durable_entity_health_exhausted_entities", "target", "outbox") != 1 {
		t.Fatal("exhaustion not observed")
	}
	id, _, problem := e.s.durable.Identity((&http.Request{}).WithContext(t.Context()), e.acct, app, api.DurableEntityInvokeRequest{Namespace: request.Namespace, Key: request.Key})
	if problem != nil {
		t.Fatal(problem)
	}
	before, err := e.s.durable.Engine.Read(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := e.s.durable.Engine.PendingOutbox(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	original := pending.Messages[0]
	rec := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/entities/retry", request, nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code, rec.Body.String())
	}
	e.s.durable.PollHealth(t.Context(), &r)
	if durableMetric(t, registry, "apid_durable_entity_health_exhausted_entities", "target", "outbox") != 0 || durableMetric(t, registry, "apid_durable_entity_health_pending_work", "target", "outbox") != 1 || durableMetric(t, registry, "apid_durable_entity_operations_total", "operation", "retry_outbox", "outcome", "success") != 1 {
		t.Fatal("recovery observations incorrect")
	}
	e.s.durable.OutboxEnabled = true
	if err := e.s.durable.DeliverOutbox(t.Context(), durableentity.OutboxWork{Entity: id, MessageID: request.HeadID}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.durable.AcceptOutbox(t.Context(), id, original); err != nil {
		t.Fatal("accepted identity failed deduplication", err)
	}
	deliveries, _, err := e.store.ListAppWebhookDeliveries(t.Context(), app.ID, original.Intent.WebhookID, 100, "")
	if err != nil || len(deliveries) != 1 || deliveries[0].ID != request.HeadID {
		t.Fatal(deliveries, err)
	}
	after, err := e.s.durable.Engine.Read(t.Context(), id)
	if err != nil || after.Version != before.Version || string(after.Data) != string(before.Data) || dispatch.calls.Load() != 0 {
		t.Fatal("handoff altered business state or dispatched guest", after, err)
	}
	e.s.durable.PollHealth(t.Context(), &r)
	if durableMetric(t, registry, "apid_durable_entity_health_pending_work", "target", "outbox") != 0 {
		t.Fatal("acknowledged head retained in health observation")
	}
}
