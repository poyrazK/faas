package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
)

type bootTrackingInvalidator struct {
	*fakeInvalidator
	booting map[string]bool
}

func (b *bootTrackingInvalidator) NoteInstanceBooting(_, instanceID string) {
	b.booting[instanceID] = true
}

func (b *bootTrackingInvalidator) NoteInstanceSettled(_, instanceID string) {
	delete(b.booting, instanceID)
}

// Prod 2026-11-15: the gateway needs to know another gateway's boot is in
// progress to wait for it at capacity. instance_changed waking/cold_booting
// marks the boot; any other state ends it.
func TestHandleInvalidationTracksBootsInProgress(t *testing.T) {
	inv := &bootTrackingInvalidator{fakeInvalidator: &fakeInvalidator{}, booting: map[string]bool{}}
	notify := func(instance, state string) {
		handleInvalidation(context.Background(), inv, db.Notification{Channel: db.NotifyInstanceChanged,
			Payload: `{"instance_id":"` + instance + `","app_id":"app-9","state":"` + state + `"}`}, testLogger())
	}
	notify("i-wake", "waking")
	notify("i-cold", "cold_booting")
	if !inv.booting["i-wake"] || !inv.booting["i-cold"] {
		t.Fatalf("booting = %v, want both announced boots", inv.booting)
	}
	notify("i-wake", "running")
	notify("i-cold", "failed")
	if len(inv.booting) != 0 {
		t.Fatalf("booting = %v after running/failed, want none", inv.booting)
	}
}
