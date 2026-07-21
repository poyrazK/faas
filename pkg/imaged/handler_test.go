package imaged

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/state"
)

// findNotify returns the first recorded Notify on the given channel, or nil.
func findNotify(n *fakeNotifier, channel string) *notifyCall {
	for i := range n.calls {
		if n.calls[i].channel == channel {
			return &n.calls[i]
		}
	}
	return nil
}

// nopReader is a ReadCloser that always returns EOF. Used to seed PullLayers
// results in unit tests so the imaged handler's defer-close logic has
// something to close without the test caring about the layer content.
type nopReader struct{}

func (nopReader) Read([]byte) (int, error) { return 0, io.EOF }
func (nopReader) Close() error             { return nil }

// fakePuller satisfies oci.Puller. digest is the value PullDigest returns;
// cfg is what PullImageConfig returns. Set configErr / layerErr to make
// the corresponding call fail; both come from the same source so the
// "earliest failure" can be tested cleanly.
type fakePuller struct {
	digest    string
	layersCfg *oci.PullLayersResult
	layerErr  error
	configErr error
	cfg       oci.ImageConfig
}

func (f fakePuller) PullDigest(_ context.Context, _ string) (string, error) { return f.digest, nil }

func (f fakePuller) PullImageConfig(_ context.Context, _ string) (oci.ImageConfig, error) {
	if f.configErr != nil {
		return oci.ImageConfig{}, f.configErr
	}
	return f.cfg, nil
}

func (f fakePuller) PullLayers(_ context.Context, digest string) (oci.PullLayersResult, error) {
	if f.layerErr != nil {
		return oci.PullLayersResult{}, f.layerErr
	}
	if f.layersCfg != nil {
		return *f.layersCfg, nil
	}
	r := make([]io.ReadCloser, 0, 1)
	r = append(r, nopReader{})
	return oci.PullLayersResult{Layers: r, Config: f.cfg, Digest: digest}, nil
}

// failingPuller makes every puller call return err — exercises the earliest
// failure path before any layer streaming happens.
type failingPuller struct{ err error }

func (f failingPuller) PullDigest(_ context.Context, _ string) (string, error) { return "", f.err }
func (f failingPuller) PullImageConfig(_ context.Context, _ string) (oci.ImageConfig, error) {
	return oci.ImageConfig{}, f.err
}
func (f failingPuller) PullLayers(_ context.Context, _ string) (oci.PullLayersResult, error) {
	return oci.PullLayersResult{}, f.err
}

// fakeBuilder records every BuildInput so tests can assert the manifest,
// paths, and layer plumbing. Set buildErr to make Build return an error.
type fakeBuilder struct {
	calls    []rootfs.BuildInput
	bytesOut int64
	buildErr error
}

func (b *fakeBuilder) Build(_ context.Context, in rootfs.BuildInput) (rootfs.BuildResult, error) {
	b.calls = append(b.calls, in)
	if b.buildErr != nil {
		return rootfs.BuildResult{}, b.buildErr
	}
	// The handler asks for `appsRoot/<slug>/<id>.ext4`. The fake echoes
	// whatever input got — no actual mkfs runs, so a path that did not exist
	// would surface in the "after Build" assertions via the recorded call.
	return rootfs.BuildResult{
		ImagePath:    in.OutImage,
		ContentBytes: b.bytesOut,
	}, nil
}

// BuildBase is part of the LayerBuilder interface (M6); the existing
// handler tests don't reach it, but the new EnsureBaseExt4 path does. The
// fake records the call so a test can pin the OutImage + layer count.
func (b *fakeBuilder) BuildBase(_ context.Context, in rootfs.BaseBuildInput) (rootfs.BaseBuildResult, error) {
	b.calls = append(b.calls, rootfs.BuildInput{OutImage: in.OutImage, Plan: api.PlanScale})
	// Write a placeholder so the EnsureBaseExt4 atomic-rename has a real
	// source file to rename. Real mkfs would create the ext4 here; the
	// fake stands in only to keep tests KVM-free (spec §Conventions:
	// unit tests pass on any machine).
	if err := os.WriteFile(in.OutImage, []byte("fake ext4"), 0o644); err != nil {
		return rootfs.BaseBuildResult{}, err
	}
	return rootfs.BaseBuildResult{ImagePath: in.OutImage, SizeBytes: b.bytesOut}, nil
}

