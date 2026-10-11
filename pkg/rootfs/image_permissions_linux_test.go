//go:build linux

package rootfs

import (
	"archive/tar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestAppImagePermissionsIgnorePrivateUmask(t *testing.T) {
	// Umask is process-wide; a child keeps unrelated race tests isolated.
	if os.Getenv("FAAS_TEST_IMAGE_PRIVATE_UMASK") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestAppImagePermissionsIgnorePrivateUmask$")
		cmd.Env = append(os.Environ(), "FAAS_TEST_IMAGE_PRIVATE_UMASK=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("private-mask image assembly: %v\n%s", err, out)
		}
		return
	}
	unix.Umask(0o077)
	staging := t.TempDir()
	if err := applyEntry(staging, filepath.Join(staging, "explicit"), &tar.Header{Typeflag: tar.TypeDir, Mode: 0o750}, strings.NewReader(""), nil); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(staging, "private")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"probe", "nested/bin/probe", "private/probe"} {
		if err := applyEntry(staging, filepath.Join(staging, name), &tar.Header{Typeflag: tar.TypeReg, Mode: 0o755, Size: 4}, strings.NewReader("test"), nil); err != nil {
			t.Fatal(err)
		}
	}
	runner := filepath.Join(t.TempDir(), "runner")
	if err := os.WriteFile(runner, []byte("runner"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := InjectFunctionRunner(staging, runner); err != nil {
		t.Fatal(err)
	}
	if err := stageAppUpper(staging); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]os.FileMode{"upper": 0o755, "upper/nested": 0o755, "upper/nested/bin": 0o755,
		"upper/probe": 0o755, "upper/nested/bin/probe": 0o755, "upper/explicit": 0o750, "upper/private": 0o700,
		"upper/usr": 0o755, "upper/usr/local/bin": 0o755, "upper/usr/local/bin/faas-runner": 0o755, ".": 0o700} {
		info, err := os.Stat(filepath.Join(staging, name))
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("image mode %s: info=%v err=%v; want %04o", name, info, err, want)
		}
	}
}
