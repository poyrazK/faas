package fcvm

import (
	"log/slog"
	"os"
	"strings"
	"sync/atomic"

	"golang.org/x/mod/semver"
)

// Restore clock advance (ADR-20261109120000).
//
// The guest keeps time with kvm-clock (guestTimerArgs). Firecracker before
// v1.16 advanced kvm-clock on restore by the host time elapsed since capture
// (KVM_CLOCK_REALTIME). v1.16 made that opt-in (#5809), so on v1.17 a restored
// guest's CLOCK_MONOTONIC and CLOCK_BOOTTIME resume where the capture left
// them: a park of three days looks like a few seconds to every timer, TTL
// cache and deadline in the app. The resume hook steps only the wall clock.
// Sending clock_realtime on /snapshot/load restores the pre-1.16 behavior.
//
// Firecracker refuses the load when the capture's saved clock lacks the
// realtime flag (ClockRealtimeNotInState). KVM records that flag only when the
// host keeps time with a stable TSC, so vmmd enables the option only on such
// hosts and turns it off for the rest of the process after repeated refusals.
// A refused restore falls back to a cold boot like any other failed load.

// clockRealtimeMinFirecracker is the first release with the clock_realtime
// LoadSnapshot field. Earlier releases advanced the clock unconditionally.
const clockRealtimeMinFirecracker = "v1.16.0"

// hostClocksourcePath is the host's active clocksource.
const hostClocksourcePath = "/sys/devices/system/clocksource/clocksource0/current_clocksource"

// clockRealtimeRefusalLimit is how many ClockRealtimeNotInState refusals a
// vmmd process accepts before it stops sending clock_realtime. One refusal is
// expected for a capture taken on a host without the flag; the snapshot is
// marked stale and recaptured here. Repeated refusals mean this host does not
// record the flag either.
const clockRealtimeRefusalLimit = 3

// clockRealtimeRefusalMarker is Firecracker's error text for a load that asked
// for clock_realtime against a capture without the realtime clock flag.
const clockRealtimeRefusalMarker = "clock_realtime requested but not present"

// ClockRealtimeSupported reports whether a detected Firecracker version
// ("1.17.0") accepts clock_realtime on LoadSnapshot.
func ClockRealtimeSupported(fcVersion string) bool {
	v := "v" + fcVersion
	return semver.IsValid(v) && semver.Compare(v, clockRealtimeMinFirecracker) >= 0
}

// HostClocksourceTSC reports whether the host keeps time with the TSC, the
// condition under which KVM saves the realtime clock flag in a capture.
func HostClocksourceTSC() bool {
	return hostClocksourceTSCAt(hostClocksourcePath)
}

func hostClocksourceTSCAt(path string) bool {
	raw, err := os.ReadFile(path)
	return err == nil && strings.TrimSpace(string(raw)) == "tsc"
}

// restoreClockRealtime is the JailerVMM's clock advance state. The zero value
// is disabled.
type restoreClockRealtime struct {
	enabled  atomic.Bool
	refusals atomic.Int32
}

// WithRestoreClockRealtime sends clock_realtime on every snapshot load
// (ADR-20261109120000). The caller checks ClockRealtimeSupported and
// HostClocksourceTSC first.
func (v *JailerVMM) WithRestoreClockRealtime(enabled bool) *JailerVMM {
	v.clockRealtime.enabled.Store(enabled)
	return v
}

// RestoreClockRealtime reports whether snapshot loads currently advance the
// guest clock.
func (v *JailerVMM) RestoreClockRealtime() bool {
	return v.clockRealtime.enabled.Load()
}

// snapshotLoadBody is the /snapshot/load request for a capture staged under
// the given names.
func (v *JailerVMM) snapshotLoadBody(stateName, memName string, resume bool) map[string]any {
	body := map[string]any{
		"snapshot_path": stateName,
		"mem_backend":   map[string]any{"backend_type": "File", "backend_path": memName},
		"resume_vm":     resume,
	}
	if v.clockRealtime.enabled.Load() {
		body["clock_realtime"] = true
	}
	return body
}

// noteSnapshotLoadError counts a ClockRealtimeNotInState refusal and turns the
// clock advance off once the limit is reached.
func (v *JailerVMM) noteSnapshotLoadError(err error) {
	if err == nil || !strings.Contains(err.Error(), clockRealtimeRefusalMarker) {
		return
	}
	n := v.clockRealtime.refusals.Add(1)
	if n < clockRealtimeRefusalLimit || !v.clockRealtime.enabled.CompareAndSwap(true, false) {
		slog.Warn("vmm: snapshot load refused clock_realtime; the capture lacks the realtime clock flag", "refusals", n)
		return
	}
	slog.Error("vmm: disabling restore clock advance after repeated refusals; restored guests' monotonic clocks will not advance while parked",
		"refusals", n, "adr", "ADR-20261109120000")
}
