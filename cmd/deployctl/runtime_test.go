package main

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
	"github.com/onebox-faas/faas/pkg/releasebundle"
)

func TestCleanupBaseScratchRemovesOnlyControllerTempFiles(t *testing.T) {
	if _, err := user.Lookup("faas-imaged"); err != nil {
		t.Skip("host has no faas-imaged user; cleanup contract is production-only")
	}
	root := t.TempDir()
	remove := filepath.Join(root, "faas-base-mkfs-old.ext4")
	keepFile := filepath.Join(root, "customer.ext4")
	keepDir := filepath.Join(root, "faas-base-mkfs-active")
	for _, path := range []string{remove, keepFile} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(keepDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := cleanupBaseScratch(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(remove); !os.IsNotExist(err) {
		t.Fatalf("controller temp file still exists: %v", err)
	}
	for _, path := range []string{keepFile, keepDir} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("protected path %s changed: %v", path, err)
		}
	}
}

// TestCleanupBaseScratchRemovesExtractionDirsAndPreservesForeignOwned
// covers the expanded contract: faas-base-* extraction DIRECTORIES are
// removed (not just mkfs .ext4 files), but only when owned by the
// faas-imaged service user — a foreign-owned directory with the same
// name prefix is preserved. The ownership filter is the defence
// against deleting an operator's artifact that happens to match the
// controller name pattern.
func TestCleanupBaseScratchRemovesExtractionDirsAndPreservesForeignOwned(t *testing.T) {
	owner, err := user.Lookup("faas-imaged")
	if err != nil {
		t.Skip("host has no faas-imaged user; ownership filter untestable here")
	}
	ownerUID, err := strconv.Atoi(owner.Uid)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	ownedDir := filepath.Join(root, "faas-base-12345")
	if err := os.Mkdir(ownedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(ownedDir, ownerUID, -1); err != nil {
		t.Skip("chown to faas-imaged requires root; ownership filter untestable here")
	}
	foreignDir := filepath.Join(root, "faas-base-foreign")
	if err := os.Mkdir(foreignDir, 0o755); err != nil {
		t.Fatal(err)
	}
	foreignFile := filepath.Join(root, "faas-base-mkfs-foreign.ext4")
	if err := os.WriteFile(foreignFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := cleanupBaseScratch(root); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(ownedDir); !os.IsNotExist(err) {
		t.Fatalf("owned extraction dir still exists: %v", err)
	}
	for _, path := range []string{foreignDir, foreignFile} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("foreign-owned path %s was removed: %v", path, err)
		}
	}
}

// TestEnsureBaseStagingRootsOwnership validates that the two staging
// roots are created with the faas-imaged service ownership (so imaged
// can write faas-base-* temp dirs inside them under
// ProtectSystem=strict). Requires root + the service user to exist;
// skipped otherwise.
func TestEnsureBaseStagingRootsOwnership(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("chown requires root")
	}
	svcUser, err := user.Lookup("faas-imaged")
	if err != nil {
		t.Skip("host has no faas-imaged user")
	}
	svcGroup, err := user.LookupGroup("faas")
	if err != nil {
		t.Skip("host has no faas group")
	}
	uid, _ := strconv.Atoi(svcUser.Uid)
	gid, _ := strconv.Atoi(svcGroup.Gid)

	for _, root := range baseStagingRoots {
		info, err := os.Stat(root)
		if err != nil {
			t.Fatalf("stat %s: %v", root, err)
		}
		sys, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			t.Fatalf("%s: unexpected stat type", root)
		}
		if int(sys.Uid) != uid || int(sys.Gid) != gid {
			t.Errorf("%s owned by %d:%d, want %d:%d", root, sys.Uid, sys.Gid, uid, gid)
		}
	}
}

