package rootfs

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestBuildRealMkfs exercises the full app-layer build against a real
// mkfs.ext4 -d (no root/loop needed). It runs wherever e2fsprogs is installed
// (Linux CI, the EX44) and skips elsewhere (developer macOS). This is the M2
// "app layer < 50 MB, real ext4" acceptance at the imaging layer.
func TestBuildRealMkfs(t *testing.T) {
	if _, err := exec.LookPath("mkfs.ext4"); err != nil {
		t.Skip("mkfs.ext4 not installed; skipping real-image build test")
	}

	// A gzipped layer with a tiny hello app.
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	writeReg := func(name, body string) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, body); err != nil {
			t.Fatal(err)
		}
	}
	writeReg("app/server.js", "require('http').createServer((_,r)=>r.end('ok')).listen(8080)")
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	gi := filepath.Join(t.TempDir(), "guest-init")
	if err := os.WriteFile(gi, bytes.Repeat([]byte{0}, 4096), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "layer.ext4")

	b := NewBuilder(wire.ExecRunner{})
	res, err := b.Build(context.Background(), BuildInput{
		Layers:        []io.Reader{&buf},
		Manifest:      api.AppManifest{Entrypoint: []string{"node", "app/server.js"}},
		GuestInitPath: gi,
		Plan:          api.PlanFree,
		OutImage:      out,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	fi, err := os.Stat(out)
	if err != nil {
		t.Fatalf("ext4 image not created: %v", err)
	}
	if fi.Size() == 0 {
		t.Error("ext4 image is empty")
	}
	// M2 acceptance: a hello app layer is small.
	if res.SizeMB > 50 {
		t.Errorf("hello app layer = %d MB, want < 50 MB (M2 acceptance)", res.SizeMB)
	}
	t.Logf("built %s: %d MB (content %d bytes)", out, res.SizeMB, res.ContentBytes)
}
