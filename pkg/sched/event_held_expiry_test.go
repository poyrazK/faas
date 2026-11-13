package sched_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/capflags"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestHeldEventDeliveryDeadLettersAfterHoldLimit: a workflow delivery held
// because workflows are off for its app waits, then moves to the dead-letter
// queue as delivery_held_expired once it has been held past
// api.EventDeliveryHoldMax, instead of being re-checked every minute forever
// (ADR-20261027125625 amendment 2026-11-13).
func TestHeldEventDeliveryDeadLettersAfterHoldLimit(t *testing.T) {
	for _, independent := range []bool{false, true} {
		t.Run(fmt.Sprintf("independent=%v", independent), func(t *testing.T) {
			ctx := t.Context()
			store := state.NewMemStore()
			account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanHobby)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "held-event", Type: state.AppTypeApp, RAMMB: 256})
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:abc", Workflows: json.RawMessage(`[{"name":"paid","trigger":{"type":"event","source":"orders","event_type":"order.created"},"steps":[{"name":"main","path":"/paid"}]}]`)})
			if err != nil {
				t.Fatal(err)
			}
			envelope, err := (events.Envelope{ID: uuid.NewString(), Source: "orders", Type: "order.created", Data: json.RawMessage(`{}`)}).Normalize(account.ID, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.AppendEvent(ctx, "apid", "event.published", &account.ID, payload); err != nil {
				t.Fatal(err)
			}
			workflowKey, _ := platformflags.CapabilityFlag("workflows")
			eventsKey, _ := platformflags.CapabilityFlag("events")
			deny := false
			flags := platformflags.NewSet(map[string]platformflags.Definition{
				workflowKey: {Enabled: true, Default: true, Rules: []platformflags.Rule{{ID: "deny", Accounts: []string{account.ID}, Value: &deny}}},
				eventsKey:   {Enabled: true, Default: true},
			})
			decider, err := capflags.Load(flags.Snapshot)
			if err != nil {
				t.Fatal(err)
			}
			loop := eventsSwitchLoop(t, store, decider, independent).WithWorkflowsDispatched(true)
			deadLetters := func() []state.DeadLetterEvent {
				t.Helper()
				entries, err := store.ListDeadLetterEvents(ctx, app.ID, 10, "")
				if err != nil {
					t.Fatal(err)
				}
				return entries
			}
			now := time.Now().UTC()
			loop.XRunEventFanoutSweepAt(ctx, now)
			loop.XRunEventFanoutSweepAt(ctx, now.Add(api.EventDeliveryHoldMax/2))
			if got := deadLetters(); len(got) != 0 {
				t.Fatalf("delivery dead-lettered while still within the hold limit: %+v", got)
			}
			loop.XRunEventFanoutSweepAt(ctx, now.Add(api.EventDeliveryHoldMax+time.Hour))
			got := deadLetters()
			if len(got) != 1 || got[0].Source != state.DeadLetterSourceEventRoute || got[0].ErrorKind != state.EventFanoutFailureCodeDeliveryHeldExpired {
				t.Fatalf("dead letters after the hold limit = %+v, want one %s", got, state.EventFanoutFailureCodeDeliveryHeldExpired)
			}
			_, total, err := store.ListWorkflowRuns(ctx, app.ID, state.ListWorkflowRunsOpts{Limit: 10})
			if err != nil || total != 0 {
				t.Fatalf("held workflow admitted %d runs: %v", total, err)
			}
		})
	}
}

// TestEventDeliveryHoldRecheckBacksOff: a held delivery is checked again
// after a tenth of its wait, between one minute and one hour, and never past
// its hold limit.
func TestEventDeliveryHoldRecheckBacksOff(t *testing.T) {
	accepted := time.Unix(1_800_000_000, 0).UTC()
	for _, tc := range []struct {
		waited, want time.Duration
	}{
		{0, time.Minute},
		{30 * time.Minute, 3 * time.Minute},
		{2 * 24 * time.Hour, time.Hour},
		{api.EventDeliveryHoldMax - 10*time.Minute, 10 * time.Minute},
	} {
		now := accepted.Add(tc.waited)
		if got := state.EventDeliveryHoldRecheck(accepted, now).Sub(now); got != tc.want {
			t.Fatalf("held %s: recheck in %s, want %s", tc.waited, got, tc.want)
		}
	}
	if state.EventDeliveryHoldExpired(accepted, accepted.Add(api.EventDeliveryHoldMax-time.Second)) ||
		!state.EventDeliveryHoldExpired(accepted, accepted.Add(api.EventDeliveryHoldMax)) {
		t.Fatal("hold limit boundary")
	}
	if state.EventDeliveryHoldExpired(time.Time{}, accepted) {
		t.Fatal("an unknown acceptance time expired")
	}
}
