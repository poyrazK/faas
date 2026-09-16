// role_test.go — whitebox tests for pkg/roleTemplating. Same package
// so the test can exercise unexported helpers (init-time invariant
// checks, the roleDaemons table itself).
//
// Whitebox pattern is per [[whitebox-test-file-pattern]]: the package
// has unexported invariants that MUST be exercised; a `_test` package
// would force every test to round-trip through the public API and
// miss the table-vs-registry cross-check.
//
// Each test is table-driven (CLAUDE.md "Table-driven tests"). The
// tests assert the load-bearing contracts:
//  1. roleDaemons is in lockstep with pkg/daemonunitspec.Registry
//  2. AllowedRoles is exhaustive over the package's role consts
//  3. Validate / Subset / DropIn reject unknown inputs cleanly
//  4. DropIn body is byte-for-byte stable across calls (idempotency)
//  5. Apply is idempotent given identical inputs
//  6. Mutate computes the right stop / start subsets in the right
//     order without calling systemctl in unit tests (execCommand
//     is the test seam)
package roleTemplating

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		role    Role
		wantErr bool
	}{
		{"control-plane accepted", RoleControlPlane, false},
		{"compute-only accepted", RoleComputeOnly, false},
		{"empty rejected", Role(""), true},
		{"unknown rejected", Role("multi-role"), true},
		{"uppercase control-plane rejected (case sensitive)", Role("Control-Plane"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.role)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate(%q) error = %v, wantErr %v", tt.role, err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrUnknownRole) && tt.role != Role("") {
				// Empty-string rejection is also ErrUnknownRole; the
				// non-empty unknown rejection must specifically wrap it.
				t.Fatalf("Validate(%q) error %v does not wrap ErrUnknownRole", tt.role, err)
			}
		})
	}
}

