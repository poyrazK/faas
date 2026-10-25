package fcvm

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/guestdisk"
)

// These tests run the production drive path end to end on real ext4: the
// stagers, a guestdisk session, and on Linux the vmmd-guestdisk sandbox (the
// test binary dispatches it from TestMain). They skip without e2fsprogs.

func realE2fsTool(t *testing.T, name string) string {
	t.Helper()
	if dir := os.Getenv("FAAS_E2FSPROGS_DIR"); dir != "" {
		if p := filepath.Join(dir, name); isRegularFile(p) {
			return p
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, dir := range []string{"/usr/sbin", "/sbin", "/opt/homebrew/opt/e2fsprogs/sbin"} {
		if p := filepath.Join(dir, name); isRegularFile(p) {
			return p
		}
	}
	t.Skipf("%s not installed (set FAAS_E2FSPROGS_DIR)", name)
	return ""
}

func isRegularFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

// realDriveRoot is a directory a root caller's sandbox does not mask.
func realDriveRoot(t *testing.T) string {
	t.Helper()
	if os.Geteuid() != 0 {
		return t.TempDir()
	}
	dir, err := os.MkdirTemp("/var/lib", "fcvm-drive-test-")
	if err != nil {
		t.Skipf("no directory outside /tmp for a root drive test: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// realDrive builds an ext4 drive from tree, owned by a jail uid when the
// test runs as root, and routes every drive session to the real editor.
func realDrive(t *testing.T, v *JailerVMM, instance string, tree func(root string)) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "tree")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if tree != nil {
		tree(src)
	}
	drive := filepath.Join(v.chrootRoot(instance), layerImageName)
	if out, err := exec.Command(realE2fsTool(t, "mke2fs"), "-q", "-F", "-t", "ext4", "-b", "4096", "-d", src, drive, "32M").CombinedOutput(); err != nil {
		t.Fatalf("mke2fs: %v: %s", err, out)
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(drive, JailUIDBase+1, JailUIDBase+1); err != nil {
			t.Fatal(err)
		}
	}
	helper := ""
	if runtime.GOOS == "linux" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		helper = exe
	}
	ed, err := guestdisk.New(guestdisk.Config{
		Helper:      helper,
		Debugfs:     realE2fsTool(t, "debugfs"),
		E2fsck:      realE2fsTool(t, "e2fsck"),
		ScratchRoot: filepath.Join(realDriveRoot(t), "scratch"),
		AllowOwner:  jailDriveOwner,
	})
	if err != nil {
		t.Fatal(err)
	}
	prev := openDriveImage
	openDriveImage = func(_ *JailerVMM, d string) (driveImage, error) { return ed.Open(d) }
	t.Cleanup(func() { openDriveImage = prev })
	return drive
}

func realStagingVMM(t *testing.T, instance string) *JailerVMM {
	t.Helper()
	v := NewJailerVMM(realDriveRoot(t), 0)
	if err := os.MkdirAll(v.chrootRoot(instance), 0o755); err != nil {
		t.Fatal(err)
	}
	return v
}

func readDriveFile(t *testing.T, drive, p string) []byte {
	t.Helper()
	out, err := exec.Command(realE2fsTool(t, "debugfs"), "-R", "cat "+p, drive).Output()
	if err != nil {
		t.Fatalf("debugfs cat %s: %v", p, err)
	}
	return out
}

func fsckDrive(t *testing.T, drive string) {
	t.Helper()
	if out, err := exec.Command(realE2fsTool(t, "e2fsck"), "-fn", drive).CombinedOutput(); err != nil {
		t.Fatalf("drive is not clean: %v\n%s", err, out)
	}
}

func TestRealDrivePreBootStagingAndForkScrub(t *testing.T) {
	v := realStagingVMM(t, "real")
	drive := realDrive(t, v, "real", func(root string) {
		mustWriteFile(t, filepath.Join(root, "upper/app/main.py"), []byte("print(1)\n"))
		mustWriteFile(t, filepath.Join(root, "upper/tmp/gregale-secret-reload/secrets.json"), []byte(`{"K":"old"}`))
	})
	// The guest had the drive mounted: leave a committed, unreplayed
	// transaction in its journal, as a captured drive has.
	block := filepath.Join(t.TempDir(), "block")
	if err := os.WriteFile(block, bytes.Repeat([]byte("J"), 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(realE2fsTool(t, "debugfs"), "-w", "-f", "-", drive)
	cmd.Stdin = strings.NewReader("jo\njw -b 7000 " + block + "\njc\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("journal fixture: %v %s", err, out)
	}
	workloads := []WorkloadSpec{
		{Name: "main", Type: "app", RamMB: 256, Port: 8080, Essential: true},
		{Name: "metrics", Type: "sidecar", RamMB: 64, Port: 9100, preparedEnvJSON: []byte(`{"A":"1"}`)},
	}
	if err := v.stagePreBootFiles("real", workloads, []byte(`{"S":"x"}`), []byte(`{"K":"v"}`), "10.156.0.1", true); err != nil {
		t.Fatal(err)
	}
	if got := readDriveFile(t, drive, "/upper/etc/faas/secrets.env"); string(got) != `{"S":"x"}` {
		t.Fatalf("secrets.env = %q", got)
	}
	if got := readDriveFile(t, drive, "/upper/etc/resolv.conf"); !strings.Contains(string(got), "nameserver 10.156.0.1") {
		t.Fatalf("resolv.conf = %q", got)
	}
	fsckDrive(t, drive)

	if err := v.scrubForkSecrets(context.Background(), nativeLaunchRecord{}, "real"); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"/upper/etc/faas/secrets.env", "/upper/tmp/gregale-secret-reload", "/upper/etc/faas/workloads/metrics/env.json"} {
		out, _ := exec.Command(realE2fsTool(t, "debugfs"), "-R", "stat "+gone, drive).CombinedOutput()
		if !strings.Contains(string(out), "File not found") {
			t.Errorf("%s survived the scrub", gone)
		}
	}
	if got := readDriveFile(t, drive, "/upper/etc/faas/env.json"); string(got) != `{"K":"v"}` {
		t.Fatalf("scrub removed env.json: %q", got)
	}
	fsckDrive(t, drive)
}

func TestRealDriveBuildExport(t *testing.T) {
	v := realStagingVMM(t, "builder")
	realDrive(t, v, "builder", func(root string) {
		mustWriteFile(t, filepath.Join(root, "upper/etc/faas/build-done.json"), []byte(`{"exit_code":0}`))
		mustWriteFile(t, filepath.Join(root, "upper/build/out/image.tar"), []byte("oci"))
		mustWriteFile(t, filepath.Join(root, "upper/build/out/cache/blob"), []byte("blob"))
		if err := os.Symlink("/etc/passwd", filepath.Join(root, "upper/build/out/escape")); err != nil {
			t.Fatal(err)
		}
	})
	exportDir := filepath.Join(realDriveRoot(t), "export")
	if err := v.exportBuildArtifacts("builder", exportDir); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(exportDir, "build-done.json")); err != nil || string(got) != `{"exit_code":0}` {
		t.Fatalf("build-done.json = %q (%v)", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(exportDir, "build/out/cache/blob")); err != nil || string(got) != "blob" {
		t.Fatalf("cache/blob = %q (%v)", got, err)
	}
	if _, err := os.Lstat(filepath.Join(exportDir, "build/out/escape")); !os.IsNotExist(err) {
		t.Fatalf("symlink exported (err=%v)", err)
	}
	if _, err := os.Lstat(filepath.Join(exportDir, ".guestdisk-dump")); !os.IsNotExist(err) {
		t.Fatalf("dump staging left behind (err=%v)", err)
	}
}