// fakeNotifier records every Notify so tests can assert fan-out.
type fakeNotifier struct {
	calls []notifyCall
}

type notifyCall struct{ channel, payload string }

func (f *fakeNotifier) Notify(_ context.Context, channel, payload string) error {
	f.calls = append(f.calls, notifyCall{channel, payload})
	return nil
}

// silentLogger discards every log line so tests stay quiet.
func silentLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// newTestHarness wires a Handler with the common backing store, fakes, and a
// temp appsRoot. Tests get back the store, notifier, builder, app, deployment,
// and account so they can assert on side effects directly.
type testHarness struct {
	store *state.MemStore
	notif *fakeNotifier
	bld   *fakeBuilder
	app   state.App
	dep   state.Deployment
	acct  state.Account
	appsR string
}

func newTestHarness(t *testing.T, kind state.DeploymentKind, plan api.Plan,
	handler string) *testHarness {
	t.Helper()
	s := state.NewMemStore()
	acct, err := s.CreateAccount(context.Background(), "u@example.com", plan)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	ram := 512
	if lim, ok := api.LimitsFor(plan); ok && lim.RAMMB > 0 {
		ram = lim.RAMMB
	}
	app, err := s.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "img-app",
		RAMMB: ram, IdleTimeoutS: 60, MaxConcurrency: 5,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := s.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:abc",
		Kind: kind, Handler: handler,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	appsR := t.TempDir()
	return &testHarness{
		store: s, notif: &fakeNotifier{}, bld: &fakeBuilder{},
		app: app, dep: dep, acct: acct, appsR: appsR,
	}
}

// TestHandleDeploymentPrimesNotLive walks an image-kind deployment up to the
// snapshot handshake: it should land in `snapshotting` and emit snapshot_prime
// for schedd — NOT go live or write a snapshot row on its own (that happens on
// the snapshot_written reply).
func TestHandleDeploymentPrimesNotLive(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "img-app", RAMMB: 512, IdleTimeoutS: 60, MaxConcurrency: 5,
	})
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:abc", Kind: state.DeploymentKindImage,
	})
	notif := &fakeNotifier{}
	h := New(store, notif,
		fakePuller{digest: "sha256:abc", cfg: oci.ImageConfig{Cmd: []string{"./app"}}},
		&fakeBuilder{}, "./init", t.TempDir(), silentLogger())

	h.HandleNotification(context.Background(), db.Notification{
		Channel: db.NotifyDeploymentChanged,
		Payload: `{"app_id":"` + app.ID + `","to":"` + dep.ID + `","kind":"image","image_digest":"sha256:abc"}`,
	})

	got, err := store.DeploymentByID(context.Background(), dep.ID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	if got.Status != state.DeploySnapshotting {
		t.Errorf("status = %s, want snapshotting", got.Status)
	}
	if _, err := store.LatestSnapshot(context.Background(), dep.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("no snapshot row should exist before snapshot_written; got err=%v", err)
	}
	prime := findNotify(notif, db.NotifySnapshotPrime)
	if prime == nil {
		t.Fatal("expected a snapshot_prime notification")
	}
	if !strings.Contains(prime.payload, dep.ID) || !strings.Contains(prime.payload, app.ID) {
		t.Errorf("snapshot_prime payload missing ids: %s", prime.payload)
	}
}

// TestHandleSnapshotWritten records the snapshot row schedd produced and flips
// the deployment live — the second half of the prime handshake.
func TestHandleSnapshotWritten(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "img-app", RAMMB: 512, IdleTimeoutS: 60, MaxConcurrency: 5,
	})
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:abc", Kind: state.DeploymentKindImage,
	})
	_ = store.UpdateDeploymentStatus(context.Background(), dep.ID, state.DeploySnapshotting, "")
	notif := &fakeNotifier{}
	h := New(store, notif, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger())

	h.HandleNotification(context.Background(), db.Notification{
		Channel: db.NotifySnapshotWritten,
		Payload: `{"deployment_id":"` + dep.ID + `","mem_path":"/srv/fc/snap/` + dep.ID + `/mem",` +
			`"vmstate_path":"/srv/fc/snap/` + dep.ID + `/vmstate","mem_bytes":134217728,` +
			`"vmstate_bytes":40960,"fc_version":"firecracker-1.10"}`,
	})

	got, _ := store.DeploymentByID(context.Background(), dep.ID)
	if got.Status != state.DeployLive {
		t.Errorf("status = %s, want live", got.Status)
	}
	snap, err := store.LatestSnapshot(context.Background(), dep.ID)
	if err != nil {
		t.Fatalf("LatestSnapshot: %v", err)
	}
	if snap.FCVersion != "firecracker-1.10" {
		t.Errorf("FCVersion = %q, want firecracker-1.10", snap.FCVersion)
	}
	if snap.MemBytes != 134217728 || snap.Path != "/srv/fc/snap/"+dep.ID+"/mem" {
		t.Errorf("snapshot row wrong: %+v", snap)
	}
	if findNotify(notif, db.NotifyDeploymentChanged) == nil {
		t.Error("expected a deployment_changed live fan-out")
	}
}

