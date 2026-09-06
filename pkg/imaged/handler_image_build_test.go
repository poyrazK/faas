package imaged

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeManifestPuller is the M6 ManifestPuller test double. It returns canned
// manifests + configs + blobs so handler.go can run its full build path
// without a registry. Implements the full oci.Puller (PullDigest,
// PullImageConfig, PullLayers) plus the M6 extensions (PullManifest,
// PullBlob) so it satisfies oci.ManifestPuller.
//
// PR-B (issue #463 / ADR-069): the sidecar build path
// (pkg/imaged/handler.go::buildSidecarLayers) calls pullLayersWithAuth
// per sidecar ref. The fake routes by exact ref → sidecarManifest to
// hand each sidecar its own (manifest, layer blobs) tuple.
type fakeManifestPuller struct {
	digest       string
	appRef       string
	appManifest  oci.Manifest
	appConfig    oci.Config
	baseManifest oci.Manifest
	baseConfig   oci.Config

	// sidecarManifests maps a sidecar's exact OCI ref → its
	// manifest. The sidecar build path's PullManifest pulls this
	// shape (a one-layer manifest is enough; the layers are read
	// via PullLayers and PullBlob by digest).
	sidecarManifests map[string]oci.Manifest

	// layerBlobs maps digest → bytes for the blobs the handler asks for.
	layerBlobs map[string][]byte
	// failOn reports an error if set when the handler calls the named method.
	failOn map[string]error
}

func (f *fakeManifestPuller) PullDigest(_ context.Context, _ string) (string, error) {
	return f.digest, nil
}

func (f *fakeManifestPuller) PullImageConfig(_ context.Context, _ string) (oci.ImageConfig, error) {
	return oci.ImageConfig{Cmd: f.appConfig.Entrypoint}, nil
}

func (f *fakeManifestPuller) PullLayers(_ context.Context, ref string) (oci.PullLayersResult, error) {
	if err, ok := f.failOn["PullLayers"]; ok {
		return oci.PullLayersResult{}, err
	}
	// PR-B: sidecar refs route through their own manifest (a single
	// layer is enough for the test path — the build ext4s that one
	// layer's contents and stamps the per-workload row).
	var layers []oci.Descriptor
	if sm, ok := f.sidecarManifests[ref]; ok {
		layers = sm.Layers
	} else {
		layers = f.appManifest.Layers
	}
	out := make([]io.ReadCloser, 0, len(layers))
	for _, l := range layers {
		if b, ok := f.layerBlobs[l.Digest]; ok {
			out = append(out, io.NopCloser(bytes.NewReader(b)))
		}
	}
	return oci.PullLayersResult{Layers: out, Digest: ref}, nil
}

func (f *fakeManifestPuller) PullManifest(_ context.Context, ref string) (oci.Manifest, error) {
	if err, ok := f.failOn["PullManifest:"+ref]; ok {
		return oci.Manifest{}, err
	}
	// Sidecar ref → its own manifest (PR-B).
	if sm, ok := f.sidecarManifests[ref]; ok {
		return sm, nil
	}
	if ref == f.appRef || strings.HasPrefix(ref, "ghcr.io/onebox-faas/app:") || strings.Contains(ref, "/app:") {
		return f.appManifest, nil
	}
	return f.baseManifest, nil
}

func (f *fakeManifestPuller) PullBlob(_ context.Context, _, digest string) (io.ReadCloser, error) {
	if err, ok := f.failOn["PullBlob:"+digest]; ok {
		return nil, err
	}
	if b, ok := f.layerBlobs[digest]; ok {
		return io.NopCloser(bytes.NewReader(b)), nil
	}
	return nil, errors.New("fake: unknown blob " + digest)
}

// PullManifestWithAuth (M6 / issue #461) is the AuthManifestPuller sidecar
// half of PullManifest. The auth parameter is ignored by the fake — the
// PR-B sidecar build path (buildSidecarLayers in handler.go) type-asserts
// h.oci to oci.AuthManifestPuller, so the fake must satisfy that interface.
func (f *fakeManifestPuller) PullManifestWithAuth(ctx context.Context, ref string, _ *oci.BasicAuth) (oci.Manifest, error) {
	return f.PullManifest(ctx, ref)
}

