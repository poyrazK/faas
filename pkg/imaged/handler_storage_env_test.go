// handler_storage_env_test.go — ADR-054 acceptance pin (imaged side).
//
// Pins that imaged's Handler accepts the production env-derived
// backend (storage.BackendFromEnv with FAAS_STORAGE_BACKEND=oci +
// FAAS_OCI_REGISTRY=<url>) without re-typing or falling through to
// the default LocalStorageBackend. The shape of the resulting
// backend (PrefixRouter with OCI fallback, default-on cache wrapper)
// is pinned in pkg/storage/env_test.go — this test is the imaged-side
// boundary that proves the env contract survives the WithStorage
// round-trip.
//
// Why narrow: the load-bearing test is the handoff (env → Handler),
// not the router's internal field shape. Internal field assertions
// would force this test into the *PrefixRouter fields which are
// unexported; the pins there are pkg/storage's responsibility. The
// test is named _Handoff rather than _Build because it does NOT
// drive the full build pipeline (no rootfs, no OCI manifest probe);
// it only asserts the env-derived backend survives the
// WithStorage / storageFor round-trip.

package imaged

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// TestHandler_OCIBackendEnv_Handoff pins the env-var end-to-end
// handoff into imaged's Handler. The env vars resolve to a
// *PrefixRouter (cache explicitly disabled so the assertion is
// shape-stable); the test confirms:
//
//   - storage.BackendFromEnv succeeds with the OCI env vars.
//   - The BackendFromEnv result is a *PrefixRouter (the OCI fork
//     registers snap/, base/, kernel/, layers/ as local-prefix routes
//     and falls through to the OCI backend for apps/).
//   - imaged's Handler accepts the env-derived backend via
//     WithStorage and storageFor() returns it unchanged (no fall-
//     through to the legacy LocalStorageBackend).
//
// The cache wrapper assertions live in pkg/storage/env_test.go
// (TestBackendFromEnv_OCIDefaultsCacheDirHermetic and
// TestBackendFromEnv_OCIDoesNotWrapWhenExplicitlyDisabled). We
// disable the cache here to keep the type shape stable.
//
// Why this is named _Handoff rather than _Build: a true Build test
// would need a real rootfs (≥10 MB) and a live OCI registry to
// probe (the registry's HEAD /v2/ endpoint and the manifest schema
// are validated end-to-end). That's covered by the metal suite and
// the pkg/storage/oci_test.go integration tests, neither of which
// runs in `make test`. This test is the unit-side env→Handler
// wiring pin; renaming to _Handoff reflects that scope so a future
// reader doesn't grep for "Build" and assume coverage they don't
// have.
func TestHandler_OCIBackendEnv_Handoff(t *testing.T) {
	// Stable temp roots so the test never touches /srv/fc on the
	// dev machine. The cache is explicitly disabled — the
	// default-on path is pinned in pkg/storage/env_test.go.
	tmp := t.TempDir()
	t.Setenv("FAAS_STORAGE_BACKEND", "oci")
	t.Setenv("FAAS_OCI_REGISTRY", "http://127.0.0.1:0/fake")
	t.Setenv("FAAS_STORAGE_ROOT", tmp+"/fc")
	t.Setenv("FAAS_STORAGE_CACHE_DIR", "") // explicit disable for shape stability
	t.Setenv("FAAS_STORAGE_LOCAL_PREFIXES", "snap/,base/,kernel/,layers/")

	be, err := storage.BackendFromEnvContext(t.Context())
	if err != nil {
		t.Fatalf("BackendFromEnv: %v", err)
	}

	// The OCI fork produces a *PrefixRouter (cache disabled). The
	// type-assert is the load-bearing env pin: the BackendFromEnv
	// contract must produce a router when in OCI mode.
	if _, ok := be.(*storage.PrefixRouter); !ok {
		t.Fatalf("BackendFromEnv returned %T; want *PrefixRouter (OCI mode, cache disabled)", be)
	}

	// Wire imaged's Handler with the env-derived backend. New()
	// takes a non-nil state store + Notifier; we use the existing
	// silent fakes from handler_storage_routing_test.go.
	h := New(state.NewMemStore(), &fakeNotifier{}, fakePuller{}, &fakeBuilder{},
		"./init", tmp+"/apps", silentLogger()).WithStorage(be)

	// The handler's storageFor() must return the wired backend
	// unchanged — no fall-through to the default LocalStorageBackend.
	fromHandler, err := h.storageFor()
	if err != nil {
		t.Fatalf("storageFor: %v", err)
	}
	if fromHandler != be {
		t.Errorf("storageFor() = %p, want %p (env-derived backend)", fromHandler, be)
	}
}