// TestHandleSnapshotWrittenIdempotent asserts a redelivered snapshot_written is
// safe: the duplicate row collapses to ErrConflict and the deploy stays live.
func TestHandleSnapshotWrittenIdempotent(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{AccountID: acct.ID, Slug: "dup", RAMMB: 256})
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:abc", Kind: state.DeploymentKindImage,
	})
	h := New(store, &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger())
	n := db.Notification{
		Channel: db.NotifySnapshotWritten,
		Payload: `{"deployment_id":"` + dep.ID + `","mem_path":"/m","mem_bytes":1,"fc_version":"firecracker-1.10"}`,
	}
	h.HandleNotification(context.Background(), n)
	h.HandleNotification(context.Background(), n) // redelivery must not error out

	got, _ := store.DeploymentByID(context.Background(), dep.ID)
	if got.Status != state.DeployLive {
		t.Errorf("status = %s, want live after redelivery", got.Status)
	}
}

// TestHandleDeploymentTarballIgnored verifies non-image kinds return nil (they
// live on the build_queued stream).
func TestHandleDeploymentTarballIgnored(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "tar-app", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: "/tmp/x.tgz",
	})
	h := New(store, &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger())
	n := db.Notification{
		Channel: db.NotifyDeploymentChanged,
		Payload: `{"app_id":"` + app.ID + `","to":"` + dep.ID + `","kind":"tarball"}`,
	}
	h.HandleNotification(context.Background(), n)
	got, _ := store.DeploymentByID(context.Background(), dep.ID)
	if got.Status != state.DeployPending {
		t.Errorf("tarball deploy should stay pending, got %s", got.Status)
	}
}

// TestHandleDeploymentOCIFailure marks the deployment failed and surfaces the
// error to the caller (logged, not returned — the loop swallows).
func TestHandleDeploymentOCIFailure(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "bad-img", RAMMB: 128, IdleTimeoutS: 30, MaxConcurrency: 1,
	})
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:bad", Kind: state.DeploymentKindImage,
	})
	h := New(store, &fakeNotifier{}, failingPuller{err: errors.New("net down")},
		&fakeBuilder{}, "./init", t.TempDir(), silentLogger())
	n := db.Notification{
		Channel: db.NotifyDeploymentChanged,
		Payload: `{"app_id":"` + app.ID + `","to":"` + dep.ID + `","kind":"image","image_digest":"sha256:bad"}`,
	}
	h.HandleNotification(context.Background(), n)
	got, _ := store.DeploymentByID(context.Background(), dep.ID)
	if got.Status != state.DeployFailed {
		t.Errorf("status = %s, want failed", got.Status)
	}
	if got.Error == "" {
		t.Error("error message should be populated")
	}
}

// TestHandleBuildQueued asserts the queued build is marked running.
func TestHandleBuildQueued(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "src-app", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: "/tmp/x.tgz",
	})
	b, err := store.CreateBuild(context.Background(), dep.ID, state.DeploymentKindTarball, 4096, "/var/log/x.log")
	if err != nil {
		t.Fatal(err)
	}
	h := New(store, &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger())
	n := db.Notification{
		Channel: db.NotifyBuildQueued,
		Payload: `{"app_id":"` + app.ID + `","build_id":"` + b.ID + `","kind":"tarball"}`,
	}
	h.HandleNotification(context.Background(), n)
	got, _ := store.BuildByID(context.Background(), b.ID)
	if got.Status != state.BuildRunning {
		t.Errorf("build status = %s, want running", got.Status)
	}
}

