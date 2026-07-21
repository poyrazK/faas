package imaged

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// mustLocalStorage builds a LocalStorageBackend rooted at the temp dir.
// Panics on construction error (which only fails on empty / NUL root, and
// t.TempDir() guarantees neither).
func mustLocalStorage(t *testing.T, root string) storage.StorageBackend {
	t.Helper()
	be, err := storage.NewLocalStorageBackend(root)
	if err != nil {
		t.Fatalf("storage.NewLocalStorageBackend(%s): %v", root, err)
	}
	return be
}

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

func (b *fakeBuilder) Build(ctx context.Context, in rootfs.BuildInput) (rootfs.BuildResult, error) {
	b.calls = append(b.calls, in)
	if b.buildErr != nil {
		return rootfs.BuildResult{}, b.buildErr
	}
	// #96: the handler publishes the produced ext4 via Storage.Put under
	// the apps/<slug>/<dep>.ext4 key. The fake mirrors real mkfs by
	// putting a non-empty placeholder there so downstream code that
	// reads the stored layer sees bytes rather than the zero-byte
	// rejection in LocalStorageBackend. Legacy OutImage path is also
	// supported for tests that still exercise it.
	if in.Storage != nil && in.StorageKey != "" {
		if err := in.Storage.Put(ctx, in.StorageKey, strings.NewReader("fake ext4")); err != nil {
			return rootfs.BuildResult{}, err
		}
		return rootfs.BuildResult{
			ImageKey:     in.StorageKey,
			ContentBytes: b.bytesOut,
		}, nil
	}
	if in.OutImage != "" {
		if err := os.WriteFile(in.OutImage, []byte("fake ext4"), 0o644); err != nil {
			return rootfs.BuildResult{}, err
		}
		return rootfs.BuildResult{
			ImagePath:    in.OutImage,
			ContentBytes: b.bytesOut,
		}, nil
	}
	return rootfs.BuildResult{ContentBytes: b.bytesOut}, nil
}

// BuildBase is part of the LayerBuilder interface (M6); the existing
// handler tests don't reach it, but the new EnsureBaseExt4 path does. The
// fake records the call so a test can pin the Storage + StorageKey +
// layer count.
//
// #96: the produced ext4 is published via Storage.Put instead of
// writing to a tmp file. The fake stands in to keep tests KVM-free
// (spec §Conventions: unit tests pass on any machine); it mirrors
// the production behaviour by streaming a small placeholder into the
// storage backend under StorageKey.
func (b *fakeBuilder) BuildBase(ctx context.Context, in rootfs.BaseBuildInput) (rootfs.BaseBuildResult, error) {
	b.calls = append(b.calls, rootfs.BuildInput{Plan: api.PlanScale})
	if in.Storage != nil && in.StorageKey != "" {
		if err := in.Storage.Put(ctx, in.StorageKey, strings.NewReader("fake ext4")); err != nil {
			return rootfs.BaseBuildResult{}, err
		}
		return rootfs.BaseBuildResult{ImageKey: in.StorageKey, SizeBytes: b.bytesOut}, nil
	}
	// Legacy OutImage path — kept for tests that still exercise the
	// deprecated code path (build_test.go's TestBuildBase_LegacyOutImage).
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
			`"vmstate_path":"/srv/fc/snap/` + dep.ID + `/vmstate",` +
			`"storage_key":"snap/` + dep.ID + `/mem",` +
			`"mem_bytes":134217728,` +
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
		Payload: `{"deployment_id":"` + dep.ID + `","mem_path":"/m","storage_key":"snap/` + dep.ID + `/mem","mem_bytes":1,"fc_version":"firecracker-1.10"}`,
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

// TestHandleDeployment_PullDigestSentinel_PersistsErrorCode walks the three
// puller-side sentinel surfaces and asserts deployments.error_code carries
// the stable RFC 7807 code (ADR-021). The wake path reads this column to
// lift the failure into a Problem, so a customer / dashboard can branch
// on a stable string rather than parsing free-text deployments.error.
//
// We use failingPuller with wrapped sentinel errors so the test exercises
// the same path production goes through (errors.As / errors.Is).
func TestHandleDeployment_PullDigestSentinel_PersistsErrorCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string // expected deployments.error_code
	}{
		{
			name: "registry 404 lifts to image_not_found",
			err:  fmt.Errorf("pull failed: %w", oci.ErrImageNotFound),
			want: api.CodeImageNotFound,
		},
		{
			name: "egress denylist lifts to image_egress_denied",
			err:  fmt.Errorf("dial failed: %w", oci.ErrImageEgressDenied),
			want: api.CodeImageEgressDenied,
		},
		{
			name: "manifest-list / parse lifts to image_manifest_invalid",
			err:  fmt.Errorf("parse: %w", oci.ErrImageManifestInvalid),
			want: api.CodeImageManifestInvalid,
		},
		{
			name: "non-sentinel error leaves code empty (free-text only)",
			err:  errors.New("net down"),
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
			app, _ := store.CreateApp(context.Background(), state.App{
				AccountID: acct.ID, Slug: "bad-img-" + tc.name, RAMMB: 128, IdleTimeoutS: 30, MaxConcurrency: 1,
			})
			dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
				AppID: app.ID, ImageDigest: "sha256:bad", Kind: state.DeploymentKindImage,
			})
			h := New(store, &fakeNotifier{}, failingPuller{err: tc.err},
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
			if got.ErrorCode != tc.want {
				t.Errorf("error_code = %q, want %q (error message was %q)",
					got.ErrorCode, tc.want, got.Error)
			}
		})
	}
}