func TestSubset(t *testing.T) {
	tests := []struct {
		name string
		role Role
		want []string // pkg/daemonunitspec.Registry declaration order, filtered by Allows[r]
	}{
		{
			"control-plane subset",
			RoleControlPlane,
			[]string{"apid", "schedd", "gatewayd-public", "meterd", "githubd", "outboundd"},
		},
		{
			"compute-only subset",
			RoleComputeOnly,
			[]string{"vmmd", "realtimed", "schedd", "gatewayd-internal", "imaged", "builderd"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Subset(tt.role)
			if err != nil {
				t.Fatalf("Subset(%q) unexpected error: %v", tt.role, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Subset(%q) =\n  %v\nwant\n  %v", tt.role, got, tt.want)
			}
		})
	}

	// Subset must reject unknown roles.
	t.Run("unknown role rejected", func(t *testing.T) {
		_, err := Subset(Role("nope"))
		if err == nil {
			t.Fatalf("Subset(nope) returned nil error")
		}
	})
}

// TestSubsetHonorsDaemonRoleGates is the post-#930 adversarial-review
// invariant: a daemon's presence in the subset for role r MUST be
// true iff cmd/<daemon>/main.go::role.Require(..., r, ...) would
// accept r. The role.Require allow-list lives one package over
// (pkg/role); this test is the cross-check that
// pkg/roleTemplating.daemonInfoTable agrees.
//
// If a future edit renames a daemon, adds a new role to a daemon's
// Require allow-list, or extends the daemonunitspec.Registry, this
// test fails first with a precise list of drift.
func TestSubsetHonorsDaemonRoleGates(t *testing.T) {
	// Hard-coded map from per-daemon role.Require call sites. Any
	// change to cmd/<daemon>/main.go's role.Require MUST be mirrored
	// here, otherwise the review invariant regresses silently.
	dmnAllows := map[string]map[Role]bool{
		"vmmd":              {RoleSingleBox: true, RoleComputeOnly: true},
		"apid":              {RoleSingleBox: true, RoleControlPlane: true},
		"schedd":            {RoleSingleBox: true, RoleControlPlane: true, RoleComputeOnly: true},
		"meterd":            {RoleSingleBox: true, RoleControlPlane: true},
		"githubd":           {RoleSingleBox: true, RoleControlPlane: true},
		"outboundd":         {RoleSingleBox: true, RoleControlPlane: true},
		"gatewayd-public":   {RoleSingleBox: true, RoleControlPlane: true},
		"imaged":            {RoleSingleBox: true, RoleComputeOnly: true},
		"gatewayd-internal": {RoleSingleBox: true, RoleComputeOnly: true},
		"builderd":          {RoleSingleBox: true, RoleComputeOnly: true},
		"realtimed":         {RoleSingleBox: true, RoleComputeOnly: true},
	}
	for dmn, want := range dmnAllows {
		for _, r := range AllowedRoles {
			wantIn := want[r]
			subset, err := Subset(r)
			if err != nil {
				t.Fatalf("Subset(%q) error: %v", r, err)
			}
			gotIn := slices.Contains(subset, dmn)
			if gotIn != wantIn {
				t.Errorf("Subset(%q) contains %q = %v, want %v (daemon role.Require allow-list disagrees with daemonInfoTable.Allows)",
					r, dmn, gotIn, wantIn)
			}
		}
	}
}

// TestDropInEnvVarMatchesDaemon ensures each daemon's drop-in emits
// the EnvKey the daemon's config.go reads (cmd/<daemon>/config.go
// role.FromConfig(..., envKey)). The adversarial review caught
// gatewayd-internal reading FAAS_GATEWAYD_ROLE, not the uppercased
// pattern; this test would catch a re-introduction of that mistake.
func TestDropInEnvVarMatchesDaemon(t *testing.T) {
	cases := []struct {
		daemon string
		envKey string
		role   Role
	}{
		{"vmmd", "FAAS_VMMD_ROLE", RoleComputeOnly},
		{"apid", "FAAS_APID_ROLE", RoleControlPlane},
		{"schedd", "FAAS_SCHEDD_ROLE", RoleControlPlane},
		{"meterd", "FAAS_METERD_ROLE", RoleControlPlane},
		{"githubd", "FAAS_GITHUBD_ROLE", RoleControlPlane},
		{"outboundd", "FAAS_OUTBOUNDD_ROLE", RoleControlPlane},
		{"gatewayd-public", "FAAS_GATEWAYD_PUBLIC_ROLE", RoleControlPlane},
		{"imaged", "FAAS_IMAGED_ROLE", RoleComputeOnly},
		{"gatewayd-internal", "FAAS_GATEWAYD_ROLE", RoleComputeOnly},
		{"builderd", "FAAS_BUILDERD_ROLE", RoleComputeOnly},
		{"realtimed", "FAAS_REALTIME_ROLE", RoleComputeOnly},
	}
	for _, tt := range cases {
		t.Run(tt.daemon, func(t *testing.T) {
			body, err := DropIn(tt.role, tt.daemon)
			if err != nil {
				t.Fatalf("DropIn(%q, %q) error: %v", tt.role, tt.daemon, err)
			}
			want := fmt.Sprintf("Environment=%s=%s", tt.envKey, tt.role)
			if !strings.Contains(body, want) {
				t.Errorf("DropIn output missing %q:\n  got: %q", want, body)
			}
		})
	}
}

// TestDropInRefusesForbiddenRole is the defence-in-depth check: even
// if a registry edit accidentally allows a role the daemon rejects,
// DropIn refuses to emit a drop-in for that pair (better to fail
// loud at install time than to fail loud on every daemon restart).
func TestDropInRefusesForbiddenRole(t *testing.T) {
	// vmmd allows compute-only, not control-plane.
	_, err := DropIn(RoleControlPlane, "vmmd")
	if err == nil {
		t.Fatalf("DropIn(control-plane, vmmd) returned nil; should refuse because vmmd rejects control-plane")
	}
	// apid allows control-plane, not compute-only.
	_, err = DropIn(RoleComputeOnly, "apid")
	if err == nil {
		t.Fatalf("DropIn(compute-only, apid) returned nil; should refuse because apid rejects compute-only")
	}
}

func TestSubsetCrossChecksRegistry(t *testing.T) {
	// The `init()` panic is already exercised at package load; this
	// asserts the operational table is non-empty AND a true
	// subset-of-registry.
	for _, r := range AllowedRoles {
		t.Run(string(r), func(t *testing.T) {
			got, err := Subset(r)
			if err != nil {
				t.Fatalf("Subset(%q) error: %v", r, err)
			}
			if len(got) == 0 {
				t.Fatalf("Subset(%q) is empty — would start no daemons", r)
			}
			registry := map[string]struct{}{}
			for _, e := range daemonunitspec.Registry {
				registry[e.Name] = struct{}{}
			}
			for _, d := range got {
				if _, ok := registry[d]; !ok {
					t.Fatalf("Subset(%q) lists %q, not in daemonunitspec.Registry; the roleTemplating table is stale", r, d)
				}
			}
		})
	}
}

func TestDropIn(t *testing.T) {
	tests := []struct {
		name   string
		role   Role
		daemon string
		want   string
	}{
		{
			"control-plane apid",
			RoleControlPlane, "apid",
			"[Service]\nEnvironment=FAAS_BOX_ROLE=control-plane\nEnvironment=FAAS_APID_ROLE=control-plane\n",
		},
		{
			"compute-only builderd",
			RoleComputeOnly, "builderd",
			"[Service]\nEnvironment=FAAS_BOX_ROLE=compute-only\nEnvironment=FAAS_BUILDERD_ROLE=compute-only\n",
		},
		{
			"daemon with hyphens gets uppercased correctly",
			RoleControlPlane, "gatewayd-public",
			"[Service]\nEnvironment=FAAS_BOX_ROLE=control-plane\nEnvironment=FAAS_GATEWAYD_PUBLIC_ROLE=control-plane\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DropIn(tt.role, tt.daemon)
			if err != nil {
				t.Fatalf("DropIn(%q, %q) error: %v", tt.role, tt.daemon, err)
			}
			if got != tt.want {
				t.Fatalf("DropIn(%q, %q):\n  got:  %q\n  want: %q", tt.role, tt.daemon, got, tt.want)
			}
		})
	}

	t.Run("unknown role rejected", func(t *testing.T) {
		_, err := DropIn(Role("nope"), "apid")
		if err == nil {
			t.Fatalf("DropIn on unknown role returned nil error")
		}
	})

	t.Run("empty daemon rejected", func(t *testing.T) {
		_, err := DropIn(RoleControlPlane, "")
		if err == nil {
			t.Fatalf("DropIn with empty daemon returned nil error")
		}
	})

	t.Run("whitespace daemon rejected", func(t *testing.T) {
		_, err := DropIn(RoleControlPlane, "   ")
		if err == nil {
			t.Fatalf("DropIn with whitespace daemon returned nil error")
		}
	})
}

