package daemonunitspec

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/daemonunit"
)

// Entry is one row of the daemon registry — the canonical (name, unit,
// restart classification) tuple that the deploy generator emits into
// the per-role systemd files/ tree and the cd-controlplane workflow's
// daemons.json output target.
//
// `Critical` controls which list the daemon lands in
// deploy/etc/daemons.json:
//
//   - true  ⇒ critical[]    (cp-cp cd-controlplane's restart loop)
//   - false ⇒ best_effort[] (advisory restart on deploy, not blocking)
//
// Adding a daemon: add UnitXxx() + register it here. `make generate` will
// pick the new file up on next run; the CI `daemonunit-check` job will
// catch a missing registration.
type Entry struct {
	Name      string
	Unit      func() daemonunit.Unit
	Critical  bool
	Lifecycle Lifecycle
	// Role is the split-box host role that runs the daemon (ADR-110).
	// It is the single source for the control-plane / compute-only
	// partition that the manifest renderer, deploy/ansible/vars/daemons.yml
	// (role_convergence + fleet_verify) and deployctl's per-role trees all
	// used to copy by hand (ADR-143).
	Role Role
}

// Role is a split-box host role.
type Role string

const (
	RoleControlPlane Role = "control-plane"
	RoleComputeOnly  Role = "compute-only"
)

// DaemonsForRole returns the Registry daemon names that run on a host of
// the given role, in activation order.
func DaemonsForRole(role Role) []string {
	var out []string
	for _, e := range Registry {
		if e.Role == role {
			out = append(out, e.Name)
		}
	}
	return out
}

type Probe string

const (
	ProbeSystemd Probe = "systemd"
	ProbeUnix    Probe = "unix"
	ProbeTCP     Probe = "tcp"
)

type Lifecycle struct {
	After       []string
	Probe       Probe
	ProbeTarget string
	// ReadyzURL is the loopback HTTP endpoint that reports dependency-aware
	// readiness for this daemon. Transport probes remain as a fallback for
	// older or intentionally metrics-disabled installations, but production
	// deploy gates should prefer this URL so an active process with a broken
	// dependency cannot be promoted as healthy.
	ReadyzURL string
}

func ActivationOrder() []string {
	order := make([]string, len(Registry))
	for i, entry := range Registry {
		order[i] = entry.Name
	}
	return order
}

