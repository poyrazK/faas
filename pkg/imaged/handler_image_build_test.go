package imaged

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeManifestPuller is the M6 ManifestPuller test double. It returns canned
// manifests + configs + blobs so handler.go can run its full build path
// without a registry. Implements the full oci.Puller (PullDigest,
// PullImageConfig, PullLayers) plus the M6 extensions (PullManifest,
// PullBlob) so it satisfies oci.ManifestPuller.
type fakeManifestPuller struct {
	digest       string
	appRef       string
	appManifest  oci.Manifest
	appConfig    oci.Config
	baseManifest oci.Manifest
	baseConfig   oci.Config

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
	out := make([]io.ReadCloser, 0, len(f.appManifest.Layers))
	for _, l := range f.appManifest.Layers {
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
			Env: cfg.Env, Entrypoint: cfg.Entrypoint, Cmd: cfg.Cmd,
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
//   [mkfs.ext4, -F, -L, applayer, -d, stagingDir, outImage, NNNM]
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
	h := New(store, notif, mp, b, guestInitPath, t.TempDir(), silentLogger())

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
	if !strings.Contains(got.Error, "above base") {
		t.Errorf("error %q should mention 'above base'", got.Error)
	}
}