// TestHandleBuildQueued asserts the queued build is dispatched to the canonical
// snapshot_boot handler: imaged decodes apid's actual payload shape
// ({"app","build","deployment","kind"}), walks the deployment to snapshotting,
// and re-emits NotifySnapshotPrime for schedd. The M5 contract changed in
// the M8 PR — apid's emit shape is the source of truth and imaged no longer
// flips the build row to running inline (builderd owns that transition).
func TestHandleBuildQueued(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "src-app", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
		Runtime: RuntimeNode22,
	})
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: "/tmp/x.tgz",
	})
	b, err := store.CreateBuild(context.Background(), dep.ID, state.DeploymentKindTarball, 4096, "/var/log/x.log")
	if err != nil {
		t.Fatal(err)
	}
	// Stamp the rootfs the way builderd would (a tarball path).
	if err := store.SetDeploymentRootfs(context.Background(), dep.ID, "/tmp/oci.tar", 4096); err != nil {
		t.Fatal(err)
	}
	notif := &fakeNotifier{}
	appsRoot := t.TempDir()
	// Pre-place a fake function runner so buildFunctionLayer doesn't bail out
	// for the empty-path case (F3 fail-loud).
	bin := filepath.Join(t.TempDir(), "faas-runner")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := New(store, notif, fakePuller{digest: "sha256:abc"}, &fakeBuilder{bytesOut: 4096}, "./init", appsRoot, silentLogger())
	h.WithFunctionRunnerNode22(bin)
	// apid emits the canonical M8 shape — app, build, deployment, kind.
	n := db.Notification{
		Channel: db.NotifyBuildQueued,
		Payload: `{"app":"` + app.ID + `","build":"` + b.ID + `","deployment":"` + dep.ID + `","kind":"tarball"}`,
	}
	h.HandleNotification(context.Background(), n)
	got, _ := store.DeploymentByID(context.Background(), dep.ID)
	if got.Status != state.DeploySnapshotting {
		t.Errorf("deployment status = %s, want snapshotting", got.Status)
	}
	// Must re-emit NotifySnapshotPrime for schedd (M6 chain).
	primeFound := false
	for _, c := range notif.calls {
		if c.channel == db.NotifySnapshotPrime &&
			strings.Contains(c.payload, app.ID) &&
			strings.Contains(c.payload, dep.ID) {
			primeFound = true
		}
	}
	if !primeFound {
		t.Errorf("expected NotifySnapshotPrime to schedd; got %v", notif.calls)
	}
}

