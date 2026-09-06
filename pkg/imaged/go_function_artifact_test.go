package imaged

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
)

func goArtifactFixture(t *testing.T, source string, config map[string]any) string {
	t.Helper()
	var layer bytes.Buffer
	tw := tar.NewWriter(&layer)
	payload := []byte("compiled-go-handler")
	if err := tw.WriteHeader(&tar.Header{Name: source, Mode: 0755, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	compressed := gzipBytes(t, layer.Bytes())
	var cfg map[string]any
	if err := json.Unmarshal(minimalConfigBytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["config"] = config
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cd, ld := digestFor(t, raw), digestFor(t, compressed)
	manifest := minimalManifestBytes(cd, []string{ld})
	md := digestFor(t, manifest)
	return buildLocalOCIArchive(t, map[string][]byte{"index.json": minimalIndexBytes(md), blobPath(md): manifest, blobPath(cd): raw, blobPath(ld): compressed})
}

func TestGoFunctionArtifactUsesRailpackCommand(t *testing.T) {
	for _, runtime := range []string{RuntimeGo124, RuntimeGo124Alpine} {
		t.Run(runtime, func(t *testing.T) {
			// Captured from the actual Railpack 0.38 export: its only compiled
			// executable is /app/out, launched through bash -c ./out.
			archive := goArtifactFixture(t, "app/out", map[string]any{"Cmd": []string{"./out"}, "Entrypoint": []string{"/bin/bash", "-c"}, "WorkingDir": "/app"})
			h := &Handler{}
			layers, source, cleanup, err := h.functionBuildArtifact(context.Background(), runtime, archive)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			if source != "/app/server" || len(layers) != 1 {
				t.Fatalf("source=%s layers=%d", source, len(layers))
			}
			target := t.TempDir()
			if err := rootfs.ApplyLayerGz(target, layers[0]); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(target, "app", "server"))
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "compiled-go-handler" {
				t.Fatalf("wrong normalized artifact: %q", data)
			}
		})
	}
}

func TestGoFunctionExecutableCommandContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config oci.Config
		want   string
	}{
		{"legacy", oci.Config{}, "/app/server"},
		{"railpack", oci.Config{Entrypoint: []string{"/bin/bash", "-c"}, Cmd: []string{"./out"}, WorkingDir: "/app"}, "/app/out"},
		{"direct", oci.Config{Entrypoint: []string{"/app/custom"}}, "/app/custom"},
		{"relative", oci.Config{Cmd: []string{"bin/function"}, WorkingDir: "/app"}, "/app/bin/function"},
		{"shell expression", oci.Config{Entrypoint: []string{"/bin/sh", "-c"}, Cmd: []string{"./out; cat /etc/passwd"}}, ""},
		{"shell substitution", oci.Config{Cmd: []string{"$(touch_pwned)"}}, ""},
		{"shell missing command", oci.Config{Entrypoint: []string{"/bin/bash", "-c"}}, ""},
		{"shell extra command", oci.Config{Entrypoint: []string{"/bin/bash", "-c"}, Cmd: []string{"./out", "extra"}}, ""},
		{"outside app", oci.Config{Cmd: []string{"/etc/secret"}}, ""},
		{"traversal", oci.Config{Cmd: []string{"../../host"}, WorkingDir: "/app"}, ""},
		{"relative working directory", oci.Config{Cmd: []string{"./out"}, WorkingDir: "relative"}, ""},
		{"empty program", oci.Config{Cmd: []string{""}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := goFunctionExecutable(tc.config)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("accepted unsupported command as %q", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got=%q err=%v want=%q", got, err, tc.want)
			}
		})
	}
}

func TestGoFunctionExecutableRejectsHostSymlink(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "app")
	if err := os.Mkdir(app, 0755); err != nil {
		t.Fatal(err)
	}
	host := filepath.Join(t.TempDir(), "host-executable")
	if err := os.WriteFile(host, []byte("host-only"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(host, filepath.Join(app, "out")); err != nil {
		t.Fatal(err)
	}
	file, _, err := openGoFunctionExecutable(root, "/app/out")
	if err == nil {
		_ = file.Close()
		t.Fatal("accepted symlink outside image tree")
	}
}

func TestGoFunctionExecutableRejectsNonExecutableAndDirectory(t *testing.T) {
	for _, directory := range []bool{false, true} {
		root := t.TempDir()
		app := filepath.Join(root, "app")
		if err := os.Mkdir(app, 0755); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(app, "out")
		if directory {
			if err := os.Mkdir(out, 0755); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(out, []byte("source"), 0644); err != nil {
			t.Fatal(err)
		}
		file, _, err := openGoFunctionExecutable(root, "/app/out")
		if err == nil {
			_ = file.Close()
			t.Fatalf("accepted invalid artifact directory=%v", directory)
		}
	}
}