// TestDefaultHostRuntimeUsesRestartOrder asserts the PR-B contract:
// the hostRuntime that production gets from defaultHostRuntime() is
// configured with serviceOrder from daemonunitspec.RestartOrder(),
// not from the legacy slice-walking ActivationOrder().
//
// The Restart() loop itself stays a simple forward iterator — it
// should NOT re-sort, because doing so would defeat the test seam
// (waitReadyOverride) and silently re-order a serviceOrder that an
// operator might have intentionally constructed for a partial restart.
// PR-B's surface is at the *construction* layer (defaultHostRuntime),
// not the iteration layer.
//
// This unit-level check replaces what would otherwise be an integration
// test that needs real systemctl + a Linux box. The shape of the
// contract is small enough that a unit test is enough: the
// topological sort itself has its own exhaustive coverage in
// pkg/daemonunitspec/restart_order_test.go.
func TestDefaultHostRuntimeUsesRestartOrder(t *testing.T) {
	want, err := daemonunitspec.RestartOrder()
	if err != nil {
		t.Fatalf("RestartOrder: %v", err)
	}

	r := defaultHostRuntime()
	if !reflect.DeepEqual(r.serviceOrder, want) {
		t.Errorf("defaultHostRuntime().serviceOrder =\n  got:  %v\n  want: %v\n(pr-B wires the host to the topological order; an operator-side override can still pass a scrambled slice to Restart, but the production default reads RestartOrder)",
			r.serviceOrder, want)
	}
}

