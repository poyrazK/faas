// adr: 1152
// meterd_lease_test.go pins ADR-1152 §4 for meterd: a second meterd waits as
// a standby while another holds the meterd lease, and the gate can be turned
// off.

package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestMeterdLeaseEnabled(t *testing.T) {
	if !meterdLeaseEnabled(nil, meterdLeaseEnv) {
		t.Fatal("nil getenv disabled the gate")
	}
	for raw, want := range map[string]bool{"": true, "1": true, "0": false, " 0": false} {
		getenv := func(string) string { return raw }
		if got := meterdLeaseEnabled(getenv, meterdLeaseEnv); got != want {
			t.Errorf("meterdLeaseEnabled(%q) = %v, want %v", raw, got, want)
		}
	}
}

// adr: 1152
func TestMeterdLeaseStandbyWaitsForHolder(t *testing.T) {
	store := state.NewMemStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	active, stop, err := acquireMeterdLease(context.Background(), store, log)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if standby, _, err := acquireMeterdLease(ctx, store, log); err == nil {
		standby.Release(context.Background())
		t.Fatal("a second meterd acquired the lease while the first held it")
	}
	active.Release(context.Background())
}
