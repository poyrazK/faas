// adr: 20261116120000 — a wake a baseline health probe alone caused is not
// billed: the sampler subtracts the wake window [woke_at, probe_done_at +
// tail] from the instance's resident seconds, never below zero, and only for
// that instance.
package meter

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSamplerLeavesPlatformProbeWakesUnbilled(t *testing.T) {
	perSecond := int64(api.BillableRAMMB(256))
	tail := int64(api.ProtectionProbeUnbilledTail / time.Second)
	tests := []struct {
		name     string
		instance string // "" = the seeded instance
		woke     time.Duration
		done     time.Duration
		wantMB   int64
	}{
		{name: "probe wake inside the minute", woke: 10 * time.Second, done: 14 * time.Second, wantMB: (60 - (4 + tail)) * perSecond},
		{name: "window past the minute never bills below zero", woke: 0, done: 59 * time.Second, wantMB: 0},
		{name: "another instance's wake bills in full", instance: "other", woke: 10 * time.Second, done: 14 * time.Second, wantMB: 60 * perSecond},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newLedgerStore()
			_, ins := seedRunningInstance(t, store, quietBase, quietBase.Add(time.Minute))
			recordComplete(t, store, quietBase.Add(-api.MeterCatchUpWindow), quietBase)
			id := ins.ID
			if tc.instance != "" {
				id = tc.instance
			}
			if err := store.RecordPlatformProbeWake(t.Context(), state.PlatformProbeWake{
				InstanceID: id, AppID: ins.AppID, WokeAt: quietBase.Add(tc.woke), ProbeDoneAt: quietBase.Add(tc.done),
			}); err != nil {
				t.Fatal(err)
			}
			now := quietBase.Add(time.Minute)
			sampler := NewSampler(store, nil, func() time.Time { return now })
			if _, err := tick(t, store, sampler, now); err != nil {
				t.Fatal(err)
			}
			if got, _ := store.appended(quietBase); got != tc.wantMB {
				t.Fatalf("billed %d MB-seconds, want %d", got, tc.wantMB)
			}
		})
	}
}
