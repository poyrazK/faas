package imaged

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

// refManifestPuller serves a different manifest per reference, so a test can
// tell the runner base a build recorded from the node's current pin.
type refManifestPuller struct {
	*minimalManifestPuller
	byRef map[string]oci.Manifest
}

func (p *refManifestPuller) PullManifest(_ context.Context, ref string) (oci.Manifest, error) {
	m, ok := p.byRef[ref]
	if !ok {
		return oci.Manifest{}, errors.New("no such reference in fake: " + ref)
	}
	return m, nil
}

func tarBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		body := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// layeredArtifactFixture writes a local OCI archive whose layers are the
// given uncompressed tarballs, bottom first.
func layeredArtifactFixture(t *testing.T, layers ...[]byte) string {
	t.Helper()
	raw := minimalConfigBytes(layers...)
	entries := map[string][]byte{blobPath(digestFor(t, raw)): raw}
	compressed := make([][]byte, len(layers))
	for i, layer := range layers {
		compressed[i] = gzipBytes(t, layer)
		entries[blobPath(digestFor(t, compressed[i]))] = compressed[i]
	}
	manifest := minimalManifestBytes(raw, compressed...)
	entries[blobPath(digestFor(t, manifest))] = manifest
	entries["index.json"] = minimalIndexBytes(manifest)
	return buildLocalOCIArchive(t, entries)
}

// spec: §4.6
// ADR-1500: a release moved the node24 runner-base pin while a build was in
// flight (production, 2026-11-27). The artifact was made FROM the previous
// base, and imaged split it at the new pin, so the deployment failed with
// image_manifest_invalid ("app not built FROM base"). imaged now splits at the
// digest-pinned base the build recorded in its provenance, and uses the
// node's current base only when no such base is recorded.
func TestSharedRuntimeBaseDiffIDsUsesRecordedBase(t *testing.T) {
	ctx := t.Context()
	oldBase := tarBytes(t, map[string]string{"usr/local/bin/node": "node 24.10"})
	newBase := tarBytes(t, map[string]string{"usr/local/bin/node": "node 24.11"})
	appLayer := tarBytes(t, map[string]string{"app/package.json": `{"scripts":{"start":"node worker.js"}}`, "app/worker.js": "tick"})
	archive := layeredArtifactFixture(t, oldBase, appLayer)

	oldRef := "ghcr.io/poyrazk/runner-node24@sha256:" + strings.Repeat("d", 64)
	newRef := "ghcr.io/poyrazk/runner-node24@sha256:" + strings.Repeat("9", 64)
	oldConfig, newConfig := minimalConfigBytes(oldBase), minimalConfigBytes(newBase)
	oldDigest, newDigest := digestFor(t, oldConfig), digestFor(t, newConfig)
	puller := &refManifestPuller{
		minimalManifestPuller: &minimalManifestPuller{layers: map[string][]byte{oldDigest: oldConfig, newDigest: newConfig}},
		byRef: map[string]oci.Manifest{
			oldRef: {Config: oci.Descriptor{Digest: oldDigest}},
			newRef: {Config: oci.Descriptor{Digest: newDigest}},
		},
	}

	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "recorded-base@example.com", "free")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "recorded-base", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	deploymentBuiltFrom := func(t *testing.T, ref string) state.Deployment {
		t.Helper()
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindGitHub})
		if err != nil {
			t.Fatal(err)
		}
		build, err := store.CreateBuild(ctx, dep.ID, state.DeploymentKindGitHub, 0, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CreateBuildProvenance(ctx, state.BuildProvenance{BuildID: build.ID, RuntimeBaseRef: ref}); err != nil {
			t.Fatal(err)
		}
		dep.BuildID = build.ID
		return dep
	}
	// The node's current pin is the new base.
	h := &Handler{store: store, oci: puller, deployBaseRefOverride: newRef}

	tests := []struct {
		name      string
		dep       state.Deployment
		wantSplit bool
	}{
		{name: "build recorded the previous base", dep: deploymentBuiltFrom(t, oldRef), wantSplit: true},
		{name: "no build", dep: state.Deployment{AppID: app.ID}},
		{name: "build without provenance", dep: state.Deployment{AppID: app.ID, BuildID: "00000000-0000-4000-8000-000000000000"}},
		{name: "recorded base is a tag", dep: deploymentBuiltFrom(t, "ghcr.io/poyrazk/runner-node24:latest")},
		{name: "recorded base is another runtime", dep: deploymentBuiltFrom(t, "ghcr.io/poyrazk/runner-node22@sha256:"+strings.Repeat("2", 64))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ids, err := h.sharedRuntimeBaseDiffIDs(ctx, RuntimeNode24, tt.dep)
			if err != nil {
				t.Fatal(err)
			}
			config, layers, cleanup, err := loadLocalOCIArchiveContext(ctx, archive)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			manifest := api.AppManifest{Entrypoint: []string{"/bin/bash", "-c", "npm run start"}, Env: map[string]string{"PATH": "/mise/shims:/usr/bin"}}
			got, err := h.sharedRuntimeAppLayersAt(ctx, RuntimeNode24, ids, archive, config, layers, &manifest, app, sharedRuntimeSplit{})
			if !tt.wantSplit {
				// The node's current base: the artifact was not built FROM it.
				if !errors.Is(err, oci.ErrLayersNotAboveBase) {
					t.Fatalf("split at the current base = %v, want ErrLayersNotAboveBase", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("split at the recorded base: %v", err)
			}
			defer got.cleanup()
			names := layerEntryNames(t, got.layers)
			if got.Reason != "" || slices.ContainsFunc(names, func(n string) bool { return strings.HasPrefix(n, "usr/") }) {
				t.Fatalf("reason=%q entries=%v, want an /app-only layer above the recorded base", got.Reason, names)
			}
		})
	}
}

func TestRecordedRunnerBase(t *testing.T) {
	digest := "@sha256:" + strings.Repeat("a", 64)
	tests := []struct {
		ref, runtime string
		want         bool
	}{
		{ref: "ghcr.io/poyrazk/runner-node24" + digest, runtime: "node24", want: true},
		{ref: "ghcr.io/poyrazk/runner-python313" + digest, runtime: "python313", want: true},
		{ref: "ghcr.io/poyrazk/runner-node24:latest", runtime: "node24"},
		{ref: "ghcr.io/poyrazk/runner-node22" + digest, runtime: "node24"},
		{ref: "ghcr.io/poyrazk/base-minimal" + digest, runtime: "node24"},
		{ref: "ghcr.io/poyrazk/runner-node24" + digest, runtime: ""},
		{ref: "", runtime: "node24"},
	}
	for _, tt := range tests {
		if got := recordedRunnerBase(tt.ref, tt.runtime); got != tt.want {
			t.Errorf("recordedRunnerBase(%q, %q) = %v, want %v", tt.ref, tt.runtime, got, tt.want)
		}
	}
}
