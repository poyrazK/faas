package instancestats

// adr: 20261112120603 pins the sustained CPU abuse episode: 90% of the quota
// for 15 minutes, dips up to 30 s tolerated, one signal per episode.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type fakeCPUAbuse struct {
	quota map[string]int
	calls []string
	fail  bool
}

func (f *fakeCPUAbuse) CPUQuotaMillicores(id string, _ time.Time) (int, bool) {
	q, ok := f.quota[id]
	return q, ok
}

func (f *fakeCPUAbuse) CPUSustained(_ context.Context, row InstanceStat, used, quota int, sustained time.Duration) error {
	if f.fail {
		return errors.New("store down")
	}
	f.calls = append(f.calls, row.InstanceID)
	if used*100 < quota*api.CPUAbuseSaturationPercent || sustained < api.CPUAbuseSustainedAfter {
		return errors.New("called before the instance was pinned long enough")
	}
	return nil
}

func TestWatchCPUAbuse(t *testing.T) {
	start := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	now := start
	h := &fakeCPUAbuse{quota: map[string]int{"miner": 250, "spiky": 250, "busy": 250}}
	p := &Poller{Now: func() time.Time { return now }, CPUAbuseHandler: h}
	row := func(id string, pct float64) InstanceStat {
		return InstanceStat{InstanceID: id, AppID: "app-" + id, CPUPct: pct}
	}
	// 15 s ticks. miner holds 24.5% of a CPU against a 250m quota (98%)
	// with a 15 s dip at minute 7; spiky drops below 90% for 45 s (longer
	// than the grace) every 5 minutes; busy sits at 80%.
	step := 15 * time.Second
	ticks := int((api.CPUAbuseSustainedAfter + time.Minute) / step)
	for i := 0; i <= ticks; i++ {
		at := time.Duration(i) * step
		now = start.Add(at)
		miner := 24.5
		if at == 7*time.Minute {
			miner = 1
		}
		spiky := 24.0
		if phase := at % (5 * time.Minute); phase >= time.Minute && phase < time.Minute+45*time.Second {
			spiky = 5
		}
		p.watchCPUAbuse(t.Context(), []InstanceStat{row("miner", miner), row("spiky", spiky), row("busy", 20)})
		if at < api.CPUAbuseSustainedAfter && len(h.calls) > 0 {
			t.Fatalf("signal at %s, before %s", at, api.CPUAbuseSustainedAfter)
		}
	}
	if len(h.calls) != 1 || h.calls[0] != "miner" {
		t.Fatalf("handler calls = %v; want miner once", h.calls)
	}

	// Leaving the snapshot (parked, frozen, destroyed) ends the episode.
	now = now.Add(step)
	p.watchCPUAbuse(t.Context(), nil)
	if len(p.cpuAbuse.episodes) != 0 {
		t.Fatalf("episodes after an empty snapshot = %d", len(p.cpuAbuse.episodes))
	}
}

// A failing handler is retried on the next tick of the same episode.
func TestWatchCPUAbuseRetriesOnError(t *testing.T) {
	start := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	now := start
	h := &fakeCPUAbuse{quota: map[string]int{"miner": 1000}, fail: true}
	p := &Poller{Now: func() time.Time { return now }, CPUAbuseHandler: h}
	tick := func(at time.Duration) {
		now = start.Add(at)
		p.watchCPUAbuse(t.Context(), []InstanceStat{{InstanceID: "miner", AppID: "app", CPUPct: 99}})
	}
	tick(0)
	tick(api.CPUAbuseSustainedAfter)
	h.fail = false
	tick(api.CPUAbuseSustainedAfter + 15*time.Second)
	tick(api.CPUAbuseSustainedAfter + 30*time.Second)
	if len(h.calls) != 1 {
		t.Fatalf("handler calls = %v; want one success after the retry", h.calls)
	}
}
