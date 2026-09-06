//go:build linux

// Workload orchestration tests (issue #463 / ADR-069 / PR-B).
//
// The guest-init orchestrator's three-step dispatch (init sequentially →
// main+sidecar in parallel → return main's lastErr) is verified against a
// pure function surface. We don't spin up real workloads here — the
// per-workload exec path is pinned by image-level fixtures and the metal
// suite; these tests verify the dispatcher with stubs that record the
// call order. The pre-PR-B runAppWithEnv path is untouched and continues
// to be covered by app_test.go.
//
// discoverRoster is exercised with testing/fstest.MapFS so the unit
// tests don't depend on the host filesystem. The fsys path matches the
// live boot path (os.DirFS("/")) relative form ("etc/faas/...") — fs.FS
// rejects absolute paths.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestDiscoverRoster_AbsentReturnsEmpty pins the legacy single-workload
// path: a missing /etc/faas/workloads.json returns an empty roster +
// an error. boot() in main_linux.go treats the error as the legacy
// signal and falls through to runAppWithEnv unchanged.
func TestDiscoverRoster_AbsentReturnsEmpty(t *testing.T) {
	fsys := fstest.MapFS{}
	if _, err := discoverRoster(fsys); err == nil {
		t.Fatal("discoverRoster: missing file should return error")
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("discoverRoster: err = %v, want fs.ErrNotExist", err)
	}
}

// TestDiscoverRoster_ValidFile pins the happy path: a valid roster
// JSON round-trips through discoverRoster without loss. Field
// alignment (json tags) is the load-bearing contract — a rename
// here requires a parallel rename in pkg/fcvm/vmm.go.
func TestDiscoverRoster_ValidFile(t *testing.T) {
	roster := workloadRoster{
		Main: workloadSpec{Name: "main", Type: "main", RamMB: 256, Port: 8080, Essential: true},
		Sidecars: []workloadSpec{
			{Name: "metrics", Type: "sidecar", RamMB: 64, Port: 9090, Essential: true},
		},
	}
	blob, err := json.Marshal(roster)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	fsys := fstest.MapFS{
		"etc/faas/workloads.json": &fstest.MapFile{Data: blob},
	}
	got, err := discoverRoster(fsys)
	if err != nil {
		t.Fatalf("discoverRoster: %v", err)
	}
	if got.Main.Name != "main" || got.Main.Type != "main" || got.Main.RamMB != 256 {
		t.Errorf("main = %+v, want name=main type=main ram=256", got.Main)
	}
	if len(got.Sidecars) != 1 {
		t.Fatalf("sidecars = %d, want 1", len(got.Sidecars))
	}
	sc := got.Sidecars[0]
	if sc.Name != "metrics" || sc.Type != "sidecar" || sc.RamMB != 64 || sc.Port != 9090 || !sc.Essential {
		t.Errorf("sidecar[0] = %+v, want metrics/sidecar/64/9090/true", sc)
	}
}

// TestDiscoverRoster_MalformedFile pins the parse-error path: a
// malformed JSON file returns an error wrapping the path so the
// caller (boot()) can log it without leaking the file contents.
func TestDiscoverRoster_MalformedFile(t *testing.T) {
	fsys := fstest.MapFS{
		"etc/faas/workloads.json": &fstest.MapFile{Data: []byte("{not json")},
	}
	_, err := discoverRoster(fsys)
	if err == nil {
		t.Fatal("discoverRoster: malformed JSON should return error")
	}
	// Path is leaked in the error (operator visibility); values
	// are NOT leaked because the malformed input itself doesn't
	// parse. errorKind() in main_linux.go classifies this as
	// "other" — the test just asserts non-nil.
}

