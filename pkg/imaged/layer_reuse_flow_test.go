package imaged

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// corruptLayerBlobs copies a local OCI archive with each gzip layer blob
// replaced by as many other bytes. Index, manifest and config still verify,
// so only a pass that reads the layers fails on it.
func corruptLayerBlobs(t *testing.T, archive string) string {
	t.Helper()
	original, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "corrupt.tar")
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	tr, tw := tar.NewReader(bytes.NewReader(original)), tar.NewWriter(out)
	corrupted := 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b {
			data = bytes.Repeat([]byte{'x'}, len(data))
			corrupted++
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	if corrupted == 0 {
		t.Fatal("archive has no gzip layer to corrupt")
	}
	return path
}

// ADR-20261112170911 amendment 1: a deployment whose layer inputs match an
// earlier layer republishes it without reading its image's layers.
func TestSourceLayerReuseDoesNotReadTheImageLayers(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "layer-reuse@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "reuse", Type: state.AppTypeApp,
		Manifest: state.AppManifest{BuildDockerfile: "Dockerfile"}})
	if err != nil {
		t.Fatal(err)
	}
	appsRoot := t.TempDir()
	be, err := storage.NewLocalStorageBackend(appsRoot)
	if err != nil {
		t.Fatal(err)
	}
	guestInit := filepath.Join(t.TempDir(), "init")
	if err := os.WriteFile(guestInit, []byte("guest-init"), 0o600); err != nil {
		t.Fatal(err)
	}
	builder := &reuseBuilder{fakeBuilder: &fakeBuilder{bytesOut: 128}}
	h := New(store, &fakeNotifier{}, nil, builder, guestInit, appsRoot, silentLogger()).
		WithStorage(be).WithLayerReuse(filepath.Join(t.TempDir(), "layer-reuse"))
	archive := sourceBuildArchive(t, nil)
	deploy := func(app state.App, archive string) error {
		t.Helper()
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindDockerfile, Status: state.DeployImaging})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, dep.ID, archive, "builder/source", 1); err != nil {
			t.Fatal(err)
		}
		if dep, err = store.DeploymentByID(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		return h.buildLocalOCIAppLayer(ctx, app, &dep, acct, nil)
	}

	if err := deploy(app, archive); err != nil {
		t.Fatalf("first deploy: %v", err)
	}
	corrupt := corruptLayerBlobs(t, archive)
	if err := deploy(app, corrupt); err != nil {
		t.Fatalf("reused deploy read the image layers: %v", err)
	}
	if len(builder.calls) != 1 || builder.republishes != 1 {
		t.Fatalf("builds %d, republishes %d; want the second deploy to republish", len(builder.calls), builder.republishes)
	}

	// Another start command is another key: the layers are read, and the
	// corrupt ones refused.
	app.StartCommand = "/app/serve --port 8080"
	err = deploy(app, corrupt)
	if err == nil || !strings.Contains(err.Error(), "load built OCI image") {
		t.Fatalf("deploy with a new key over corrupt layers = %v, want a layer verification failure", err)
	}
	if builder.republishes != 1 {
		t.Fatalf("republishes %d after a key change, want 1", builder.republishes)
	}
}
