package imaged

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
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

// BuildBaseFromStaging (ADR-053) is part of the LayerBuilder
// interface. The imaged parent-ref path calls it after cp -a +
// delta layer apply; the fake streams a small placeholder so
// tests don't need a host mkfs binary.
func (b *fakeBuilder) BuildBaseFromStaging(ctx context.Context, _ string, in rootfs.BaseBuildInput) (rootfs.BaseBuildResult, error) {
	b.calls = append(b.calls, rootfs.BuildInput{Plan: api.PlanScale})
	if in.Storage != nil && in.StorageKey != "" {
		if err := in.Storage.Put(ctx, in.StorageKey, strings.NewReader("fake ext4 parent-ref")); err != nil {
			return rootfs.BaseBuildResult{}, err
		}
		return rootfs.BaseBuildResult{ImageKey: in.StorageKey, SizeBytes: b.bytesOut}, nil
	}
	return rootfs.BaseBuildResult{}, nil
}

// BuildFullRootfs (M-3 commit 5+6) is part of the LayerBuilder
// interface. The full-rootfs build path (ADR-141 §Decision 1)
// bypasses the two-drive shared-base path; the fake streams a
// small placeholder via Storage.Put so dispatchFullRootfs /
// buildFullRootfsLayer tests stay KVM-free.
func (b *fakeBuilder) BuildFullRootfs(ctx context.Context, in rootfs.BuildFullRootfsInput) (rootfs.BuildResult, error) {
	b.calls = append(b.calls, rootfs.BuildInput{Plan: in.Plan})
	if in.Storage != nil && in.StorageKey != "" {
		if err := in.Storage.Put(ctx, in.StorageKey, strings.NewReader("fake ext4 full-rootfs")); err != nil {
			return rootfs.BuildResult{}, err
		}
		return rootfs.BuildResult{ImageKey: in.StorageKey, ContentBytes: b.bytesOut}, nil
	}
	return rootfs.BuildResult{ContentBytes: b.bytesOut}, nil
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

// createReplacementDeployment persists h.dep as a new row and makes it the
// harness target. CreateDeployment is insert-only in production, so tests that
// add overrides after constructing the harness must not re-use the original
// deployment ID.
func (h *testHarness) createReplacementDeployment(t *testing.T) {
	t.Helper()
	h.dep.ID = ""
	h.dep.CreatedAt = time.Time{}
	dep, err := h.store.CreateDeployment(context.Background(), h.dep)
	if err != nil {
		t.Fatalf("create replacement deployment: %v", err)
	}
	h.dep = dep
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
		Payload: `{"deployment_id":"` + dep.ID + `",` +
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
	if snap.MemBytes != 134217728 || snap.StorageKey != state.SnapMemKey(dep.ID) {
		t.Errorf("snapshot row wrong: %+v", snap)
	}
	// Issue #470 / PR #470-FU-B: tier defaults to "init" when the
	// payload omits the field (legacy schedd callers). The DB
	// column default 'init' is the source of truth; the imaged
	// handler mirrors it on the Go side.
	if snap.Tier != state.SnapshotTierInit {
		t.Errorf("Tier = %q, want %q (default)", snap.Tier, state.SnapshotTierInit)
	}
}

// TestHandleSnapshotWritten_Tier (issue #470 / PR #470-FU-B)
// exercises the warm-tier payload path: when schedd's
// captureWarmSnapshot emits a snapshot_written with tier="warm",
// imaged stamps tier="warm" on the row. The MemStore test
// doubles as the wire-shape regression — a future payload-shape
// change that drops the tier field would slip back to "init"
// and trip this assertion.
func TestHandleSnapshotWritten_Tier(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "warm-img", RAMMB: 512, IdleTimeoutS: 60, MaxConcurrency: 5,
	})
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:warm", Kind: state.DeploymentKindImage,
	})
	_ = store.UpdateDeploymentStatus(context.Background(), dep.ID, state.DeploySnapshotting, "")
	notif := &fakeNotifier{}
	h := New(store, notif, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger())

	h.HandleNotification(context.Background(), db.Notification{
		Channel: db.NotifySnapshotWritten,
		Payload: `{"deployment_id":"` + dep.ID + `",` +
			`"vmstate_path":"/srv/fc/snap/` + dep.ID + `/vmstate",` +
			`"storage_key":"snap/` + dep.ID + `/warm/mem",` +
			`"mem_bytes":134217728,` +
			`"vmstate_bytes":40960,"fc_version":"firecracker-1.10",` +
			`"tier":"warm"}`,
	})

	snap, err := store.LatestSnapshot(context.Background(), dep.ID)
	if err != nil {
		t.Fatalf("LatestSnapshot: %v", err)
	}
	if snap.Tier != state.SnapshotTierWarm {
		t.Errorf("Tier = %q, want %q (warm payload)", snap.Tier, state.SnapshotTierWarm)
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
		Payload: `{"deployment_id":"` + dep.ID + `","storage_key":"snap/` + dep.ID + `/mem","mem_bytes":1,"fc_version":"firecracker-1.10"}`,
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

// TestHandler_StorageFor_MissingRootErrors is the B3.8 regression guard.
// Pre-fix: storageFor() panicked when appsRoot was empty or contained a
// NUL byte, taking the daemon down mid-deploy. Post-fix: it returns an
// error and the handler logs a Warn instead of crashing.
func TestHandler_StorageFor_MissingRootErrors(t *testing.T) {
	store := state.NewMemStore()
	notif := &fakeNotifier{}
	// appsRoot="" forces the lazy default in storageFor to call
	// NewLocalStorageBackend("") which rejects empty roots.
	h := New(store, notif, fakePuller{}, &fakeBuilder{}, "./init", "", silentLogger())
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("storageFor panicked on empty appsRoot: %v (issue #197 B3.8)", r)
		}
	}()
	be, err := h.storageFor()
	if err == nil {
		t.Fatalf("storageFor: expected error on empty appsRoot, got nil")
	}
	if be != nil {
		t.Fatalf("storageFor: expected nil backend on error, got %T", be)
	}
}