// PullBlobWithAuth is the AuthManifestPuller sidecar half of PullBlob.
// Same auth-ignore posture as PullManifestWithAuth; the fake is
// registry-cred-less by construction.
func (f *fakeManifestPuller) PullBlobWithAuth(ctx context.Context, repo, digest string, _ *oci.BasicAuth) (io.ReadCloser, error) {
	return f.PullBlob(ctx, repo, digest)
}

// putConfig feeds a Config back as the JSON the handler's PullBlob reads via
// oci.ParseConfig (M6 path). The on-wire shape matches the OCI image-spec
// config document exactly.
func (f *fakeManifestPuller) putConfig(digest string, cfg oci.Config) {
	doc := struct {
		Config struct {
			Env        []string `json:"Env"`
			Entrypoint []string `json:"Entrypoint"`
			Cmd        []string `json:"Cmd"`
			WorkingDir string   `json:"WorkingDir"`
			User       string   `json:"User"`
		} `json:"config"`
		RootFS struct {
			Type  string   `json:"type"`
			Diffs []string `json:"diff_ids"`
		} `json:"rootfs"`
	}{
		Config: struct {
			Env        []string `json:"Env"`
			Entrypoint []string `json:"Entrypoint"`
			Cmd        []string `json:"Cmd"`
			WorkingDir string   `json:"WorkingDir"`
			User       string   `json:"User"`
		}{
			Env: envMapToSlice(cfg.Env), Entrypoint: cfg.Entrypoint, Cmd: cfg.Cmd,
			WorkingDir: cfg.WorkingDir, User: cfg.User,
		},
		RootFS: struct {
			Type  string   `json:"type"`
			Diffs []string `json:"diff_ids"`
		}{Type: "layers", Diffs: cfg.DiffIDs},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	if f.layerBlobs == nil {
		f.layerBlobs = map[string][]byte{}
	}
	f.layerBlobs[digest] = b
}

// recordingRunner captures the argv handed to mkfs.ext4 and stubs the output
// file (we never actually run mkfs on macOS CI; the build's apply + inject
// steps run in pure Go and only the final mkfs would need root + /dev/loop,
// which Linux-only integration tests cover).
//
// argv shape from rootfs.MkfsCommand:
//
//	[mkfs.ext4, -F, -L, applayer, -d, stagingDir, outImage, NNNM]
//
// Skip the -d flag's argument so we don't write to stagingDir.
type recordingRunner struct {
	argv []string
}

func (r *recordingRunner) Run(_ context.Context, argv []string) error {
	r.argv = argv
	// MkfsCommand layout:
	//   [mkfs.ext4, -F, -L, applayer, -d, stagingDir, outImage, sizeMB+"M"]
	// Pick outImage = argv[len-2] and stub it so SetDeploymentRootfs can
	// stamp a size. The handler writes to <appsRoot>/<slug>/<dep>.ext4 so
	// mkdir parent first.
	var out string
	if len(argv) >= 2 {
		out = argv[len(argv)-2]
	}
	if out != "" {
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		if err := writeFileImpl(out, bytes.Repeat([]byte{0}, 1024), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// TestHandleDeployment_RealBuildPath drives the M6 wired-up build: the
// ManifestPuller returns canned manifests, the rootfs.Builder applies layers
// + injects guest-init + manifest, and the deployment row gets a rootfs_path
// stamped. It uses a recordingRunner that captures mkfs argv (we don't need
// a real mkfs to validate the wired-up logic).
func TestHandleDeployment_RealBuildPath(t *testing.T) {
	for _, resolve := range []bool{false, true} {
		t.Run(fmt.Sprintf("platform_resolution=%t", resolve), func(t *testing.T) {
			store := state.NewMemStore()
			acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
			app, _ := store.CreateApp(context.Background(), state.App{
				AccountID: acct.ID, Slug: "img-app", RAMMB: 512, Runtime: "node22",
				IdleTimeoutS: 60, MaxConcurrency: 5,
			})
			dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
				AppID: app.ID, ImageDigest: "ghcr.io/org/app:v1", Kind: state.DeploymentKindImage,
			})

			appConfigDigest := "sha256:" + strings.Repeat("a", 64)
			baseConfigDigest := "sha256:" + strings.Repeat("b", 64)
			layer1 := "sha256:" + strings.Repeat("1", 64)
			layer2 := "sha256:" + strings.Repeat("2", 64)
			baseLayer := "sha256:" + strings.Repeat("0", 64)

			diffID1 := "sha256:" + strings.Repeat("c", 64)
			diffID2 := "sha256:" + strings.Repeat("d", 64)
			baseDiffID := "sha256:" + strings.Repeat("e", 64)

			appConfigJSON := `{"config":{"Env":["NODE_ENV=production"],"Entrypoint":["node"],"Cmd":["index.js"]},"rootfs":{"type":"layers","diff_ids":["` + baseDiffID + `","` + diffID1 + `","` + diffID2 + `"]}}`
			baseConfigJSON := `{"config":{"Env":[]},"rootfs":{"type":"layers","diff_ids":["` + baseDiffID + `"]}}`

			mp := &fakeManifestPuller{
				digest: "ghcr.io/org/app@sha256:" + strings.Repeat("9", 64),
				appRef: dep.ImageDigest,
				appManifest: oci.Manifest{
					Config: oci.Descriptor{Digest: appConfigDigest, Size: int64(len(appConfigJSON))},
					Layers: []oci.Descriptor{
						{Digest: baseLayer, Size: 100},
						{Digest: layer1, Size: 200},
						{Digest: layer2, Size: 300},
					},
				},
				appConfig:    oci.Config{Entrypoint: []string{"node"}, Cmd: []string{"index.js"}, DiffIDs: []string{baseDiffID, diffID1, diffID2}},
				baseManifest: oci.Manifest{Config: oci.Descriptor{Digest: baseConfigDigest, Size: int64(len(baseConfigJSON))}},
				baseConfig:   oci.Config{DiffIDs: []string{baseDiffID}},
			}
			mp.putConfig(appConfigDigest, mp.appConfig)
			mp.putConfig(baseConfigDigest, mp.baseConfig)
			mp.layerBlobs[layer1] = gzTar(t, map[string]string{"app/index.js": "console.log('hi')\n"})
			mp.layerBlobs[layer2] = gzTar(t, map[string]string{"app/lib/util.js": "module.exports = {}\n"})

			run := &recordingRunner{}
			b := rootfs.NewBuilder(run)
			tmp := t.TempDir()
			guestInitPath := filepath.Join(tmp, "guest-init")
			if err := writeFileImpl(guestInitPath, []byte("fake guest init"), 0o755); err != nil {
				t.Fatal(err)
			}

			notif := &fakeNotifier{}
			var puller oci.Puller = mp
			var resolving *resolvingTestPuller
			if resolve {
				child := "sha256:" + strings.Repeat("8", 64)
				mp.appRef = "ghcr.io/org/app@" + child
				resolving = &resolvingTestPuller{fakeManifestPuller: mp, resolution: oci.ImageResolution{
					SourceReference: "ghcr.io/org/app@sha256:" + strings.Repeat("9", 64),
					Reference:       mp.appRef, Digest: child,
				}}
				puller = resolving
			}
			h := New(store, notif, puller, b, guestInitPath, t.TempDir(), silentLogger())

			h.HandleNotification(context.Background(), db.Notification{
				Channel: db.NotifyDeploymentChanged,
				Payload: `{"app_id":"` + app.ID + `","to":"` + dep.ID + `","kind":"image","image_digest":"ghcr.io/org/app:v1"}`,
			})

			// Should have transitioned through building → imaging → snapshotting.
			got, _ := store.DeploymentByID(context.Background(), dep.ID)
			if got.Status != state.DeploySnapshotting {
				t.Errorf("status = %s, want snapshotting (err=%q)", got.Status, got.Error)
			}
			if got.RootfsPath == "" {
				t.Error("rootfs_path not stamped")
			}
			if findNotify(notif, db.NotifySnapshotPrime) == nil {
				t.Error("expected snapshot_prime notification")
			}
			if resolving != nil {
				if len(resolving.configRefs) != 1 || resolving.configRefs[0] != mp.appRef || len(resolving.manifestRefs) < 1 || resolving.manifestRefs[0] != mp.appRef {
					t.Fatalf("build did not consume selected child: configs=%v manifests=%v", resolving.configRefs, resolving.manifestRefs)
				}
				if got.ImageDigest != dep.ImageDigest {
					t.Fatal("original deployment reference was overwritten")
				}
			}
		})
	}
}

// TestHandleDeployment_FullRootfsWithSidecars proves the full-rootfs dispatch
// keeps the deployment's sidecar set instead of rejecting it before the main
// artifact is built. The fake builder stands in for mkfs and the fake puller
// supplies an app whose diff-id prefix does not match the shared base, forcing
// the typed full-rootfs fallback.
func TestHandleDeployment_FullRootfsWithSidecars(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "img-app-fullroot-sidecars", RAMMB: 512, Runtime: "node22",
		IdleTimeoutS: 60, MaxConcurrency: 5,
	})
	fullRootfs := true
	sidecarRef := "ghcr.io/org/metrics@sha256:" + strings.Repeat("a", 64)
	sidecarsJSON, err := json.Marshal([]map[string]any{
		{"name": "metrics", "image": sidecarRef, "type": "sidecar", "port": 9090},
	})
	if err != nil {
		t.Fatal(err)
	}
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, ImageDigest: "ghcr.io/org/app:v1", Kind: state.DeploymentKindImage,
		Sidecars: sidecarsJSON, FullRootfsOverride: &fullRootfs,
	})

	appConfigDigest := "sha256:" + strings.Repeat("b", 64)
	baseConfigDigest := "sha256:" + strings.Repeat("c", 64)
	baseLayer := "sha256:" + strings.Repeat("d", 64)
	appLayer := "sha256:" + strings.Repeat("e", 64)
	sidecarLayer := "sha256:" + strings.Repeat("f", 64)
	appDiff := "sha256:" + strings.Repeat("1", 64)
	baseDiff := "sha256:" + strings.Repeat("2", 64)
	mp := &fakeManifestPuller{
		digest: "ghcr.io/org/app@sha256:" + strings.Repeat("9", 64),
		appRef: dep.ImageDigest,
		appManifest: oci.Manifest{Config: oci.Descriptor{Digest: appConfigDigest}, Layers: []oci.Descriptor{
			{Digest: baseLayer, Size: 100}, {Digest: appLayer, Size: 100},
		}},
		appConfig:    oci.Config{Entrypoint: []string{"/bin/sh"}, Cmd: []string{"-c", "echo ok"}, DiffIDs: []string{appDiff}},
		baseManifest: oci.Manifest{Config: oci.Descriptor{Digest: baseConfigDigest}},
		baseConfig:   oci.Config{DiffIDs: []string{baseDiff}},
		sidecarManifests: map[string]oci.Manifest{
			sidecarRef: {Layers: []oci.Descriptor{{Digest: sidecarLayer, Size: 100}}},
		},
		layerBlobs: make(map[string][]byte),
	}
	mp.putConfig(appConfigDigest, mp.appConfig)
	mp.putConfig(baseConfigDigest, mp.baseConfig)
	mp.layerBlobs[baseLayer] = gzTar(t, map[string]string{"etc/passwd": "app:x:1000:1000::/home/app:/bin/sh\n"})
	mp.layerBlobs[appLayer] = gzTar(t, map[string]string{"app/server": "#!/bin/sh\n"})
	mp.layerBlobs[sidecarLayer] = gzTar(t, map[string]string{"metrics": "#!/bin/sh\n"})

	b := &fakeBuilder{}
	notif := &fakeNotifier{}
	h := New(store, notif, mp, b, "/tmp/guest-init", t.TempDir(), silentLogger())
	h.HandleNotification(context.Background(), db.Notification{
		Channel: db.NotifyDeploymentChanged,
		Payload: `{"app_id":"` + app.ID + `","to":"` + dep.ID + `","kind":"image","image_digest":"ghcr.io/org/app:v1"}`,
	})

	got, _ := store.DeploymentByID(context.Background(), dep.ID)
	if got.Status != state.DeploySnapshotting {
		t.Fatalf("status = %s, want snapshotting (err=%q)", got.Status, got.Error)
	}
	rows, err := store.ListDeploymentSidecarLayers(context.Background(), dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].SidecarName != "metrics" {
		t.Fatalf("sidecar rows = %#v, want one metrics row", rows)
	}
	if findNotify(notif, db.NotifySnapshotPrime) == nil {
		t.Fatal("expected snapshot_prime notification")
	}
}

