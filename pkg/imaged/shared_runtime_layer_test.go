package imaged

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/buildpublisher"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/sharedruntime"
	"github.com/onebox-faas/faas/pkg/state"
)

func gzipTarLayer(t *testing.T, files map[string]string) io.ReadCloser {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, body := range files {
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
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return io.NopCloser(&buf)
}

// spec: §4.6
// H10-140: the /app filter reports content outside /app that the runner base
// does not replace, and captures package.json for start resolution.
func TestFilterAppLayersReport(t *testing.T) {
	const pkg = `{"scripts":{"start":"node worker.js"}}`
	layers := []io.ReadCloser{
		gzipTarLayer(t, map[string]string{"mise/installs/node/24.1.0/bin/node": "elf", "root/.cache/npm/x": "c"}),
		gzipTarLayer(t, map[string]string{"app/package.json": pkg, "app/worker.js": "tick", "usr/lib/libvips.so": "so", "etc/.wh.motd": ""}),
	}
	report := &appLayerReport{}
	readers, cleanup, err := filterAppLayers(layers, report)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if len(readers) != 1 {
		t.Fatalf("filtered layers = %d, want only the layer holding /app", len(readers))
	}
	if !slices.Equal(report.Unexpected, []string{"usr/lib/libvips.so"}) {
		t.Fatalf("unexpected = %v, want only the system library", report.Unexpected)
	}
	if string(report.PackageJSON) != pkg {
		t.Fatalf("package.json = %q", report.PackageJSON)
	}
}

func TestSharedRuntimeStart(t *testing.T) {
	const pkg = `{"scripts":{"start":"node worker.js"}}`
	tests := []struct {
		name       string
		entrypoint []string
		start      string
		want       []string
		wantReason string
	}{
		{name: "shell npm start", entrypoint: []string{"/bin/bash", "-c", "npm run start"}, want: []string{"/bin/sh", "-c", "node worker.js"}},
		{name: "exec npm start", entrypoint: []string{"npm", "start"}, want: []string{"/bin/sh", "-c", "node worker.js"}},
		{name: "direct exec", entrypoint: []string{"node", "server.js"}, want: []string{"node", "server.js"}},
		{name: "customer start command", entrypoint: []string{"npm", "start"}, start: "npm run start", want: []string{"/bin/sh", "-c", "node worker.js"}},
		{name: "other package manager", entrypoint: []string{"/bin/sh", "-c", "yarn start"}, wantReason: "package_manager_start"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := sharedRuntimeStart(api.AppManifest{Entrypoint: tt.entrypoint}, state.App{StartCommand: tt.start}, []byte(pkg))
			if reason != tt.wantReason || !slices.Equal(got, tt.want) {
				t.Fatalf("start = %q (%q), want %q (%q)", got, reason, tt.want, tt.wantReason)
			}
		})
	}
}

func TestSharedRuntimeClaim(t *testing.T) {
	binding := func(runtime string) *sourceBuildRootfsBinding {
		return &sourceBuildRootfsBinding{Approval: state.BuildExportPublication{Input: state.BuildExportPublicationInput{Claims: buildpublisher.Claims{Runtime: runtime}}}}
	}
	app := state.App{Type: state.AppTypeApp}
	if got := sharedRuntimeClaim(app, binding("node24")); got != "node24" {
		t.Fatalf("claim = %q, want node24", got)
	}
	if got := sharedRuntimeClaim(app, binding("")); got != "" {
		t.Fatalf("plain claim = %q, want none", got)
	}
	if got := sharedRuntimeClaim(app, nil); got != "" {
		t.Fatalf("unapproved build = %q, want none", got)
	}
	fn := state.App{Type: state.AppTypeFunction, Runtime: "node22"}
	if got := sharedRuntimeClaim(fn, binding("node22")); got != "" {
		t.Fatalf("function = %q, want none", got)
	}
}

// sharedRuntimeArtifactFixture is goArtifactFixture with several files in
// its one layer.
func sharedRuntimeArtifactFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	var layer bytes.Buffer
	tw := tar.NewWriter(&layer)
	for name, body := range files {
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
	compressed := gzipBytes(t, layer.Bytes())
	raw := minimalConfigBytes(layer.Bytes())
	cd, ld := digestFor(t, raw), digestFor(t, compressed)
	manifest := minimalManifestBytes(raw, compressed)
	md := digestFor(t, manifest)
	return buildLocalOCIArchive(t, map[string][]byte{"index.json": minimalIndexBytes(manifest), blobPath(md): manifest, blobPath(cd): raw, blobPath(ld): compressed})
}

