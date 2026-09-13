package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestGuestVsockReceiverHealthReadinessAndBoundedMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	health := newGuestVsockReceiverHealth(reg)

	for _, receiver := range []string{guestReceiverEvents, guestReceiverIdentity} {
		ready, reason := health.Signal(receiver).Report()
		if ready || !strings.Contains(reason, "not registered") {
			t.Fatalf("initial %s signal = %v %q", receiver, ready, reason)
		}
	}

	health.Observe(fcvm.VsockGuestEventHostPort, "", nil)
	if ready, reason := health.Signal(guestReceiverEvents).Report(); !ready || reason != "" {
		t.Fatalf("registered event signal = %v %q", ready, reason)
	}
	if got := testutil.ToFloat64(health.up.WithLabelValues(guestReceiverEvents)); got != 1 {
		t.Fatalf("event receiver up = %v", got)
	}

	health.Observe(fcvm.VsockGuestEventHostPort, "read", errors.New("short frame"))
	if ready, reason := health.Signal(guestReceiverEvents).Report(); ready || !strings.Contains(reason, "read failed") {
		t.Fatalf("failed event signal = %v %q", ready, reason)
	}
	if got := testutil.ToFloat64(health.errors.WithLabelValues(guestReceiverEvents, "read")); got != 1 {
		t.Fatalf("read errors = %v", got)
	}
	if got := testutil.ToFloat64(health.up.WithLabelValues(guestReceiverEvents)); got != 0 {
		t.Fatalf("failed receiver up = %v", got)
	}

	// Protocol and overload failures are counted, but untrusted or excessive
	// guest input cannot hold the whole node out of service.
	health.Observe(fcvm.VsockGuestEventHostPort, "", nil)
	health.Observe(fcvm.VsockGuestEventHostPort, "protocol", errors.New("bad json"))
	if ready, _ := health.Signal(guestReceiverEvents).Report(); !ready {
		t.Fatal("protocol input degraded receiver readiness")
	}
	health.Observe(fcvm.VsockGuestEventHostPort, "unbounded-label", errors.New("boom"))
	if got := testutil.ToFloat64(health.errors.WithLabelValues(guestReceiverEvents, "unknown")); got != 1 {
		t.Fatalf("unknown errors = %v", got)
	}
	if ready, _ := health.Signal(guestReceiverEvents).Report(); ready {
		t.Fatal("unknown transport failure did not degrade readiness")
	}

	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		for _, metric := range family.Metric {
			if len(metric.Label) > 2 {
				t.Fatalf("metric %s has unbounded labels: %+v", family.GetName(), metric.Label)
			}
		}
	}
}