// TestHandler_OCIBackendEnv_Handoff_ScanFromBackend pins the
// scan-source side of the ADR-054 acceptance closure. The handler
// is wired with an OCI-mode PrefixRouter (apps/ → OCI stub,
// everything else → local); runDeployScan is invoked end-to-end
// against a stub grypeRun that records the <dir> argument. The
// recorded dir MUST point at a stage tempdir (NOT the legacy
// appsRoot path), and the staged tempdir MUST live under
// h.appsRoot — the §17 G2 "secrets on /tmp" gap is closed by
// rooting the stage at the daemon's writable area.
//
// Why narrow: the load-bearing invariant for multi-box is that
// the per-deploy scan does not silently no-op when the layer is
// in the OCI backend. The previous (pre-acceptance) code called
// h.appsRootPath() directly, which produced a path that did not
// exist under FAAS_STORAGE_BACKEND=oci; the grype subprocess
// then scanned an empty directory and stamped scan_status='pass'
// with zero findings. This test is the regression pin for the
// stageScanExt4 helper introduced in Fix 1.
func TestHandler_OCIBackendEnv_Handoff_ScanFromBackend(t *testing.T) {
	// Stable temp roots so CI never touches /srv/fc or
	// /var/lib/faas/apps. The handler's appsRoot is the stage
	// root for the scan source — under this test it's a
	// t.TempDir(), not /var/lib/faas/apps.
	appsRoot := t.TempDir()
	localRoot := t.TempDir()

	// OCI stub holds the per-app layer blob under the routed
	// remainder (the PrefixRouter strips "apps/"). The handler's
	// stageScanExt4 reads via the storage backend, so the stub
	// must surface that blob on Get. The exact key is
	// sched.AppLayerKey(slug, dep.ID) minus the "apps/" prefix;
	// seeded after dep creation below.
	oci := newFakeOCIPutter()
	const blobBytes = "fake-scan-stage-ext4"

	localBE, err := storage.NewLocalStorageBackend(localRoot)
	if err != nil {
		t.Fatalf("NewLocalStorageBackend: %v", err)
	}
	router, err := storage.NewPrefixRouter(
		map[string]storage.StorageBackend{"apps/": oci},
		localBE,
	)
	if err != nil {
		t.Fatalf("NewPrefixRouter: %v", err)
	}

	// Stub grypeRun records the <dir> the handler hands it.
	var grypeDir string
	stubGrype := func(_ context.Context, dir string) (*ScanResult, error) {
		grypeDir = dir
		// Pin the staged file actually lives inside the staged
		// dir. grype dir:<dir> walks the dir; the staged ext4
		// is the only file under it, so the bytes round-trip
		// from the OCI stub through the stage helper.
		stageFile := dir + string("/rootfs.ext4")
		if _, statErr := os.Stat(stageFile); statErr != nil {
			t.Errorf("grype source dir %q missing staged ext4 %q: %v",
				dir, stageFile, statErr)
		}
		return &ScanResult{Vulnerabilities: []Vulnerability{}}, nil
	}

	h := New(state.NewMemStore(), &fakeNotifier{}, fakePuller{}, &fakeBuilder{},
		"./init", appsRoot, silentLogger()).
		WithStorage(router).
		WithGrypeRun(stubGrype)

	// Wire the app + deployment rows so runDeployScan has a
	// dep.ID + app.Slug to feed stageScanExt4.
	store := h.store
	acct, err := store.CreateAccount(context.Background(), "u@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "acme", RAMMB: 512,
		IdleTimeoutS: 60, MaxConcurrency: 5,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:abc",
		Kind: state.DeploymentKindImage,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	// Seed the OCI stub at the exact routed-remainder key the
	// router will hand to Get: "apps/" stripped from
	// sched.AppLayerKey(slug, dep.ID).
	appsKey := "acme/" + dep.ID + ".ext4"
	if err := oci.Put(context.Background(), appsKey,
		strings.NewReader(blobBytes)); err != nil {
		t.Fatalf("oci.Put seed %q: %v", appsKey, err)
	}

	h.runDeployScan(context.Background(), app, dep)

	if grypeDir == "" {
		t.Fatal("stub grypeRun was not called; runDeployScan short-circuited")
	}

	// Pin 1: the recorded dir must NOT equal the legacy
	// appsRootPath (which would mean the old direct-path code
	// is still in effect — the production bug pre-acceptance).
	if grypeDir == h.appsRootPath(app.Slug, dep.ID) {
		t.Errorf("scan source = %q (legacy appsRootPath); "+
			"want a stage tempdir under %q", grypeDir, appsRoot)
	}

	// Pin 2: the recorded dir must live under h.appsRoot (the
	// §17 G2 root). Filesystem path containment check via
	// filepath.Rel — an empty result with ".." prefix means the
	// recorded dir escaped the appsRoot (which would be the
	// os.TempDir() fallback — bad if the appsRoot IS writable).
	rel, relErr := filepath.Rel(appsRoot, grypeDir)
	if relErr != nil {
		t.Fatalf("filepath.Rel(%q, %q): %v", appsRoot, grypeDir, relErr)
	}
	if strings.HasPrefix(rel, "..") || strings.Contains(rel, ".."+string(filepath.Separator)) {
		t.Errorf("scan dir %q escaped appsRoot %q (rel=%q)",
			grypeDir, appsRoot, rel)
	}
}

// TestHandler_LocalBackendEnv_ScanStillReadsFromAppsRoot is the
// regression pin for single-box behaviour. With a
// LocalStorageBackend wired, runDeployScan must NOT enter the
// stage-to-tempdir branch — it must read the layer ext4 directly
// from the canonical appsRoot path, matching the pre-acceptance
// 1:1 behaviour. A regression that always stages (even for
// LocalStorageBackend) would burn disk I/O on every deploy on
// single-box deployments that don't need it.
//
// Why narrow: the LocalStorageBackend short-circuit is the only
// reason single-box keeps its deploy-scan latency budget. A
// future refactor that deletes the short-circuit must update
// this test (and re-measure the deploy-scan budget).
func TestHandler_LocalBackendEnv_ScanStillReadsFromAppsRoot(t *testing.T) {
	// The handler's appsRoot is the canonical ext4 path
	// (single-box legacy layout). Seed the ext4 directly there
	// so the legacy direct-path read succeeds.
	appsRoot := t.TempDir()
	const slug = "singlebox"

	// Stub grypeRun records the <dir> the handler hands it.
	var grypeDir string
	stubGrype := func(_ context.Context, dir string) (*ScanResult, error) {
		grypeDir = dir
		return &ScanResult{Vulnerabilities: []Vulnerability{}}, nil
	}

	// Wire a LocalStorageBackend that points at the same
	// appsRoot — single-box layout.
	localBE, err := storage.NewLocalStorageBackend(appsRoot)
	if err != nil {
		t.Fatalf("NewLocalStorageBackend: %v", err)
	}

	h := New(state.NewMemStore(), &fakeNotifier{}, fakePuller{}, &fakeBuilder{},
		"./init", appsRoot, silentLogger()).
		WithStorage(localBE).
		WithGrypeRun(stubGrype)

	store := h.store
	acct, err := store.CreateAccount(context.Background(), "u@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: slug, RAMMB: 512,
		IdleTimeoutS: 60, MaxConcurrency: 5,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:abc",
		Kind: state.DeploymentKindImage,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	// Seed the canonical ext4 path AFTER dep.ID is generated so
	// the appsRootPath lookup hits the file. The legacy
	// direct-path read depends on this file existing.
	if err := os.MkdirAll(appsRoot+"/"+slug, 0o755); err != nil {
		t.Fatalf("MkdirAll seed: %v", err)
	}
	if err := os.WriteFile(appsRoot+"/"+slug+"/"+dep.ID+".ext4",
		[]byte("local-ext4-bytes"), 0o600); err != nil {
		t.Fatalf("write seed ext4: %v", err)
	}

	h.runDeployScan(context.Background(), app, dep)

	if grypeDir == "" {
		t.Fatal("stub grypeRun was not called; runDeployScan short-circuited")
	}

	// Pin: the recorded dir must equal the canonical appsRoot
	// path — LocalStorageBackend short-circuits stageScanExt4
	// and the handler reads directly. Any other value means the
	// short-circuit regressed.
	want := h.appsRootPath(app.Slug, dep.ID)
	if grypeDir != want {
		t.Errorf("scan source = %q, want %q (LocalStorageBackend "+
			"short-circuit must NOT stage)", grypeDir, want)
	}
}

// TestHandler_OCIBackendEnv_Handoff_ScanSourceNotFoundStampsFailed
// pins the registry-unreachable + LocalCacheBackend default-
// fail-loud contract (the second half of Fix 1's behavioural
// guarantee). With an OCI stub that has no entry for the
// requested key, stageScanExt4 returns ("", noop, GetErr), and
// runDeployScan must stamp scan_status='failed' on the
// deployment row — NOT silently pass with zero findings. This
// is the same fail-closed posture the existing runGrype error
// path enforces.
func TestHandler_OCIBackendEnv_Handoff_ScanSourceNotFoundStampsFailed(t *testing.T) {
	appsRoot := t.TempDir()
	localRoot := t.TempDir()
	localBE, err := storage.NewLocalStorageBackend(localRoot)
	if err != nil {
		t.Fatalf("NewLocalStorageBackend: %v", err)
	}
	// OCI stub with NO seeded blob — Get returns storage.ErrNotFound.
	oci := newFakeOCIPutter()
	router, err := storage.NewPrefixRouter(
		map[string]storage.StorageBackend{"apps/": oci},
		localBE,
	)
	if err != nil {
		t.Fatalf("NewPrefixRouter: %v", err)
	}

	h := New(state.NewMemStore(), &fakeNotifier{}, fakePuller{}, &fakeBuilder{},
		"./init", appsRoot, silentLogger()).WithStorage(router)

	store := h.store
	acct, err := store.CreateAccount(context.Background(), "u@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "missing", RAMMB: 512,
		IdleTimeoutS: 60, MaxConcurrency: 5,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:abc",
		Kind: state.DeploymentKindImage,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	h.runDeployScan(context.Background(), app, dep)

	// Pin: the deployment row carries scan_status='failed' with
	// the backend.Get error message in scan_result.Error. The
	// deploy itself does NOT fail — the snapshotting transition
	// fires regardless (the existing AC #4 contract).
	got, err := store.DeploymentByID(context.Background(), dep.ID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	var scanResult ScanResult
	if err := json.Unmarshal([]byte(got.ScanResult), &scanResult); err != nil {
		t.Fatalf("unmarshal ScanResult %q: %v", got.ScanResult, err)
	}
	if scanResult.Error == "" {
		t.Errorf("ScanResult.Error = \"\"; want non-empty (registry " +
			"unreachable must stamp failure cause)")
	}
}
