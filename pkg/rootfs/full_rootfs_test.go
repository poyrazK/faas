package rootfs

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBuildFullRootfsPublishesAllLayers(t *testing.T) {
	guestInit := filepath.Join(t.TempDir(), "guest-init")
	if err := os.WriteFile(guestInit, []byte("guest-init"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := &mkfsFakeRunner{fill: []byte("FULL-ROOTFS")}
	be := newTestStorage(t)
	b := NewBuilder(run)

	res, err := b.BuildFullRootfs(context.Background(), BuildFullRootfsInput{
		Layers: []io.Reader{
			gzLayer(t, []entry{{name: "bin/", typeflag: tar.TypeDir}, {name: "bin/base", body: "base"}}),
			gzLayer(t, []entry{{name: "bin/app", body: "app"}}),
		},
		Manifest:      api.AppManifest{Entrypoint: []string{"/bin/app"}},
		GuestInitPath: guestInit,
		Plan:          api.PlanHobby,
		Storage:       be,
		StorageKey:    "apps/full/dep.ext4",
	})
	if err != nil {
		t.Fatalf("BuildFullRootfs: %v", err)
	}
	if res.ImageKey != "apps/full/dep.ext4" {
		t.Fatalf("ImageKey = %q", res.ImageKey)
	}
	got, err := be.Get(context.Background(), res.ImageKey)
	if err != nil {
		t.Fatalf("Get published image: %v", err)
	}
	defer got.Close()
	body, err := io.ReadAll(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, run.fill) {
		t.Fatalf("published image = %q, want %q", body, run.fill)
	}
	if len(run.argv) == 0 || run.argv[0] != "mkfs.ext4" {
		t.Fatalf("mkfs was not invoked: %v", run.argv)
	}
}

func TestFullRootfsMarkerIsWrittenAndTwoDriveMarkerRemoved(t *testing.T) {
	staging := t.TempDir()
	if err := writeFullRootfsMarker(staging); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(staging, filepath.FromSlash("etc/faas/.full-rootfs"))
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker missing: %v", err)
	}
	if err := removeFullRootfsMarker(staging); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("marker still exists: %v", err)
	}
}
