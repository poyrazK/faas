// adr: 1152
// central_lease_test.go pins ADR-1152 §4 for the central schedd: a second
// central schedd waits as a standby while another holds the
// central-scheduler lease, and the gate can be turned off.

package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestCentralSchedulerLeaseEnabled(t *testing.T) {
	for raw, want := range map[string]bool{"": true, "1": true, " 0 ": false, "0": false} {
		if got := centralSchedulerLeaseEnabled(raw); got != want {
			t.Errorf("centralSchedulerLeaseEnabled(%q) = %v, want %v", raw, got, want)
		}
	}
}

// adr: 1152
func TestCentralSchedulerLeaseStandbyWaitsForHolder(t *testing.T) {
	store := state.NewMemStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	active, stopA, err := acquireCentralSchedulerLease(context.Background(), store, log)
	if err != nil {
		t.Fatal(err)
	}
	defer stopA()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	standby, _, err := acquireCentralSchedulerLease(ctx, store, log)
	if err == nil {
		standby.Release()
		t.Fatal("a second central schedd acquired the lease while the first held it")
	}
	if ctx.Err() == nil {
		t.Fatalf("standby returned %v before its context ended", err)
	}
	active.Release()
}
