// adr: 20261027125625 — in-guest publishing stops while the events
// capability is off for the app's account or platform-wide.
package main

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/capflags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGuestEventGateFollowsTheEventsFlag(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "guest-events-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "guest-events",
		Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	key, ok := platformflags.CapabilityFlag(eventsCapability)
	if !ok {
		t.Fatal("events has no platform flag")
	}
	allow := true
	for _, tc := range []struct {
		name string
		def  platformflags.Definition
		want error
	}{
		{"killed", platformflags.Definition{Enabled: false, Default: true}, errGuestEventsPaused},
		{"off for the account", platformflags.Definition{Enabled: true}, errGuestEventsPaused},
		{"released to the account", platformflags.Definition{Enabled: true,
			Rules: []platformflags.Rule{{ID: "partner", Accounts: []string{account.ID}, Value: &allow}}}, nil},
	} {
		set := platformflags.NewSet(map[string]platformflags.Definition{key: tc.def})
		decider, err := capflags.Load(set.Snapshot)
		if err != nil {
			t.Fatal(err)
		}
		gate := &guestEventGate{enabled: decider.Enabled}
		if err := gate.allow(ctx, store, app.ID); !errors.Is(err, tc.want) {
			t.Fatalf("%s: allow = %v, want %v", tc.name, err, tc.want)
		}
	}
	var none *guestEventGate
	if err := none.allow(ctx, store, app.ID); err != nil {
		t.Fatalf("a node without platform flags must allow: %v", err)
	}
}
