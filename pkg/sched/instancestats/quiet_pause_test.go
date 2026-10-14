// adr: 1151 — each tick publishes the instances every node reported
// quiet-paused, and admission ignores a stale sample.
package instancestats

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestQuietPausedSnapshot(t *testing.T) {
	r := NewReader()
	p := &Poller{Reader: r}
	now := time.Unix(1_800_000_000, 0)
	if _, _, ok := r.QuietPaused("n1", now); ok {
		t.Fatal("QuietPaused before any tick returned ok=true")
	}
	p.publishQuietPaused([]InstanceStat{
		{InstanceID: "a", NodeID: "n1", QuietPaused: true},
		{InstanceID: "b", NodeID: "n1"},
		{InstanceID: "c", NodeID: "n2", QuietPaused: true},
	}, now)
	got, asOf, ok := r.QuietPaused("n1", now.Add(time.Second))
	if !ok || !asOf.Equal(now) || len(got) != 1 {
		t.Fatalf("QuietPaused(n1) = %v, %v, %v; want {a} at the tick", got, asOf, ok)
	}
	if _, found := got["a"]; !found {
		t.Fatalf("QuietPaused(n1) = %v, want a", got)
	}
	if got, _, ok := r.QuietPaused("n3", now); !ok || len(got) != 0 {
		t.Fatalf("QuietPaused for a node with nothing paused = %v, %v; want an empty fresh sample", got, ok)
	}
	stale := now.Add(time.Duration(api.QuietPauseSampleMaxAgeMillis)*time.Millisecond + time.Millisecond)
	if _, _, ok := r.QuietPaused("n1", stale); ok {
		t.Fatal("QuietPaused accepted a stale sample")
	}
}