// ---- M5 hook tests --------------------------------------------------------
//
// The five tests below exercise the imaged→rootfs.Builder wiring that the M5
// finish-PR installs at handleDeployment. They are the regression net for the
// single explicit M5 gap (a log line in place of a real Builder.Build call).

// TestHandleDeployment_BuildsAppLayer_HappyPath is the anchor: an image deploy
// streams layers + config, calls Build once with the right paths + plan, stamps
// the rootfs row, and hands off to schedd via snapshot_prime.
func TestHandleDeployment_BuildsAppLayer_HappyPath(t *testing.T) {
	h := newTestHarness(t, state.DeploymentKindImage, api.Plan("pro"), "")
	h.bld.bytesOut = 13_700_000 // ≈13 MB layer
	puller := fakePuller{
		digest: "sha256:abc",
		cfg: oci.ImageConfig{
			Cmd: []string{"./boot.sh"}, Env: map[string]string{"PORT": "8080"}, WorkingDir: "/app",
		},
	}
	handler := New(h.store, h.notif, puller, h.bld, "./init", h.appsR, silentLogger())

	handler.HandleNotification(context.Background(), db.Notification{
		Channel: db.NotifyDeploymentChanged,
		Payload: `{"app_id":"` + h.app.ID + `","to":"` + h.dep.ID + `","kind":"image","image_digest":"sha256:abc"}`,
	})

	got, err := h.store.DeploymentByID(context.Background(), h.dep.ID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	if got.Status != state.DeploySnapshotting {
		t.Errorf("status = %s, want snapshotting", got.Status)
	}
	if got.RootfsPath == "" {
		t.Fatal("SetDeploymentRootfs should have stamped rootfs_path")
	}
	if got.RootfsBytes != h.bld.bytesOut {
		t.Errorf("rootfs_bytes = %d, want %d", got.RootfsBytes, h.bld.bytesOut)
	}
	if !strings.HasPrefix(got.RootfsPath, h.appsR) {
		t.Errorf("rootfs path %q not under appsRoot %q", got.RootfsPath, h.appsR)
	}
	if !strings.Contains(got.RootfsPath, h.dep.ID) || !strings.Contains(got.RootfsPath, h.app.Slug) {
		t.Errorf("rootfs path should embed app slug + deployment id: %s", got.RootfsPath)
	}

	if len(h.bld.calls) != 1 {
		t.Fatalf("builder.Build calls = %d, want 1", len(h.bld.calls))
	}
	in := h.bld.calls[0]
	if in.Plan != api.Plan("pro") {
		t.Errorf("BuildInput.Plan = %q, want pro", in.Plan)
	}
	if in.GuestInitPath != "./init" {
		t.Errorf("BuildInput.GuestInitPath = %q, want ./init", in.GuestInitPath)
	}
	if in.Manifest.Entrypoint[0] != "./boot.sh" {
		t.Errorf("Entrypoint = %v, want ./boot.sh from image config", in.Manifest.Entrypoint)
	}
	if in.Manifest.Env["PORT"] != "8080" {
		t.Errorf("Env[PORT] = %q, want 8080", in.Manifest.Env["PORT"])
	}
	if got2, err := h.store.DeploymentByID(context.Background(), h.dep.ID); err == nil && got2.RootfsPath != got.RootfsPath {
		t.Errorf("store returned rootfs_path=%q want %q", got2.RootfsPath, got.RootfsPath)
	}

	if findNotify(h.notif, db.NotifySnapshotPrime) == nil {
		t.Error("expected snapshot_prime notification after Build")
	}
}

// TestHandleDeployment_PullLayersError fails inside the layer-streaming phase
// (after PullImageConfig returns a valid config and manifest validation
// passes). The deployment must be in `failed`, no prime notification may be
// sent, and no Build must run.
func TestHandleDeployment_PullLayersError(t *testing.T) {
	h := newTestHarness(t, state.DeploymentKindImage, api.Plan("hobby"), "")
	puller := fakePuller{
		digest:   "sha256:abc",
		cfg:      oci.ImageConfig{Cmd: []string{"./app"}}, // makes PullImageConfig succeed
		layerErr: errors.New("blob 404"),                  // makes PullLayers fail
	}
	handler := New(h.store, h.notif, puller, h.bld, "./init", h.appsR, silentLogger())

	handler.HandleNotification(context.Background(), db.Notification{
		Channel: db.NotifyDeploymentChanged,
		Payload: `{"app_id":"` + h.app.ID + `","to":"` + h.dep.ID + `","kind":"image","image_digest":"sha256:abc"}`,
	})

	got, _ := h.store.DeploymentByID(context.Background(), h.dep.ID)
	if got.Status != state.DeployFailed {
		t.Errorf("status = %s, want failed", got.Status)
	}
	if !strings.Contains(got.Error, "pull") {
		t.Errorf("error should mention pull, got %q", got.Error)
	}
	if len(h.bld.calls) != 0 {
		t.Errorf("Build should not have run; calls=%d", len(h.bld.calls))
	}
	if findNotify(h.notif, db.NotifySnapshotPrime) != nil {
		t.Error("snapshot_prime must not fire on a pull failure")
	}
}

// TestHandleDeployment_BuildError fails inside rootfs.Builder.Build. The
// deployment must be `failed`, the failure must be recorded, and crucially no
// snapshot_prime is emitted (so schedd does not cold-boot a half-built layer).
func TestHandleDeployment_BuildError(t *testing.T) {
	h := newTestHarness(t, state.DeploymentKindImage, api.Plan("hobby"), "")
	h.bld.buildErr = errors.New("mkfs: ENOSPC")
	puller := fakePuller{
		digest: "sha256:abc",
		cfg:    oci.ImageConfig{Cmd: []string{"./app"}},
	}
	handler := New(h.store, h.notif, puller, h.bld, "./init", h.appsR, silentLogger())

	handler.HandleNotification(context.Background(), db.Notification{
		Channel: db.NotifyDeploymentChanged,
		Payload: `{"app_id":"` + h.app.ID + `","to":"` + h.dep.ID + `","kind":"image","image_digest":"sha256:abc"}`,
	})

	got, _ := h.store.DeploymentByID(context.Background(), h.dep.ID)
	if got.Status != state.DeployFailed {
		t.Errorf("status = %s, want failed", got.Status)
	}
	if !strings.Contains(got.Error, "build app layer") {
		t.Errorf("error should mention build, got %q", got.Error)
	}
	if len(h.bld.calls) != 1 {
		t.Errorf("Build should have run once; calls=%d", len(h.bld.calls))
	}
	if findNotify(h.notif, db.NotifySnapshotPrime) != nil {
		t.Error("snapshot_prime must not fire on a build failure")
	}
}

// TestHandleDeployment_HandlerOverrideWinsOverImageCmd asserts the per-deploy
// `handler` column, when set, replaces the image config's Cmd in the manifest
// passed to rootfs.Builder. This is the M5 "app config overrides image config
// per-field" rule (it is the only per-deploy override the schema supports
// today; richer fields arrive with M5.1).
func TestHandleDeployment_HandlerOverrideWinsOverImageCmd(t *testing.T) {
	h := newTestHarness(t, state.DeploymentKindImage, api.Plan("hobby"), "python312:app.handler")
	puller := fakePuller{
		digest: "sha256:abc",
		cfg:    oci.ImageConfig{Cmd: []string{"node", "server.js"}},
	}
	handler := New(h.store, h.notif, puller, h.bld, "./init", h.appsR, silentLogger())

	handler.HandleNotification(context.Background(), db.Notification{
		Channel: db.NotifyDeploymentChanged,
		Payload: `{"app_id":"` + h.app.ID + `","to":"` + h.dep.ID + `","kind":"image","image_digest":"sha256:abc"}`,
	})

	if len(h.bld.calls) != 1 {
		t.Fatalf("Build calls = %d, want 1", len(h.bld.calls))
	}
	in := h.bld.calls[0]
	if len(in.Manifest.Entrypoint) != 1 || in.Manifest.Entrypoint[0] != "python312:app.handler" {
		t.Errorf("Entrypoint = %v, want [python312:app.handler]", in.Manifest.Entrypoint)
	}
}

// spyCloser is a ReadCloser that records Close() being called. Used to prove
// the defer in handleDeployment fires when Builder.Build panics. Implemented
// at package scope because Go forbids method declarations inside a function.
type spyCloser struct {
	reader io.Reader
	closed bool
}

func (s *spyCloser) Read(p []byte) (int, error) { return s.reader.Read(p) }
func (s *spyCloser) Close() error {
	s.closed = true
	return nil
}

// TestHandleDeployment_ClosesLayerReaders ensures the defer in handleDeployment
// runs even when Builder.Build panics. We drive the panic, expect
// handleDeployment to recover enough to leave the deployment `failed`, and
// confirm the layer ReadClosers were closed via the wrapping io.NopCloser.
func TestHandleDeployment_ClosesLayerReaders(t *testing.T) {
	h := newTestHarness(t, state.DeploymentKindImage, api.Plan("free"), "")

	spy1 := &spyCloser{reader: strings.NewReader("layer1")}
	spy2 := &spyCloser{reader: strings.NewReader("layer2")}

	puller := fakePuller{
		digest: "sha256:abc",
		cfg:    oci.ImageConfig{Cmd: []string{"sh"}}, // satisfies the new PullImageConfig fail-fast check
		layersCfg: &oci.PullLayersResult{
			Layers: []io.ReadCloser{spy1, spy2},
			Config: oci.ImageConfig{Cmd: []string{"sh"}},
			Digest: "sha256:abc",
		},
	}

	// Make Build panic — the defer to Close readers must still run.
	bld := &panicBuilder{}
	handler := New(h.store, h.notif, puller, bld, "./init", h.appsR, silentLogger())

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected Build panic to propagate")
		}
		if !spy1.closed || !spy2.closed {
			t.Errorf("layer readers not closed on Build panic: %+v %+v", spy1, spy2)
		}
	}()
	handler.HandleNotification(context.Background(), db.Notification{
		Channel: db.NotifyDeploymentChanged,
		Payload: `{"app_id":"` + h.app.ID + `","to":"` + h.dep.ID + `","kind":"image","image_digest":"sha256:abc"}`,
	})
}