func TestDropInIdempotent(t *testing.T) {
	// Two calls must produce byte-identical output. PR-A's first-boot
	// re-run guarantee. Pick a daemon that DOES allow each role so
	// the test exercises the idempotency contract, not the
	// allows-side rejection.
	daemonFor := map[Role]string{RoleControlPlane: "apid", RoleComputeOnly: "vmmd"}
	for _, r := range AllowedRoles {
		d := daemonFor[r]
		t.Run(string(r), func(t *testing.T) {
			a, err := DropIn(r, d)
			if err != nil {
				t.Fatalf("DropIn error: %v", err)
			}
			b, err := DropIn(r, d)
			if err != nil {
				t.Fatalf("DropIn error: %v", err)
			}
			if a != b {
				t.Fatalf("DropIn not idempotent for %q:\n  first:  %q\n  second: %q", r, a, b)
			}
		})
	}
}

func TestApplyWritesAllDropIns(t *testing.T) {
	var buf bytes.Buffer
	calls := 0
	daemonReload := func() error {
		calls++
		return nil
	}
	if err := Apply(RoleControlPlane, &buf, daemonReload); err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("daemonReload called %d times, want 1", calls)
	}
	// RoleControlPlane subset per daemonInfoTable + Registry
	// declaration order (Fix 1, Fix 2 cross-check):
	wantBodies := []string{
		"== /etc/systemd/system/faas-apid.service.d ==\n[Service]\nEnvironment=FAAS_BOX_ROLE=control-plane\nEnvironment=FAAS_APID_ROLE=control-plane\n\n",
		"== /etc/systemd/system/faas-schedd.service.d ==\n[Service]\nEnvironment=FAAS_BOX_ROLE=control-plane\nEnvironment=FAAS_SCHEDD_ROLE=control-plane\n\n",
		"== /etc/systemd/system/faas-gatewayd-public.service.d ==\n[Service]\nEnvironment=FAAS_BOX_ROLE=control-plane\nEnvironment=FAAS_GATEWAYD_PUBLIC_ROLE=control-plane\n\n",
		"== /etc/systemd/system/faas-meterd.service.d ==\n[Service]\nEnvironment=FAAS_BOX_ROLE=control-plane\nEnvironment=FAAS_METERD_ROLE=control-plane\n\n",
		"== /etc/systemd/system/faas-githubd.service.d ==\n[Service]\nEnvironment=FAAS_BOX_ROLE=control-plane\nEnvironment=FAAS_GITHUBD_ROLE=control-plane\n\n",
		"== /etc/systemd/system/faas-outboundd.service.d ==\n[Service]\nEnvironment=FAAS_BOX_ROLE=control-plane\nEnvironment=FAAS_OUTBOUNDD_ROLE=control-plane\n\n",
	}
	got := buf.String()
	for _, want := range wantBodies {
		if !strings.Contains(got, want) {
			t.Errorf("Apply output missing drop-in body %q\ngot:\n%s", want, got)
		}
	}
	// Defensive: the daemons that REJECT control-plane must not
	// be in the output (Fix 1 + Fix 2 regression guard).
	forbidden := []string{"vmmd", "imaged", "gatewayd-internal", "builderd"}
	for _, d := range forbidden {
		if strings.Contains(got, "faas-"+d+".service.d ==") {
			t.Errorf("Apply(control-plane) emitted %q; daemon does not allow control-plane — drop-in would fail systemd + daemon role.Require", d)
		}
	}
}

