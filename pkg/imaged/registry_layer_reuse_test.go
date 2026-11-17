package imaged

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/state"
)

// countingRunner counts mkfs runs: one per converted layer.
type countingRunner struct {
	recordingRunner
	mkfs int
}

func (r *countingRunner) Run(ctx context.Context, argv []string) error {
	if len(argv) > 0 && strings.HasPrefix(filepath.Base(argv[0]), "mkfs") {
		r.mkfs++
	}
	return r.recordingRunner.Run(ctx, argv)
}

// Kroki feedback 2026-11-17: a registry image and its companions were
// converted again on every deployment, even for the same digests.
func TestRegistryImageAndCompanionsReuseTheirLayers(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(ctx, "reuse@example.com", "pro")
	app, _ := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "kroki", RAMMB: 512, Runtime: "node22", IdleTimeoutS: 60, MaxConcurrency: 5})

	mermaidRef := "ghcr.io/org/mermaid@sha256:" + strings.Repeat("c", 64)
	mermaidLayer := "sha256:" + strings.Repeat("m", 64)
	sidecars, _ := json.Marshal([]map[string]any{{"name": "mermaid", "image": mermaidRef, "type": "sidecar"}})

	appConfigDigest := "sha256:" + strings.Repeat("a", 64)
	baseConfigDigest := "sha256:" + strings.Repeat("b", 64)
	layer1 := "sha256:" + strings.Repeat("1", 64)
	baseLayer := "sha256:" + strings.Repeat("0", 64)
	diffID1 := "sha256:" + strings.Repeat("d", 64)
	baseDiffID := "sha256:" + strings.Repeat("e", 64)
	mp := &fakeManifestPuller{
		digest: "ghcr.io/org/kroki@sha256:" + strings.Repeat("9", 64),
		appRef: "ghcr.io/org/kroki:v1",
		appManifest: oci.Manifest{
			Config: oci.Descriptor{Digest: appConfigDigest, Size: 100},
			Layers: []oci.Descriptor{{Digest: baseLayer, Size: 100}, {Digest: layer1, Size: 200}},
		},
		appConfig:        oci.Config{Entrypoint: []string{"node"}, Cmd: []string{"index.js"}, DiffIDs: []string{baseDiffID, diffID1}},
		baseManifest:     oci.Manifest{Config: oci.Descriptor{Digest: baseConfigDigest, Size: 100}},
		baseConfig:       oci.Config{DiffIDs: []string{baseDiffID}},
		sidecarManifests: map[string]oci.Manifest{mermaidRef: {Layers: []oci.Descriptor{{Digest: mermaidLayer, Size: 128}}}},
	}
	mp.putConfig(appConfigDigest, mp.appConfig)
	mp.putConfig(baseConfigDigest, mp.baseConfig)
	mp.layerBlobs[layer1] = gzTar(t, map[string]string{"app/index.js": "console.log('hi')\n"})
	mp.layerBlobs[mermaidLayer] = gzTar(t, map[string]string{"mermaid": "#!/bin/sh\necho mermaid\n"})

	run := &countingRunner{}
	tmp := t.TempDir()
	guestInitPath := filepath.Join(tmp, "guest-init")
	if err := writeFileImpl(guestInitPath, []byte("fake guest init"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := New(store, &fakeNotifier{}, mp, rootfs.NewBuilder(run), guestInitPath, t.TempDir(), silentLogger()).
		WithLayerReuse(filepath.Join(t.TempDir(), "layer-reuse"))

	deploy := func() state.Deployment {
		t.Helper()
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "ghcr.io/org/kroki:v1", Kind: state.DeploymentKindImage, Sidecars: sidecars})
		if err != nil {
			t.Fatal(err)
		}
		h.HandleNotification(ctx, db.Notification{
			Channel: db.NotifyDeploymentChanged,
			Payload: `{"app_id":"` + app.ID + `","to":"` + dep.ID + `","kind":"image","image_digest":"ghcr.io/org/kroki:v1"}`,
		})
		got, _ := store.DeploymentByID(ctx, dep.ID)
		if got.Status != state.DeploySnapshotting {
			t.Fatalf("status = %s, want snapshotting (err=%q)", got.Status, got.Error)
		}
		return got
	}

	first := deploy()
	built := run.mkfs
	if built < 2 {
		t.Fatalf("first deployment ran mkfs %d times, want the app layer and the companion", built)
	}
	second := deploy()
	if run.mkfs != built {
		t.Fatalf("second deployment of the same digests ran mkfs %d more times, want every layer reused", run.mkfs-built)
	}
	if second.RootfsKey == "" || second.RootfsKey == first.RootfsKey {
		t.Fatalf("second rootfs key = %q (first %q), want its own published key", second.RootfsKey, first.RootfsKey)
	}
	rows, err := store.ListDeploymentSidecarLayers(ctx, second.ID)
	if err != nil || len(rows) != 1 || rows[0].SidecarName != "mermaid" || rows[0].ContentDigest != mermaidRef || rows[0].Bytes <= 0 {
		t.Fatalf("second deployment's companion rows = %+v, %v", rows, err)
	}
}

// A signed conversion publishes per-layer evidence a reuse cannot produce.
func TestRegistryReuseExcludesSignedImages(t *testing.T) {
	unsigned := preparedContainerWorkload{}
	unsigned.Digest = "sha256:" + strings.Repeat("a", 64)
	if !registryReuseEligible(state.App{}, unsigned) {
		t.Fatal("an unsigned digest-pinned image is eligible")
	}
	verified := unsigned
	verified.Verification = state.DeploymentRegistryVerification{ID: "v-1"}
	if registryReuseEligible(state.App{}, verified) {
		t.Fatal("a verified conversion must not be reused")
	}
	if registryReuseEligible(state.App{RequireSigned: true}, unsigned) {
		t.Fatal("an app that requires signed images must not reuse")
	}
	if registryReuseEligible(state.App{}, preparedContainerWorkload{}) {
		t.Fatal("an image without a resolved digest must not reuse")
	}
}