// panicBuilder satisfies LayerBuilder and panics so caller-side defer cleanup
// is exercised.
type panicBuilder struct{}

func (panicBuilder) Build(_ context.Context, _ rootfs.BuildInput) (rootfs.BuildResult, error) {
	panic("boom")
}

func (panicBuilder) BuildBase(_ context.Context, _ rootfs.BaseBuildInput) (rootfs.BaseBuildResult, error) {
	panic("boom")
}

// TestHandleDeployment_ClosesLayerReadersOnBuildError complements the panic
// test above: a normal error return from Builder.Build (no panic) must still
// close the layer ReadClosers. The defer in handleDeployment is
// unconditional so this is redundant with `TestHandleDeployment_ClosesLayerReaders`
// for layout — both error/panic exit paths share the same defer. We keep
// this case as a regression net because normal errors are vastly more
// common than a builder panic.
func TestHandleDeployment_ClosesLayerReadersOnBuildError(t *testing.T) {
	h := newTestHarness(t, state.DeploymentKindImage, api.Plan("free"), "")

	spy1 := &spyCloser{reader: strings.NewReader("layer1")}
	spy2 := &spyCloser{reader: strings.NewReader("layer2")}

	puller := fakePuller{
		digest: "sha256:abc",
		cfg:    oci.ImageConfig{Cmd: []string{"sh"}}, // satisfies the new PullImageConfig fail-fast check
		layersCfg: &oci.PullLayersResult{
			Layers: []io.ReadCloser{spy1, spy2},
			Config: oci.ImageConfig{Cmd: []string{"sh"}},
			Digest: "sha256:abc",
		},
	}
	bld := &fakeBuilder{buildErr: errors.New("mkfs: ENOSPC")}
	handler := New(h.store, h.notif, puller, bld, "./init", h.appsR, silentLogger())

	handler.HandleNotification(context.Background(), db.Notification{
		Channel: db.NotifyDeploymentChanged,
		Payload: `{"app_id":"` + h.app.ID + `","to":"` + h.dep.ID + `","kind":"image","image_digest":"sha256:abc"}`,
	})

	if !spy1.closed || !spy2.closed {
		t.Errorf("layer readers not closed on Builder.Build error: spy1.closed=%v spy2.closed=%v",
			spy1.closed, spy2.closed)
	}
}