func TestApplyIdempotent(t *testing.T) {
	// Two Apply calls with the same role produce byte-identical output.
	var buf1, buf2 bytes.Buffer
	noopReload := func() error { return nil }
	if err := Apply(RoleComputeOnly, &buf1, noopReload); err != nil {
		t.Fatalf("Apply first: %v", err)
	}
	if err := Apply(RoleComputeOnly, &buf2, noopReload); err != nil {
		t.Fatalf("Apply second: %v", err)
	}
	if buf1.String() != buf2.String() {
		t.Fatalf("Apply not idempotent:\n  first:\n%s\n  second:\n%s", buf1.String(), buf2.String())
	}
}

func TestApplySurfacesDaemonReloadError(t *testing.T) {
	want := errors.New("systemctl blew up")
	err := Apply(RoleControlPlane, &bytes.Buffer{}, func() error { return want })
	if err == nil {
		t.Fatalf("Apply with failing daemonReload returned nil")
	}
	if !errors.Is(err, want) {
		t.Fatalf("Apply error %v does not wrap %v", err, want)
	}
}

func TestApplyRejectsUnknownRole(t *testing.T) {
	err := Apply(Role("nope"), &bytes.Buffer{}, func() error { return nil })
	if err == nil {
		t.Fatalf("Apply on unknown role returned nil")
	}
	if !errors.Is(err, ErrUnknownRole) {
		t.Fatalf("Apply error %v does not wrap ErrUnknownRole", err)
	}
}

func TestMutateControlPlaneToComputeOnly(t *testing.T) {
	calls := []string{}
	exec := func(name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return "", nil
	}
	stopped, started, err := Mutate(RoleControlPlane, RoleComputeOnly, exec)
	if err != nil {
		t.Fatalf("Mutate error: %v", err)
	}

	// Stop: 5 control-plane-only daemons (apid, gatewayd-public,
	// meterd, githubd, outboundd). schedd is shared by both roles and remains
	// active while the host transitions to a compute-only node.
	// Note gatewayd-public
	// REJECTS compute-only (per cmd/gatewayd-public/main.go:144),
	// so it IS in the stop list under the corrected model — the
	// pre-review expectation was wrong.
	wantStopped := map[string]bool{
		"apid": true, "gatewayd-public": true,
		"meterd": true, "githubd": true, "outboundd": true,
	}
	for _, d := range stopped {
		if !wantStopped[d] {
			t.Errorf("Mutate stopped %q, expected only %v", d, wantStopped)
		}
	}
	if len(stopped) != len(wantStopped) {
		t.Errorf("Mutate stopped %d daemons (%v), want exactly %v", len(stopped), stopped, wantStopped)
	}

	// Start: 5 compute-only daemons (vmmd, realtimed, imaged, builderd,
	// gatewayd-internal). gatewayd-public is NOT started because
	// it's not in the compute-only role allow-list.
	wantStarted := map[string]bool{
		"vmmd": true, "realtimed": true, "imaged": true, "builderd": true, "gatewayd-internal": true,
	}
	for _, d := range started {
		if !wantStarted[d] {
			t.Errorf("Mutate started unexpected daemon %q", d)
		}
	}
	if len(started) != len(wantStarted) {
		t.Errorf("Mutate started %d daemons (%v), want exactly %v", len(started), started, wantStarted)
	}

	// Defence-in-depth: gatewayd-public must be the LAST stop per
	// the package's documented invariant (PR-B Fix 7). The Mutate
	// function emits stops in reverse subset order, AND emits
	// gatewayd-public last as a separate final pass.
	if len(stopped) > 0 && stopped[len(stopped)-1] != "gatewayd-public" {
		t.Errorf("Mutate's last stop is %q, want gatewayd-public (last-stop invariant)", stopped[len(stopped)-1])
	}
}

