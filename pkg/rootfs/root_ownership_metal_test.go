//go:build metal && linux

package rootfs

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// adr: 1239 — production imaged runs as an unprivileged service user with
// CAP_CHOWN, and mkfs.ext4 -d publishes staging owners as-is. Run this test
// that way (for example under setpriv with ambient chown, dac_override and
// fowner) to prove root-owned entries reach the ext4 as root. As root it
// would pass trivially, so it skips.
func TestRootOwnershipPublishedByUnprivilegedBuilder(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("run as an unprivileged user with CAP_CHOWN; as root every file is root-owned anyway")
	}
	for _, tool := range []string{"mkfs.ext4", "debugfs"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available: %v", tool, err)
		}
	}
	SetPreserveRootOwnership(true)
	defer SetPreserveRootOwnership(false)

	staging := filepath.Join(t.TempDir(), "staging")
	if err := os.Mkdir(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	layer := gzLayer(t, []entry{
		{name: "etc/", typeflag: tar.TypeDir},
		{name: "etc/passwd", body: "root:x:0:0:root:/root:/bin/sh\n"},
		{name: "data/", typeflag: tar.TypeDir},
	})
	if err := ApplyLayerGz(staging, layer); err != nil {
		t.Fatalf("apply root-owned layer: %v", err)
	}
	// An OpenShift-style entry: an arbitrary UID with group 0.
	if err := ApplyLayerGz(staging, ownedLayer(t, "data/owned", 1001, 0)); err != nil {
		t.Fatalf("apply 1001:0 layer: %v", err)
	}

	image := filepath.Join(t.TempDir(), "app.ext4")
	mkfs := MkfsCommand(staging, image, 32)
	if out, err := exec.Command(mkfs[0], mkfs[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("mkfs.ext4: %v: %s", err, out)
	}
	for path, want := range map[string][2]int{
		"/etc/passwd": {0, 0},
		"/etc":        {0, 0},
		"/data":       {0, 0},
		"/data/owned": {1001, 0},
	} {
		uid, gid := ext4Owner(t, image, path)
		if uid != want[0] || gid != want[1] {
			t.Fatalf("%s owned %d:%d in the published ext4, want %d:%d", path, uid, gid, want[0], want[1])
		}
	}
}

// ownedLayer is a gzip layer with one regular file owned uid:gid.
func ownedLayer(t *testing.T, name string, uid, gid int) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o664, Size: 1, Uid: uid, Gid: gid}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return &out
}

func ext4Owner(t *testing.T, image, path string) (int, int) {
	t.Helper()
	out, err := exec.Command("debugfs", "-R", "stat "+path, image).CombinedOutput()
	if err != nil {
		t.Fatalf("debugfs stat %s: %v: %s", path, err, out)
	}
	match := debugfsOwnerRE.FindSubmatch(out)
	if match == nil {
		t.Fatalf("debugfs stat %s: no owner in %s", path, out)
	}
	uid, _ := strconv.Atoi(string(match[1]))
	gid, _ := strconv.Atoi(string(match[2]))
	return uid, gid
}