// TestHandleBuildQueued_EmptyRootfsPath_NoOp is the F-01 regression. apid
// emits build_queued immediately at deploy creation; builderd stamps
// deployments.rootfs_path later (after the build VM produces an OCI
// image). Prior to F-01, handleBuildQueued dispatched unconditionally to
// handleSnapshotBoot, which transitioned the deployment to DeployFailed
// with "empty rootfs_path" on every tarball/dockerfile deploy — blocking
// the whole product. The fix: handleBuildQueued is a thin pass-through
// for the build status update and is otherwise a no-op when the stamp is
// absent; the canonical path is builderd's NotifySnapshotBoot emit that
// arrives AFTER the stamp.
func TestHandleBuildQueued_EmptyRootfsPath_NoOp(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "src-app", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
		Runtime: RuntimeNode22,
	})
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: "/tmp/x.tgz",
	})
	b, _ := store.CreateBuild(context.Background(), dep.ID, state.DeploymentKindTarball, 4096, "/var/log/x.log")
	// Crucially: NO SetDeploymentRootfs call — apid's build_queued
	// arrives before builderd has had a chance to stamp.
	notif := &fakeNotifier{}
	bin := filepath.Join(t.TempDir(), "faas-runner")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := New(store, notif, fakePuller{digest: "sha256:abc"}, &fakeBuilder{bytesOut: 4096}, "./init", t.TempDir(), silentLogger())
	h.WithFunctionRunnerNode22(bin)
	n := db.Notification{
		Channel: db.NotifyBuildQueued,
		Payload: `{"app":"` + app.ID + `","build":"` + b.ID + `","deployment":"` + dep.ID + `","kind":"tarball"}`,
	}
	h.HandleNotification(context.Background(), n)
	got, _ := store.DeploymentByID(context.Background(), dep.ID)
	if got.Status == state.DeployFailed {
		t.Fatalf("F-01 regression: deployment transitioned to failed despite empty rootfs_path (the bug); status=%s", got.Status)
	}
	// Must NOT have re-emitted NotifySnapshotPrime — no work happened.
	for _, c := range notif.calls {
		if c.channel == db.NotifySnapshotPrime {
			t.Errorf("F-01 regression: NotifySnapshotPrime emitted for an un-stamped build; payload=%s", c.payload)
		}
	}
	// The build status MUST still flip to running — that's the part that
	// belongs in imaged even when there's no rootfs to convert.
	gotBuild, _ := store.BuildByID(context.Background(), b.ID)
	if gotBuild.Status != state.BuildRunning {
		t.Errorf("build status = %s, want %s", gotBuild.Status, state.BuildRunning)
	}
}

// TestHandleNotification_AppChanged_Deleted_CarriesAppID is the F-04
// regression. apid's emit shape for app_changed is now
// {"kind":"deleted","slug":"<slug>","app_id":"<uuid>"} (was
// {"kind":"deleted","slug":"<slug>"} with no app_id). imaged switches on
// app_id to drive cleanupAppFiles; without the field, deleted apps
// accumulated orphans in appsRoot/<slug>/.
//
// #96: the per-app ext4 layer lives in the StorageBackend (under
// sched.AppLayerKey(slug, depID)). The test seeds the storage backend
// before dispatching the notification and asserts the key is gone after.
func TestHandleNotification_AppChanged_Deleted_CarriesAppID(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "soon-gone", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
		Runtime: RuntimeNode22,
	})
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc",
	})
	appsRoot := t.TempDir()
	be := mustLocalStorage(t, appsRoot)
	appsKey := sched.AppLayerKey(app.Slug, dep.ID)
	if err := be.Put(context.Background(), appsKey, strings.NewReader("layer")); err != nil {
		t.Fatalf("seed apps layer: %v", err)
	}
	notif := &fakeNotifier{}
	h := New(store, notif, fakePuller{digest: "sha256:abc"}, &fakeBuilder{bytesOut: 4096}, "./init", appsRoot, silentLogger()).WithStorage(be)
	// New payload: carries app_id. F-04.
	n := db.Notification{
		Channel: db.NotifyAppChanged,
		Payload: `{"kind":"deleted","slug":"` + app.Slug + `","app_id":"` + app.ID + `"}`,
	}
	h.HandleNotification(context.Background(), n)
	rc, err := be.Get(context.Background(), appsKey)
	if err == nil {
		_ = rc.Close()
		t.Errorf("F-04 regression: per-app ext4 layer survived a deleted app_changed emit (key=%s)", appsKey)
	}
}

