//go:build metal

package fcvm

import (
	"testing"
	"time"
)

// adr: 20261109120000 — a restored guest's monotonic clock advances by the
// time its snapshot spent parked. The probe reports, on each request, how
// much monotonic and wall time passed since the process started. Parked for
// compatClockPark, a restored probe must see at least that much monotonic
// time. The control arm loads without clock_realtime; on Firecracker 1.16+
// it must see the frozen clock, which proves the probe can catch the bug.
//
//	FAAS_TEST_KERNEL=<release vmlinux> FAAS_TEST_RNG_GO_ROOTFS=/var/tmp/alpine.tar \
//	make test-metal PKGS=./pkg/fcvm RUN_REGEX=TestMetalRestoreAdvancesMonotonicClock
func TestMetalRestoreAdvancesMonotonicClock(t *testing.T) {
	tarball := compatGoRootfs(t)
	probe := compatGoProbe(t, compatClockProbe)
	for _, arm := range []struct {
		name     string
		realtime bool
	}{{"clock-realtime", true}, {"control-off", false}} {
		t.Run(arm.name, func(t *testing.T) {
			p, _ := startCompatProbe(t, "clock-"+arm.name, tarball, "srv/probe", probe, []string{"/srv/probe"}, map[string]string{},
				func(v *JailerVMM) { v.WithRestoreClockRealtime(arm.realtime) })
			supported := ClockRealtimeSupported(p.fcVersion)
			if arm.realtime && !(supported && HostClocksourceTSC()) {
				t.Skipf("clock_realtime unavailable here (firecracker %s, tsc host %v)", p.fcVersion, HostClocksourceTSC())
			}
			p.park()
			time.Sleep(compatClockPark)
			got := p.getJSON(p.restore(), "/")
			mono := time.Duration(got["mono_ms"].(float64)) * time.Millisecond
			wall := time.Duration(got["wall_ms"].(float64)) * time.Millisecond
			t.Logf("firecracker %s, clock_realtime %v: monotonic %v, wall %v since process start", p.fcVersion, arm.realtime, mono, wall)
			if wall < compatClockPark {
				t.Fatalf("wall clock advanced %v, less than the %v park: the resume hook's clock step broke", wall, compatClockPark)
			}
			switch {
			case arm.realtime:
				if mono < compatClockPark {
					t.Fatalf("monotonic clock advanced %v across a %v park; it must include the parked time", mono, compatClockPark)
				}
				if diff := (wall - mono).Abs(); diff > 5*time.Second {
					t.Errorf("monotonic %v and wall %v differ by %v after restore", mono, wall, diff)
				}
			case supported:
				if mono >= compatClockPark {
					t.Errorf("control: monotonic advanced %v without clock_realtime on firecracker %s; the probe no longer shows the frozen clock", mono, p.fcVersion)
				}
			default:
				t.Logf("control: firecracker %s predates the opt-in and advances the clock itself", p.fcVersion)
			}
		})
	}
}

const compatClockPark = 20 * time.Second

const compatClockProbe = `package main

import (
	"encoding/json"
	"net/http"
	"time"
)

func main() {
	start := time.Now()
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		now := time.Now()
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"mono_ms": now.Sub(start).Milliseconds(),
			"wall_ms": now.Round(0).Sub(start.Round(0)).Milliseconds(),
		})
	})
	_ = http.ListenAndServe("0.0.0.0:8080", nil)
}
`