// TestHandleDeployment_RealBuild_BaseMismatchErrors asserts the M6 build path
// refuses an app whose layers don't sit on the chosen base.
func TestHandleDeployment_RealBuild_BaseMismatchErrors(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "img-app", RAMMB: 512, Runtime: "node22",
		IdleTimeoutS: 60, MaxConcurrency: 5,
	})
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, ImageDigest: "ghcr.io/org/app:v1", Kind: state.DeploymentKindImage,
	})

	appConfigDigest := "sha256:" + strings.Repeat("a", 64)
	baseConfigDigest := "sha256:" + strings.Repeat("b", 64)
	baseLayer := "sha256:" + strings.Repeat("0", 64)
	layer1 := "sha256:" + strings.Repeat("1", 64)

	diffID1 := "sha256:" + strings.Repeat("c", 64)
	baseDiffID := "sha256:" + strings.Repeat("x", 64)
	appBaseDiff := "sha256:" + strings.Repeat("e", 64)

	mp := &fakeManifestPuller{
		appRef: dep.ImageDigest,
		appManifest: oci.Manifest{
			Config: oci.Descriptor{Digest: appConfigDigest, Size: 100},
			Layers: []oci.Descriptor{
				{Digest: baseLayer, Size: 100},
				{Digest: layer1, Size: 200},
			},
		},
		appConfig:    oci.Config{Entrypoint: []string{"node"}, DiffIDs: []string{appBaseDiff, diffID1}},
		baseManifest: oci.Manifest{Config: oci.Descriptor{Digest: baseConfigDigest, Size: 100}},
		baseConfig:   oci.Config{DiffIDs: []string{baseDiffID}},
	}
	mp.putConfig(appConfigDigest, mp.appConfig)
	mp.putConfig(baseConfigDigest, mp.baseConfig)
	mp.layerBlobs[layer1] = gzTar(t, map[string]string{"app/x.js": "console.log('x')\n"})

	run := &recordingRunner{}
	b := rootfs.NewBuilder(run)
	tmp := t.TempDir()
	guestInitPath := filepath.Join(tmp, "guest-init")
	if err := writeFileImpl(guestInitPath, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	notif := &fakeNotifier{}
	h := New(store, notif, mp, b, guestInitPath, t.TempDir(), silentLogger())

	h.HandleNotification(context.Background(), db.Notification{
		Channel: db.NotifyDeploymentChanged,
		Payload: `{"app_id":"` + app.ID + `","to":"` + dep.ID + `","kind":"image","image_digest":"ghcr.io/org/app:v1"}`,
	})

	got, _ := store.DeploymentByID(context.Background(), dep.ID)
	if got.Status != state.DeployFailed {
		t.Errorf("status = %s, want failed", got.Status)
	}
	// ADR-141 §Decision 3: the prefix-check failure surfaces the
	// typed ErrLayersNotAboveBase sentinel. Free plan + no
	// override keeps the today-equivalent failure behavior,
	// surfaced as DeployFailed with the canonical sentinel lifted
	// to CodeImageManifestInvalid.
	if !strings.Contains(got.Error, "above base") && !strings.Contains(got.Error, "full-rootfs") {
		t.Errorf("error %q should mention 'above base' or 'full-rootfs'", got.Error)
	}
}

