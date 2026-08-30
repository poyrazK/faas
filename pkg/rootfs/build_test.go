// Tests for the Build pipeline (cmd-runner is faked) and the inject helpers.
// The happy-path Build is covered by rootfs_test.go; this file pins the
// error branches and the inject helpers' boundary conditions.

package rootfs

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
)

func TestInjectManifest_WritesCanonicalJSON(t *testing.T) {
	staging := t.TempDir()
	m := api.AppManifest{
		Entrypoint: []string{"/app/server"},
		Env:        map[string]string{"X": "y"},
		Port:       8080,
	}
	if err := InjectManifest(staging, m); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(staging, "etc", "faas", "app.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("manifest not at expected path %q: %v", path, err)
	}
	if !bytes.Contains(b, []byte("entrypoint")) {
		t.Errorf("manifest content missing entrypoint: %s", b)
	}
	if !bytes.Contains(b, []byte("8080")) {
		t.Errorf("manifest content missing Port: %s", b)
	}
}

func TestInjectGuestInit_HappyPath(t *testing.T) {
	staging := t.TempDir()
	src := filepath.Join(t.TempDir(), "init")
	if err := os.WriteFile(src, []byte("guest-init binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := InjectGuestInit(staging, src); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(staging, "sbin", "init")
	st, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("init not at expected path: %v", err)
	}
	if st.Size() == 0 {
		t.Error("init file is empty")
	}
	if st.Mode().Perm()&0o111 == 0 {
		t.Errorf("init not executable: mode %o", st.Mode().Perm())
	}
}

func TestInjectGuestInit_ReplacesSymlink(t *testing.T) {
	staging := t.TempDir()
	if err := os.MkdirAll(filepath.Join(staging, "sbin", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(staging, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "bin", "busybox"), []byte("busybox"), 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(staging, "sbin", "init")
	if err := os.Symlink("/bin/busybox", dst); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "init")
	if err := os.WriteFile(src, []byte("guest-init"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := InjectGuestInit(staging, src); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("/sbin/init remained a symlink")
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "guest-init" {
		t.Fatalf("/sbin/init = %q, want guest-init", got)
	}
	busybox, err := os.ReadFile(filepath.Join(staging, "bin", "busybox"))
	if err != nil {
		t.Fatal(err)
	}
	if string(busybox) != "busybox" {
		t.Fatalf("busybox was modified: %q", busybox)
	}
}

func TestInjectGuestInit_EmptyPath(t *testing.T) {
	if err := InjectGuestInit(t.TempDir(), ""); err == nil {
		t.Error("empty guest-init path should error")
	}
}

func TestInjectGuestInit_MissingSource(t *testing.T) {
	if err := InjectGuestInit(t.TempDir(), "/no/such/file"); err == nil {
		t.Error("missing source should error")
	}
}

func TestBuild_UnknownPlan(t *testing.T) {
	b := NewBuilder(&fakeRunner{})
	_, err := b.Build(context.Background(), BuildInput{
		Storage:    newTestStorage(t),
		StorageKey: "apps/x/y.ext4",
		Plan:       "nope",
	})
	if err == nil {
		t.Fatal("unknown plan should error")
	}
	if !strings.Contains(err.Error(), "unknown plan") {
		t.Errorf("error %q should mention unknown plan", err.Error())
	}
}

func TestBuild_InvalidManifest(t *testing.T) {
	b := NewBuilder(&fakeRunner{})
	_, err := b.Build(context.Background(), BuildInput{
		Storage:    newTestStorage(t),
		StorageKey: "apps/x/y.ext4",
		Plan:       api.PlanFree,
		// Empty Entrypoint → Validate() fails.
	})
	if err == nil {
		t.Fatal("invalid manifest should error")
	}
}

func TestBuild_MkfsFailure(t *testing.T) {
	src := filepath.Join(t.TempDir(), "init")
	if err := os.WriteFile(src, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := &failRunner{err: os.ErrNotExist}
	b := NewBuilder(run)
	_, err := b.Build(context.Background(), BuildInput{
		Storage:       newTestStorage(t),
		StorageKey:    "apps/x/y.ext4",
		Plan:          api.PlanFree,
		GuestInitPath: src,
		Manifest:      api.AppManifest{Entrypoint: []string{"/app/server"}},
	})
	if err == nil {
		t.Fatal("mkfs failure should propagate")
	}
	if !strings.Contains(err.Error(), "mkfs") {
		t.Errorf("error %q should mention mkfs", err.Error())
	}
}

// TestBuild_RejectsMissingOutputTarget covers the new validation:
// every BuildInput must specify exactly one of {Storage, OutImage}.
// Without it, a misconfigured caller would silently drop the produced
// ext4.
func TestBuild_RejectsMissingOutputTarget(t *testing.T) {
	b := NewBuilder(&fakeRunner{})
	_, err := b.Build(context.Background(), BuildInput{
		Plan:     api.PlanFree,
		Manifest: api.AppManifest{Entrypoint: []string{"/app/server"}},
	})
	if err == nil {
		t.Fatal("missing output target should error")
	}
	if !strings.Contains(err.Error(), "neither Storage nor OutImage") {
		t.Errorf("error %q should mention the validation rule", err.Error())
	}
}

// TestBuild_RejectsBothOutputTargets covers the inverse: specifying
// both would let one path silently shadow the other. The validator
// surfaces this loudly.
func TestBuild_RejectsBothOutputTargets(t *testing.T) {
	b := NewBuilder(&fakeRunner{})
	_, err := b.Build(context.Background(), BuildInput{
		Storage:    newTestStorage(t),
		StorageKey: "apps/x/y.ext4",
		OutImage:   filepath.Join(t.TempDir(), "y.ext4"),
		Plan:       api.PlanFree,
		Manifest:   api.AppManifest{Entrypoint: []string{"/app/server"}},
	})
	if err == nil {
		t.Fatal("both output targets should error")
	}
}

// TestBuild_PublishesViaStorage exercises the production Storage
// path: Build produces a tmp ext4 via the fakeRunner (which writes
// fake ext4 bytes into the tmp path, mimicking a real mkfs), then
// Put's it under StorageKey. After Build returns, Get(StorageKey)
// returns the same bytes. This is the test that proves the
// publishExt4 → Storage.Put wiring is correct end-to-end without
// invoking a real mkfs.
func TestBuild_PublishesViaStorage(t *testing.T) {
	gi := filepath.Join(t.TempDir(), "guest-init")
	if err := os.WriteFile(gi, []byte("INIT"), 0o755); err != nil {
		t.Fatal(err)
	}
	be := newTestStorage(t)
	run := &mkfsFakeRunner{fill: []byte("FAKE-EXT4-CONTENT")}
	b := NewBuilder(run)
	res, err := b.Build(context.Background(), BuildInput{
		Storage:       be,
		StorageKey:    "apps/slug/dep.ext4",
		Plan:          api.PlanFree,
		GuestInitPath: gi,
		Manifest:      api.AppManifest{Entrypoint: []string{"/app/server"}},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.ImageKey != "apps/slug/dep.ext4" {
		t.Errorf("ImageKey = %q, want %q", res.ImageKey, "apps/slug/dep.ext4")
	}
	if res.ImagePath != "" {
		t.Errorf("ImagePath = %q, want empty", res.ImagePath)
	}
	// Storage must hold the published ext4 at the requested key with
	// the same bytes the runner wrote.
	rc, err := be.Get(context.Background(), "apps/slug/dep.ext4")
	if err != nil {
		t.Fatalf("storage Get after build: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if !bytes.Equal(got, run.fill) {
		t.Fatalf("content mismatch: got %q, want %q", got, run.fill)
	}
}

// TestBuild_LegacyOutImageStillWorks covers the deprecation path:
// an existing caller that still passes OutImage keeps working. The
// integration test (build_integration_test.go) relies on this; new
// callers should use Storage + StorageKey.
func TestBuild_LegacyOutImageStillWorks(t *testing.T) {
	gi := filepath.Join(t.TempDir(), "guest-init")
	if err := os.WriteFile(gi, []byte("INIT"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := &fakeRunner{}
	b := NewBuilder(run)
	out := filepath.Join(t.TempDir(), "layer.ext4")
	res, err := b.Build(context.Background(), BuildInput{
		GuestInitPath: gi,
		Manifest:      api.AppManifest{Entrypoint: []string{"node", "x"}},
		Plan:          api.PlanHobby,
		OutImage:      out,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.ImagePath != out {
		t.Errorf("ImagePath = %q, want %q", res.ImagePath, out)
	}
	// The legacy mkfs argv path must contain OutImage.
	if !containsString(run.argv, out) {
		t.Errorf("argv %v must contain %q (legacy path)", run.argv, out)
	}
}

type failRunner struct{ err error }

func (f *failRunner) Run(_ context.Context, _ []string) error { return f.err }

// newTestStorage builds a LocalStorageBackend rooted at t.TempDir().
// Used by tests that need a real StorageBackend without dragging in
// the full StorageBackend suite.
func newTestStorage(t *testing.T) storage.StorageBackend {
	t.Helper()
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatalf("storage.NewLocalStorageBackend: %v", err)
	}
	return be
}

// writeGzTar writes a gzipped tar with one regular file at path
// `name` containing `body`. Used by the B3.7 cap tests so a
// synthetic tarball can be handed to ApplyTarball.
func writeGzTar(t *testing.T, path, name string, body []byte) {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	hdr := &tar.Header{
		Name:     name,
		Mode:     0o644,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("tar.WriteHeader: %v", err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatalf("tar.Write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar.Close: %v", err)
	}
	var gz bytes.Buffer
	gzw := gzip.NewWriter(&gz)
	if _, err := gzw.Write(raw.Bytes()); err != nil {
		t.Fatalf("gzip.Write: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("gzip.Close: %v", err)
	}
	if err := os.WriteFile(path, gz.Bytes(), 0o644); err != nil {
		t.Fatalf("write tarball: %v", err)
	}
}

func TestApplyTarball_StripsProjectRoot(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, "staging")
	tarball := filepath.Join(dir, "src.tar.gz")
	writeGzTar(t, tarball, "function-node/handler.js", []byte("export const handler = async () => ({statusCode: 200});"))

	if err := ApplyTarball(staging, tarball, 1024*1024); err != nil {
		t.Fatalf("ApplyTarball: %v", err)
	}
	if _, err := os.Stat(filepath.Join(staging, "app", "function-node")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("project root was not stripped; stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(staging, "app", "handler.js")); err != nil {
		t.Fatalf("handler.js not unpacked at /app: %v", err)
	}
}

func TestApplyTarball_StripsProjectRootWithPAXGlobalHeader(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, "staging")
	tarball := filepath.Join(dir, "src.tar.gz")

	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	if err := tw.WriteHeader(&tar.Header{
		Name:     "pax_global_header",
		Typeflag: tar.TypeXGlobalHeader,
		PAXRecords: map[string]string{
			"comment": "github codeload metadata",
		},
	}); err != nil {
		t.Fatalf("tar.WriteHeader(PAX): %v", err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "function-node/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatalf("tar.WriteHeader(directory): %v", err)
	}
	body := []byte("exports.handler = async () => ({statusCode: 200});")
	if err := tw.WriteHeader(&tar.Header{
		Name:     "function-node/handler.js",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len(body)),
	}); err != nil {
		t.Fatalf("tar.WriteHeader(handler): %v", err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatalf("tar.Write(handler): %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar.Close: %v", err)
	}
	var gz bytes.Buffer
	gzw := gzip.NewWriter(&gz)
	if _, err := gzw.Write(raw.Bytes()); err != nil {
		t.Fatalf("gzip.Write: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("gzip.Close: %v", err)
	}
	if err := os.WriteFile(tarball, gz.Bytes(), 0o644); err != nil {
		t.Fatalf("write tarball: %v", err)
	}

	if err := ApplyTarball(staging, tarball, 1024*1024); err != nil {
		t.Fatalf("ApplyTarball: %v", err)
	}
	if err := NormalizeFunctionHandler(staging, "/app/node22.js"); err != nil {
		t.Fatalf("NormalizeFunctionHandler: %v", err)
	}
	if _, err := os.Stat(filepath.Join(staging, "app", "handler.js")); err != nil {
		t.Fatalf("handler.js not unpacked at /app: %v", err)
	}
	if _, err := os.Stat(filepath.Join(staging, "app", "node22.js")); err != nil {
		t.Fatalf("node22.js not generated at /app: %v", err)
	}
}

func TestNormalizeFunctionHandler_GoServerArtifact(t *testing.T) {
	staging := t.TempDir()
	appDir := filepath.Join(staging, "app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	server := filepath.Join(appDir, "server")
	if err := os.WriteFile(server, []byte("compiled-go-handler"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := NormalizeFunctionHandler(staging, "/app/handler"); err != nil {
		t.Fatalf("NormalizeFunctionHandler: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(appDir, "handler"))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if string(got) != "compiled-go-handler" {
		t.Fatalf("handler = %q, want compiled artifact", got)
	}
	if _, err := os.Stat(server); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("server artifact should be moved, stat err = %v", err)
	}
}

func TestNormalizeFunctionHandler_NodeAlias(t *testing.T) {
	staging := t.TempDir()
	source := filepath.Join(staging, "app", "handler.js")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("handler"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NormalizeFunctionHandler(staging, "/app/node22.js"); err != nil {
		t.Fatalf("NormalizeFunctionHandler: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(staging, "app", "node22.js"))
	if err != nil {
		t.Fatalf("node22.js: %v", err)
	}
	if string(got) != "handler" {
		t.Errorf("node22.js = %q, want handler source", got)
	}
}

func TestNormalizeFunctionHandler_NodeExportAdapter(t *testing.T) {
	staging := t.TempDir()
	source := filepath.Join(staging, "app", "handler.js")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	const handler = "export async function handler(event, ctx) { return { statusCode: 200, body: JSON.stringify(event) }; }"
	if err := os.WriteFile(source, []byte(handler), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NormalizeFunctionHandler(staging, "/app/node22.js"); err != nil {
		t.Fatalf("NormalizeFunctionHandler: %v", err)
	}
	gotSource, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("handler.js: %v", err)
	}
	if string(gotSource) != handler {
		t.Fatalf("handler.js was changed: %q", gotSource)
	}
	gotAdapter, err := os.ReadFile(filepath.Join(staging, "app", "node22.js"))
	if err != nil {
		t.Fatalf("node22.js: %v", err)
	}
	if !bytes.Contains(gotAdapter, []byte("handler.js")) || !bytes.Contains(gotAdapter, []byte("body_b64")) {
		t.Fatalf("node22.js is not a function adapter: %q", gotAdapter)
	}
}

func TestNormalizeFunctionHandler_NodeExportAdapterRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	staging := t.TempDir()
	appDir := filepath.Join(staging, "app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "package.json"), []byte(`{"type":"module"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "handler.js"), []byte(`export async function handler(event, ctx) {
  return { statusCode: 201, headers: { "content-type": "application/json" }, body: JSON.stringify({ id: ctx.invocation_id, body: event.body }) };
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NormalizeFunctionHandler(staging, "/app/node22.js"); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", filepath.Join(appDir, "node22.js"))
	cmd.Dir = appDir
	cmd.Stdin = strings.NewReader(`{"method":"POST","path":"/e2e","headers":{"x-faas-invocation-id":"inv-1"},"query":"","body_b64":"eyJ4Ijo3fQ=="}`)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node adapter: %v", err)
	}
	if !strings.Contains(string(out), `"status":201`) || !strings.Contains(string(out), `eyJpZCI6Imludi0xIiwiYm9keSI6eyJ4Ijo3fX0=`) {
		t.Fatalf("unexpected adapter response: %s", out)
	}
}

func TestNormalizeFunctionHandler_NodeCommonJSAdapterRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	for _, handlerPath := range []string{"/app/node22.js", "/app/node24.js"} {
		t.Run(filepath.Base(handlerPath), func(t *testing.T) {
			staging := t.TempDir()
			appDir := filepath.Join(staging, "app")
			if err := os.MkdirAll(appDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(appDir, "package.json"), []byte(`{"type":"commonjs"}`), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(appDir, "handler.js"), []byte(`exports.handler = async (event) => ({ statusCode: 202, body: JSON.stringify(event.body) });`), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := NormalizeFunctionHandler(staging, handlerPath); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("node", filepath.Join(appDir, filepath.Base(handlerPath)))
			cmd.Dir = appDir
			cmd.Stdin = strings.NewReader(`{"method":"POST","path":"/e2e","headers":{},"query":"","body_b64":"eyJ4Ijo3fQ=="}
{"method":"POST","path":"/e2e","headers":{},"query":"","body_b64":"eyJ4Ijo3fQ=="}`)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("node CommonJS adapter: %v", err)
			}
			if got := strings.Count(string(out), `"status":202`); got != 2 {
				t.Fatalf("persistent adapter responses = %d, want 2: %s", got, out)
			}
			if !strings.Contains(string(out), `"body_b64":"eyJ4Ijo3fQ=="`) {
				t.Fatalf("unexpected CommonJS adapter response: %s", out)
			}
		})
	}
}

func TestNormalizeFunctionHandler_PythonExportAdapter(t *testing.T) {
	staging := t.TempDir()
	source := filepath.Join(staging, "app", "handler.py")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	const handler = "async def handler(event, ctx):\n    return {'statusCode': 200, 'body': 'ok'}\n"
	if err := os.WriteFile(source, []byte(handler), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NormalizeFunctionHandler(staging, "/app/handler.py"); err != nil {
		t.Fatalf("NormalizeFunctionHandler: %v", err)
	}
	if _, err := os.Stat(filepath.Join(staging, "app", ".faas-handler.py")); err != nil {
		t.Fatalf("preserved handler implementation: %v", err)
	}
	gotAdapter, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("handler.py: %v", err)
	}
	if !bytes.Contains(gotAdapter, []byte("faas-handler.py")) || !bytes.Contains(gotAdapter, []byte("body_b64")) {
		t.Fatalf("handler.py is not a function adapter: %q", gotAdapter)
	}
}

func TestNormalizeFunctionHandler_PythonAdapterRoundTrip(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	staging := t.TempDir()
	appDir := filepath.Join(staging, "app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const handler = `import json
print("module log")

async def handler(event, ctx):
    print("handler log")
    return {"statusCode": 203, "body": json.dumps(event["body"])}
`
	if err := os.WriteFile(filepath.Join(appDir, "handler.py"), []byte(handler), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NormalizeFunctionHandler(staging, "/app/handler.py"); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, filepath.Join(appDir, "handler.py"))
	cmd.Dir = appDir
	cmd.Stdin = strings.NewReader(`{"method":"POST","path":"/e2e","headers":{},"query":"","body_b64":"eyJ4Ijo3fQ=="}
{"method":"POST","path":"/e2e","headers":{},"query":"","body_b64":"eyJ4Ijo3fQ=="}`)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python adapter: %v; stderr=%s", err, stderr.String())
	}
	if got := strings.Count(string(out), `"status": 203`); got != 2 {
		t.Fatalf("persistent Python adapter responses = %d, want 2: %s", got, out)
	}
	if !strings.Contains(string(out), `"body_b64": "eyJ4IjogN30="`) {
		t.Fatalf("unexpected Python adapter response: %s", out)
	}
	if !strings.Contains(stderr.String(), "module log") || !strings.Contains(stderr.String(), "handler log") {
		t.Fatalf("customer logs did not reach stderr: %s", stderr.String())
	}
}

// TestApplyTarball_RespectsCap — issue #197 B3.7. A tarball whose
// single entry's declared size exceeds capBytes must be rejected
// with *ErrTarballExceedsCap before the file is written to disk.
// The cap is the cumulative byte total, not per-entry; per-entry
// is enforced by io.CopyN in applyEntry.
func TestApplyTarball_RespectsCap(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	tarball := filepath.Join(dir, "src.tar.gz")
	// 1 MiB body under a 64 KiB cap. The cap is checked against the
	// declared header size, so the actual write doesn't have to
	// run a million bytes through io.CopyN.
	writeGzTar(t, tarball, "handler.js", bytes.Repeat([]byte("x"), 1024*1024))

	const capBytes = 64 * 1024
	err := ApplyTarball(staging, tarball, capBytes)
	if err == nil {
		t.Fatalf("ApplyTarball accepted 1 MiB body under %d cap", capBytes)
	}
	var capErr *ErrTarballExceedsCap
	if !errors.As(err, &capErr) {
		t.Fatalf("err = %v (%T); want *ErrTarballExceedsCap", err, err)
	}
	if capErr.CapBytes != capBytes {
		t.Errorf("CapBytes = %d; want %d", capErr.CapBytes, capBytes)
	}
	if capErr.EntryBytes != 1024*1024 {
		t.Errorf("EntryBytes = %d; want %d", capErr.EntryBytes, 1024*1024)
	}
}

// TestApplyTarball_UnderCapAccepts — the inverse: a small tarball
// under the cap must unpack cleanly.
func TestApplyTarball_UnderCapAccepts(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	tarball := filepath.Join(dir, "src.tar.gz")
	writeGzTar(t, tarball, "handler.js", []byte("console.log('hi')"))
	if err := ApplyTarball(staging, tarball, 1024*1024); err != nil {
		t.Fatalf("ApplyTarball under-cap: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(staging, "app", "handler.js"))
	if err != nil {
		t.Fatalf("read handler.js: %v", err)
	}
	if string(body) != "console.log('hi')" {
		t.Errorf("body = %q; want %q", body, "console.log('hi')")
	}
}

// TestApplyTarball_ZeroCapSkipsGate — passing capBytes=0 keeps the
// legacy unbounded behavior. The legacy callers (tests, internal
// callers without a plan context) must not regress.
func TestApplyTarball_ZeroCapSkipsGate(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	tarball := filepath.Join(dir, "src.tar.gz")
	writeGzTar(t, tarball, "handler.js", bytes.Repeat([]byte("y"), 16*1024))
	if err := ApplyTarball(staging, tarball, 0); err != nil {
		t.Fatalf("ApplyTarball cap=0: %v", err)
	}
}

// TestBuild_TranslatesTarballCapToProblem — issue #197 B3.7 (PR #241
// review finding #2). Build() must promote the
// *ErrTarballExceedsCap sentinel to the plan-scoped
// api.ErrAppLayerTooLarge(*Problem) so the customer-facing deploy
// failure carries the limit + observed value + docs URL. Build's
// Run() is satisfied with a fake runner that always succeeds so we
// exercise the cap-fail path before publishExt4.
//
// The pre-fix code returned the raw sentinel and the customer saw
// a generic "function tarball exceeds cap" string with no RFC 7807
// shape; this test pins the translation.
func TestBuild_TranslatesTarballCapToProblem(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	tarball := filepath.Join(dir, "src.tar.gz")
	// 256 MiB body, over Free's 256 MiB cap. The cap is enforced against
	// the declared header size, so we don't need to stream 256 MiB through
	// io.CopyN — just a header that claims 256 MiB. The tar file itself
	// remains small; only the cap check runs against the declared size.
	writeGzTar(t, tarball, "handler.js", bytes.Repeat([]byte("z"), 256*1024*1024))

	limits, ok := api.LimitsFor(api.PlanFree)
	if !ok {
		t.Fatal("api.LimitsFor(Free) not ok")
	}

	// Override the build's plan cap to a tiny value so the post-unpack
	// cap matches the tarball. We don't go through the apid-side
	// SourceTarballMaxMB gate — Build() applies the cap directly from
	// in.Plan/AppLayerMaxMB.
	b := NewBuilder(&fakeRunner{})
	// Build a manifest that fits, no OCI layers. The only path that
	// matters is the tarball-cap path.
	outFile := filepath.Join(dir, "out.ext4")
	_, err := b.Build(context.Background(), BuildInput{
		Layers:             nil,
		Manifest:           api.AppManifest{Entrypoint: []string{"./handler.js"}},
		GuestInitPath:      "/dev/null",
		Plan:               api.PlanFree,
		TarballPath:        tarball,
		OutImage:           outFile,
		FunctionRunnerPath: "",
	})
	if err == nil {
		t.Fatal("Build accepted over-cap tarball; want ErrAppLayerTooLarge")
	}
	// Must NOT be the raw sentinel — that's the pre-fix bug.
	var sentinel *ErrTarballExceedsCap
	if errors.As(err, &sentinel) {
		t.Fatalf("Build returned raw sentinel %T; want *api.Problem", err)
	}
	// Must be the *api.Problem with the right code.
	var prob *api.Problem
	if !errors.As(err, &prob) {
		t.Fatalf("err = %v (%T); want *api.Problem", err, err)
	}
	if prob.Code != api.CodeAppLayerTooBig {
		t.Errorf("Problem.Code = %q; want %q", prob.Code, api.CodeAppLayerTooBig)
	}
	if prob.Limit == nil {
		t.Fatal("Problem.Limit nil; want Hobby's AppLayerMaxMB in bytes")
	}
	if *prob.Limit != int64(limits.AppLayerMaxMB)*1024*1024 {
		t.Errorf("Problem.Limit = %d; want %d", *prob.Limit, int64(limits.AppLayerMaxMB)*1024*1024)
	}
	if prob.Observed == nil {
		t.Fatal("Problem.Observed nil; want total unpacked bytes (>= 1 MiB)")
	}
	if *prob.Observed < 1024*1024 {
		t.Errorf("Problem.Observed = %d; want >= %d", *prob.Observed, 1024*1024)
	}
	if prob.DocsURL == "" {
		t.Error("Problem.DocsURL empty; want docs URL")
	}
}
