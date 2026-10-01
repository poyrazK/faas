//go:build linux

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
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Privileged metadata acceptance: real extraction and mkfs, not a fake runner
// or a parser-only assertion. No KVM or host lifecycle mutation is involved.
func TestFullRootfsZeroGIDOwnershipRealExt4(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root required to preserve OCI numeric ownership")
	}
	for _, tool := range []string{"mkfs.ext4", "debugfs"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " required")
		}
	}
	var layer bytes.Buffer
	zip := gzip.NewWriter(&layer)
	tarWriter := tar.NewWriter(zip)
	for _, header := range []*tar.Header{
		{Name: "var/", Typeflag: tar.TypeDir, Mode: 0755, Uid: 0, Gid: 0},
		{Name: "var/cache/", Typeflag: tar.TypeDir, Mode: 0755, Uid: 0, Gid: 0},
		{Name: "var/cache/nginx/", Typeflag: tar.TypeDir, Mode: 0755, Uid: 101, Gid: 0},
		{Name: "var/cache/nginx/data", Typeflag: tar.TypeReg, Mode: 0644, Size: 1, Uid: 101, Gid: 0},
		{Name: "var/cache/nginx/link", Typeflag: tar.TypeSymlink, Mode: 0777, Linkname: "data", Uid: 101, Gid: 0},
	} {
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := io.WriteString(tarWriter, "x"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zip.Close(); err != nil {
		t.Fatal(err)
	}
	guestInit := filepath.Join(t.TempDir(), "init")
	if err := os.WriteFile(guestInit, []byte("INIT"), 0755); err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(t.TempDir(), "root.ext4")
	_, err := NewBuilder(wire.ExecRunner{}).BuildFullRootfs(context.Background(), BuildFullRootfsInput{
		Layers: []io.Reader{&layer}, Manifest: api.AppManifest{Entrypoint: []string{"/bin/app"}, User: "101"},
		GuestInitPath: guestInit, Plan: api.PlanHobby, OutImage: image,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/var/cache/nginx", "/var/cache/nginx/data", "/var/cache/nginx/link", "/var"} {
		result, err := exec.Command("debugfs", "-R", "stat "+path, image).CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		want := "User:   101   Group:     0"
		if path == "/var" {
			want = "User:     0   Group:     0"
		}
		if !strings.Contains(string(result), want) {
			t.Fatalf("%s ownership lost:\n%s", path, result)
		}
	}
}
