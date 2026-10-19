package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
	"github.com/onebox-faas/faas/pkg/releasebundle"
)

// Per-unit releases (docs/adr/1650-per-unit-control-plane-releases.md):
// `deployctl deploy --only` restarts and gates only the services whose
// binaries the release replaced.

func manifestWithServices(services ...string) releasebundle.Manifest {
	manifest := releasebundle.Manifest{}
	for _, service := range services {
		manifest.Files = append(manifest.Files, releasebundle.File{Path: "systemd/faas-" + service + ".service"})
	}
	return manifest
}

func TestRestartServicesHonorsOnly(t *testing.T) {
	order, err := daemonunitspec.RestartOrder()
	if err != nil {
		t.Fatal(err)
	}
	manifest := manifestWithServices("apid", "schedd", "gatewayd-public", "meterd", "githubd", "outboundd")
	for _, tc := range []struct {
		name string
		only map[string]struct{}
		want []string
		err  string
	}{
		{name: "unset restarts the release", want: []string{"apid", "schedd", "gatewayd-public", "meterd", "githubd", "outboundd"}},
		{name: "subset keeps restart order", only: set("outboundd", "apid"), want: []string{"apid", "outboundd"}},
		{name: "service outside the release", only: set("apid", "vmmd"), err: "does not run: vmmd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := hostRuntime{serviceOrder: order, only: tc.only}
			got, err := r.restartServices(manifest)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("restartServices error = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("restartServices: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("restartServices = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHostRestartWithOnlyLeavesOtherDaemonsRunning(t *testing.T) {
	orig := runCommand
	t.Cleanup(func() { runCommand = orig })
	var restarted, probed []string
	runCommand = func(_ context.Context, name string, args ...string) error {
		if name == "systemctl" && len(args) >= 2 && args[0] == "restart" {
			restarted = append(restarted, args[1])
		}
		return nil
	}
	binaryDir := t.TempDir()
	for _, service := range []string{"apid", "meterd", "schedd"} {
		if err := os.WriteFile(filepath.Join(binaryDir, service), []byte("candidate"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r := hostRuntime{
		binaryDir:    binaryDir,
		serviceOrder: []string{"apid", "schedd", "meterd"},
		readyTimeout: 100 * time.Millisecond,
		only:         set("meterd"),
		waitReadyOverride: func(_ context.Context, service string) error {
			probed = append(probed, service)
			return nil
		},
	}
	if err := r.Restart(context.Background(), manifestWithServices("apid", "schedd", "meterd")); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if want := []string{"faas-meterd.service"}; !reflect.DeepEqual(restarted, want) {
		t.Fatalf("restarted %v, want %v", restarted, want)
	}
	if want := []string{"meterd"}; !reflect.DeepEqual(probed, want) {
		t.Fatalf("readiness probed %v, want %v", probed, want)
	}
}

func TestActivateWithOnlyRetiresOnlyRestartedCanaries(t *testing.T) {
	root := t.TempDir()
	releaseRoot := filepath.Join(root, "release")
	unitDir := filepath.Join(root, "installed")
	for _, service := range []string{"apid", "meterd"} {
		writeFile(t, filepath.Join(releaseRoot, "systemd", "faas-"+service+".service"), "[Unit]\n")
		writeFile(t, filepath.Join(unitDir, "faas-"+service+".service.d", "zz-canary.conf"),
			"[Service]\nExecStart=\nExecStart=/opt/faas/canaries/x/bin/"+service+"\n")
	}
	orig := runCommand
	t.Cleanup(func() { runCommand = orig })
	runCommand = func(context.Context, string, ...string) error { return nil }

	r := hostRuntime{unitDir: unitDir, only: set("apid")}
	if err := r.Activate(context.Background(), releaseRoot); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(unitDir, "faas-apid.service.d", "zz-canary.conf")); !os.IsNotExist(err) {
		t.Fatalf("restarted apid kept its canary override: %v", err)
	}
	if _, err := os.Stat(filepath.Join(unitDir, "faas-meterd.service.d", "zz-canary.conf")); err != nil {
		t.Fatalf("meterd is not restarted, so its canary override must stay: %v", err)
	}
}

func TestParseOnly(t *testing.T) {
	if got, err := parseOnly("", false); err != nil || got != nil {
		t.Fatalf("absent --only = %v, %v; want nil (every service)", got, err)
	}
	if got, err := parseOnly("", true); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty --only = %v, %v; want an empty set (no service)", got, err)
	}
	got, err := parseOnly("apid,meterd", true)
	if err != nil || !reflect.DeepEqual(got, set("apid", "meterd")) {
		t.Fatalf("parseOnly = %v, %v", got, err)
	}
	for _, bad := range []string{"apid,", "nope", "apid,,meterd"} {
		if _, err := parseOnly(bad, true); err == nil {
			t.Errorf("parseOnly(%q) accepted an unknown service", bad)
		}
	}
}

func TestEmptyOnlyRestartsNothing(t *testing.T) {
	order, err := daemonunitspec.RestartOrder()
	if err != nil {
		t.Fatal(err)
	}
	r := hostRuntime{serviceOrder: order, only: set()}
	got, err := r.restartServices(manifestWithServices("apid", "meterd"))
	if err != nil || len(got) != 0 {
		t.Fatalf("restartServices = %v, %v; want none", got, err)
	}
}

func set(names ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, name := range names {
		out[name] = struct{}{}
	}
	return out
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A compute host's release ships no units: --only is mandatory, the
// services must be installed on the host, and nothing migrates.
func TestHostManagedReleaseSwapsBinariesOnly(t *testing.T) {
	root := t.TempDir()
	release := filepath.Join(root, "release")
	unitDir := filepath.Join(root, "units")
	writeFile(t, filepath.Join(release, "bin", "gatewayd-internal"), "binary")
	writeFile(t, filepath.Join(unitDir, "faas-gatewayd-internal.service"), "[Unit]\n")
	writeFile(t, filepath.Join(unitDir, "faas-gatewayd-internal.service.d", "zz-canary.conf"),
		"[Service]\nExecStart=\nExecStart=/opt/faas/canaries/x/bin/gatewayd-internal\n")
	if !hostManagedUnits(release) {
		t.Fatal("a release without systemd/ is host-managed")
	}
	orig := runCommand
	t.Cleanup(func() { runCommand = orig })
	var calls []string
	runCommand = func(_ context.Context, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	order, err := daemonunitspec.RestartOrder()
	if err != nil {
		t.Fatal(err)
	}
	stubSystemdShow(t, map[string]string{"faas-gatewayd-internal.service": "faas-vmmd.service", "faas-vmmd.service": "active loaded"})
	manifest := releasebundle.Manifest{Target: "linux/amd64"}

	all := hostRuntime{unitDir: unitDir, serviceOrder: order}
	if err := all.Preflight(context.Background(), manifest, release); err == nil || !strings.Contains(err.Error(), "--only") {
		t.Fatalf("Preflight without --only = %v", err)
	}
	r := hostRuntime{unitDir: unitDir, serviceOrder: order, only: set("gatewayd-internal")}
	if err := r.Preflight(context.Background(), manifest, release); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	missing := hostRuntime{unitDir: unitDir, serviceOrder: order, only: set("vmmd")}
	if err := missing.Preflight(context.Background(), manifest, release); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("Preflight for an uninstalled service = %v", err)
	}
	if err := r.Migrate(context.Background(), manifest, release, ""); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := r.Activate(context.Background(), release); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if want := []string{"systemctl daemon-reload"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("host-managed activation ran %v, want only %v (no unit install, no migration)", calls, want)
	}
	if _, err := os.Stat(filepath.Join(unitDir, "faas-gatewayd-internal.service.d", "zz-canary.conf")); !os.IsNotExist(err) {
		t.Fatalf("restarted service kept its canary override: %v", err)
	}
	if _, err := os.Stat(filepath.Join(unitDir, "faas-gatewayd-internal.service")); err != nil {
		t.Fatalf("host unit removed: %v", err)
	}
}

// A release without unit files carries no service list, so restarts fall
// back to the registry's restart order. --only must still narrow it: an edge
// deploy of gatewayd-internal on a compute host restarted vmmd first.
func TestOnlyNarrowsTheRestartOrderOfAHostManagedRelease(t *testing.T) {
	order, err := daemonunitspec.RestartOrder()
	if err != nil {
		t.Fatal(err)
	}
	hostManaged := releasebundle.Manifest{Files: []releasebundle.File{{Path: "bin/gatewayd-internal"}}}
	r := hostRuntime{serviceOrder: order, only: set("gatewayd-internal")}
	got, err := r.restartServices(hostManaged)
	if err != nil || !reflect.DeepEqual(got, []string{"gatewayd-internal"}) {
		t.Fatalf("restartServices = %v, %v; want only gatewayd-internal", got, err)
	}

	orig := runCommand
	t.Cleanup(func() { runCommand = orig })
	var restarted []string
	runCommand = func(_ context.Context, name string, args ...string) error {
		if name == "systemctl" && len(args) >= 2 && (args[0] == "restart" || args[0] == "reload") {
			restarted = append(restarted, args[1])
		}
		return nil
	}
	r.binaryDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(r.binaryDir, "gatewayd-internal"), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	r.readyTimeout = 100 * time.Millisecond
	r.waitReadyOverride = func(context.Context, string) error { return nil }
	if err := r.Restart(context.Background(), hostManaged); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if want := []string{"faas-gatewayd-internal.service"}; !reflect.DeepEqual(restarted, want) {
		t.Fatalf("host-managed --only restarted %v, want %v", restarted, want)
	}
}

// stubSystemdShow answers `systemctl show` for the dependency guard: a
// service's entry is its Wants/Requires/BindsTo values, a dependency's its
// "ActiveState LoadState", printed the way systemctl does: Key=Value lines
// in systemd's own order (LoadState before ActiveState), whatever order -p
// asked for.
func stubSystemdShow(t *testing.T, units map[string]string) {
	t.Helper()
	orig := commandOutput
	t.Cleanup(func() { commandOutput = orig })
	commandOutput = func(_ context.Context, name string, args ...string) (string, error) {
		if name != "systemctl" || len(args) < 2 || args[0] != "show" {
			return "", fmt.Errorf("unexpected %s %v", name, args)
		}
		value := units[args[1]]
		if strings.Contains(strings.Join(args, " "), "ActiveState") {
			states := strings.Fields(value)
			return "LoadState=" + states[1] + "\nActiveState=" + states[0] + "\n", nil
		}
		return value + "\n", nil
	}
}

func TestOnlyDeployRefusesToStartAStoppedDependency(t *testing.T) {
	order, err := daemonunitspec.RestartOrder()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, vmmd, want string
	}{
		{name: "dependency active", vmmd: "active loaded"},
		{name: "dependency masked", vmmd: "inactive masked"},
		{name: "dependency stopped", vmmd: "inactive loaded", want: "faas-vmmd.service, which is inactive (loaded)"},
		{name: "dependency crash-looping", vmmd: "activating loaded", want: "which is activating (loaded)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubSystemdShow(t, map[string]string{
				// gatewayd-internal Wants vmmd and realtimed, and the slice.
				"faas-gatewayd-internal.service": "faas-cp.slice faas-vmmd.service faas-realtimed.service",
				"faas-vmmd.service":              tc.vmmd,
				"faas-realtimed.service":         "active loaded",
			})
			r := hostRuntime{serviceOrder: order, only: set("gatewayd-internal")}
			err := r.requireDependenciesUp(context.Background())
			if tc.want == "" {
				if err != nil {
					t.Fatalf("requireDependenciesUp: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("requireDependenciesUp = %v, want %q", err, tc.want)
			}
		})
	}
	// A full deploy restarts every daemon anyway, so it is not guarded.
	if err := (hostRuntime{serviceOrder: order}).requireDependenciesUp(context.Background()); err != nil {
		t.Fatalf("full deploy guarded: %v", err)
	}
}