func TestMutateComputeOnlyToControlPlane(t *testing.T) {
	exec := func(name string, args ...string) (string, error) {
		return "", nil
	}
	stopped, started, err := Mutate(RoleComputeOnly, RoleControlPlane, exec)
	if err != nil {
		t.Fatalf("Mutate error: %v", err)
	}
	// Stop: vmmd, realtimed, imaged, builderd, gatewayd-internal — the 5
	// compute-only daemons. gatewayd-public is NOT in the stop
	// list because it doesn't allow compute-only.
	wantStopped := map[string]bool{
		"vmmd": true, "realtimed": true, "imaged": true, "builderd": true, "gatewayd-internal": true,
	}
	for _, d := range stopped {
		if !wantStopped[d] {
			t.Errorf("Mutate stopped unexpected daemon %q", d)
		}
	}
	if len(stopped) != len(wantStopped) {
		t.Errorf("Mutate stopped %d daemons (%v), want exactly %v", len(stopped), stopped, wantStopped)
	}
	// Start: 5 control-plane-only daemons (apid, gatewayd-public,
	// meterd, githubd, outboundd). schedd is already running on the compute node.
	wantStarted := map[string]bool{
		"apid": true, "gatewayd-public": true,
		"meterd": true, "githubd": true, "outboundd": true,
	}
	for _, d := range started {
		if !wantStarted[d] {
			t.Errorf("Mutate started unexpected daemon %q", d)
		}
	}
	if len(started) != len(wantStarted) {
		t.Errorf("Mutate started %d daemons (%v), want exactly %v", len(started), started, wantStarted)
	}
}

func TestMutateNoChange(t *testing.T) {
	exec := func(name string, args ...string) (string, error) {
		t.Errorf("Mutate no-change invoked systemctl: %s %v", name, args)
		return "", nil
	}
	stopped, started, err := Mutate(RoleControlPlane, RoleControlPlane, exec)
	if err != nil {
		t.Fatalf("Mutate error: %v", err)
	}
	if len(stopped) != 0 || len(started) != 0 {
		t.Fatalf("Mutate no-change should produce empty diff, got stopped=%v started=%v", stopped, started)
	}
}

// TestMutateBlankBox is the regression for PR-B review-fix #1:
// empty `from` is the documented blank-box first-boot path and
// must NOT be rejected by Validate. The expected behaviour is
// "start the entire `to` subset, stop nothing".
func TestMutateBlankBox(t *testing.T) {
	var calls []string
	exec := func(name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return "", nil
	}
	stopped, started, err := Mutate(Role(""), RoleControlPlane, exec)
	if err != nil {
		t.Fatalf("Mutate(\"\", control-plane) error: %v", err)
	}
	if len(stopped) != 0 {
		t.Fatalf("blank-box Mutate should stop nothing, got stopped=%v", stopped)
	}
	wantStart, _ := Subset(RoleControlPlane)
	if len(started) != len(wantStart) {
		t.Fatalf("blank-box Mutate should start the entire control-plane subset (%v), got started=%v",
			wantStart, started)
	}
	if len(calls) == 0 {
		t.Fatalf("blank-box Mutate invoked no systemctl calls; expected %d starts", len(wantStart))
	}
	for _, c := range calls {
		if !strings.HasPrefix(c, "systemctl start ") {
			t.Fatalf("blank-box Mutate must only start; got call %q", c)
		}
	}
}

func TestMutateUnknownRole(t *testing.T) {
	exec := func(name string, args ...string) (string, error) {
		return "", nil
	}
	_, _, err := Mutate(RoleControlPlane, Role("nope"), exec)
	if err == nil {
		t.Fatalf("Mutate with unknown target returned nil")
	}
}

func TestMutateSurfacesExecError(t *testing.T) {
	boom := errors.New("systemctl timeout")
	exec := func(name string, args ...string) (string, error) {
		return "", boom
	}
	_, _, err := Mutate(RoleControlPlane, RoleComputeOnly, exec)
	if err == nil {
		t.Fatalf("Mutate with failing exec returned nil")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("Mutate error %v does not wrap %v", err, boom)
	}
}

func TestAllowedRolesExhaustive(t *testing.T) {
	// AllowedRoles must contain BOTH RoleControlPlane and RoleComputeOnly.
	want := map[Role]struct{}{
		RoleControlPlane: {},
		RoleComputeOnly:  {},
	}
	got := map[Role]struct{}{}
	for _, r := range AllowedRoles {
		got[r] = struct{}{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AllowedRoles mismatch:\n  got:  %v\n  want: %v", AllowedRoles, want)
	}
}