// TestHandleNotification_Supersede_KeepsSnapBlob_EndToEnd is the F-02
// regression. Prior to F-02, cleanupDeploymentFiles(..., false /* keepSnap */)
// was called on every supersede — deleting the snapshot blob and forcing
// every cross-supersede rollback to cold-boot. Spec §4.6 requires the snap
// blob survive; the per-app ext4 layer is the only thing the cleanup may
// drop. The test exercises the full wire path: HandleNotification on the
// NotifyDeploymentChanged channel with status="superseded" must drop the
// ext4 layer but leave the snap blob intact.
//
// #96: the ext4 layer lives at the storage key sched.AppLayerKey(slug,
// depID) and the snap blob at sched.SnapshotMemKey(depID). The test
// seeds both, fires the supersede notification, and asserts the layer is
// gone while the snap blob key still resolves through the storage
// backend.
func TestHandleNotification_Supersede_KeepsSnapBlob_EndToEnd(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "rolled-app", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
		Runtime: RuntimeNode22,
	})
	// Live deployment that's about to be superseded.
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:v1",
	})
	appsRoot := t.TempDir()
	be := mustLocalStorage(t, appsRoot)
	appsKey := sched.AppLayerKey(app.Slug, dep.ID)
	memKey := sched.SnapshotMemKey(dep.ID)
	if err := be.Put(context.Background(), appsKey, strings.NewReader("layer")); err != nil {
		t.Fatalf("seed apps layer: %v", err)
	}
	if err := be.Put(context.Background(), memKey, strings.NewReader("snap-mem")); err != nil {
		t.Fatalf("seed snap mem: %v", err)
	}
	notif := &fakeNotifier{}
	h := New(store, notif, fakePuller{digest: "sha256:abc"}, &fakeBuilder{bytesOut: 4096}, "./init", appsRoot, silentLogger()).WithStorage(be)
	// Supersede payload — F-02: status must be in payload for the branch
	// to fire, and to must equal the deployment id being superseded.
	n := db.Notification{
		Channel: db.NotifyDeploymentChanged,
		Payload: `{"kind":"superseded","status":"superseded","app_id":"` + app.ID + `","deployment_id":"` + dep.ID + `","to":"` + dep.ID + `"}`,
	}
	h.HandleNotification(context.Background(), n)
	if rc, err := be.Get(context.Background(), appsKey); err == nil {
		_ = rc.Close()
		t.Errorf("F-05 regression: superseded ext4 layer not removed (key=%s)", appsKey)
	}
	if rc, err := be.Get(context.Background(), memKey); err != nil {
		t.Errorf("F-02 regression: snap mem blob was dropped on supersede (key=%s, err=%v)", memKey, err)
	} else {
		_ = rc.Close()
	}
	dep2, _ := store.DeploymentByID(context.Background(), dep.ID)
	if dep2.Status == state.DeploySuperseded {
		t.Errorf("F-02 regression: imaged shouldn't transition deployment status on supersede (status=%s)", dep2.Status)
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
