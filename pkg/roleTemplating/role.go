// Package roleTemplating — ADR-112 role-image-collapse.
//
// ADR-112 collapses the role segment out of the Gregale Compute Image.
// `gregalectl release install --role` calls into this package to
// template per-daemon `99-faas-role.conf` systemd drop-ins + start the
// role-appropriate daemon subset.
//
// The image bakes ALL 8 daemon binaries + the maximal set of cgroup
// scopes. Per-daemon drop-ins (Environment=FAAS_BOX_ROLE=...,
// Environment=FAAS_<DAEMON_UPPER>_ROLE=...) are written HERE at
// first-boot / re-role time, not at packer build-time. The role
// subset decides which daemons `systemctl start` brings up; the
// daemons NOT in the subset are present on disk but consume zero
// RAM (no systemd unit enabled, no cgroup scope carved).
//
// PR-A wires the first-boot path. PR-B re-uses the same primitives
// for in-place role mutation (cmd/gregalectl/commands_release.go
// `cmdReleaseInstall --role`).
//
// Layering: this package is PURE (no I/O). Callers (releaseinstall,
// runbook-step-9.sh wrapper via the gregalectl binary, the e2e role
// mutation test under //go:build e2eimage) supply the I/O. Pure
// functions are exhaustively unit-tested in role_test.go.
//
// Reused surfaces:
//   - pkg/daemonunitspec.Registry — canonical 8-daemon inventory.
//     `Subset()` cross-checks its result against the registry's
//     `Name` field (asserts no rogue daemon entries here that aren't
//     in the registry).
//   - ADR-092 (per-role PKI subset) is enforced by means of the same
//     role string this package consumes — see ADR-112
//     cross-references in docs/adr/112-role-image-collapse.md.
package roleTemplating

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
)

// Role is a box role. Allowed values match /etc/faas/first-boot.env's
// FAAS_BOX_ROLE contract (ADR-092: one role per box; multi-role is a
// separate ADR).
type Role string

const (
	RoleControlPlane Role = "control-plane"
	RoleComputeOnly  Role = "compute-only"
	// RoleSingleBox is the pre-Gate-B single-host posture every
	// daemon allows. Duplicated here rather than imported from
	// pkg/role to keep pkg/roleTemplating import-free of pkg/role
	// (the daemons already own the canonical enum). The invariant
	// test in role_test.go asserts these strings stay byte-equal.
	RoleSingleBox Role = "single-box"
)

// AllowedRoles is the canonical list of role values that
// Subset / Apply / Mutate accept. RoleSingleBox is intentionally
// absent — single-box dev boots are the daemon's own back-compat
// default; this package is the "deploy a box as control-plane OR
// compute-only" path, not the "deploy a box as single-box" path
// (the isolated image-seed and local test paths cover single-box).
var AllowedRoles = []Role{RoleControlPlane, RoleComputeOnly}

// ValidRoles is a set-based view of AllowedRoles for O(1) lookup.
// Derived once at package init.
var validRoles = func() map[Role]struct{} {
	m := make(map[Role]struct{}, len(AllowedRoles))
	for _, r := range AllowedRoles {
		m[r] = struct{}{}
	}
	return m
}()

// ErrUnknownRole is the error returned when a string value is not in
// the AllowedRoles set. Caller surfaces this as a stable CLI exit code
// (2 = usage error) per CLAUDE.md "Handlers ≤50 lines" / convention
// guidance for flag.Parse failures.
var ErrUnknownRole = errors.New("roleTemplating: role is not control-plane|compute-only (ADR-092)")

// daemonInfo is the per-daemon role-templating contract. Each entry
// mirrors the load-bearing surface the daemon itself enforces:
//
//   - EnvKey: the exact FAAS_<X>_ROLE env var name the daemon reads
//     in cmd/<daemon>/config.go::role.FromConfig(tomlValue, envKey).
//     The legacy per-daemon ansible templates
//     (deploy/ansible/roles/*_service/templates/99-faas-role.conf.j2)
//     match these names byte-for-byte; writing the wrong key here
//     would silently make the daemon drop its env override and fall
//     back to single-box (no error, just wrong).
//   - Allows: the (RoleSingleBox|RoleControlPlane|RoleComputeOnly)
//     allow-list the daemon enforces at boot via
//     role.Require(daemon, cfg.Role, allow...). Must match
//     cmd/<daemon>/main.go's role.Require call site byte-for-byte.
//
// Why keep the allow-list here as well as the registry role metadata: the
// role-templating table mirrors the daemon binary's boot contract and its
// exact environment key. The registry owns placement and activation order;
// this table owns the values rendered into each daemon's role drop-in.
type daemonInfo struct {
	EnvKey string
	Allows map[Role]bool
}