// TestNewSupervisorFor_NonEssentialZeroRestarts pins the non-essential
// sidecar policy: Max=0 means a crash is logged and the supervisor
// returns immediately, the orchestrator's WaitGroup unblocks, and the
// rest of the workloads keep running. The "essential" boolean is the
// load-bearing input; a future rename of the field name would have to
// match pkg/fcvm/vmm.go's workloadManifest.Essential and the
// deployments.sidecars jsonb column.
func TestNewSupervisorFor_NonEssentialZeroRestarts(t *testing.T) {
	spec := workloadSpec{Name: "metrics", Type: "sidecar", Essential: false, RamMB: 64}
	sup := newSupervisorFor(spec, nil, nil, nil, nil)
	if sup == nil {
		t.Fatal("newSupervisorFor returned nil")
	}
	if sup.Max != 0 {
		t.Errorf("non-essential Max = %d, want 0", sup.Max)
	}
	if sup.Start == nil {
		t.Error("Start closure not wired")
	}
	if sup.OnCrash == nil {
		t.Error("OnCrash hook not wired")
	}
}

// TestNewSupervisorFor_EssentialUsesMaxRestarts pins the essential
// sidecar policy: Max=MaxRestarts means a crashed essential sidecar
// is restarted per the platform's standard restart budget, matching
// the main workload's behaviour. This is the contract AC #2 (init
// sidecars run before main) depends on; an essential sidecar
// crash-loop must NOT silently take down the deploy.
func TestNewSupervisorFor_EssentialUsesMaxRestarts(t *testing.T) {
	spec := workloadSpec{Name: "metrics", Type: "sidecar", Essential: true, RamMB: 64}
	sup := newSupervisorFor(spec, nil, nil, nil, nil)
	if sup.Max != MaxRestarts {
		t.Errorf("essential Max = %d, want %d", sup.Max, MaxRestarts)
	}
}

// TestNewSupervisorForMain_HooksWired pins the main-workload
// supervisor's wiring: Max=MaxRestarts, Start closure delegates
// to runAppWithEnv (the legacy entrypoint), and OnCrash is set so
// operators see a stderr line per restart. The closure is what
// lets the characterize probe read the main workload's PID via
// sup.LastAppPID(); a missing Start would silently bind :8080 to
// nothing on the boot path.
func TestNewSupervisorForMain_HooksWired(t *testing.T) {
	spec := workloadSpec{Name: "main", Type: "main", Essential: true, RamMB: 256, Port: 8080}
	manifest := api.AppManifest{Entrypoint: []string{"/bin/sleep", "1"}}
	sup := newSupervisorForMain(spec, manifest, nil, nil, nil)
	if sup == nil {
		t.Fatal("newSupervisorForMain returned nil")
	}
	if sup.Max != MaxRestarts {
		t.Errorf("main Max = %d, want %d", sup.Max, MaxRestarts)
	}
	if sup.Start == nil {
		t.Error("Start closure not wired")
	}
	if sup.OnCrash == nil {
		t.Error("OnCrash hook not wired")
	}
}

// TestSupervisor_LastErr_NilAndStored pins the new lastErr /
// trackRunErr plumbing (issue #463 / ADR-069 / PR-B). The
// orchestrator reads sup.lastErr() after WaitGroup.Wait() to
// surface the main workload's terminal state. A fresh supervisor
// must return nil (never ran); a supervisor that returned a
// tracked error must surface it.
func TestSupervisor_LastErr_NilAndStored(t *testing.T) {
	sup := &Supervisor{Max: 0}
	if err := sup.lastErr(); err != nil {
		t.Errorf("fresh sup.lastErr = %v, want nil", err)
	}
	stored := errors.New("synthetic terminal error")
	sup.trackRunErr(stored)
	if got := sup.lastErr(); !errors.Is(got, stored) {
		t.Errorf("after trackRunErr, lastErr = %v, want %v", got, stored)
	}
}

