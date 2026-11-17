package imaged

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// Every AppManifest field must be classified: a field that can change an app
// layer's files belongs in the reuse key, any other only in the embedded
// manifest. An unclassified new field fails here instead of silently leaving
// the key.
func TestLayerManifestFieldsAreClassified(t *testing.T) {
	for _, name := range appManifestFieldNames() {
		content := slices.Contains(layerContentFields, name)
		runtime := slices.Contains(layerRuntimeOnlyFields, name)
		if content == runtime {
			t.Errorf("AppManifest.%s: classify it in layerContentFields (changes layer files) or layerRuntimeOnlyFields (only the embedded manifest)", name)
		}
	}
}

func TestSplitManifestReplaysTheRunnerRewrites(t *testing.T) {
	input := api.AppManifest{Entrypoint: []string{"node", "index.js"}, Env: map[string]string{"A": "1"}, Port: 8080}
	final := api.AppManifest{Entrypoint: []string{"/runner/node", "index.js"}, Env: map[string]string{"A": "1", "PATH": "/runner/bin"}, Port: 8080}
	next := api.AppManifest{Entrypoint: []string{"node", "index.js"}, Env: map[string]string{"A": "2", "B": "x"}, Port: 9090, Healthz: "/h"}
	got, ok := splitManifest(next, input, final)
	if !ok {
		t.Fatal("splitManifest refused")
	}
	want := api.AppManifest{Entrypoint: []string{"/runner/node", "index.js"}, Env: map[string]string{"A": "2", "B": "x", "PATH": "/runner/bin"}, Port: 9090, Healthz: "/h"}
	if !manifestsEqual(got, want) {
		t.Fatalf("split = %+v, want %+v", got, want)
	}
	if next.Env["PATH"] != "" {
		t.Fatal("splitManifest mutated its input")
	}
	removed := api.AppManifest{Entrypoint: input.Entrypoint, Env: map[string]string{}}
	if _, ok := splitManifest(next, input, removed); ok {
		t.Fatal("a build that removed an env key must not be replayed")
	}
}

// Kroki feedback 2026-11-17: a redeploy of the same image with new runtime
// settings converted the image again.
func TestBuildOrReuseAppLayerPatchesRuntimeSettings(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	be, err := storage.NewLocalStorageBackend(root)
	if err != nil {
		t.Fatal(err)
	}
	builder := &reuseBuilder{fakeBuilder: &fakeBuilder{}}
	h := New(state.NewMemStore(), &fakeNotifier{}, fakePuller{}, builder, "./init", root, slog.New(slog.DiscardHandler)).
		WithStorage(be).WithLayerReuse(filepath.Join(t.TempDir(), "layer-reuse"))
	app := state.App{ID: "app-1", AccountID: "acct-1", Slug: "kroki"}
	key := strings.Repeat("d", 64)
	v1 := api.AppManifest{Entrypoint: []string{"java", "-jar", "kroki.jar"}, Port: 8000, Env: map[string]string{"KROKI_MERMAID_HOST": "mermaid"}}
	v2 := api.AppManifest{Entrypoint: v1.Entrypoint, Port: 8000, Env: map[string]string{}}
	deploy := func(dep string, m api.AppManifest) rootfs.BuildResult {
		t.Helper()
		res, err := h.buildOrReuseAppLayer(ctx, key, app, state.Deployment{ID: dep}, rootfs.BuildInput{Manifest: m, Storage: be, StorageKey: "apps/kroki/" + dep + ".ext4"}, nil)
		if err != nil {
			t.Fatalf("deploy %s: %v", dep, err)
		}
		return res
	}

	first := deploy("dep-1", v1)
	second := deploy("dep-2", v2)
	if len(builder.calls) != 1 || len(builder.patches) != 1 || builder.republishes != 0 {
		t.Fatalf("builds %d patches %d republishes %d; want the settings change patched", len(builder.calls), len(builder.patches), builder.republishes)
	}
	p := builder.patches[0]
	if !manifestsEqual(p.Old, v1) || !manifestsEqual(p.New, v2) || p.Source.Digest != first.ArtifactDigest {
		t.Fatalf("patch = %+v, want v1 -> v2 of dep-1's layer", p)
	}
	if second.ArtifactDigest == first.ArtifactDigest || second.ContentBytes != first.ContentBytes+3 {
		t.Fatalf("patched result %+v, want a new digest and the content delta", second)
	}
	if _, ok := h.layerReuse.scanSource(second.ArtifactDigest); ok {
		t.Fatal("a patched layer must be scanned, not inherit a scan source")
	}

	// The record now describes dep-2's patched layer: the same settings
	// republish it as it is.
	deploy("dep-3", v2)
	if len(builder.calls) != 1 || len(builder.patches) != 1 || builder.republishes != 1 {
		t.Fatalf("builds %d patches %d republishes %d; want an unchanged republish", len(builder.calls), len(builder.patches), builder.republishes)
	}

	// A layer the patch cannot handle is built, and its record survives for
	// its own manifest.
	builder.patchErr = rootfs.ErrManifestPatchUnsupported
	deploy("dep-4", v1)
	if len(builder.calls) != 2 {
		t.Fatalf("builds %d, want a build after an unsupported patch", len(builder.calls))
	}
	builder.patchErr = errors.New("disk full")
	deploy("dep-5", v2)
	if len(builder.calls) != 3 {
		t.Fatalf("builds %d, want a build after a failed patch", len(builder.calls))
	}
}
