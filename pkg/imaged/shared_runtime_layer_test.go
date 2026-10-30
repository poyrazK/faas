package imaged

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/buildpublisher"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
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
			got, reason := sharedRuntimeStart(api.AppManifest{Entrypoint: tt.entrypoint}, state.App{StartCommand: tt.start}, RuntimeNode24, &appLayerReport{PackageJSON: []byte(pkg)})
			if reason != tt.wantReason || !slices.Equal(got, tt.want) {
				t.Fatalf("start = %q (%q), want %q (%q)", got, reason, tt.want, tt.wantReason)
			}
		})
	}
}

// ADR-1500 amendment 2: a Python app's start command runs as written; a
// package manager's `run` is dropped when the app layer resolves what it
// starts (amendment 3), and any other package manager keeps the full layer.
func TestSharedRuntimeStartPython(t *testing.T) {
	report := &appLayerReport{Python: sharedruntime.PythonLayer{Scripts: map[string]bool{"python": true, "app": true, "uvicorn": true}}}
	tests := []struct {
		name       string
		entrypoint []string
		start      string
		want       []string
		wantReason string
	}{
		{name: "shell start", entrypoint: []string{"/bin/bash", "-c", "python app.py"}, want: []string{"/bin/sh", "-c", "python app.py"}},
		{name: "exec start", entrypoint: []string{"gunicorn", "--bind", "0.0.0.0:8080", "app:app"}, want: []string{"gunicorn", "--bind", "0.0.0.0:8080", "app:app"}},
		{name: "customer start command", entrypoint: []string{"/bin/bash", "-c", "python app.py"}, start: "uvicorn main:app", want: []string{"/bin/sh", "-c", "uvicorn main:app"}},
		{name: "uv run", entrypoint: []string{"/bin/sh", "-c", "uv run app.py"}, want: []string{"/bin/sh", "-c", "python app.py"}},
		{name: "exec poetry", entrypoint: []string{"/mise/shims/poetry", "run", "app"}, want: []string{"/bin/sh", "-c", "app"}},
		{name: "customer poetry script", start: "poetry run serve", wantReason: sharedruntime.ReasonRunTargetUnresolved},
		{name: "uvx", entrypoint: []string{"/bin/sh", "-c", "uvx gunicorn app:app"}, wantReason: "package_manager_start"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := sharedRuntimeStart(api.AppManifest{Entrypoint: tt.entrypoint}, state.App{StartCommand: tt.start}, RuntimePython313, report)
			if reason != tt.wantReason || !slices.Equal(got, tt.want) {
				t.Fatalf("start = %q (%q), want %q (%q)", got, reason, tt.want, tt.wantReason)
			}
		})
	}
}

type tarEntry struct {
	name, body, link string
}

