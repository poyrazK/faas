package instancestats

// adr: 1155 pins CPU saturation episodes that queue automatic CPU captures.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type fakeCPUSaturation struct {
	quota map[string]int
	calls []string
	fail  bool
}

func (f *fakeCPUSaturation) CPUQuotaMillicores(id string, _ time.Time) (int, bool) {
	q, ok := f.quota[id]
	return q, ok
}

func (f *fakeCPUSaturation) CPUSaturated(_ context.Context, row InstanceStat, used, quota int, sustained time.Duration) error {
	if f.fail {
		return errors.New("store down")
	}
	f.calls = append(f.calls, row.InstanceID)
	if used < quota*api.ProfileCPUSaturationPercent/100 || sustained < api.ProfileCPUSaturationAfter {
		return errors.New("called before the instance was saturated long enough")
	}
	return nil
}

// ADR-1155: an instance held at its CPU quota for ProfileCPUSaturationAfter
// gets one automatic capture per saturation episode.
func TestWatchCPUSaturation(t *testing.T) {
	start := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	now := start
	h := &fakeCPUSaturation{quota: map[string]int{"busy": 500, "idle": 500, "brief": 1000}}
	p := &Poller{Now: func() time.Time { return now }, CPUSaturationHandler: h}
	tick := func(at time.Duration, rows ...InstanceStat) {
		t.Helper()
		now = start.Add(at)
		p.watchCPUSaturation(t.Context(), rows)
	}
	row := func(id string, pct float64) InstanceStat {
		return InstanceStat{InstanceID: id, AppID: "app-" + id, CPUPct: pct}
	}

	// busy holds 48% of a CPU against a 500m quota (96%), with a 2 s dip;
	// idle stays at 20%; brief saturates its 1000m quota for 10 s, then idles
	// longer than the grace.
	for s := 0; s <= 40; s++ {
		busy := 48.0
		if s == 12 || s == 13 {
			busy = 5
		}
		brief := 99.0
		if s > 10 {
			brief = 10
		}
		tick(time.Duration(s)*time.Second, row("busy", busy), row("idle", 20), row("brief", brief))
	}
	if len(h.calls) != 1 || h.calls[0] != "busy" {
		t.Fatalf("handler calls = %v; want busy once", h.calls)
	}
	// Still saturated: no second capture in the same episode.
	tick(60*time.Second, row("busy", 50))
	if len(h.calls) != 1 {
		t.Fatalf("handler re-ran within one episode: %v", h.calls)
	}
	// A dip longer than the grace re-arms; a new 30 s episode fires again.
	tick(60*time.Second+api.ProfileCPUSaturationGrace+time.Second, row("busy", 5))
	for s := 70; s <= 101; s++ {
		tick(time.Duration(s)*time.Second, row("busy", 50))
	}
	if len(h.calls) != 2 {
		t.Fatalf("handler calls = %v; want a second episode", h.calls)
	}

	// Unknown quota (boosted or not in the ledger) never fires; a failing
	// handler retries on the next tick.
	h.calls, h.quota = nil, map[string]int{"retry": 250}
	h.fail = true
	for s := 200; s <= 231; s++ {
		tick(time.Duration(s)*time.Second, row("unknown", 100), row("retry", 25))
	}
	h.fail = false
	tick(232*time.Second, row("retry", 25))
	if len(h.calls) != 1 || h.calls[0] != "retry" {
		t.Fatalf("handler calls = %v; want the failed capture retried once", h.calls)
	}
	if !cpuSaturated(900, 1000) || cpuSaturated(899, 1000) || cpuSaturated(100, 0) {
		t.Fatal("cpuSaturated threshold is not ProfileCPUSaturationPercent of the quota")
	}
}
