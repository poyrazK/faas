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
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
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

// recordedBaseFixture is an artifact built FROM an old node24 runner base,
// a registry serving that base and a newer one, and an app to deploy it.
type recordedBaseFixture struct {
	archive, oldRef, newRef string
	puller                  *refManifestPuller
	store                   *state.MemStore
	acct                    state.Account
	app                     state.App
}

func newRecordedBaseFixture(t *testing.T) recordedBaseFixture {
	t.Helper()
	ctx := t.Context()
	oldBase := tarBytes(t, map[string]string{"usr/local/bin/node": "node 24.10"})
	newBase := tarBytes(t, map[string]string{"usr/local/bin/node": "node 24.11"})
	appLayer := tarBytes(t, map[string]string{"app/package.json": `{"scripts":{"start":"node worker.js"}}`, "app/worker.js": "tick"})
	f := recordedBaseFixture{
		archive: layeredArtifactFixture(t, oldBase, appLayer),
		oldRef:  "ghcr.io/poyrazk/runner-node24@sha256:" + strings.Repeat("d", 64),
		newRef:  "ghcr.io/poyrazk/runner-node24@sha256:" + strings.Repeat("9", 64),
		store:   state.NewMemStore(),
	}
	oldConfig, newConfig := minimalConfigBytes(oldBase), minimalConfigBytes(newBase)
	oldDigest, newDigest := digestFor(t, oldConfig), digestFor(t, newConfig)
	f.puller = &refManifestPuller{
		minimalManifestPuller: &minimalManifestPuller{layers: map[string][]byte{oldDigest: oldConfig, newDigest: newConfig}},
		byRef: map[string]oci.Manifest{
			f.oldRef: {Config: oci.Descriptor{Digest: oldDigest}},
			f.newRef: {Config: oci.Descriptor{Digest: newDigest}},
		},
	}
	var err error
	if f.acct, err = f.store.CreateAccount(ctx, "recorded-base@example.com", "free"); err != nil {
		t.Fatal(err)
	}
	if f.app, err = f.store.CreateApp(ctx, state.App{AccountID: f.acct.ID, Slug: "recorded-base", Type: state.AppTypeApp}); err != nil {
		t.Fatal(err)
	}
	// The node's current pin is the new base.
	t.Setenv("FAAS_DEPLOY_BASE_REF_NODE24", f.newRef)
	return f
}

// builtFrom returns a deployment of the fixture app whose build recorded ref.
func (f recordedBaseFixture) builtFrom(t *testing.T, ref string) state.Deployment {
	t.Helper()
	ctx := t.Context()
	dep, err := f.store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Kind: state.DeploymentKindGitHub})
	if err != nil {
		t.Fatal(err)
	}
	build, err := f.store.CreateBuild(ctx, dep.ID, state.DeploymentKindGitHub, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.CreateBuildProvenance(ctx, state.BuildProvenance{BuildID: build.ID, RuntimeBaseRef: ref}); err != nil {
		t.Fatal(err)
	}
	dep.BuildID = build.ID
	dep.RootfsPath = f.archive
	return dep
}

// provenanceErrStore fails every build provenance read.
type provenanceErrStore struct {
	MemStore *state.MemStore
}

func (*provenanceErrStore) BuildProvenanceByBuildID(context.Context, string) (state.BuildProvenance, error) {
	return state.BuildProvenance{}, errors.New("connection reset")
}

func npmStartManifest() api.AppManifest {
	return api.AppManifest{Entrypoint: []string{"/bin/bash", "-c", "npm run start"}, Env: map[string]string{"PATH": "/mise/shims:/usr/bin"}}
}

