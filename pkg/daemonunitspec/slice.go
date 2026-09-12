// Slice unit for the faas-cp control-plane slice. PR-2 of issue #911
// ships the manifest renderer; the slice is the 8th daemon's parent
// cgroup (every faas daemon sets Slice = FaasCPSlice in its unit file).
// The pipeline places this file at /etc/systemd/system/faas-cp.slice
// after the eight per-daemon .service units.
//
// The 6 GB ceiling mirrors api.ControlPlaneReserveMB and the financial model
// §13. It includes the nested builder slice, so the release renderer and the
// bootstrap role must keep the same value.

package daemonunitspec

import (
	"github.com/onebox-faas/faas/pkg/daemonunit"
)

// FaasCPSliceMemoryMax is the [Slice] MemoryMax ceiling for the control-
// plane slice. It mirrors api.ControlPlaneReserveMB.
const FaasCPSliceMemoryMax = "6G"

// UnitSlice returns the daemonunit.Unit for the faas-cp.slice.
//
// The unit is a `[Slice]` section unit (instead of the [Service] sections
// the eight daemons use). pkg/daemonunit.Unit.Render() emits the [Unit] +
// [Service] + [Install] triple; slices only consume [Unit] + [Slice] +
// [Install]. The struct fields not relevant to a slice (Type, ExecStart,
// etc.) are left empty — Render() skips empty scalars, so the output is
// a clean slice unit.
//
// The MemoryMax is the load-bearing directive; it caps the total
// memory the control plane can consume across all 8 daemons. The
// per-daemon MemoryMax lives in UnitXxx() (256M for most daemons,
// 512M for gatewayd-{internal,public}, 1G for imaged).
func UnitSlice() daemonunit.Unit {
	return daemonunit.Unit{
		// [Unit]
		Description: "faas control-plane slice (issue #911 / ADR-078)",

		// [Slice]
		Slice:     "faas-cp.slice",
		MemoryMax: FaasCPSliceMemoryMax,

		// [Install]
		WantedBy: "multi-user.target",
	}
}