// TestHandler_StorageFor_NULByteErrors covers the other NewLocalStorageBackend
// rejection case (NUL in the path). Belt-and-braces for B3.8.
func TestHandler_StorageFor_NULByteErrors(t *testing.T) {
	store := state.NewMemStore()
	notif := &fakeNotifier{}
	h := New(store, notif, fakePuller{}, &fakeBuilder{}, "./init", "/bad\x00path", silentLogger())
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("storageFor panicked on NUL-byte appsRoot: %v (issue #197 B3.8)", r)
		}
	}()
	be, err := h.storageFor()
	if err == nil {
		t.Fatalf("storageFor: expected error on NUL-byte appsRoot, got nil")
	}
	if be != nil {
		t.Fatalf("storageFor: expected nil backend on error, got %T", be)
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
			// Wave 0 PR-A / PR-C: stateless base image lifts to
			// stateless_only_violation. The base deny-list in
			// pkg/imaged/base.go returns this sentinel for
			// postgres/redis/mysql/etc — the deploy path's
			// error_code column carries it so a customer can
			// branch on a stable string.
			name: "stateless base image lifts to stateless_only_violation",
			err:  fmt.Errorf("base deny-list: %w", oci.ErrStatelessOnlyViolation),
			want: api.CodeStatelessOnlyViolation,
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

// PR-B: TestHandleBuildQueued + TestHandleBuildQueued_EmptyRootfsPath_NoOp retired.
// imaged no longer subscribes to db.NotifyBuildQueued (builderd owns the
// build-queue durability surface now). handleBuildQueued stays
// (referenced via handleSnapshotBoot's wait path) but is no longer
// dispatched from HandleNotification. See LoopConstructionNoReaper
// for the new contract assertion.

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

// TestHandleDeployment_OverrideEntrypointWinsOverImageCmd pins the PR-B
// (issue #460 / ADR-053) entrypoint-override seam on the image deploy path:
// override_entrypoint replaces the OCI cmd-derived argv before the manifest
// reaches rootfs.Builder.Build. This is the runtime-effect half of entrypoint
// overrides; the contract half (persistence + echo) is in PR-A's handler test
// surface.
func TestHandleDeployment_OverrideEntrypointWinsOverImageCmd(t *testing.T) {
	h := newTestHarness(t, state.DeploymentKindImage, api.Plan("hobby"), "")
	h.dep.OverrideEntrypoint = []string{"/usr/local/bin/custom-runner"}
	h.createReplacementDeployment(t)
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
	if len(in.Manifest.Entrypoint) != 1 || in.Manifest.Entrypoint[0] != "/usr/local/bin/custom-runner" {
		t.Errorf("Entrypoint = %v, want [/usr/local/bin/custom-runner]", in.Manifest.Entrypoint)
	}
}

// TestHandleDeployment_OverrideEnvMergesWithImageEnv pins the env-merge seam:
// override_env wins on key collision, non-colliding OCI keys pass through.
// Mirrors the applyOverrides table case but at the handler level so a
// regression in manifestFromImageConfig → applyOverrides wiring surfaces here.
func TestHandleDeployment_OverrideEnvMergesWithImageEnv(t *testing.T) {
	h := newTestHarness(t, state.DeploymentKindImage, api.Plan("hobby"), "")
	override := map[string]string{"LOG_LEVEL": "debug", "IMAGE_VER": "9.9.9"}
	overrideRaw, _ := json.Marshal(override)
	h.dep.OverrideEnv = overrideRaw
	h.createReplacementDeployment(t)
	puller := fakePuller{
		digest: "sha256:abc",
		cfg: oci.ImageConfig{
			Cmd: []string{"node", "server.js"},
			Env: map[string]string{"IMAGE_VER": "1.2.3", "OCI_VAR": "from_image"},
		},
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
	if in.Manifest.Env["IMAGE_VER"] != "9.9.9" {
		t.Errorf("Env[IMAGE_VER] = %q, want 9.9.9 (override wins on collision)", in.Manifest.Env["IMAGE_VER"])
	}
	if in.Manifest.Env["LOG_LEVEL"] != "debug" {
		t.Errorf("Env[LOG_LEVEL] = %q, want debug (override-only key added)", in.Manifest.Env["LOG_LEVEL"])
	}
	if in.Manifest.Env["OCI_VAR"] != "from_image" {
		t.Errorf("Env[OCI_VAR] = %q, want from_image (non-colliding OCI key preserved)", in.Manifest.Env["OCI_VAR"])
	}
}

// TestHandleDeployment_OverridePortStampsManifest pins the source-of-truth
// half of port: the override writes manifest.Port (so PR-C can consume it).
// The runtime-effect half (DNAT, waitReady, runners) ships in PR-C — that
// regression is NOT here. Keeping this as the manifest-stamp regression net.
func TestHandleDeployment_OverridePortStampsManifest(t *testing.T) {
	h := newTestHarness(t, state.DeploymentKindImage, api.Plan("hobby"), "")
	h.dep.OverridePort = 9090
	h.createReplacementDeployment(t)
	puller := fakePuller{digest: "sha256:abc", cfg: oci.ImageConfig{Cmd: []string{"node"}}}
	handler := New(h.store, h.notif, puller, h.bld, "./init", h.appsR, silentLogger())

	handler.HandleNotification(context.Background(), db.Notification{
		Channel: db.NotifyDeploymentChanged,
		Payload: `{"app_id":"` + h.app.ID + `","to":"` + h.dep.ID + `","kind":"image","image_digest":"sha256:abc"}`,
	})

	if len(h.bld.calls) != 1 {
		t.Fatalf("Build calls = %d, want 1", len(h.bld.calls))
	}
	if h.bld.calls[0].Manifest.Port != 9090 {
		t.Errorf("Manifest.Port = %d, want 9090", h.bld.calls[0].Manifest.Port)
	}
}

// TestBuildFunctionLayer_OverrideEntrypointWinsOverRuntimeDefault is the
// function-deploy mirror of TestHandleDeployment_OverrideEntrypointWinsOverImageCmd.
// Covers issue #460 §5 PR-B acceptance (override applies at imaged time on
// function deploys too).
func TestBuildFunctionLayer_OverrideEntrypointWinsOverRuntimeDefault(t *testing.T) {
	h := newFunctionTestHarness(t, api.PlanHobby, RuntimeNode22)
	h.dep.OverrideEntrypoint = []string{"/usr/local/bin/custom", "--port", "9090"}
	h.createReplacementDeployment(t)
	handler := New(h.store, h.notif, fakePuller{}, h.bld, "./init", h.appsR, silentLogger())
	handler.WithFunctionRunnerNode22("/runners/node22")

	if err := handler.buildFunctionLayer(context.Background(), h.app, h.dep, h.acct); err != nil {
		t.Fatalf("buildFunctionLayer: %v", err)
	}
	if len(h.bld.calls) != 1 {
		t.Fatalf("Build calls = %d, want 1", len(h.bld.calls))
	}
	got := h.bld.calls[0].Manifest.Entrypoint
	want := []string{"/usr/local/bin/custom", "--port", "9090"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Entrypoint = %v, want %v", got, want)
	}
}

// TestHandleDeployment_NoOverrideLeavesManifestUntouched is the no-op
// regression net: a deployment with all six override columns nil must
// produce the OCI argv (image path) or the runtime-default argv (function
// path) with no field changes from applyOverrides. Defends against a future
// refactor accidentally writing default values.
func TestHandleDeployment_NoOverrideLeavesManifestUntouched(t *testing.T) {
	h := newTestHarness(t, state.DeploymentKindImage, api.Plan("hobby"), "")
	puller := fakePuller{
		digest: "sha256:abc",
		cfg: oci.ImageConfig{
			Cmd:        []string{"node", "server.js"},
			Env:        map[string]string{"FROM": "image"},
			WorkingDir: "/srv",
		},
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
	// applyOverrides must NOT write any field when the deployment has no
	// overrides. The base values (Env["PORT"]=8080 default + Healthz
	// default) come from manifestFromImageConfig (ADR-051 Phase 4
	// characterization-boot seeding) — applyOverrides is downstream of
	// that and only layers ON TOP. So the expected want mirrors whatever
	// manifestFromImageConfig produced plus identity (no additional
	// writes from applyOverrides).
	want := api.AppManifest{
		Entrypoint: []string{"node", "server.js"},
		Env:        map[string]string{"FROM": "image", "PORT": "8080"},
		WorkingDir: "/srv",
		Healthz:    "/healthz",
	}
	if !reflect.DeepEqual(in.Manifest, want) {
		t.Errorf("manifest changed by applyOverrides with no override: got %+v, want %+v", in.Manifest, want)
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

func (panicBuilder) BuildBaseFromStaging(_ context.Context, _ string, _ rootfs.BaseBuildInput) (rootfs.BaseBuildResult, error) {
	panic("boom")
}

func (panicBuilder) BuildFullRootfs(_ context.Context, _ rootfs.BuildFullRootfsInput) (rootfs.BuildResult, error) {
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

// Drive the happy-path pull through the M5 legacy fallback (no
// ManifestPuller) and assert the imaged_oci_pull histogram surfaces
// the observation under op="blob",result="ok". The pre-instantiated
// tuples cover the unused arms at zero counts so the §12 dashboard
// doesn't show "no data" before the first scrape.
//
// The failing-puller arms are exercised by imaged's other tests
// (TestHandleDeployment_PullLayersError); this test focuses on the
// metric wiring specifically.
func TestOCIPullMetrics_LegacyFallback(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@example.com", api.PlanPro)
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "metric-app", RAMMB: 512, IdleTimeoutS: 60, MaxConcurrency: 5,
	})
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:abc", Kind: state.DeploymentKindImage,
	})

	// Success path: drive a deployment through the legacy PullLayers
	// branch and assert imaged_oci_pull_duration_seconds_count{op="blob",
	// result="ok"} reaches 1.
	notif := &fakeNotifier{}
	ops := wire.NewOpsMetrics("imaged_test")
	h := New(store, notif,
		fakePuller{digest: "sha256:abc", cfg: oci.ImageConfig{Cmd: []string{"./app"}}},
		&fakeBuilder{}, "./init", t.TempDir(), silentLogger()).
		WithOpsMetrics(ops)
	h.HandleNotification(context.Background(), db.Notification{
		Channel: db.NotifyDeploymentChanged,
		Payload: `{"app_id":"` + app.ID + `","to":"` + dep.ID + `","kind":"image","image_digest":"sha256:abc"}`,
	})

	srv := httptest.NewServer(ops.Handler())
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get /metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	bodyB, _ := io.ReadAll(resp.Body)
	body := string(bodyB)

	for _, want := range []string{
		// Legacy PullLayers observes a single "blob" call.
		`imaged_test_oci_pull_duration_seconds_count{op="blob",result="ok"} 1`,
		// Pre-instantiated tuples the legacy path never touches surface at 0.
		`imaged_test_oci_pull_duration_seconds_count{op="config",result="ok"} 0`,
		`imaged_test_oci_pull_duration_seconds_count{op="manifest",result="ok"} 0`,
		// above_base is only emitted from the M6 aboveBaseLayers path,
		// which this fake (no ManifestPuller) doesn't exercise. Pre-
		// instantiated so it surfaces as 0 here.
		`imaged_test_oci_pull_duration_seconds_count{op="above_base",result="ok"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics body missing %q:\n%s", want, body)
		}
	}
}

// ---- Tier 1 follow-up (post PR #201) ---------------------------------------
//
// PR #201 added the go124 runtime to baseRefFor / runnerPathFor /
// runtimeToEnvSuffix / buildFunctionLayer. These tests pin those switch arms
// at unit-test speed so a runtime omission surfaces here rather than on first
// wake. PR #201 also shipped a node22/handler.js default-flag inconsistency
// (the runner default is now /app/node22.js) — the buildFunctionLayer branch
// collapse is verified here too. See docs/runtimes/go124.md for the per-runtime
// handler-path contract.

// newFunctionTestHarness is the function-deploy twin of newTestHarness. It
// creates a Type=Function app with the supplied runtime pinned (so
// buildFunctionLayer reads app.Runtime rather than the dep.Handler fallback)
// and a tarball deployment. The function-deploy path cannot share the image
// harness because the app.Type switch in handleDeployment is load-bearing.
//
// The harness is intentionally small — every caller explicitly wires the
// matching WithFunctionRunner* setter so the table rows stay legible.
func newFunctionTestHarness(t *testing.T, plan api.Plan, runtime string) *testHarness {
	t.Helper()
	s := state.NewMemStore()
	acct, err := s.CreateAccount(context.Background(), "u@example.com", plan)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	ram := 256
	if lim, ok := api.LimitsFor(plan); ok && lim.RAMMB > 0 {
		ram = lim.RAMMB
	}
	app, err := s.CreateApp(context.Background(), state.App{
		AccountID:      acct.ID,
		Slug:           "fn-app-" + runtime,
		Type:           state.AppTypeFunction,
		Runtime:        runtime,
		RAMMB:          ram,
		IdleTimeoutS:   30,
		MaxConcurrency: 2,
	})
	if err != nil {
		t.Fatalf("CreateApp(function): %v", err)
	}
	dep, err := s.CreateDeployment(context.Background(), state.Deployment{
		AppID:      app.ID,
		Kind:       state.DeploymentKindTarball,
		SourcePath: "/tmp/source-" + runtime + ".tgz",
		Handler:    runtime,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(function): %v", err)
	}
	appsR := t.TempDir()
	return &testHarness{
		store: s, notif: &fakeNotifier{}, bld: &fakeBuilder{},
		app: app, dep: dep, acct: acct, appsR: appsR,
	}
}

// TestBaseRefFor_Runtimes pins the host-side runtime → base image
// translation. The go124 row is the regression net for PR #201: a silent
// omission in baseRefFor would have shipped a go124 app against
// base-minimal, breaking the two-drive diff_ids math at first wake.
func TestBaseRefFor_Runtimes(t *testing.T) {
	cases := []struct {
		name    string
		runtime string
		want    string
	}{
		{"node22 → node22 base", RuntimeNode22, BaseRefNode22},
		{"python312 → python312 base", RuntimePython312, BaseRefPython312},
		{"go124 → go124 base (PR #201 row)", RuntimeGo124, BaseRefGo124},
		{"go124-alpine → go124-alpine base (Tier 2 PR row)", RuntimeGo124Alpine, BaseRefGo124Alpine},
		{"node24 → node24 base (Tier 1 PR 1 row)", RuntimeNode24, BaseRefNode24},
		{"python313 → python313 base (Tier 1 PR 1 row)", RuntimePython313, BaseRefPython313},
		{"empty runtime → minimal base", "", BaseRefMinimal},
		{"unknown runtime → minimal base", "ruby33", BaseRefMinimal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := baseRefFor(tc.runtime); got != tc.want {
				t.Errorf("baseRefFor(%q) = %q, want %q", tc.runtime, got, tc.want)
			}
		})
	}
}

// TestRunnerPathFor_Runtimes pins the per-runtime function-runner binary
// path switch. The go124 row is the regression net for PR #201's
// WithFunctionRunnerGo124 wiring. Sentinel paths make cross-wiring visible:
// if a future refactor swaps the go124 case body for the node22 case body,
// the assertion still fails because each branch owns a distinct value.
func TestRunnerPathFor_Runtimes(t *testing.T) {
	cases := []struct {
		name    string
		set     func(h *Handler)
		runtime string
		want    string
	}{
		{"node22 → node22Path", func(h *Handler) { h.WithFunctionRunnerNode22("/runners/node22") }, RuntimeNode22, "/runners/node22"},
		{"python312 → python312Path", func(h *Handler) { h.WithFunctionRunnerPython312("/runners/python312") }, RuntimePython312, "/runners/python312"},
		{"go124 → go124Path (PR #201 row)", func(h *Handler) { h.WithFunctionRunnerGo124("/runners/go124") }, RuntimeGo124, "/runners/go124"},
		{"go124-alpine → go124AlpinePath (Tier 2 PR row)", func(h *Handler) { h.WithFunctionRunnerGo124Alpine("/runners/go124-alpine") }, RuntimeGo124Alpine, "/runners/go124-alpine"},
		{"node24 → node24Path (Tier 1 PR 1 row)", func(h *Handler) { h.WithFunctionRunnerNode24("/runners/node24") }, RuntimeNode24, "/runners/node24"},
		{"python313 → python313Path (Tier 1 PR 1 row)", func(h *Handler) { h.WithFunctionRunnerPython313("/runners/python313") }, RuntimePython313, "/runners/python313"},
		{"empty runtime → \"\"", func(h *Handler) {}, "", ""},
		{"unknown runtime → \"\"", func(h *Handler) {}, "ruby33", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := New(state.NewMemStore(), &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger())
			tc.set(h)
			if got := h.runnerPathFor(tc.runtime); got != tc.want {
				t.Errorf("runnerPathFor(%q) = %q, want %q", tc.runtime, got, tc.want)
			}
		})
	}
}

// TestRuntimeToEnvSuffix_Runtimes pins the per-runtime env-var suffix used
// in the fail-loud error message ("set FAAS_FUNCTION_RUNNER_<SUFFIX> on the
// imaged unit"). The go124 row is the regression net for PR #201: a
// silent drop of the GO124 case would have produced
// FAAS_FUNCTION_RUNNER_go124 in the error and confused operators looking
// for the documented FAAS_FUNCTION_RUNNER_GO124 knob.
func TestRuntimeToEnvSuffix_Runtimes(t *testing.T) {
	cases := []struct {
		name    string
		runtime string
		want    string
	}{
		{"node22 → NODE22", RuntimeNode22, "NODE22"},
		{"python312 → PYTHON312", RuntimePython312, "PYTHON312"},
		{"go124 → GO124 (PR #201 row)", RuntimeGo124, "GO124"},
		{"go124-alpine → GO124_ALPINE (Tier 2 PR row)", RuntimeGo124Alpine, "GO124_ALPINE"},
		{"node24 → NODE24 (Tier 1 PR 1 row)", RuntimeNode24, "NODE24"},
		{"python313 → PYTHON313 (Tier 1 PR 1 row)", RuntimePython313, "PYTHON313"},
		{"unknown runtime → unchanged", "ruby33", "ruby33"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runtimeToEnvSuffix(tc.runtime); got != tc.want {
				t.Errorf("runtimeToEnvSuffix(%q) = %q, want %q", tc.runtime, got, tc.want)
			}
		})
	}
}

// TestBuildFunctionLayer_Runtimes sweeps all three supported function
// runtimes end-to-end through buildFunctionLayer. It is the regression net
// for both the go124 branch (PR #201) and the node22 branch collapse (this
// PR). Each row pins:
//
//  1. one Builder.Build call
//  2. FunctionRunnerPath = row's sentinel
//  3. TarballPath = dep.SourcePath
//  4. Layers empty (function deploys use the tarball, not blobs)
//  5. manifest argv = [/usr/local/bin/faas-runner --runtime R --handler H]
//  6. manifest.Port = api.DefaultAppPort
//  7. manifest.Healthz = "/healthz"
//
// If the collapse in Phase 2 ever causes a runtime to lose its explicit
// handler-path branch, the per-row assertion catches it at unit-test speed.
func TestBuildFunctionLayer_Runtimes(t *testing.T) {
	cases := []struct {
		name        string
		runtime     string
		runnerPath  string
		handlerPath string
		wire        func(h *Handler)
	}{
		{
			name:        "node22",
			runtime:     RuntimeNode22,
			runnerPath:  "/runners/node22",
			handlerPath: "/app/node22.js",
			wire:        func(h *Handler) { h.WithFunctionRunnerNode22("/runners/node22") },
		},
		{
			name:        "python312",
			runtime:     RuntimePython312,
			runnerPath:  "/runners/python312",
			handlerPath: "/app/handler.py",
			wire:        func(h *Handler) { h.WithFunctionRunnerPython312("/runners/python312") },
		},
		{
			name:        "go124",
			runtime:     RuntimeGo124,
			runnerPath:  "/runners/go124",
			handlerPath: "/app/handler",
			wire:        func(h *Handler) { h.WithFunctionRunnerGo124("/runners/go124") },
		},
		{
			// Tier 2 PR row: go124-alpine shares the runner shim
			// (guest/runners/go124) and the customer handler path
			// (/app/handler) with go124. Only the base image's libc
			// differs. The argv assertion is identical to go124.
			name:        "go124-alpine",
			runtime:     RuntimeGo124Alpine,
			runnerPath:  "/runners/go124-alpine",
			handlerPath: "/app/handler",
			wire:        func(h *Handler) { h.WithFunctionRunnerGo124Alpine("/runners/go124-alpine") },
		},
		{
			// Tier 1 PR 1 row: node24 mirrors node22 with a
			// versioned handler filename. The runner shim is the
			// same `node` binary; the underlying Node version is
			// bound by images/runner-node24.Dockerfile (PR 2).
			name:        "node24",
			runtime:     RuntimeNode24,
			runnerPath:  "/runners/node24",
			handlerPath: "/app/node24.js",
			wire:        func(h *Handler) { h.WithFunctionRunnerNode24("/runners/node24") },
		},
		{
			// Tier 1 PR 1 row: python313 stays version-neutral on
			// the handler filename, matching python312's /app/handler.py.
			// Only --runtime differs in argv.
			name:        "python313",
			runtime:     RuntimePython313,
			runnerPath:  "/runners/python313",
			handlerPath: "/app/handler.py",
			wire:        func(h *Handler) { h.WithFunctionRunnerPython313("/runners/python313") },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newFunctionTestHarness(t, api.PlanHobby, tc.runtime)
			handler := New(h.store, h.notif, fakePuller{}, h.bld, "./init", h.appsR, silentLogger())
			tc.wire(handler)

			if err := handler.buildFunctionLayer(context.Background(), h.app, h.dep, h.acct); err != nil {
				t.Fatalf("buildFunctionLayer(%s): %v", tc.runtime, err)
			}

			if got, _ := h.store.DeploymentByID(context.Background(), h.dep.ID); got.Status != state.DeployImaging {
				t.Errorf("status = %s, want imaging", got.Status)
			}
			if len(h.bld.calls) != 1 {
				t.Fatalf("Builder.Build calls = %d, want 1", len(h.bld.calls))
			}
			in := h.bld.calls[0]
			if in.FunctionRunnerPath != tc.runnerPath {
				t.Errorf("FunctionRunnerPath = %q, want %q", in.FunctionRunnerPath, tc.runnerPath)
			}
			if in.TarballPath != h.dep.SourcePath {
				t.Errorf("TarballPath = %q, want %q (dep.SourcePath)", in.TarballPath, h.dep.SourcePath)
			}
			if len(in.Layers) != 0 {
				t.Errorf("Layers = %d readers, want 0 (function deploys use the tarball)", len(in.Layers))
			}

			wantArgv := []string{
				"/usr/local/bin/faas-runner",
				"--runtime", tc.runtime,
				"--handler", tc.handlerPath,
			}
			if got := in.Manifest.Entrypoint; !equalStrings(got, wantArgv) {
				t.Errorf("Manifest.Entrypoint = %v, want %v", got, wantArgv)
			}
			if in.Manifest.Port != api.DefaultAppPort {
				t.Errorf("Manifest.Port = %d, want %d", in.Manifest.Port, api.DefaultAppPort)
			}
			if in.Manifest.Healthz != "/healthz" {
				t.Errorf("Manifest.Healthz = %q, want \"/healthz\"", in.Manifest.Healthz)
			}
		})
	}
}

// equalStrings is a tiny helper kept here so the table assertion above is
// readable without pulling reflect.DeepEqual into the call.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestBuildFunctionLayer_MissingRunnerFailsLoud exercises the per-runtime
// fail-loud path: a runtime wired without its matching FAAS_FUNCTION_RUNNER_*
// binary must transition the deployment to failed, surface the operator-
// facing knob name in the error, and never call Builder.Build. The go124
// row is the regression net for PR #201 — the FAAS_FUNCTION_RUNNER_GO124
// env-var name MUST appear in the error message so operators can find it
// without grepping the source.
func TestBuildFunctionLayer_MissingRunnerFailsLoud(t *testing.T) {
	cases := []struct {
		name        string
		runtime     string
		wantEnvKnob string
	}{
		{"node22", RuntimeNode22, "FAAS_FUNCTION_RUNNER_NODE22"},
		{"python312", RuntimePython312, "FAAS_FUNCTION_RUNNER_PYTHON312"},
		{"go124 (PR #201 row)", RuntimeGo124, "FAAS_FUNCTION_RUNNER_GO124"},
		{"go124-alpine (Tier 2 PR row)", RuntimeGo124Alpine, "FAAS_FUNCTION_RUNNER_GO124_ALPINE"},
		{"node24 (Tier 1 PR 1 row)", RuntimeNode24, "FAAS_FUNCTION_RUNNER_NODE24"},
		{"python313 (Tier 1 PR 1 row)", RuntimePython313, "FAAS_FUNCTION_RUNNER_PYTHON313"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newFunctionTestHarness(t, api.PlanHobby, tc.runtime)
			// Intentionally do NOT wire the matching runner path.
			handler := New(h.store, h.notif, fakePuller{}, h.bld, "./init", h.appsR, silentLogger())

			err := handler.buildFunctionLayer(context.Background(), h.app, h.dep, h.acct)
			if err == nil {
				t.Fatal("expected error when function runner path is empty")
			}
			if !strings.Contains(err.Error(), tc.wantEnvKnob) {
				t.Errorf("error %q must mention %q so operators can find the knob", err.Error(), tc.wantEnvKnob)
			}
			got, _ := h.store.DeploymentByID(context.Background(), h.dep.ID)
			if got.Status != state.DeployFailed {
				t.Errorf("status = %s, want failed", got.Status)
			}
			if !strings.Contains(got.Error, tc.wantEnvKnob) {
				t.Errorf("deployment error %q must mention %q", got.Error, tc.wantEnvKnob)
			}
			if len(h.bld.calls) != 0 {
				t.Errorf("Builder.Build must not run when runner path is empty; calls=%d", len(h.bld.calls))
			}
		})
	}
}

// ---- App-mode Cmd regression net (PR #201 follow-up #4) -------------------
//
// docs/runtimes/go124.md explicitly warns that if manifestFromImageConfig
// ever flips from reading cfg.Cmd to cfg.Entrypoint, the manifest will be
// empty and validation will fail loudly. The two tests below pin the
// contract at unit-test speed so a future refactor cannot silently regress
// the Railpack go plan's emitted Cmd: ["/app/server"].

// TestManifestFromImageConfig_AppModeCmd pins the positive contract: the
// OCI image's Cmd becomes the manifest's Entrypoint verbatim. The
// defensive-copy assertion guards against a regression where the
// conversion aliases the input map.
func TestManifestFromImageConfig_AppModeCmd(t *testing.T) {
	cfg := oci.ImageConfig{
		Cmd:        []string{"/app/server"},
		WorkingDir: "/app",
		Env:        map[string]string{"PORT": "3000"},
	}
	manifest, err := manifestFromImageConfig(cfg)
	if err != nil {
		t.Fatalf("manifestFromImageConfig: %v", err)
	}

	wantArgv := []string{"/app/server"}
	if !equalStrings(manifest.Entrypoint, wantArgv) {
		t.Errorf("Entrypoint = %v, want %v", manifest.Entrypoint, wantArgv)
	}
	if manifest.WorkingDir != "/app" {
		t.Errorf("WorkingDir = %q, want \"/app\"", manifest.WorkingDir)
	}
	if manifest.Env["PORT"] != "3000" {
		t.Errorf("Env[PORT] = %q, want \"3000\"", manifest.Env["PORT"])
	}
	if err := manifest.Validate(); err != nil {
		t.Errorf("manifest.Validate() = %v, want nil", err)
	}

	// Defensive copy: mutating cfg.Env after conversion must not leak
	// into manifest.Env. manifestFromImageConfig goes through cloneEnv
	// for exactly this reason.
	cfg.Env["PORT"] = "9999"
	if manifest.Env["PORT"] != "3000" {
		t.Errorf("manifest.Env aliased cfg.Env (mutation leaked): PORT=%q", manifest.Env["PORT"])
	}

	// Defensive copy on the Cmd→Entrypoint slice mapping. The OCI puller
	// allocates cfg.Cmd fresh per call today, but the contract is
	// fragile: a future puller that pools ImageConfig, or a future
	// handleDeployment that normalizes cfg.Cmd in place, would silently
	// mutate the stored manifest. slices.Clone is the fix; this assertion
	// pins it.
	cfg.Cmd[0] = "/mutated"
	if manifest.Entrypoint[0] != "/app/server" {
		t.Errorf("manifest.Entrypoint aliased cfg.Cmd (mutation leaked): Entrypoint=%v", manifest.Entrypoint)
	}
}

// TestManifestFromImageConfig_NoCmdYieldsEmptyEntrypoint pins the
// negative contract: an OCI image without Cmd must produce an empty
// manifest (and fail loud at Validate). The imaged-side oci.ImageConfig
// struct only exposes `Cmd` — Entrypoint is an upstream OCI concept that
// manifestFromImageConfig deliberately ignores (Railpack emits Cmd on the
// Go plan, not Entrypoint). The negative test guards against a future
// refactor that flips the function to read any other field, or drops the
// `len(cfg.Cmd) > 0` guard, which would silently produce a manifest
// whose Entrypoint is empty.
//
// (The earlier test name referenced the historical doc-warning shape
// "IgnoresOCIEntrypointWithoutCmd" — renamed here so the negative
// contract is self-describing: no Cmd → empty entrypoint.)
//
// End-to-end coverage that "empty manifest → deployment failed" is
// already provided by TestHandleDeployment_NoCmdImageSkipsLayerStream;
// this test pins the conversion helper directly so a regression in
// manifestFromImageConfig surfaces here, before the wire.
func TestManifestFromImageConfig_NoCmdYieldsEmptyEntrypoint(t *testing.T) {
	// oci.ImageConfig has only Cmd/Env/WorkingDir/ExposedPorts. An image
	// without Cmd is the canonical misconfiguration this test pins.
	//
	// M-1 (ADR-136 §Decision 5): the helper now fails fast with
	// oci.ErrImageManifestInvalid when neither Entrypoint nor Cmd is
	// declared. The legacy expectation was "empty Entrypoint + downstream
	// Validate rejection" — both are now collapsed into a single
	// canonical error at the helper boundary so the deploy path
	// surfaces a stable error code (mirrors local_oci.go's behaviour).
	cfg := oci.ImageConfig{
		// Cmd intentionally absent. WorkingDir + Env are present to
		// prove the function doesn't crash on the other fields.
		WorkingDir: "/app",
		Env:        map[string]string{"PORT": "3000"},
	}
	manifest, err := manifestFromImageConfig(cfg)
	if err == nil {
		t.Fatal("manifestFromImageConfig(no Cmd) = nil err; want ErrImageManifestInvalid")
	}
	if !errors.Is(err, oci.ErrImageManifestInvalid) {
		t.Errorf("err = %v; want ErrImageManifestInvalid", err)
	}
	// WorkingDir + Env still flow through on the error path? — no,
	// the helper short-circuits at the Entrypoint/Cmd check before
	// doing the env clone, so manifest is the zero value. Pin that.
	if manifest.WorkingDir != "" {
		t.Errorf("WorkingDir = %q, want zero (helper short-circuited)", manifest.WorkingDir)
	}
	if len(manifest.Env) != 0 {
		t.Errorf("Env = %v, want nil (helper short-circuited)", manifest.Env)
	}
}