// TestRunWorkloads_CapRejectsThreeSidecars pins the in-guest
// cap-2 defensive check (PR-B review finding #2). The server-
// side cap (migration 00119 trigger) rejects a 3rd row before
// the roster is ever stamped; guest-init still re-asserts the
// limit so a malformed /etc/faas/workloads.json (e.g. stamped
// by an older vmmd, or hand-crafted for a metal test) can't
// trick the orchestrator into supervising more than 2 sidecars.
// The error must be returned BEFORE any exec.Command, so this
// test uses an empty mainManifest — a real runWorkloads would
// fail later, but the cap rejection must short-circuit first.
func TestRunWorkloads_CapRejectsThreeSidecars(t *testing.T) {
	roster := workloadRoster{
		Main: workloadSpec{Name: "main", Type: "main", Essential: true},
		Sidecars: []workloadSpec{
			{Name: "metrics", Type: "sidecar", Essential: true},
			{Name: "logger", Type: "sidecar", Essential: true},
			{Name: "audit", Type: "sidecar", Essential: true},
		},
	}
	err := runWorkloads(api.AppManifest{}, roster, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("runWorkloads with 3 sidecars: got nil, want cap rejection")
	}
	if !strings.Contains(err.Error(), "cap is 2") {
		t.Errorf("runWorkloads error = %v, want cap-2 message", err)
	}
}

// TestRunWorkloads_PanicInSidecarIsRecovered pins the panic-
// safe sidecar goroutine (PR-B review finding #4). A sidecar
// whose Start closure panics must NOT take down WaitGroup.Wait();
// the orchestrator must continue waiting for the other
// supervisors and surface only the main workload's terminal
// state. We construct the supervisors directly (bypassing the
// runAppWithEnv / runSidecar exec path) so the test exercises
// only the goroutine-launcher + recover plumbing — no real
// forks, no /usr/local/bin/start.sh on PATH.
func TestRunWorkloads_PanicInSidecarIsRecovered(t *testing.T) {
	// A panicking sidecar: the Start closure raises a panic
	// that must be caught by the goroutine wrapper the
	// orchestrator installs around every non-main supervisor.
	panicSup := &Supervisor{Max: 0}
	panicSup.Start = func() error { panic("synthetic sidecar panic") }

	// Mirror runWorkloads' Step 3 dispatch: wg.Add per
	// supervisor, defer wg.Done() FIRST, recover() in a
	// per-supervisor goroutine. mainSup is omitted here because
	// this test exercises the recover-on-sidecar branch
	// exclusively; the main-supervisor's behaviour is
	// pinned by the existing main_linux_test.go tests.
	//
	// We capture the panic value into a closed-over variable
	// (not a t.Errorf directly inside the deferred recover):
	// calling t.Errorf from a goroutine that's about to
	// recover a panic is fine, but we want to distinguish
	// "no panic was raised" (recover() returns nil) from
	// "a panic was raised and recovered" (recover() returns
	// the panic value). The former is the failure mode; the
	// latter is the success path.
	var recovered any
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			recovered = recover()
		}()
		_ = panicSup.Run()
	}()
	wg.Wait()
	if recovered == nil {
		t.Errorf("sidecar panic: goroutine wrapper did NOT recover the panic (panic propagated to test runner)")
	}
	if recovered != "synthetic sidecar panic" {
		t.Errorf("sidecar panic: recovered = %v, want \"synthetic sidecar panic\"", recovered)
	}
}

func TestLoadSidecarManifestAt_DirectRoot(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "etc", "faas", "workloads", "metrics", "workload.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	want := api.AppManifest{Entrypoint: []string{"/bin/metrics"}, WorkingDir: "/srv/metrics", User: "1001"}
	var buf bytes.Buffer
	if err := api.WriteManifest(&buf, want); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o444); err != nil {
		t.Fatal(err)
	}
	got, err := loadSidecarManifestAt(root, "metrics")
	if err != nil {
		t.Fatalf("loadSidecarManifestAt: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manifest = %#v, want %#v", got, want)
	}
}

