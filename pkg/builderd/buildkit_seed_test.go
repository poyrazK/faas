package builderd

// adr: 20261030122204

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestParseBuildKitSeed(t *testing.T) {
	cases := []struct {
		raw     string
		want    bool
		wantErr bool
	}{
		{raw: "", want: false},
		{raw: "  ", want: false},
		{raw: "true", want: true},
		{raw: "1", want: true},
		{raw: " false ", want: false},
		{raw: "0", want: false},
		{raw: "yes", wantErr: true},
	}
	for _, tc := range cases {
		got, err := ParseBuildKitSeed(tc.raw)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("ParseBuildKitSeed(%q) = %v, %v; want %v, error %v", tc.raw, got, err, tc.want, tc.wantErr)
		}
	}
}

func TestBuildKitSeedSourceTarIsStableAndHoldsTheSeedApp(t *testing.T) {
	first, err := buildkitSeedSourceTar()
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildkitSeedSourceTar()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("the seed source tarball differs between calls, so the seed key would too")
	}
	gz, err := gzip.NewReader(bytes.NewReader(first))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	if strings.Join(names, ",") != "package.json,index.js" {
		t.Fatalf("seed source holds %v, want package.json and index.js", names)
	}
}

func TestBuildKitSeedKeyCoversWhatTheSeedHolds(t *testing.T) {
	env := BuildEnvironment{BuilderBaseIdentity: "base-a", BaseDigest: "sha256:aa", TargetPlatform: "linux/amd64"}
	base, err := buildkitSeedKey(env, api.BuildKitSnapshotterAuto)
	if err != nil {
		t.Fatal(err)
	}
	again, err := buildkitSeedKey(env, api.BuildKitSnapshotterAuto)
	if err != nil || again != base {
		t.Fatalf("key not stable: %q then %q (%v)", base, again, err)
	}
	changed := map[string]func(*BuildEnvironment, *string){
		"builder base identity": func(e *BuildEnvironment, _ *string) { e.BuilderBaseIdentity = "base-b" },
		"base digest":           func(e *BuildEnvironment, _ *string) { e.BaseDigest = "sha256:bb" },
		"platform":              func(e *BuildEnvironment, _ *string) { e.TargetPlatform = "linux/arm64" },
		"snapshotter":           func(_ *BuildEnvironment, s *string) { *s = "" },
	}
	for name, change := range changed {
		e, snapshotter := env, api.BuildKitSnapshotterAuto
		change(&e, &snapshotter)
		key, err := buildkitSeedKey(e, snapshotter)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if key == base {
			t.Errorf("changing the %s keeps the seed key", name)
		}
	}
	for _, incomplete := range []BuildEnvironment{
		{BaseDigest: "sha256:aa", TargetPlatform: "linux/amd64"},
		{BuilderBaseIdentity: "base-a", TargetPlatform: "linux/amd64"},
		{BuilderBaseIdentity: "base-a", BaseDigest: "sha256:aa"},
	} {
		if _, err := buildkitSeedKey(incomplete, ""); err == nil {
			t.Errorf("buildkitSeedKey(%+v) accepted an incomplete builder base identity", incomplete)
		}
	}
}

func TestBuildKitSeedPaths(t *testing.T) {
	if got := buildkitSeedDir("/srv/fc/builder/drive/"); got != "/srv/fc/builder/buildkit-seed" {
		t.Fatalf("seed dir = %q, want a sibling of the drive dir", got)
	}
	if got := buildkitSeedCopyName("build-x"); got != "build-x-buildkit.ext4" {
		t.Fatalf("copy name = %q", got)
	}
}