// Registry is the single source of truth for which daemons the platform
// ships. Order in the slice is the order cd-controlplane's restart loop
// runs them (vmmd first because it owns /run/faas; imaged last because
// it is best-effort). The CI gate `daemonunit-check` is order-insensitive
// (Diff matches by set membership) — order here is for human readability
// and the order the workflow actually restarts the services in.
var Registry = []Entry{
	{Name: "vmmd", Unit: UnitVmmd, Role: RoleComputeOnly, Critical: true, Lifecycle: Lifecycle{Probe: ProbeUnix, ProbeTarget: "/run/faas/vmmd.sock", ReadyzURL: "http://127.0.0.1:9104/readyz"}},
	{Name: "apid", Unit: UnitApid, Role: RoleControlPlane, Critical: true, Lifecycle: Lifecycle{Probe: ProbeSystemd, ReadyzURL: "http://127.0.0.1:9101/readyz"}},
	{Name: "schedd", Unit: UnitSchedd, Role: RoleControlPlane, Critical: true, Lifecycle: Lifecycle{After: []string{"vmmd"}, Probe: ProbeUnix, ProbeTarget: "/run/faas/schedd.sock", ReadyzURL: "http://127.0.0.1:9103/readyz"}},
	{Name: "gatewayd-internal", Unit: UnitGatewaydInternal, Role: RoleComputeOnly, Critical: true, Lifecycle: Lifecycle{After: []string{"schedd", "apid"}, Probe: ProbeTCP, ProbeTarget: "127.0.0.1:9090", ReadyzURL: "http://127.0.0.1:9090/readyz"}},
	{Name: "gatewayd-public", Unit: UnitGatewaydPublic, Role: RoleControlPlane, Critical: true, Lifecycle: Lifecycle{After: []string{"apid"}, Probe: ProbeTCP, ProbeTarget: "127.0.0.1:8080", ReadyzURL: "http://127.0.0.1:9092/readyz"}},
	{Name: "meterd", Unit: UnitMeterd, Role: RoleControlPlane, Critical: true, Lifecycle: Lifecycle{After: []string{"apid"}, Probe: ProbeSystemd, ReadyzURL: "http://127.0.0.1:9106/readyz"}},
	{Name: "githubd", Unit: UnitGithubd, Role: RoleControlPlane, Critical: true, Lifecycle: Lifecycle{After: []string{"apid"}, Probe: ProbeSystemd, ReadyzURL: "http://127.0.0.1:8083/readyz"}},
	{Name: "imaged", Unit: UnitImaged, Role: RoleComputeOnly, Critical: false, Lifecycle: Lifecycle{After: []string{"vmmd"}, Probe: ProbeTCP, ProbeTarget: "127.0.0.1:9102", ReadyzURL: "http://127.0.0.1:9102/readyz"}},
	// Mega-PR-C (issue #911 / ADR-110): builderd is the build
	// orchestrator on fsn-2 (compute-only). Spawns ephemeral
	// builder microVMs through vmmd (ADR-003) — no KVM direct
	// from this unit. After=vmmd: the build path needs the
	// per-box capacity signal vmmd writes at boot, otherwise
	// the first build_claim can race the vmmd register row.
	//
	// builderd does NOT depend on apid — apid runs on the
	// control-plane box only (fsn-1), while builderd runs on
	// the compute-only box (fsn-2). On fsn-2, the faas-apid
	// unit doesn't exist; `After=apid` would silently no-op
	// to a 90s boot timeout before systemd fails the unit.
	// builderd schedules builds via gRPC over the wire to
	// apid on fsn-1 (the [apphub] layer), so there is no
	// ordering dependency at unit-activation time.
	{Name: "builderd", Unit: UnitBuilderd, Role: RoleComputeOnly, Critical: true, Lifecycle: Lifecycle{After: []string{"vmmd"}, Probe: ProbeUnix, ProbeTarget: "/run/faas/builderd.sock", ReadyzURL: "http://127.0.0.1:9105/readyz"}},
}

// UnitByName returns the daemonunit.Unit for the given daemon name.
// The mapping is the canonical manifest.HostKeys → daemonunitspec.Unit
// name (the manifest uses underscores in two cases — gatewayd_internal,
// gatewayd_public — which the renderer flattens to dashes before
// reaching this lookup). Returns an error if name is not in the
// Registry; the renderer surfaces this as a schema-drift ship-blocker.
//
// The Registry's Unit field is itself a constructor (`func() daemonunit.Unit`)
// so the renderer gets a fresh copy per call — every renderer run
// produces a per-daemon unit without cross-run aliasing.
func UnitByName(name string) (daemonunit.Unit, error) {
	for _, e := range Registry {
		if e.Name == name {
			return e.Unit(), nil
		}
	}
	return daemonunit.Unit{}, fmt.Errorf("daemonunitspec: unknown daemon %q (known: %v)", name, ActivationOrder())
}

// FaasCPSlice is the [Slice] MemoryMax=3G ceiling for the entire
// control-plane slice. The 3 GB is hardcoded here (not derived from
// the financial model §13 line 431 — the model says 6 GB but the
// shipped slice is 3 GB; tracked as a known under-utilisation that
// can be widened in a future PR when the daemon set + memory profile
// stabilises post-DEPLOY-1).
//
// The slice is emitted to deploy/controlplane/systemd/faas-cp.slice
// AND mirrored to deploy/ansible/roles/control_plane_service/files/
// faas-cp.slice (PR-1: the v2 tree is the canonical source for the CD
// pipeline; the v1 deploy/controlplane/ is a tombstone now, scheduled
// for deletion in PR-1 Phase 2 after PR-X). The slice is NOT a daemon
// and lives outside the Registry iteration because it is the wrapper,
// not a member.
const FaasCPSlice = "faas-cp.slice"