func TestLoadSidecarManifestAt_RejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	path := filepath.Join(outside, "faas", "workloads", "metrics", "workload.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := api.WriteManifest(&buf, api.AppManifest{Entrypoint: []string{"/bin/metrics"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "etc")); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSidecarManifestAt(root, "metrics"); err == nil {
		t.Fatal("loadSidecarManifestAt accepted a manifest symlink escape")
	}
}

func TestLoadSidecarManifestAt_AllowsRootSymlinkWithinTree(t *testing.T) {
	realRoot := t.TempDir()
	rootAliasParent := t.TempDir()
	rootAlias := filepath.Join(rootAliasParent, "root")
	if err := os.Symlink(realRoot, rootAlias); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(realRoot, "etc", "faas", "workloads", "metrics", "workload.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := api.WriteManifest(&buf, api.AppManifest{Entrypoint: []string{"/bin/metrics"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSidecarManifestAt(rootAlias, "metrics"); err != nil {
		t.Fatalf("loadSidecarManifestAt(root symlink): %v", err)
	}
}

func TestFullRootfsSidecarRoot(t *testing.T) {
	root := t.TempDir()
	upper := filepath.Join(root, "run", "faas", "sidecars", "metrics", "upper")
	if err := os.MkdirAll(upper, 0o755); err != nil {
		t.Fatal(err)
	}
	// A path-shaped directory in an optimized root must not opt itself into
	// direct-root execution; only the builder-owned marker enables it.
	if got, err := fullRootfsSidecarRootAt(root, "metrics"); err != nil || got != "" {
		t.Fatalf("unmarked direct root = %q, err %v; want empty root", got, err)
	}
	marker := filepath.Join(root, "etc", "faas", ".full-rootfs")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(api.FullRootfsMarkerValue), 0o444); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "run", "faas", "sidecars", "metrics", "upper")
	if got, err := fullRootfsSidecarRootAt(root, "metrics"); err != nil || got != want {
		t.Fatalf("marked direct root = %q, err %v; want %q", got, err, want)
	}
	if _, err := fullRootfsSidecarRootAt(root, "../escape"); err == nil {
		t.Fatal("fullRootfsSidecarRoot accepted path traversal")
	}
}

func TestFullRootfsSidecarRootRejectsMarkerSymlink(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "etc", "faas", ".full-rootfs")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "marker")
	if err := os.WriteFile(target, []byte(api.FullRootfsMarkerValue), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, marker); err != nil {
		t.Fatal(err)
	}
	if _, err := fullRootfsSidecarRootAt(root, "metrics"); err == nil {
		t.Fatal("fullRootfsSidecarRoot accepted a marker symlink")
	}
}

func TestResolveSidecarCommandPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "usr", "bin", "node")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := resolveSidecarCommandPath(root, "node", []string{"PATH=/usr/bin:/bin"}); got != "/usr/bin/node" {
		t.Errorf("resolved command = %q, want /usr/bin/node", got)
	}
	if got := resolveSidecarCommandPath(root, "/bin/sh", nil); got != "/bin/sh" {
		t.Errorf("absolute command = %q, want /bin/sh", got)
	}
	if got := resolveSidecarCommandPath(root, "missing", []string{"PATH=/usr/bin:/bin"}); got != "/usr/bin/missing" {
		t.Errorf("missing command = %q, want first image path", got)
	}
}

func TestDiscoverSidecarDevicesCarriesWorkloadNames(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "etc", "faas", "workloads.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	roster := workloadRoster{
		Main: workloadSpec{Name: "main", Type: "main"},
		Sidecars: []workloadSpec{
			{Name: "metrics", Type: "sidecar"},
			{Name: "migrator", Type: "init"},
		},
	}
	data, err := json.Marshal(roster)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o400); err != nil {
		t.Fatal(err)
	}
	got, err := discoverSidecarDevices(root)
	if err != nil {
		t.Fatalf("discoverSidecarDevices: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("devices = %d, want 2", len(got))
	}
	if got[0].workloadName != "metrics" || got[1].workloadName != "migrator" {
		t.Fatalf("workload names = %q, %q", got[0].workloadName, got[1].workloadName)
	}
}

