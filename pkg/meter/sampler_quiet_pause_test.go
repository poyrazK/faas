// adr: 20261023171905 — a quiet-paused second is not a billed second: the
// sampler subtracts what the pause ledger proves, and nothing it cannot.
package meter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var quietBase = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

func TestSamplerSubtractsProvenQuietPauses(t *testing.T) {
	perSecond := int64(api.BillableRAMMB(256))
	tests := []struct {
		name         string
		mode         QuietPauseMode
		paused       *state.QuietPausedSeconds
		wantMB       int64
		wantUnbilled float64
		wantUnproven float64
	}{
		{name: "off bills residency and reads no ledger", mode: QuietPauseOff, paused: &state.QuietPausedSeconds{Seconds: 50}, wantMB: 60 * perSecond},
		{name: "shadow counts the pause but bills residency", mode: QuietPauseShadow, paused: &state.QuietPausedSeconds{Seconds: 50}, wantMB: 60 * perSecond, wantUnbilled: 50},
		{name: "on bills only the running seconds", mode: QuietPauseOn, paused: &state.QuietPausedSeconds{Seconds: 50}, wantMB: 10 * perSecond, wantUnbilled: 50},
		{name: "on never bills below zero", mode: QuietPauseOn, paused: &state.QuietPausedSeconds{Seconds: 75}, wantMB: 0, wantUnbilled: 60},
		{name: "an unproven pause bills what the ledger could not prove", mode: QuietPauseOn, paused: &state.QuietPausedSeconds{Seconds: 30, Unproven: true}, wantMB: 30 * perSecond, wantUnbilled: 30, wantUnproven: 1},
		{name: "no pause bills residency", mode: QuietPauseOn, wantMB: 60 * perSecond},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newLedgerStore()
			_, ins := seedRunningInstance(t, store, quietBase, quietBase.Add(time.Minute))
			recordComplete(t, store, quietBase.Add(-api.MeterCatchUpWindow), quietBase)
			if tc.paused != nil {
				store.paused[quietBase] = map[string]state.QuietPausedSeconds{ins.ID: *tc.paused}
			}
			now := quietBase.Add(time.Minute)
			sampler := NewSampler(store, nil, func() time.Time { return now }).WithQuietPause(tc.mode, prometheus.NewRegistry())
			if _, err := tick(t, store, sampler, now); err != nil {
				t.Fatal(err)
			}
			if got, _ := store.appended(quietBase); got != tc.wantMB {
				t.Fatalf("billed %d MB-seconds, want %d", got, tc.wantMB)
			}
			if tc.mode == QuietPauseOff {
				if store.pausedReads != 0 || sampler.quietMetrics != nil {
					t.Fatalf("off read the ledger %d times", store.pausedReads)
				}
				return
			}
			m := sampler.quietMetrics
			if got := testutil.ToFloat64(m.seconds.WithLabelValues(string(tc.mode), string(api.PlanHobby))); got != tc.wantUnbilled {
				t.Fatalf("unbilled seconds = %v, want %v", got, tc.wantUnbilled)
			}
			if got := testutil.ToFloat64(m.unproven.WithLabelValues(string(tc.mode))); got != tc.wantUnproven {
				t.Fatalf("unproven minutes = %v, want %v", got, tc.wantUnproven)
			}
		})
	}
}

// A ledger that cannot be read fails the minute, like a residency read, so
// ADR-790's catch-up rolls it later instead of billing it without the pause.
func TestSamplerQuietPauseLedgerErrorFailsTheMinute(t *testing.T) {
	store := newLedgerStore()
	seedRunningInstance(t, store, quietBase, quietBase.Add(time.Minute))
	recordComplete(t, store, quietBase.Add(-api.MeterCatchUpWindow), quietBase)
	store.pausedErr = errors.New("database down")
	now := quietBase.Add(time.Minute)
	sampler := NewSampler(store, nil, func() time.Time { return now }).WithQuietPause(QuietPauseOn, nil)
	if _, err := tick(t, store, sampler, now); err == nil {
		t.Fatal("tick with an unreadable ledger succeeded")
	}
	if got, calls := store.appended(quietBase); got != 0 || calls != 0 {
		t.Fatalf("billed %d MB-seconds in %d rows without the ledger", got, calls)
	}
}

// An app's capacity floor still bills while its instance is paused: the
// subtraction runs before the floor fill.
func TestSamplerQuietPauseKeepsTheFloor(t *testing.T) {
	store := newLedgerStore()
	appID, depID := seedFloorApp(t, store, api.PlanHobby, 256)
	setPolicy(t, store, appID, state.ScalingPolicy{MinInstances: 1})
	ins, err := store.CreateInstance(context.Background(), appID, depID, string(state.StateRunning), 256, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatal(err)
	}
	store.resident[quietBase] = map[string]int64{ins.ID: 60}
	store.paused[quietBase] = map[string]state.QuietPausedSeconds{ins.ID: {Seconds: 60}}
	recordComplete(t, store, quietBase.Add(-api.MeterCatchUpWindow), quietBase)
	now := quietBase.Add(time.Minute)
	sampler := NewSampler(store, nil, func() time.Time { return now }).WithQuietPause(QuietPauseOn, nil)
	rows, err := tick(t, store, sampler, now)
	if err != nil {
		t.Fatal(err)
	}
	floor := 60 * int64(api.BillableRAMMB(256))
	if got, _ := store.appended(quietBase); got != floor {
		t.Fatalf("billed %d MB-seconds for a paused floor instance, want the floor's %d", got, floor)
	}
	// The paused instance itself bills nothing; the floor row carries it.
	var floorRow, liveRow int64
	for _, row := range rows {
		switch row.InstanceID {
		case FloorInstanceID(appID, 0).String():
			floorRow += row.MBSeconds
		case ins.ID:
			liveRow += row.MBSeconds
		}
	}
	if floorRow != floor || liveRow != 0 {
		t.Fatalf("floor row %d, live row %d MB-seconds; want %d and 0", floorRow, liveRow, floor)
	}
}

func TestParseQuietPauseMode(t *testing.T) {
	for in, want := range map[string]QuietPauseMode{"": QuietPauseOff, "off": QuietPauseOff, "shadow": QuietPauseShadow, "on": QuietPauseOn} {
		if got, err := ParseQuietPauseMode(in); err != nil || got != want {
			t.Errorf("ParseQuietPauseMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"true", "ON", "partial"} {
		if _, err := ParseQuietPauseMode(bad); err == nil {
			t.Errorf("ParseQuietPauseMode(%q) accepted", bad)
		}
	}
}