// daemonInfoTable is the canonical per-daemon role-templating
// surface. Source-of-truth cross-checks live in init() below;
// pkg/role/role_test.go covers the daemon side.
//
// Adversarial review (post-merge #930) caught two regressions in the
// role-deny-style table that this struct replaces:
//
//   - vmmd, imaged, gatewayd-internal had been put in
//     RoleControlPlane even though cmd/{vmmd,imaged,gatewayd-internal}
//     each role.Require only "single-box|compute-only". First-boot on
//     a control-plane box would have written a drop-in set
//     FAAS_VMMD_ROLE=control-plane and then `systemctl start faas-vmmd`
//     would refuse to start with "vmmd: refusing to start as role
//     control-plane".
//   - gatewayd-internal reads FAAS_GATEWAYD_ROLE
//     (cmd/gatewayd-internal/config.go:191), not the uppercased-daemon
//     pattern FAAS_GATEWAYD_INTERNAL_ROLE. The drop-in for
//     gatewayd-internal must use the per-daemon EnvKey from this
//     table, never a derived one.
//
// Adding a new daemon: add an entry here AND register it in
// pkg/daemonunitspec.Registry. init() panics on a mismatch either way.
var daemonInfoTable = map[string]daemonInfo{
	"vmmd": {
		EnvKey: "FAAS_VMMD_ROLE",
		Allows: map[Role]bool{RoleSingleBox: true, RoleComputeOnly: true},
	},
	"apid": {
		EnvKey: "FAAS_APID_ROLE",
		Allows: map[Role]bool{RoleSingleBox: true, RoleControlPlane: true},
	},
	"schedd": {
		EnvKey: "FAAS_SCHEDD_ROLE",
		Allows: map[Role]bool{RoleSingleBox: true, RoleControlPlane: true, RoleComputeOnly: true},
	},
	"meterd": {
		EnvKey: "FAAS_METERD_ROLE",
		Allows: map[Role]bool{RoleSingleBox: true, RoleControlPlane: true},
	},
	"githubd": {
		EnvKey: "FAAS_GITHUBD_ROLE",
		Allows: map[Role]bool{RoleSingleBox: true, RoleControlPlane: true},
	},
	"gatewayd-public": {
		EnvKey: "FAAS_GATEWAYD_PUBLIC_ROLE",
		Allows: map[Role]bool{RoleSingleBox: true, RoleControlPlane: true},
	},
	"imaged": {
		EnvKey: "FAAS_IMAGED_ROLE",
		Allows: map[Role]bool{RoleSingleBox: true, RoleComputeOnly: true},
	},
	"gatewayd-internal": {
		EnvKey: "FAAS_GATEWAYD_ROLE", // not the uppercased pattern; see cmd/gatewayd-internal/config.go:191
		Allows: map[Role]bool{RoleSingleBox: true, RoleComputeOnly: true},
	},
	"builderd": {
		EnvKey: "FAAS_BUILDERD_ROLE",
		Allows: map[Role]bool{RoleSingleBox: true, RoleComputeOnly: true},
	},
	"realtimed": {
		EnvKey: "FAAS_REALTIME_ROLE",
		Allows: map[Role]bool{RoleSingleBox: true, RoleComputeOnly: true},
	},
}

// init cross-checks daemonInfoTable against pkg/daemonunitspec.Registry
// so a stale or missing entry is a ship-blocker that fires at package
// load (caught by `go test ./...` on the first run after the edit),
// not at first deploy when the box silently runs the wrong daemon set.
// Every registered daemon MUST have a daemonInfoTable entry; every
// daemonInfoTable entry MUST have a registry row.
func init() {
	registryNames := make(map[string]struct{}, len(daemonunitspec.Registry))
	for _, e := range daemonunitspec.Registry {
		registryNames[e.Name] = struct{}{}
	}
	// Set-equality: same names on both sides. A typo (e.g.
	// "gatewayd-internal" vs "gatewayd_internal") would otherwise
	// silently ship.
	if len(registryNames) != len(daemonInfoTable) {
		panic(fmt.Sprintf("roleTemplating: daemon registry has %d entries, daemonInfoTable has %d (registry=%v, table keys=%v)",
			len(registryNames), len(daemonInfoTable),
			registryKeyList(registryNames), daemonInfoKeys()))
	}
	for name := range registryNames {
		if _, ok := daemonInfoTable[name]; !ok {
			panic(fmt.Sprintf("roleTemplating: daemon %q is in pkg/daemonunitspec.Registry but has no daemonInfoTable entry", name))
		}
	}
	for name, info := range daemonInfoTable {
		if _, ok := registryNames[name]; !ok {
			panic(fmt.Sprintf("roleTemplating: daemon %q in daemonInfoTable is not in pkg/daemonunitspec.Registry (EnvKey=%s)", name, info.EnvKey))
		}
		if info.EnvKey == "" {
			panic(fmt.Sprintf("roleTemplating: daemon %q has empty EnvKey", name))
		}
		// Every daemon must allow at least RoleSingleBox (the
		// single-box dev back-compat posture).
		if !info.Allows[RoleSingleBox] {
			panic(fmt.Sprintf("roleTemplating: daemon %q does not allow RoleSingleBox; single-box dev boots would refuse to start it", name))
		}
	}
}