// spec: §4.6
// H10-140: an export built FROM the runner base keeps only /app when the
// rest is toolchain, and keeps every layer above the base otherwise.
func TestSharedRuntimeAppLayers(t *testing.T) {
	const pkg = `{"scripts":{"start":"node worker.js"}}`
	base := minimalConfigBytes()
	digest := digestFor(t, base)
	h := &Handler{
		oci:                   &minimalManifestPuller{manifest: oci.Manifest{Config: oci.Descriptor{Digest: digest}}, layers: map[string][]byte{digest: base}},
		deployBaseRefOverride: "ghcr.io/test/runner-node24@sha256:" + strings.Repeat("4", 64),
	}
	tests := []struct {
		name       string
		files      map[string]string
		wantReason string
	}{
		{name: "app and toolchain", files: map[string]string{"app/package.json": pkg, "app/worker.js": "tick", "mise/installs/node/24.1.0/bin/node": "elf"}},
		{name: "system library", files: map[string]string{"app/package.json": pkg, "app/worker.js": "tick", "usr/lib/libvips.so": "so"}, wantReason: "content_outside_app"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive := sharedRuntimeArtifactFixture(t, tt.files)
			config, layers, cleanup, err := loadLocalOCIArchiveContext(t.Context(), archive)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			manifest := api.AppManifest{Entrypoint: []string{"/bin/bash", "-c", "npm run start"}, Env: map[string]string{"PATH": "/mise/shims:/usr/bin", "NODE_ENV": "production"}}
			got, err := h.sharedRuntimeAppLayers(t.Context(), RuntimeNode24, archive, config, layers, &manifest, state.App{Type: state.AppTypeApp})
			if err != nil {
				t.Fatal(err)
			}
			defer got.cleanup()
			if got.Reason != tt.wantReason || len(got.layers) != 1 {
				t.Fatalf("reason=%q layers=%d, want %q with one layer", got.Reason, len(got.layers), tt.wantReason)
			}
			if tt.wantReason == "" {
				if !slices.Equal(manifest.Entrypoint, []string{"/bin/sh", "-c", "node worker.js"}) || manifest.Env["PATH"] != sharedruntime.RunnerPath || manifest.Env["NODE_ENV"] != "production" {
					t.Fatalf("manifest = %+v, want npm resolved on the runner PATH", manifest)
				}
				return
			}
			if !slices.Equal(manifest.Entrypoint, []string{"/bin/bash", "-c", "npm run start"}) {
				t.Fatalf("fallback rewrote the start command: %v", manifest.Entrypoint)
			}
		})
	}
}

func TestRuntimeFromBaseRef(t *testing.T) {
	for ref, want := range map[string]string{
		"ghcr.io/poyrazk/runner-node24@sha256:" + strings.Repeat("a", 64): "node24",
		"ghcr.io/poyrazk/runner-node22:latest":                            "node22",
		"ghcr.io/poyrazk/base-minimal@sha256:" + strings.Repeat("b", 64):  "",
		"not a reference": "",
	} {
		if got := runtimeFromBaseRef(ref); got != want {
			t.Errorf("%s: %q, want %q", ref, got, want)
		}
	}
}

// spec: §4.6
// H10-140: without a build publisher, the runner base an app was built FROM
// comes from its build provenance, and only for an eligible container app.
func TestSharedRuntimeFromProvenance(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "shared-runtime@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	newDeployment := func(t *testing.T, app state.App, ref string) state.Deployment {
		t.Helper()
		app.AccountID = acct.ID
		app.Slug = "shared-" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))
		created, err := store.CreateApp(ctx, app)
		if err != nil {
			t.Fatal(err)
		}
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: created.ID, Kind: state.DeploymentKindTarball})
		if err != nil {
			t.Fatal(err)
		}
		build, err := store.CreateBuild(ctx, dep.ID, state.DeploymentKindTarball, 0, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CreateBuildProvenance(ctx, state.BuildProvenance{BuildID: build.ID, RuntimeBaseRef: ref}); err != nil {
			t.Fatal(err)
		}
		dep.BuildID = build.ID
		return dep
	}
	h := &Handler{store: store}
	runner := "ghcr.io/poyrazk/runner-node24@sha256:" + strings.Repeat("c", 64)
	tests := []struct {
		name string
		app  state.App
		ref  string
		want string
	}{
		{name: "container app on runner", app: state.App{Type: state.AppTypeApp}, ref: runner, want: "node24"},
		{name: "container app on minimal", app: state.App{Type: state.AppTypeApp}, ref: "ghcr.io/poyrazk/base-minimal@sha256:" + strings.Repeat("d", 64)},
		{name: "container app with own runtime", app: state.App{Type: state.AppTypeApp, Runtime: "go124"}, ref: runner},
		// Job boots use the minimal base; a runner-image minimal base must
		// not make the job's layer a runner layer.
		{name: "job app on runner", app: state.App{Type: state.AppTypeApp, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeJob}}, ref: runner},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dep := newDeployment(t, tt.app, tt.ref)
			app, err := store.AppByID(ctx, dep.AppID)
			if err != nil {
				t.Fatal(err)
			}
			got, err := h.sharedRuntimeFromProvenance(ctx, app, dep)
			if err != nil || got != tt.want {
				t.Fatalf("runtime = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
	if got, err := h.sharedRuntimeFromProvenance(ctx, state.App{Type: state.AppTypeApp}, state.Deployment{BuildID: "00000000-0000-4000-8000-000000000000"}); err != nil || got != "" {
		t.Fatalf("missing provenance = %q, %v; want the app's own base", got, err)
	}
}
