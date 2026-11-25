package sharedruntime

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ADR-20261124160000: the post-build step keeps the standalone tree, with
// static and public files beside the server and the marker last, only for
// an app whose /app (without the tree and the build caches) and guest-init
// are over the cap; otherwise it removes the tree. It leaves a standalone build the
// adapter did not ask for alone, and it never fails.
func TestNextStandaloneStepRuns(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	// The over-cap app (16 MiB in /app) is over 40 MiB only with guest-init
	// counted: 24 MiB of content plus 16 MiB headroom and the 4 MiB reserve.
	const capMB = 40
	const initBytes = 8 << 20
	write := func(t *testing.T, path string, size int) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name       string
		adapterSet bool
		files      map[string]int
		wantTree   bool
		wantMarker bool
	}{
		{name: "over the cap", adapterSet: true, files: map[string]int{"node_modules/next/dist.js": 16 << 20}, wantTree: true, wantMarker: true},
		{name: "fits the cap", adapterSet: true, files: map[string]int{"node_modules/next/dist.js": 100 << 10}},
		{name: "build caches do not count", adapterSet: true, files: map[string]int{".next/cache/webpack.pack": 16 << 20, "node_modules/.cache/x": 16 << 20}},
		{name: "app's own standalone output", files: map[string]int{"node_modules/next/dist.js": 16 << 20}, wantTree: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			app := filepath.Join(dir, "app")
			flag := filepath.Join(dir, "set")
			write(t, filepath.Join(app, ".next/standalone/server.js"), 10)
			write(t, filepath.Join(app, ".next/standalone/node_modules/next/x.js"), 3<<20)
			write(t, filepath.Join(app, ".next/static/chunks/a.js"), 10)
			write(t, filepath.Join(app, "public/hello.txt"), 10)
			for name, size := range tt.files {
				write(t, filepath.Join(app, name), size)
			}
			if tt.adapterSet {
				write(t, flag, 0)
			}
			command := strings.ReplaceAll(nextStandaloneCommand(capMB, initBytes), nextAdapterSetFlag, flag)
			script := strings.TrimSuffix(strings.TrimPrefix(command, "sh -c '"), "'")
			cmd := exec.Command("sh", "-c", script)
			cmd.Dir = app
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("step failed: %v\n%s", err, out)
			}
			exists := func(rel string) bool {
				_, err := os.Stat(filepath.Join(app, rel))
				return err == nil
			}
			if got := exists(".next/standalone/server.js"); got != tt.wantTree {
				t.Fatalf("standalone tree kept = %t, want %t", got, tt.wantTree)
			}
			if got := exists(".next/standalone/" + nextStandaloneMarkerName); got != tt.wantMarker {
				t.Fatalf("marker = %t, want %t", got, tt.wantMarker)
			}
			if tt.wantMarker && (!exists(".next/standalone/.next/static/chunks/a.js") || !exists(".next/standalone/public/hello.txt")) {
				t.Fatal("static or public files not copied beside the server")
			}
		})
	}
}