func registryKeyList(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func daemonInfoKeys() []string {
	out := make([]string, 0, len(daemonInfoTable))
	for k := range daemonInfoTable {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Validate returns nil if r is one of AllowedRoles, ErrUnknownRole
// otherwise. Pure function; safe for CLI flag validation.
func Validate(r Role) error {
	if _, ok := validRoles[r]; !ok {
		return fmt.Errorf("%w (got %q)", ErrUnknownRole, string(r))
	}
	return nil
}

// Subset returns the daemon subset for the given role. The subset is
// computed by walking pkg/daemonunitspec.Registry in declaration
// order and including every daemon whose daemonInfoTable.Allows
// entry for r is true. The output is in declaration order — the
// registry slice order is itself the dependency-respecting order
// from cmd/deployctl/runtime.go (vmmd first because it owns
// /run/faas), so callers that feed the slice to `systemctl start`
// get the right order without further topological work.
//
// Returns ErrUnknownRole if r is not one of AllowedRoles. Returns
// the raw registry slice if r is RoleSingleBox (every daemon allows
// it); single-box dev boots can still hit Subset without crashing.
//
// The returned slice is safe for the caller to mutate; it is a fresh
// allocation per call.
func Subset(r Role) ([]string, error) {
	if err := Validate(r); err != nil && r != RoleSingleBox {
		return nil, err
	}
	out := make([]string, 0, len(daemonunitspec.Registry))
	for _, e := range daemonunitspec.Registry {
		info := daemonInfoTable[e.Name]
		if info.Allows[r] {
			out = append(out, e.Name)
		}
	}
	return out, nil
}

// StartOrder returns the role's daemon subset in dependency order:
// every daemon appears AFTER every daemon it lists in
// Lifecycle.After. Computed by intersecting the role subset with
// pkg/daemonunitspec.RestartOrder() (Kahn's algorithm on the full
// Registry Lifecycle.After graph).
//
// Use this when feeding `systemctl start` at first-boot or in PR-B's
// re-role path; using Subset() directly works in practice today but
// is brittle against future Registry edits that add an After edge
// crossing role boundaries.
//
// The returned slice is safe for the caller to mutate; it is a fresh
// allocation per call.
func StartOrder(r Role) ([]string, error) {
	subset, err := Subset(r)
	if err != nil {
		return nil, err
	}
	inSubset := make(map[string]struct{}, len(subset))
	for _, d := range subset {
		inSubset[d] = struct{}{}
	}
	order, err := daemonunitspec.RestartOrder()
	if err != nil {
		return nil, fmt.Errorf("roleTemplating: StartOrder: %w", err)
	}
	out := make([]string, 0, len(subset))
	for _, d := range order {
		if _, ok := inSubset[d]; ok {
			out = append(out, d)
		}
	}
	return out, nil
}

// DropInFileName is the per-daemon drop-in file name
// ("99-faas-role.conf"). Mega-PR-C Commit 4 established this
// convention; PR-A preserves it byte-for-byte.
const DropInFileName = "99-faas-role.conf"

// DropIn renders the per-daemon drop-in body for the given role.
//
// The output is the canonical 99-faas-role.conf content; same shape
// the packer build-time loop in build-base.sh (pre-ADR-112) wrote.
// PR-A moves the templating here; the on-disk bytes are unchanged
// for daemons that follow the FAAS_<DAEMON_UPPER>_ROLE pattern
// (apid, schedd, meterd, githubd, vmmd, imaged, builderd,
// gatewayd-public). gatewayd-internal is the lone outlier: the
// daemon reads FAAS_GATEWAYD_ROLE (cmd/gatewayd-internal/config.go:191)
// not the uppercased pattern; the daemonInfoTable owns the
// per-daemon EnvKey so this function has no per-daemon special case.
func DropIn(r Role, daemon string) (string, error) {
	if err := Validate(r); err != nil && r != RoleSingleBox {
		return "", err
	}
	daemon = strings.TrimSpace(daemon)
	if daemon == "" {
		return "", errors.New("roleTemplating: daemon must be non-empty")
	}
	info, ok := daemonInfoTable[daemon]
	if !ok {
		return "", fmt.Errorf("roleTemplating: daemon %q has no daemonInfoTable entry (registry drift — pkg/daemonunitspec.Registry vs pkg/roleTemplating.daemonInfoTable)", daemon)
	}
	// Additive defence-in-depth: every daemon must allow the role
	// it is being templated for. Otherwise the drop-in is a
	// foot-gun — the systemd unit will start and the daemon will
	// refuse to start, leaving the box half-configured with a
	// failed systemd journal.
	if !info.Allows[r] {
		return "", fmt.Errorf("roleTemplating: daemon %q does not allow role %q (allows=%v); refusing to emit a drop-in that the daemon would refuse to honor", daemon, r, info.Allows)
	}
	return fmt.Sprintf(
		"[Service]\nEnvironment=FAAS_BOX_ROLE=%s\nEnvironment=%s=%s\n",
		string(r), info.EnvKey, string(r),
	), nil
}

// DropInDir returns the systemd drop-in directory for the named
// daemon. ADR-092: per-daemon drop-ins live at
// /etc/systemd/system/faas-<daemon>.service.d/.
func DropInDir(daemon string) string {
	return fmt.Sprintf("/etc/systemd/system/faas-%s.service.d", daemon)
}

// Apply writes the per-daemon drop-ins for the role's subset into
// the supplied writer (caller owns I/O; the function does not touch
// the filesystem directly). Intended use: callers wire this to a
// per-host file writer in the production path; tests wire it to a
// bytes.Buffer for assertions.
//
// Apply is idempotent: calling it twice with the same inputs produces
// the same on-disk bytes (callers can short-circuit by checking
// whether the drop-in already matches).
func Apply(r Role, w io.Writer, daemonReload func() error) error {
	if err := Validate(r); err != nil {
		return err
	}
	daemons, err := Subset(r)
	if err != nil {
		return err
	}
	for _, d := range daemons {
		body, err := DropIn(r, d)
		if err != nil {
			return fmt.Errorf("roleTemplating: render drop-in for %s: %w", d, err)
		}
		if _, err := io.WriteString(w, fmt.Sprintf("== %s ==\n%s\n", DropInDir(d), body)); err != nil {
			return fmt.Errorf("roleTemplating: write drop-in for %s: %w", d, err)
		}
	}
	if daemonReload != nil {
		if err := daemonReload(); err != nil {
			return fmt.Errorf("roleTemplating: daemon-reload: %w", err)
		}
	}
	return nil
}

// ApplyFilesystem is the production wiring: writes drop-ins to
// disk + runs `systemctl daemon-reload`. Use Apply() in tests
// (no I/O); use ApplyFilesystem() at first-boot and PR-B re-role
// time.
//
// The function is safe to call on a converged box: re-running
// overwrites identical content for every daemon in the subset and
// triggers one daemon-reload per call.
//
// The earlier `templateBaseDir` parameter was speculative scaffolding
// for a role.conf.tpl file that build-base.sh wrote into the image
// but nothing read; the post-merge review surfaced it as a dead-code
// path that could silently drift from DropIn's actual output. The
// correct surface (one-per-daemon env keys owned by daemonInfoTable)
// can't be expressed as a single textual template, so the template
// hook is removed. DropIn() remains the single source of truth.
func ApplyFilesystem(r Role) error {
	if err := Validate(r); err != nil {
		return err
	}
	daemons, err := Subset(r)
	if err != nil {
		return err
	}
	for _, d := range daemons {
		body, err := DropIn(r, d)
		if err != nil {
			return fmt.Errorf("roleTemplating: render drop-in for %s: %w", d, err)
		}
		dir := DropInDir(d)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("roleTemplating: mkdir %s: %w", dir, err)
		}
		dst := filepath.Join(dir, DropInFileName)
		if err := os.WriteFile(dst, []byte(body), 0o644); err != nil {
			return fmt.Errorf("roleTemplating: write %s: %w", dst, err)
		}
	}
	// `systemctl daemon-reload` — synchronous, blocks until systemd
	// finishes processing the drop-ins. Failure here is a hard
	// ship-blocker (the role is not in effect); surface it as an
	// error so cmdReleaseInstall can exit 4 (runtime error, per
	// releaseinstall convention).
	if err := systemctlDaemonReload(); err != nil {
		return fmt.Errorf("roleTemplating: systemctl daemon-reload: %w", err)
	}
	return nil
}

// systemctlDaemonReload is the test seam for ApplyFilesystem's
// systemd reload. Production invokes `systemctl daemon-reload`
// synchronously; tests override this var to inject errors without
// touching the host's systemd. Default shim mirrors the
// production call exactly.
var systemctlDaemonReload = func() error {
	_, err := exec.Command("systemctl", "daemon-reload").CombinedOutput()
	return err
}

// Mutate transitions a box from role `from` to role `to`. Computes
// the diff subset (stop-only, start-only, leave-only) and emits the
// right `systemctl stop / start` calls in dependency order.
//
// PR-B's primary entry point. NOT used by PR-A's first-boot path
// (first-boot always starts from a blank state — only Apply is
// needed). The dependency ordering requirement (gatewayd-public
// last to stop, first to start) is satisfied by emitting stop calls
// in REVERSE subset order and start calls in subset order, with one
// exception: gatewayd-public is excluded from the stop ordering
// because stopping it is the action that triggers connection
// drain elsewhere; it MUST be the last stop.
//
// `execCommand` is the test seam — production calls exec.Command
// directly; tests inject a recording implementation. Returns
// (stoppedDaemons, startedDaemons) for assertion.
func Mutate(from, to Role, execCommand func(name string, args ...string) (string, error)) (stopped, started []string, err error) {
	// `from` may be empty: that is the blank-box first-boot path
	// (no compute_nodes row, or `--no-db` legacy) and there is
	// nothing to stop. `Validate` would still reject "" against the
	// strict allow-list — so we skip it explicitly here and treat
	// from as "no from-set". The CLI-side `Validate` keeps its loud
	// contract for flag validation.
	var fromSet []string
	if from != "" {
		if err := Validate(from); err != nil {
			return nil, nil, fmt.Errorf("roleTemplating: from: %w", err)
		}
		fromSet, err = Subset(from)
		if err != nil {
			return nil, nil, err
		}
	}
	if err := Validate(to); err != nil {
		return nil, nil, fmt.Errorf("roleTemplating: to: %w", err)
	}
	toSet, err := Subset(to)
	if err != nil {
		return nil, nil, err
	}
	fromMap := make(map[string]struct{}, len(fromSet))
	for _, d := range fromSet {
		fromMap[d] = struct{}{}
	}
	toMap := make(map[string]struct{}, len(toSet))
	for _, d := range toSet {
		toMap[d] = struct{}{}
	}

	// Stop: daemons in `from` not in `to`. Reverse order so
	// dependents stop before their dependencies
	// (gatewayd-public → gatewayd-internal → schedd → ...).
	//
	// gatewayd-public is additionally pulled to the very END of the
	// stop list (regardless of its registry position) — it is the
	// public-facing TLS terminator and stopping it is what triggers
	// connection drain elsewhere; its drop must be last so the
	// upstream load balancer drains the connection pool before the
	// listeners go away. TestMutateControlPlaneToComputeOnly asserts
	// this invariant byte-for-byte.
	var gwLast *string
	for i := len(fromSet) - 1; i >= 0; i-- {
		d := fromSet[i]
		if _, keep := toMap[d]; keep {
			continue
		}
		if d == "gatewayd-public" {
			pd := d
			gwLast = &pd
			continue
		}
		out, err := execCommand("systemctl", "stop", "faas-"+d+".service")
		if err != nil {
			return stopped, started, fmt.Errorf("roleTemplating: stop %s: %s: %w", d, strings.TrimSpace(out), err)
		}
		stopped = append(stopped, d)
	}
	if gwLast != nil {
		out, err := execCommand("systemctl", "stop", "faas-gatewayd-public.service")
		if err != nil {
			return stopped, started, fmt.Errorf("roleTemplating: stop gatewayd-public: %s: %w", strings.TrimSpace(out), err)
		}
		stopped = append(stopped, *gwLast)
	}

	// Start: daemons in `to` not in `from`. Forward order so
	// dependencies start before their dependents
	// (vmmd → schedd → gatewayd-internal → gatewayd-public).
	for _, d := range toSet {
		if _, alreadyRunning := fromMap[d]; alreadyRunning {
			continue
		}
		out, err := execCommand("systemctl", "start", "faas-"+d+".service")
		if err != nil {
			return stopped, started, fmt.Errorf("roleTemplating: start %s: %s: %w", d, strings.TrimSpace(out), err)
		}
		started = append(started, d)
	}
	return stopped, started, nil
}
