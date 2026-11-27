//go:build linux || darwin

package storage

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestRunEnvironmentDirectoriesUnderPrivateUmask(t *testing.T) {
	if os.Getenv("GREGALE_ENVIRONMENT_UMASK_HELPER") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRunEnvironmentDirectoriesUnderPrivateUmask$")
		cmd.Env = append(os.Environ(), "GREGALE_ENVIRONMENT_UMASK_HELPER=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("private umask: %s %v", out, err)
		}
		return
	}
	old := syscall.Umask(0077)
	defer syscall.Umask(old)
	root := t.TempDir()
	backend, err := NewLocalStorageBackend(root)
	if err != nil {
		t.Fatal(err)
	}
	key := "run-environments/account/environment/attempt/image.tar"
	if err := backend.Put(context.Background(), key, strings.NewReader("immutable export")); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"run-environments", "run-environments/account", "run-environments/account/environment/attempt"} {
		info, err := os.Stat(filepath.Join(root, dir))
		if err != nil || info.Mode().Perm()&0070 != 0070 {
			t.Fatalf("group cannot reclaim %s: %v %v", dir, info, err)
		}
	}
	if err := backend.Delete(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "run-environments/account")); !os.IsNotExist(err) {
		t.Fatal("environment directory leaked", err)
	}
}