// TestHandleDeployment_BuildSidecarLayers exercises the
// pkg/imaged/handler.go::buildSidecarLayers path on a deployment
// that carries two sidecars (issue #463 / ADR-069 / PR-B).
//
// The test pins the AC #6 contract: after a successful
// deployment_changed notify, the per-workload filesystem handle
// (the deployment_sidecar_layers row) materializes for every
// sidecar in the jsonb envelope. We:
//
//   - stamp the deployment with two sidecars (1 init + 1 sidecar,
//     both digest-pinned),
//   - extend fakeManifestPuller with one layer each so the build
//     path can stream their blobs,
//   - drive HandleNotification with kind=image,
//   - assert (a) the deployment reaches DeploySnapshotting (the
//     same terminal state as the no-sidecar case — the sidecar
//     pass is invisible to the state machine), (b) the
//     per-workload rows materialize with name-ASC ordering and
//     the canonical apps/<slug>/<depID>-<name>.ext4 storage keys,
//     and (c) the digest-pinned image ref is captured on the row
//     so vmmd has a stable cross-check at wake time.
//
// Denylist rejection (stateful sidecar image) is covered
// separately by the per-sidecar StatefulDenyListMatch code path
// inside buildSidecarLayers; this test pins the happy path
// because that's where the new storage keys + row inserts land.
func TestHandleDeployment_BuildSidecarLayers(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "img-app-sidecars", RAMMB: 512, Runtime: "node22",
		IdleTimeoutS: 60, MaxConcurrency: 5,
	})

	// Two digest-pinned sidecar refs. The handler's
	// StatefulDenyListMatch accepts these (alpine / busybox
	// are not in the deny list), so the path proceeds past the
	// gate. Each sidecar carries a unique layer blob so we
	// can assert the build actually streamed that sidecar's
	// content (not just a no-op pass).
	migratorRef := "ghcr.io/org/migrator@sha256:" + strings.Repeat("c", 64)
	scraperRef := "ghcr.io/org/scraper@sha256:" + strings.Repeat("d", 64)
	migratorLayer := "sha256:" + strings.Repeat("m", 64)
	scraperLayer := "sha256:" + strings.Repeat("s", 64)
	sidecarsJSON, err := json.Marshal([]map[string]any{
		{"name": "migrator", "image": migratorRef, "type": "init"},
		{"name": "scraper", "image": scraperRef, "type": "sidecar"},
	})
	if err != nil {
		t.Fatal(err)
	}

	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID:       app.ID,
		ImageDigest: "ghcr.io/org/app:v1",
		Kind:        state.DeploymentKindImage,
		Sidecars:    sidecarsJSON,
	})

	// Main app's manifest + layers.
	appConfigDigest := "sha256:" + strings.Repeat("a", 64)
	baseConfigDigest := "sha256:" + strings.Repeat("b", 64)
	layer1 := "sha256:" + strings.Repeat("1", 64)
	layer2 := "sha256:" + strings.Repeat("2", 64)
	baseLayer := "sha256:" + strings.Repeat("0", 64)

	diffID1 := "sha256:" + strings.Repeat("c", 64)
	diffID2 := "sha256:" + strings.Repeat("d", 64)
	baseDiffID := "sha256:" + strings.Repeat("e", 64)

	mp := &fakeManifestPuller{
		digest: "ghcr.io/org/app@sha256:" + strings.Repeat("9", 64),
		appRef: dep.ImageDigest,
		appManifest: oci.Manifest{
			Config: oci.Descriptor{Digest: appConfigDigest, Size: 100},
			Layers: []oci.Descriptor{
				{Digest: baseLayer, Size: 100},
				{Digest: layer1, Size: 200},
				{Digest: layer2, Size: 300},
			},
		},
		appConfig: oci.Config{
			Entrypoint: []string{"node"},
			Cmd:        []string{"index.js"},
			DiffIDs:    []string{baseDiffID, diffID1, diffID2},
		},
		baseManifest: oci.Manifest{Config: oci.Descriptor{Digest: baseConfigDigest, Size: 100}},
		baseConfig:   oci.Config{DiffIDs: []string{baseDiffID}},
		// Sidecar manifests — one layer each, distinct digests so
		// the build path's PullLayers routes to the right blob.
		sidecarManifests: map[string]oci.Manifest{
			migratorRef: {Layers: []oci.Descriptor{{Digest: migratorLayer, Size: 128}}},
			scraperRef:  {Layers: []oci.Descriptor{{Digest: scraperLayer, Size: 256}}},
		},
	}
	mp.putConfig(appConfigDigest, mp.appConfig)
	mp.putConfig(baseConfigDigest, mp.baseConfig)
	mp.layerBlobs[layer1] = gzTar(t, map[string]string{"app/index.js": "console.log('hi')\n"})
	mp.layerBlobs[layer2] = gzTar(t, map[string]string{"app/lib/util.js": "module.exports = {}\n"})
	mp.layerBlobs[migratorLayer] = gzTar(t, map[string]string{"migrate": "#!/bin/sh\necho migrate\n"})
	mp.layerBlobs[scraperLayer] = gzTar(t, map[string]string{"scrape": "#!/bin/sh\necho scrape\n"})

	run := &recordingRunner{}
	b := rootfs.NewBuilder(run)
	tmp := t.TempDir()
	guestInitPath := filepath.Join(tmp, "guest-init")
	if err := writeFileImpl(guestInitPath, []byte("fake guest init"), 0o755); err != nil {
		t.Fatal(err)
	}

	notif := &fakeNotifier{}
	h := New(store, notif, mp, b, guestInitPath, t.TempDir(), silentLogger())

	h.HandleNotification(context.Background(), db.Notification{
		Channel: db.NotifyDeploymentChanged,
		Payload: `{"app_id":"` + app.ID + `","to":"` + dep.ID + `","kind":"image","image_digest":"ghcr.io/org/app:v1"}`,
	})

	// Terminal state matches the no-sidecar case (DeploySnapshotting)
	// — the sidecar pass is invisible to the state machine.
	got, _ := store.DeploymentByID(context.Background(), dep.ID)
	if got.Status != state.DeploySnapshotting {
		t.Fatalf("status = %s, want snapshotting (err=%q)", got.Status, got.Error)
	}

	// Per-workload rows materialized, ordered by sidecar_name ASC
	// (the load-bearing property — sched snapshot hashing relies
	// on deterministic ordering).
	rows, err := store.ListDeploymentSidecarLayers(context.Background(), dep.ID)
	if err != nil {
		t.Fatalf("list sidecar layers: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows; want 2", len(rows))
	}
	if rows[0].SidecarName != "migrator" || rows[1].SidecarName != "scraper" {
		t.Errorf("ordering: got [%q, %q]; want [migrator, scraper]",
			rows[0].SidecarName, rows[1].SidecarName)
	}
	// Storage keys follow sched.AppSidecarLayerKey's canonical
	// shape (sibling of the main apps/<slug>/<depID>.ext4 key).
	wantMigrator := sched.AppSidecarLayerKey(app.Slug, dep.ID, "migrator")
	wantScraper := sched.AppSidecarLayerKey(app.Slug, dep.ID, "scraper")
	if rows[0].StorageKey != wantMigrator {
		t.Errorf("migrator.StorageKey = %q; want %q", rows[0].StorageKey, wantMigrator)
	}
	if rows[1].StorageKey != wantScraper {
		t.Errorf("scraper.StorageKey = %q; want %q", rows[1].StorageKey, wantScraper)
	}
	// The digest-pinned image ref is captured on the row so vmmd
	// has a stable cross-check at wake time (PR-B's
	// restoreCompatibility invariant).
	if rows[0].ContentDigest != migratorRef {
		t.Errorf("migrator.ContentDigest = %q; want %q", rows[0].ContentDigest, migratorRef)
	}
	if rows[1].ContentDigest != scraperRef {
		t.Errorf("scraper.ContentDigest = %q; want %q", rows[1].ContentDigest, scraperRef)
	}

	// The build ext4'd each sidecar's blob (Bytes > 0) — proves
	// the per-sidecar rootfs.Builder.Build call landed rather
	// than no-op-passing on the layer slice.
	if rows[0].Bytes <= 0 {
		t.Errorf("migrator.Bytes = %d; want > 0", rows[0].Bytes)
	}
	if rows[1].Bytes <= 0 {
		t.Errorf("scraper.Bytes = %d; want > 0", rows[1].Bytes)
	}

	// snapshot_prime fired (the main app path's terminal handshake).
	if findNotify(notif, db.NotifySnapshotPrime) == nil {
		t.Error("expected snapshot_prime notification")
	}
}

