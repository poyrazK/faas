package main

import (
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPublicObjectStorageRootOnTheEdgeStore: the object-storage handlers
// assert the store they get to further interfaces (durable meters, request
// admission, tracked uploads). On 2026-10-21 rc.287's edge store lacked some
// and gatewayd-public refused to start with gateway safety accounting on.
func TestPublicObjectStorageRootOnTheEdgeStore(t *testing.T) {
	registry := &objectstorage.Registry{Accounting: api.ObjectStoragePolicy{AccountingMode: api.ObjectStorageGatewaySafetyV1}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := state.NewEdgeStore(nil)
	handler, err := publicObjectStorageRoot(store, store, registry, nil, func() bool { return true },
		testDomains(t, "", "apps.example"), http.NotFoundHandler(), log)
	if err != nil || handler == nil {
		t.Fatalf("publicObjectStorageRoot on the edge store: handler=%v err=%v", handler, err)
	}
}

// TestPublicReadAdmitterFollowsAccountingMode: under gateway safety public
// reads are admitted from cached decisions with their own metrics
// (ADR-20261030075301 step 2); other modes keep the locking admission.
func TestPublicReadAdmitterFollowsAccountingMode(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := state.NewEdgeStore(nil)
	reports := &objectstorage.Registry{}
	if reads, stop := startPublicReadAdmitter(t.Context(), store, reports, prometheus.NewRegistry(), log); reads != nil {
		stop()
		t.Fatal("provider-report accounting got a cached read admitter")
	}
	metrics := prometheus.NewRegistry()
	safety := &objectstorage.Registry{Accounting: api.ObjectStoragePolicy{AccountingMode: api.ObjectStorageGatewaySafetyV1}}
	reads, stop := startPublicReadAdmitter(t.Context(), store, safety, metrics, log)
	stop()
	if reads == nil {
		t.Fatal("gateway safety got no read admitter")
	}
	families, err := metrics.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if name := f.GetName(); name != "gatewayd_public_object_read_admission_decisions_total" && name != "gatewayd_public_object_read_usage_flushes_total" {
			t.Fatalf("unexpected metric %s", name)
		}
	}
	if err = metrics.Register(prometheus.NewCounter(prometheus.CounterOpts{Name: "gatewayd_public_object_read_usage_flushes_total"})); err == nil {
		t.Fatal("the flush counter was not registered under its edge name")
	}
}
