// adr: 847
package main

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestHealthPollRetainsLastObservationOnCorruptState(t *testing.T) {
	e, app, _, bucket := entityAPIFixture(t, false)
	hook := entityOutboxHook(t, e, app)
	seedAPIOutbox(t, e, app, entityRequest("health"), hook.ID)
	e.s.durableEntityMetrics = newDurableEntityMetrics(prometheus.NewRegistry(), "apid")
	r := durableEntityHealthRotation{}
	e.s.pollDurableEntityHealth(t.Context(), &r)
	m := e.s.durableEntityMetrics.health
	if testutil.ToFloat64(m.pending.WithLabelValues("outbox")) != 1 || testutil.ToFloat64(m.lastRotation.WithLabelValues()) == 0 {
		t.Fatal("completed rotation lost pending work")
	}
	previous := testutil.ToFloat64(m.lastRotation.WithLabelValues())
	bucket.mu.Lock()
	for key, value := range bucket.objects {
		if strings.HasSuffix(key, "/manifest.json") {
			value.body = []byte(`{`)
			bucket.objects[key] = value
		}
	}
	bucket.mu.Unlock()
	e.s.pollDurableEntityHealth(t.Context(), &r)
	if testutil.ToFloat64(m.pollSuccess.WithLabelValues()) != 0 || testutil.ToFloat64(m.pending.WithLabelValues("outbox")) != 1 || testutil.ToFloat64(m.lastRotation.WithLabelValues()) != previous {
		t.Fatal("failed rotation published healthy empty backlog")
	}
}

func TestHealthRotationsAggregateWithoutIdentityLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	m := newDurableEntityMetrics(registry, "apid")
	r := durableEntityHealthRotation{}
	at := time.Now().Add(-time.Hour)
	r.add(durableentity.HealthPage{NextCursor: "page2", Samples: []durableentity.HealthSample{{AlarmPending: true, AlarmExhausted: true, AlarmAt: &at, OutboxPending: 2, OutboxUnknownAge: 1, OldestOutboxAt: &at}}})
	if r.cursor != "page2" || r.entities != 1 {
		t.Fatal(r)
	}
	r.add(durableentity.HealthPage{Samples: []durableentity.HealthSample{{OutboxPending: 3, OutboxExhausted: true}}})
	m.health.publish(r, time.Now())
	if testutil.ToFloat64(m.health.pending.WithLabelValues("outbox")) != 5 || testutil.ToFloat64(m.health.exhausted.WithLabelValues("alarm")) != 1 || testutil.ToFloat64(m.health.exhausted.WithLabelValues("outbox")) != 1 || testutil.ToFloat64(m.health.unknownAge.WithLabelValues()) != 1 {
		t.Fatal("rotation aggregation lost observations")
	}
	for _, err := range []error{nil, durableentity.ErrRecoveryObsolete, durableentity.ErrBusy, durableentity.ErrUncertain, errors.New("private")} {
		m.observeResult("retry_outbox", durableentity.Result{}, err)
	}
	for _, outcome := range []string{"success", "conflict", "busy", "uncertain", "failed"} {
		if testutil.ToFloat64(m.outcomes.WithLabelValues("retry_outbox", outcome)) != 1 {
			t.Fatal(outcome)
		}
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				switch label.GetName() {
				case "operation", "outcome", "kind", "target":
				default:
					t.Fatal("identity label", label.GetName())
				}
			}
		}
	}
}

func TestHealthTracksRecoveryAndDeduplicatedHandoff(t *testing.T) {
	e, app, dispatch, request := apiRecoveryFixture(t, "outbox")
	e.s.durableEntityMetrics = newDurableEntityMetrics(prometheus.NewRegistry(), "apid")
	r := durableEntityHealthRotation{}
	e.s.pollDurableEntityHealth(t.Context(), &r)
	m := e.s.durableEntityMetrics
	if testutil.ToFloat64(m.health.exhausted.WithLabelValues("outbox")) != 1 {
		t.Fatal("exhaustion not observed")
	}
	id, _, problem := e.s.durableEntityIdentity((&http.Request{}).WithContext(t.Context()), e.acct, app, api.DurableEntityInvokeRequest{Namespace: request.Namespace, Key: request.Key})
	if problem != nil {
		t.Fatal(problem)
	}
	before, err := e.s.durableEntities.Read(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := e.s.durableEntities.PendingOutbox(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	original := pending.Messages[0]
	rec := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/entities/retry", request, nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code, rec.Body.String())
	}
	e.s.pollDurableEntityHealth(t.Context(), &r)
	if testutil.ToFloat64(m.health.exhausted.WithLabelValues("outbox")) != 0 || testutil.ToFloat64(m.health.pending.WithLabelValues("outbox")) != 1 || testutil.ToFloat64(m.outcomes.WithLabelValues("retry_outbox", "success")) != 1 {
		t.Fatal("recovery observations incorrect")
	}
	e.s.durableEntityOutboxEnabled = true
	if err := e.s.deliverDurableEntityOutbox(t.Context(), durableentity.OutboxWork{Entity: id, MessageID: request.HeadID}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.acceptDurableEntityOutbox(t.Context(), id, original); err != nil {
		t.Fatal("accepted identity failed deduplication", err)
	}
	deliveries, _, err := e.store.ListAppWebhookDeliveries(t.Context(), app.ID, original.Intent.WebhookID, 100, "")
	if err != nil || len(deliveries) != 1 || deliveries[0].ID != request.HeadID {
		t.Fatal(deliveries, err)
	}
	after, err := e.s.durableEntities.Read(t.Context(), id)
	if err != nil || after.Version != before.Version || string(after.Data) != string(before.Data) || dispatch.calls.Load() != 0 {
		t.Fatal("handoff altered business state or dispatched guest", after, err)
	}
	e.s.pollDurableEntityHealth(t.Context(), &r)
	if testutil.ToFloat64(m.health.pending.WithLabelValues("outbox")) != 0 {
		t.Fatal("acknowledged head retained in health observation")
	}
}