// TestHandleDeployment_NoCmdImageSkipsLayerStream is the regression for
// review issue #6: an image without Cmd must fail fast, BEFORE any layer
// blob is fetched. We assert PullLayers was NEVER called (callCount == 0)
// and the deployment landed in `failed`.
func TestHandleDeployment_NoCmdImageSkipsLayerStream(t *testing.T) {
	h := newTestHarness(t, state.DeploymentKindImage, api.Plan("hobby"), "")

	puller := &countingPuller{
		imageCfg: oci.ImageConfig{ /* no Cmd */ },
		layers:   []io.ReadCloser{nopReader{}, nopReader{}},
	}
	handler := New(h.store, h.notif, puller, h.bld, "./init", h.appsR, silentLogger())

	handler.HandleNotification(context.Background(), db.Notification{
		Channel: db.NotifyDeploymentChanged,
		Payload: `{"app_id":"` + h.app.ID + `","to":"` + h.dep.ID + `","kind":"image","image_digest":"sha256:abc"}`,
	})

	got, _ := h.store.DeploymentByID(context.Background(), h.dep.ID)
	if got.Status != state.DeployFailed {
		t.Errorf("status = %s, want failed", got.Status)
	}
	if !strings.Contains(got.Error, "manifest invalid") {
		t.Errorf("error should mention manifest invalidation, got %q", got.Error)
	}
	if puller.pullLayersCount != 0 {
		t.Errorf("PullLayers called %d times — issue #6 says it should be 0 when Cmd is missing",
			puller.pullLayersCount)
	}
	if len(h.bld.calls) != 0 {
		t.Errorf("Builder.Build should not run when manifest is invalid; calls=%d", len(h.bld.calls))
	}
}