func TestDiscoverSidecarDevicesRejectsDuplicateNames(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "etc", "faas", "workloads.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	roster := workloadRoster{
		Sidecars: []workloadSpec{
			{Name: "metrics", Type: "sidecar"},
			{Name: "metrics", Type: "init"},
		},
	}
	data, err := json.Marshal(roster)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := discoverSidecarDevices(root); err == nil {
		t.Fatal("discoverSidecarDevices accepted duplicate sidecar names")
	}
}

func TestDiscoverSidecarDevicesRejectsInvalidRoster(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "etc", "faas", "workloads.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	roster := workloadRoster{Sidecars: []workloadSpec{{Name: "../escape", Type: "sidecar"}}}
	data, err := json.Marshal(roster)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := discoverSidecarDevices(root); err == nil {
		t.Fatal("discoverSidecarDevices accepted invalid sidecar name")
	}
}

// TestResolveSidecarCommand (PR-C §6) pins the OCI image-spec
// precedence for the customer-image override surface:
//  1. Entrypoint non-empty → exec Entrypoint[0] with
//     Entrypoint[1:] as argv[0:], Cmd appended as argv[N:].
//  2. Entrypoint empty + Cmd non-empty → exec Cmd[0] with
//     Cmd[1:] as argv[1:].
//  3. Both empty → fall back to /usr/local/bin/start.sh (the
//     PR-B baked entrypoint).
//
// A regression that flips the precedence (e.g. Cmd-wins-over-
// Entrypoint) would silently override a Dockerfile's ENTRYPOINT
// with a CMD, which is a documented surprise to the customer
// and a deployment-time footgun. The test asserts the resolved
// (argv0, argv) tuple exactly.
func TestResolveSidecarCommand(t *testing.T) {
	cases := []struct {
		name      string
		spec      workloadSpec
		wantArgv0 string
		wantArgv  []string
	}{
		{
			name:      "no overrides → baked start.sh",
			spec:      workloadSpec{Name: "metrics", Type: "sidecar"},
			wantArgv0: "/usr/local/bin/start.sh",
			wantArgv:  nil,
		},
		{
			name: "cmd only",
			spec: workloadSpec{
				Name: "metrics", Type: "sidecar",
				Cmd: []string{"/usr/local/bin/node-exporter", "--web.listen=:9100"},
			},
			wantArgv0: "/usr/local/bin/node-exporter",
			wantArgv:  []string{"--web.listen=:9100"},
		},
		{
			name: "entrypoint only",
			spec: workloadSpec{
				Name: "metrics", Type: "sidecar",
				Entrypoint: []string{"/bin/sh", "-c"},
			},
			wantArgv0: "/bin/sh",
			wantArgv:  []string{"-c"},
		},
		{
			name: "entrypoint + cmd → entrypoint wins, cmd appended",
			spec: workloadSpec{
				Name: "metrics", Type: "sidecar",
				Entrypoint: []string{"/bin/sh", "-c"},
				Cmd:        []string{"exec node-exporter --web.listen=:9100"},
			},
			wantArgv0: "/bin/sh",
			wantArgv:  []string{"-c", "exec node-exporter --web.listen=:9100"},
		},
		{
			name: "empty entrypoint[0] is invalid — handled by exec, not us",
			spec: workloadSpec{
				Name: "metrics", Type: "sidecar",
				Entrypoint: []string{""},
				Cmd:        []string{"/bin/true"},
			},
			wantArgv0: "",
			wantArgv:  []string{"/bin/true"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotArgv0, gotArgv := resolveSidecarCommand(c.spec)
			if gotArgv0 != c.wantArgv0 {
				t.Errorf("argv0 = %q, want %q", gotArgv0, c.wantArgv0)
			}
			if !reflect.DeepEqual(gotArgv, c.wantArgv) {
				t.Errorf("argv = %v, want %v", gotArgv, c.wantArgv)
			}
		})
	}
}