func TestServicesInUnitDirScopesToBundledRole(t *testing.T) {
	dir := t.TempDir()
	for _, service := range []string{
		"apid", "schedd", "gatewayd-public", "meterd", "githubd",
	} {
		if err := os.WriteFile(filepath.Join(dir, "faas-"+service+".service"), []byte("[Unit]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "faas-cp.slice"), []byte("[Slice]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := servicesInUnitDir(dir)
	if err != nil {
		t.Fatalf("servicesInUnitDir: %v", err)
	}
	want := []string{"apid", "schedd", "gatewayd-public", "meterd", "githubd"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("servicesInUnitDir = %v, want %v", got, want)
	}
}

func TestServicesInUnitDirRejectsUnknownDaemon(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "faas-not-a-daemon.service"), []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := servicesInUnitDir(dir); err == nil {
		t.Fatal("servicesInUnitDir accepted an unknown daemon unit")
	}
}

func TestRemoveCanaryExecOverridesPreservesConfigurationCanaries(t *testing.T) {
	unitDir := t.TempDir()
	dropInDir := filepath.Join(unitDir, "faas-vmmd.service.d")
	if err := os.Mkdir(dropInDir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"zz-old-daemon.conf":  "[Service]\nExecStart=\nExecStart=\"/opt/faas/canaries/restore/bin/vmmd\" --config /etc/faas/vmmd.toml\n",
		"zz-firecracker.conf": "[Service]\nEnvironment=PATH=/opt/faas/canaries/tsc-runtime:/usr/bin\n",
		"99-managed.conf":     "[Service]\nEnvironment=FAAS_NODE_NAME=fsn-2.faas\n",
		"zz-local.conf":       "[Service]\nExecStart=\nExecStart=/opt/faas/current/bin/vmmd --config /etc/faas/vmmd.toml\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dropInDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := removeCanaryExecOverrides(unitDir, []string{"vmmd", "imaged"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dropInDir, "zz-old-daemon.conf")); !os.IsNotExist(err) {
		t.Fatalf("canary ExecStart override still exists: %v", err)
	}
	for _, name := range []string{"zz-firecracker.conf", "99-managed.conf", "zz-local.conf"} {
		if _, err := os.Stat(filepath.Join(dropInDir, name)); err != nil {
			t.Fatalf("preserved drop-in %s changed: %v", name, err)
		}
	}
}

func TestIsCanaryExecOverrideIgnoresCommentsAndEnvironment(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "quoted exec", body: "ExecStart=\"/opt/faas/canaries/test/bin/apid\"", want: true},
		{name: "unquoted exec", body: "ExecStart=/opt/faas/canaries/test/bin/apid", want: true},
		{name: "environment path", body: "Environment=PATH=/opt/faas/canaries/tsc:/usr/bin", want: false},
		{name: "comment", body: "# ExecStart=/opt/faas/canaries/test/bin/apid", want: false},
		{name: "release binary", body: "ExecStart=/opt/faas/current/bin/apid", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCanaryExecOverride(tt.body); got != tt.want {
				t.Fatalf("isCanaryExecOverride() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReconcileServiceTopologyRemovesOppositeRoleResidue(t *testing.T) {
	unitDir := t.TempDir()
	for _, name := range []string{"faas-vmmd.service", "faas-gatewayd.service"} {
		if err := os.WriteFile(filepath.Join(unitDir, name), []byte("[Unit]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/dev/null", filepath.Join(unitDir, "faas-builderd.service")); err != nil {
		t.Fatal(err)
	}

	orig := runCommand
	t.Cleanup(func() { runCommand = orig })
	var calls [][]string
	runCommand = func(_ context.Context, name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		return nil
	}

	r := hostRuntime{unitDir: unitDir}
	if err := r.reconcileServiceTopology(context.Background(), []string{"apid"}); err != nil {
		t.Fatalf("reconcileServiceTopology: %v", err)
	}

	hasCall := func(want ...string) bool {
		for _, call := range calls {
			if reflect.DeepEqual(call, want) {
				return true
			}
		}
		return false
	}
	if !hasCall("systemctl", "disable", "--now", "faas-vmmd.service") {
		t.Errorf("missing disable for stale vmmd: %v", calls)
	}
	if hasCall("systemctl", "disable", "--now", "faas-builderd.service") {
		t.Errorf("already-masked builderd was sent through disable --now: %v", calls)
	}
	for _, service := range []string{"vmmd", "gatewayd"} {
		if _, err := os.Lstat(filepath.Join(unitDir, "faas-"+service+".service")); !os.IsNotExist(err) {
			t.Errorf("stale %s unit still exists, err=%v", service, err)
		}
	}
	if target, err := os.Readlink(filepath.Join(unitDir, "faas-builderd.service")); err != nil || target != "/dev/null" {
		t.Errorf("existing builderd mask was removed or changed, target=%q err=%v", target, err)
	}
	for _, service := range []string{"vmmd", "builderd", "gatewayd", "spool-sync"} {
		if !hasCall("systemctl", "mask", "--force", "faas-"+service+".service") {
			t.Errorf("missing mask for omitted %s: %v", service, calls)
		}
	}
}

func TestRestartServicesFiltersRegistryByManifest(t *testing.T) {
	order, err := daemonunitspec.RestartOrder()
	if err != nil {
		t.Fatal(err)
	}
	manifest := releasebundle.Manifest{}
	for _, service := range []string{"apid", "schedd", "gatewayd-public", "meterd", "githubd"} {
		manifest.Files = append(manifest.Files, releasebundle.File{Path: "systemd/faas-" + service + ".service"})
	}
	r := hostRuntime{serviceOrder: order}
	got, err := r.restartServices(manifest)
	if err != nil {
		t.Fatalf("restartServices: %v", err)
	}
	want := []string{"apid", "schedd", "gatewayd-public", "meterd", "githubd"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("restartServices = %v, want %v", got, want)
	}
}

func TestHealthAddressFollowsBundledRole(t *testing.T) {
	manifest := releasebundle.Manifest{}
	manifest.Files = []releasebundle.File{{Path: "systemd/faas-gatewayd-public.service"}}
	got, err := healthAddressForManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://127.0.0.1:9092/healthz" {
		t.Fatalf("control-plane health address = %q, want public control listener", got)
	}

	manifest.Files = []releasebundle.File{{Path: "systemd/faas-gatewayd-internal.service"}}
	got, err = healthAddressForManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://127.0.0.1:9090/healthz" {
		t.Fatalf("compute health address = %q, want internal control listener", got)
	}
}

func TestReadinessProbeFromConfigUsesSplitTCPListener(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedd.toml")
	if err := os.WriteFile(path, []byte("socket_path = \"/run/faas/schedd.sock\"\nlisten_addr = \"tcp://0.0.0.0:9091\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	probe, target, err := readinessProbeFromConfig(path, daemonunitspec.ProbeUnix, "/run/faas/schedd.sock")
	if err != nil {
		t.Fatal(err)
	}
	if probe != daemonunitspec.ProbeTCP || target != "127.0.0.1:9091" {
		t.Fatalf("probe = %q %q, want tcp 127.0.0.1:9091", probe, target)
	}
}

func TestReadinessProbeFromConfigUsesLegacyUnixFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedd.toml")
	if err := os.WriteFile(path, []byte("socket_path = \"/run/faas/custom-schedd.sock\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	probe, target, err := readinessProbeFromConfig(path, daemonunitspec.ProbeUnix, "/run/faas/schedd.sock")
	if err != nil {
		t.Fatal(err)
	}
	if probe != daemonunitspec.ProbeUnix || target != "/run/faas/custom-schedd.sock" {
		t.Fatalf("probe = %q %q, want unix /run/faas/custom-schedd.sock", probe, target)
	}
}

func TestReadinessProbeForListenTargetAcceptsBareTCP(t *testing.T) {
	probe, target, err := readinessProbeForListenTarget("0.0.0.0:50051")
	if err != nil {
		t.Fatal(err)
	}
	if probe != daemonunitspec.ProbeTCP || target != "127.0.0.1:50051" {
		t.Fatalf("probe = %q %q, want tcp 127.0.0.1:50051", probe, target)
	}
}

// TestHostRestartIteratesServiceOrderInOrder asserts that Restart()
// walks serviceOrder forward without re-sorting. This guards against
// a future refactor that adds a hidden toposort inside Restart —
// that would defeat the waitReadyOverride test seam (Restart would
// re-skip an operator's hand-built slice) and surprise operators
// reading the iteration order in journalctl.
//
// The scrambled slice is *not* a topological order on purpose. If
// Restart secretly toposorted, the test would still pass on a
// Registry sorted the same way as the scrambled slice; that's the
// false positive we're guarding against. We assert the actual emission
// order equals the input order — Restart is a forward iterator.
//
// Mutates package-level runCommand under a mutex; restores on test
// exit via t.Cleanup so a future parallel test cannot be polluted.
func TestHostRestartIteratesServiceOrderInOrder(t *testing.T) {
	var mu sync.Mutex
	orig := runCommand
	t.Cleanup(func() { mu.Lock(); runCommand = orig; mu.Unlock() })

	var (
		recordingMu sync.Mutex
		restarted   []string
	)
	rec := func(_ context.Context, name string, args ...string) error {
		recordingMu.Lock()
		defer recordingMu.Unlock()
		// Capture "systemctl restart faas-X.service" only; skip the
		// reset-failed + chown + chmod + is-active noise so the
		// comparison slice is the exact restart sequence.
		if name == "systemctl" && len(args) >= 2 && args[0] == "restart" {
			restarted = append(restarted, args[1])
		}
		return nil
	}

	mu.Lock()
	runCommand = rec
	mu.Unlock()

	scrambled := []string{
		"gatewayd-public",
		"meterd",
		"gatewayd-internal",
		"vmmd",
		"imaged",
		"githubd",
		"schedd",
		"apid",
	}
	r := hostRuntime{
		unitDir:           "/tmp/nonexistent",
		databaseURL:       "",
		serviceOrder:      scrambled,
		readyTimeout:      100 * time.Millisecond,
		waitReadyOverride: func(_ context.Context, _ string) error { return nil },
	}
	if err := r.Restart(context.Background(), releasebundle.Manifest{}); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	recordingMu.Lock()
	got := append([]string{}, restarted...)
	recordingMu.Unlock()

	// Map recorded service names → daemon names ("faas-X.service" → "X").
	want := scrambled
	for i, svc := range got {
		got[i] = strings.TrimSuffix(strings.TrimPrefix(svc, "faas-"), ".service")
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Restart() iteration order = %v, want exactly %v (Restart must not re-sort)", got, want)
	}
}