// TestHandleDeployment_BuildSidecarLayers_DenylistRejects asserts
// the stateful-deny gate inside buildSidecarLayers fails the
// deployment before any pull (issue #463 / ADR-069 / PR-B §"no
// stateful sidecars"). The re-check at the storage boundary is
// defence-in-depth — apid's handler already gated at the API
// surface, but a malformed row written directly to deployments
// (a leaked service token, a buggy migration replay) must still
// trip the deny list here. The build path transitions the
// deployment to FAILED with the sidecar name in the message
// so the customer sees WHICH sidecar tripped the gate.
func TestHandleDeployment_BuildSidecarLayers_DenylistRejects(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "img-app-stateful", RAMMB: 512, Runtime: "node22",
		IdleTimeoutS: 60, MaxConcurrency: 5,
	})
	// postgres:14 hits the stateful deny list (well-known
	// database image). Image is digest-pinned (the API gate
	// already rejected the tag form).
	statefulRef := "ghcr.io/org/postgres@sha256:" + strings.Repeat("a", 64)
	sidecarsJSON, err := json.Marshal([]map[string]any{
		{"name": "db", "image": statefulRef, "type": "sidecar"},
	})
	if err != nil {
		t.Fatal(err)
	}
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID:       app.ID,
		ImageDigest: "ghcr.io/org/app:v1",
		Kind:        state.DeploymentKindImage,
		Sidecars:    sidecarsJSON,
	})

	// Main app manifest + layers.
	appConfigDigest := "sha256:" + strings.Repeat("a", 64)
	baseConfigDigest := "sha256:" + strings.Repeat("b", 64)
	layer1 := "sha256:" + strings.Repeat("1", 64)
	baseLayer := "sha256:" + strings.Repeat("0", 64)
	diffID1 := "sha256:" + strings.Repeat("c", 64)
	baseDiffID := "sha256:" + strings.Repeat("e", 64)

	mp := &fakeManifestPuller{
		appRef: dep.ImageDigest,
		appManifest: oci.Manifest{
			Config: oci.Descriptor{Digest: appConfigDigest, Size: 100},
			Layers: []oci.Descriptor{
				{Digest: baseLayer, Size: 100},
				{Digest: layer1, Size: 200},
			},
		},
		appConfig: oci.Config{
			Entrypoint: []string{"node"}, Cmd: []string{"index.js"},
			DiffIDs: []string{baseDiffID, diffID1},
		},
		baseManifest: oci.Manifest{Config: oci.Descriptor{Digest: baseConfigDigest, Size: 100}},
		baseConfig:   oci.Config{DiffIDs: []string{baseDiffID}},
	}
	mp.putConfig(appConfigDigest, mp.appConfig)
	mp.putConfig(baseConfigDigest, mp.baseConfig)
	mp.layerBlobs[layer1] = gzTar(t, map[string]string{"app/x.js": "x"})

	run := &recordingRunner{}
	b := rootfs.NewBuilder(run)
	tmp := t.TempDir()
	guestInitPath := filepath.Join(tmp, "guest-init")
	if err := writeFileImpl(guestInitPath, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	notif := &fakeNotifier{}
	h := New(store, notif, mp, b, guestInitPath, t.TempDir(), silentLogger())

	h.HandleNotification(context.Background(), db.Notification{
		Channel: db.NotifyDeploymentChanged,
		Payload: `{"app_id":"` + app.ID + `","to":"` + dep.ID + `","kind":"image","image_digest":"ghcr.io/org/app:v1"}`,
	})

	got, _ := store.DeploymentByID(context.Background(), dep.ID)
	if got.Status != state.DeployFailed {
		t.Errorf("status = %s, want failed", got.Status)
	}
	if !strings.Contains(got.Error, "db") || !strings.Contains(got.Error, "stateful") {
		t.Errorf("error %q should mention sidecar name + stateful", got.Error)
	}

	// No per-workload row materialized (deny happened before pull).
	rows, _ := store.ListDeploymentSidecarLayers(context.Background(), dep.ID)
	if len(rows) != 0 {
		t.Errorf("got %d rows after deny; want 0", len(rows))
	}

	// snapshot_prime was NOT fired — the sidecar deny short-
	// circuits the deployment before the main app's terminal
	// handshake.
	if findNotify(notif, db.NotifySnapshotPrime) != nil {
		t.Error("snapshot_prime should not fire on sidecar denylist rejection")
	}
}

// envMapToSlice converts a map[string]string back to "KEY=VALUE" slice
// form for OCI wire JSON marshalling in putConfig. The OCI image-spec
// config document carries Env as []string, while oci.Config stores it
// as map[string]string after the F7 round-trip removal — so the test
// helper bridges the two at the wire boundary. Sorted output for
// deterministic test JSON.
func envMapToSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(env))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}