func gzipTarEntries(t *testing.T, entries []tarEntry) io.ReadCloser {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		if e.link != "" {
			hdr = &tar.Header{Name: e.name, Mode: 0o777, Linkname: e.link, Typeflag: tar.TypeSymlink}
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
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
// ADR-1500 amendment 2: Railpack's virtualenv is built on mise's Python; on
// the runner base its interpreter links and pyvenv.cfg move to the base's
// Python, and a link that would still dangle is reported.
func TestFilterAppLayersRelinksVenv(t *testing.T) {
	const mise = "/mise/installs/python/3.13.15/bin/python3.13"
	layers := []io.ReadCloser{gzipTarEntries(t, []tarEntry{
		{name: "mise/installs/python/3.13.15/bin/python3.13", body: "elf"},
		{name: "app/.venv/pyvenv.cfg", body: "home = /mise/installs/python/3.13.15/bin\ninclude-system-site-packages = false\n"},
		{name: "app/.venv/bin/python", link: mise},
		{name: "app/.venv/bin/python3", link: "python"},
		{name: "app/.venv/bin/uv", link: "/mise/installs/uv/0.9.0/uv"},
		{name: "app/.venv/bin/gunicorn", body: "#!/app/.venv/bin/python\n"},
		{name: "app/app.py", body: "print('hi')"},
		{name: "app/tools/fetch.py", body: "#!/usr/bin/env python\n# /// script\n# dependencies = [\"httpx\"]\n# ///\n"},
		{name: "app/.env", body: "DEBUG=1\n"},
	})}
	report := &appLayerReport{PythonInterpreter: "/usr/bin/python3.13"}
	readers, cleanup, err := filterAppLayers(layers, report)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !slices.Equal(report.Unexpected, []string{"app/.venv/bin/uv -> /mise/installs/uv/0.9.0/uv"}) {
		t.Fatalf("unexpected = %q, want only the uv link", report.Unexpected)
	}
	// ADR-1500 amendment 3: what a package manager's `run` is checked against.
	wantPython := sharedruntime.PythonLayer{
		Scripts:       map[string]bool{"python": true, "python3": true, "uv": true, "gunicorn": true},
		InlineScripts: map[string]bool{"fetch.py": true},
		DotEnv:        true,
	}
	if !maps.Equal(report.Python.Scripts, wantPython.Scripts) || !maps.Equal(report.Python.InlineScripts, wantPython.InlineScripts) || !report.Python.DotEnv {
		t.Fatalf("python layer = %+v, want %+v", report.Python, wantPython)
	}
	zr, err := rootfs.OpenCompressedLayer(readers[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	got := map[string]string{}
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		got[hdr.Name] = string(body) + hdr.Linkname
	}
	want := map[string]string{
		"app/.venv/pyvenv.cfg":   "home = /usr/bin\ninclude-system-site-packages = false\n",
		"app/.venv/bin/python":   "/usr/bin/python3.13",
		"app/.venv/bin/python3":  "python",
		"app/.venv/bin/uv":       "/mise/installs/uv/0.9.0/uv",
		"app/.venv/bin/gunicorn": "#!/app/.venv/bin/python\n",
		"app/app.py":             "print('hi')",
		"app/tools/fetch.py":     "#!/usr/bin/env python\n# /// script\n# dependencies = [\"httpx\"]\n# ///\n",
		"app/.env":               "DEBUG=1\n",
	}
	if !maps.Equal(got, want) {
		t.Fatalf("filtered layer = %q\nwant %q", got, want)
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

// spec: §4.6
// ADR-1500 amendment 2: a Python export keeps only /app on the runner base,
// with the virtualenv's scripts first on PATH. Amendment 3: a `uv run` of a
// script in the virtualenv is dropped; a Poetry script it does not hold keeps
// every layer above the base.
func TestSharedRuntimeAppLayersPython(t *testing.T) {
	base := minimalConfigBytes()
	digest := digestFor(t, base)
	h := &Handler{
		oci:                   &minimalManifestPuller{manifest: oci.Manifest{Config: oci.Descriptor{Digest: digest}}, layers: map[string][]byte{digest: base}},
		deployBaseRefOverride: "ghcr.io/test/runner-python313@sha256:" + strings.Repeat("4", 64),
	}
	tests := []struct {
		name       string
		start      string
		want       string
		wantReason string
	}{
		{name: "python", start: "python app.py", want: "python app.py"},
		{name: "uv run", start: "uv run uvicorn main:app", want: "uvicorn main:app"},
		{name: "poetry script", start: "poetry run serve", wantReason: sharedruntime.ReasonRunTargetUnresolved},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive := sharedRuntimeArtifactFixture(t, map[string]string{
				"app/app.py":                                  "print('hi')",
				"app/.venv/pyvenv.cfg":                        "home = /mise/installs/python/3.13.15/bin\n",
				"app/.venv/bin/uvicorn":                       "#!/app/.venv/bin/python\n",
				"mise/installs/python/3.13.15/bin/python3.13": "elf",
			})
			config, layers, cleanup, err := loadLocalOCIArchiveContext(t.Context(), archive)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			entrypoint := []string{"/bin/bash", "-c", tt.start}
			manifest := api.AppManifest{Entrypoint: entrypoint, Env: map[string]string{"PATH": "/app/.venv/bin:/mise/shims:/usr/bin", "PYTHONUNBUFFERED": "1"}}
			got, err := h.sharedRuntimeAppLayers(t.Context(), RuntimePython313, archive, config, layers, &manifest, state.App{Type: state.AppTypeApp})
			if err != nil {
				t.Fatal(err)
			}
			defer got.cleanup()
			if got.Reason != tt.wantReason || len(got.layers) != 1 {
				t.Fatalf("reason=%q layers=%d, want %q with one layer", got.Reason, len(got.layers), tt.wantReason)
			}
			if tt.wantReason != "" {
				if !slices.Equal(manifest.Entrypoint, entrypoint) {
					t.Fatalf("fallback rewrote the start command: %v", manifest.Entrypoint)
				}
				return
			}
			if !slices.Equal(manifest.Entrypoint, []string{"/bin/sh", "-c", tt.want}) || manifest.Env["PATH"] != sharedruntime.PythonRunnerPath ||
				manifest.Env["VIRTUAL_ENV"] != "/app/.venv" || manifest.Env["PYTHONUNBUFFERED"] != "1" {
				t.Fatalf("manifest = %+v, want %q on the Python runner PATH", manifest, tt.want)
			}
		})
	}
}

func TestRuntimeFromBaseRef(t *testing.T) {
	for ref, want := range map[string]string{
		"ghcr.io/poyrazk/runner-node24@sha256:" + strings.Repeat("a", 64): "node24",
		"ghcr.io/poyrazk/runner-node22:latest":                            "node22",
		"ghcr.io/poyrazk/runner-python312:latest":                         "python312",
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
		{name: "container app on python runner", app: state.App{Type: state.AppTypeApp}, ref: "ghcr.io/poyrazk/runner-python313@sha256:" + strings.Repeat("e", 64), want: "python313"},
		{name: "container app on minimal", app: state.App{Type: state.AppTypeApp}, ref: "ghcr.io/poyrazk/base-minimal@sha256:" + strings.Repeat("d", 64)},
		{name: "container app with own runtime", app: state.App{Type: state.AppTypeApp, Runtime: "go124"}, ref: runner},
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