func TestBuildKitSeedEligible(t *testing.T) {
	cases := []struct {
		name string
		req  VMRequest
		want bool
	}{
		{name: "railpack node build", req: VMRequest{Framework: FrameworkNode}, want: true},
		{name: "railpack auto-detected build", req: VMRequest{Framework: FrameworkUnknown}, want: true},
		{name: "Dockerfile build", req: VMRequest{Framework: FrameworkDocker}},
		{name: "Compose build options", req: VMRequest{Framework: FrameworkNode, DockerfileBuild: &api.DockerfileBuildOptions{}}},
		{name: "warm builder", req: VMRequest{Framework: FrameworkNode, KeepWarm: true}},
		{name: "seed run", req: VMRequest{Framework: FrameworkNode, BuildKitSeedOnly: true}},
	}
	for _, tc := range cases {
		if got := buildKitSeedEligible(tc.req); got != tc.want {
			t.Errorf("%s: eligible = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestBuildKitSeedStoreInstallsOneReadySeed(t *testing.T) {
	root := t.TempDir()
	s := &buildkitSeedStore{dir: filepath.Join(root, "buildkit-seed")}
	if _, ok := s.ready("new"); ok {
		t.Fatal("an empty store reports a ready seed")
	}
	if err := s.copyTo("new", filepath.Join(root, "copy.ext4")); !errors.Is(err, errNoReadySeed) {
		t.Fatalf("copyTo without a seed = %v, want errNoReadySeed", err)
	}
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, s.seedPath("old"), "old seed")
	writeFile(t, s.readyPath("old"), "ready")
	// A seed drive without its ready marker is a run that never finished.
	writeFile(t, s.seedPath("unfinished"), "partial")
	if _, ok := s.ready("unfinished"); ok {
		t.Fatal("a seed without a ready marker is ready")
	}
	if err := os.Mkdir(filepath.Join(s.dir, "keep-dir"), 0o750); err != nil {
		t.Fatal(err)
	}

	drive := filepath.Join(root, "build-seed-buildkit.ext4")
	writeFile(t, drive, "new seed")
	if err := s.install("new", drive); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(drive); !os.IsNotExist(err) {
		t.Fatalf("install left the run's drive in place: %v", err)
	}
	path, ok := s.ready("new")
	if !ok || path != s.seedPath("new") {
		t.Fatalf("ready(new) = %q, %v after install", path, ok)
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if got := strings.Join(names, ","); got != "keep-dir,new.ext4,new.ready" {
		t.Fatalf("seed dir after install holds %s, want only the new seed (and directories it does not own)", got)
	}

	s.removeAll()
	if _, ok := s.ready("new"); ok {
		t.Fatal("removeAll kept the seed")
	}
}

func TestAcquireIdleSlotNeverTakesTheSlotFromABuild(t *testing.T) {
	b := &Builderd{resid: fakeResid{mb: 0}}
	_, releaseBuild, ok := b.acquireSlot()
	if !ok {
		t.Fatal("build slot unavailable")
	}
	if _, ok := b.acquireIdleSlot(); ok {
		t.Fatal("the seed run took the slot a build holds")
	}
	releaseBuild()

	releaseSeed, ok := b.acquireIdleSlot()
	if !ok {
		t.Fatal("idle slot unavailable")
	}
	if _, _, ok := b.acquireSlot(); ok {
		t.Fatal("a build got a second slot past the parent cgroup fence while the seed run held one")
	}
	releaseSeed()
	releaseSeed()
	if _, release, ok := b.acquireSlot(); !ok {
		t.Fatal("the slot stayed held after the seed run released it")
	} else {
		release()
	}
}

type seedVM struct {
	VM
	b        *Builderd
	calls    int
	made     bool
	err      error
	buildID  string
	snapshot string
	tracked  bool
}

func (v *seedVM) CreateBuildKitSeed(_ context.Context, buildID, snapshotter string) (bool, error) {
	v.calls++
	v.buildID, v.snapshot = buildID, snapshotter
	v.b.lifecycleMu.Lock()
	_, v.tracked = v.b.activeVMs[buildID]
	v.b.lifecycleMu.Unlock()
	return v.made, v.err
}

func TestCreateBuildKitSeedRunsInTheIdleSlot(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	newBuilderd := func() (*Builderd, *seedVM) {
		vm := &seedVM{made: true}
		b := &Builderd{resid: fakeResid{mb: 0}, log: log, cfg: Config{BuildKitSeed: true, BuildKitSnapshotter: api.BuildKitSnapshotterAuto}}
		vm.b, b.vm = b, vm
		return b, vm
	}

	b, vm := newBuilderd()
	if err := b.createBuildKitSeed(context.Background()); err != nil {
		t.Fatal(err)
	}
	if vm.calls != 1 || !strings.HasPrefix(vm.buildID, "seed-") || vm.snapshot != api.BuildKitSnapshotterAuto {
		t.Fatalf("seed run = %d calls, id %q, snapshotter %q", vm.calls, vm.buildID, vm.snapshot)
	}
	if !vm.tracked {
		t.Fatal("the seed run's VM was not registered, so Drain could not stop it")
	}
	if len(b.activeVMs) != 0 {
		t.Fatalf("active VMs after the run: %v", b.activeVMs)
	}
	if _, release, ok := b.acquireSlot(); !ok {
		t.Fatal("the seed run kept the builder slot")
	} else {
		release()
	}

	b, vm = newBuilderd()
	_, release, _ := b.acquireSlot()
	if err := b.createBuildKitSeed(context.Background()); err != nil || vm.calls != 0 {
		t.Fatalf("with a build in the slot: err %v, %d seed runs; want none", err, vm.calls)
	}
	release()

	b, vm = newBuilderd()
	b.lifecycleMu.Lock()
	b.draining = true
	b.lifecycleMu.Unlock()
	if err := b.createBuildKitSeed(context.Background()); err != nil || vm.calls != 0 {
		t.Fatalf("while draining: err %v, %d seed runs; want none", err, vm.calls)
	}

	b, vm = newBuilderd()
	vm.err = errors.New("boom")
	if err := b.createBuildKitSeed(context.Background()); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("a failed seed run returned %v", err)
	}
}

func TestRunBuildKitSeedLoopIsOffByDefault(t *testing.T) {
	vm := &seedVM{}
	b := &Builderd{vm: vm}
	vm.b = b
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// With the switch off the loop returns at once instead of ticking.
	b.RunBuildKitSeedLoop(ctx)
	if vm.calls != 0 {
		t.Fatalf("%d seed runs with the switch off", vm.calls)
	}
}