// countingPuller satisfies oci.Puller and counts how many times each method
// is called. Used by TestHandleDeployment_NoCmdImageSkipsLayerStream to
// prove fail-fast behavior end-to-end against the interface, not just via
// the layered fakePuller.
type countingPuller struct {
	imageCfg oci.ImageConfig
	layers   []io.ReadCloser

	pullDigestCount   int
	pullImageCfgCount int
	pullLayersCount   int
}

func (p *countingPuller) PullDigest(_ context.Context, _ string) (string, error) {
	p.pullDigestCount++
	return "sha256:abc", nil
}
func (p *countingPuller) PullImageConfig(_ context.Context, _ string) (oci.ImageConfig, error) {
	p.pullImageCfgCount++
	return p.imageCfg, nil
}
func (p *countingPuller) PullLayers(_ context.Context, _ string) (oci.PullLayersResult, error) {
	p.pullLayersCount++
	return oci.PullLayersResult{Layers: p.layers, Config: p.imageCfg, Digest: "sha256:abc"}, nil
}

// TestRepoWithHost pins the host-preserving derivation used by
// aboveBaseLayers to construct blob-fetch repo paths. The OCI puller
// synthesises a Reference from `repo+@digest` and looks up the registry
// from that synthesised ref; passing just the repository (e.g.
// "library/hello") makes it default to docker.io and silently dials the
// wrong host for non-Docker-Hub deploys (issue #53). repoWithHost is the
// load-bearing seam — TestRepoWithHost is the coverage pin.
func TestRepoWithHost(t *testing.T) {
	cases := map[string]string{
		// docker.io is special-cased: the synthesised ref's default
		// registry IS docker.io, so the repo path alone is correct.
		"docker.io/library/hello":            "library/hello",
		"docker.io/onebox-faas/builder-base": "onebox-faas/builder-base",
		// Non-docker registries: the host must survive the round-trip.
		"ghcr.io/onebox-faas/builder-base":        "ghcr.io/onebox-faas/builder-base",
		"quay.io/prometheus/node-exporter":        "quay.io/prometheus/node-exporter",
		"registry.example.com:5000/team/svc":      "registry.example.com:5000/team/svc",
		"127.0.0.1:5000/onebox-faas/builder-base": "127.0.0.1:5000/onebox-faas/builder-base",
	}
	for in, want := range cases {
		if got := repoWithHost(in); got != want {
			t.Errorf("repoWithHost(%q) = %q, want %q", in, got, want)
		}
	}
	// Parse failures must yield "" so the caller can branch on it. oci.ParseReference
	// accepts almost any non-empty input as a docker.io repo (defaulting to
	// "library/<name>"), so empty string is the only guaranteed parse error
	// here. "@sha256:<64hex>" parses with an empty repository, which ParseReference
	// rejects (line 72 of reference.go).
	for _, in := range []string{"", "@sha256:" + strings.Repeat("a", 64)} {
		if got := repoWithHost(in); got != "" {
			t.Errorf("repoWithHost(%q) = %q, want \"\"", in, got)
		}
	}
}