// spec: §4.6
// ADR-1500: a release moved the node24 runner-base pin while a build was in
// flight (production, 2026-11-27). The artifact was made FROM the previous
// base, and imaged split it at the new pin, so the deployment failed with
// image_manifest_invalid ("app not built FROM base"). imaged now splits at the
// digest-pinned base the build recorded in its provenance. It uses the node's
// current base when no such base is recorded, when the provenance cannot be
// read, and under the test harness's base redirect.
func TestSharedRuntimeBaseDiffIDsUsesRecordedBase(t *testing.T) {
	ctx := t.Context()
	f := newRecordedBaseFixture(t)
	tests := []struct {
		name            string
		dep             state.Deployment
		unreadable      bool
		redirect        string
		wantRecordedCut bool
	}{
		{name: "build recorded the previous base", dep: f.builtFrom(t, f.oldRef), wantRecordedCut: true},
		{name: "no build", dep: state.Deployment{AppID: f.app.ID}},
		{name: "build without provenance", dep: state.Deployment{AppID: f.app.ID, BuildID: "00000000-0000-4000-8000-000000000000"}},
		{name: "recorded base is a tag", dep: f.builtFrom(t, "ghcr.io/poyrazk/runner-node24:latest")},
		{name: "recorded base is another runtime", dep: f.builtFrom(t, "ghcr.io/poyrazk/runner-node22@sha256:"+strings.Repeat("2", 64))},
		{name: "provenance unreadable", dep: f.builtFrom(t, f.oldRef), unreadable: true},
		{name: "test base redirect", dep: f.builtFrom(t, f.oldRef), redirect: f.newRef},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var store state.Store = f.store
			if tt.unreadable {
				store = statetest.NewMem(&provenanceErrStore{MemStore: f.store})
			}
			h := &Handler{store: store, oci: f.puller, deployBaseRefOverride: tt.redirect}
			ids, err := h.sharedRuntimeBaseDiffIDs(ctx, RuntimeNode24, tt.dep)
			if err != nil {
				t.Fatal(err)
			}
			config, layers, cleanup, err := loadLocalOCIArchiveContext(ctx, f.archive)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			manifest := npmStartManifest()
			got, err := h.sharedRuntimeAppLayersAt(ctx, RuntimeNode24, ids, f.archive, config, layers, &manifest, f.app, sharedRuntimeSplit{})
			if !tt.wantRecordedCut {
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

// layerNamesBuilder records the entries and manifest each Build receives.
type layerNamesBuilder struct {
	*fakeBuilder
	t        *testing.T
	names    []string
	manifest api.AppManifest
}

func (b *layerNamesBuilder) Build(ctx context.Context, in rootfs.BuildInput) (rootfs.BuildResult, error) {
	b.names = layerEntryNames(b.t, in.Layers)
	b.manifest = in.Manifest
	return b.fakeBuilder.Build(ctx, in)
}

// spec: §4.6
// ADR-1500: the source-build app layer path itself splits at the recorded
// base, so the production failure cannot come back through the call site.
func TestLocalOCIAppLayerSplitsAtRecordedBase(t *testing.T) {
	f := newRecordedBaseFixture(t)
	builder := &layerNamesBuilder{fakeBuilder: &fakeBuilder{}, t: t}
	h := &Handler{store: f.store, oci: f.puller, builder: builder}
	if _, err := h.localOCIAppLayer(t.Context(), f.app, f.builtFrom(t, f.oldRef), f.acct, nil, RuntimeNode24, npmStartManifest(), rootfs.BuildInput{}); err != nil {
		t.Fatalf("app layer across a runner-base change: %v", err)
	}
	outsideApp := slices.ContainsFunc(builder.names, func(n string) bool { return !strings.HasPrefix(n, "app/") })
	if len(builder.calls) != 1 || outsideApp || !slices.Contains(builder.names, "app/worker.js") {
		t.Fatalf("builds=%d entries=%v, want one /app-only layer", len(builder.calls), builder.names)
	}
	if !slices.Equal(builder.manifest.Entrypoint, []string{"/bin/sh", "-c", "exec node worker.js"}) {
		t.Fatalf("entrypoint = %v, want the start command resolved for the runner base", builder.manifest.Entrypoint)
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
