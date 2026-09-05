//go:build linux && metal

package fcvm

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestMetalImageBindMount(t *testing.T) {
	if os.Getenv("FAAS_TEST_NETWORK_BATCH") != "1" {
		t.Skip("requires an isolated mount namespace")
	}
	root := t.TempDir()
	sourceDir := filepath.Join(root, "source")
	if err := os.Mkdir(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mount("tmpfs", sourceDir, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "size=1m"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Unmount(sourceDir, 0) })
	source, target := filepath.Join(sourceDir, "image"), filepath.Join(root, "target")
	if err := os.WriteFile(source, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Unmount(target, 0) })
	samples := map[string][]float64{"commands": {}, "syscalls": {}}
	for round := 0; round < 53; round++ {
		order := []string{"commands", "syscalls"}
		if round%2 == 1 {
			order[0], order[1] = order[1], order[0]
		}
		for _, mode := range order {
			start := time.Now()
			var out []byte
			var err error
			if mode == "commands" {
				out, err = exec.Command("mount", "--bind", source, target).CombinedOutput()
				if err == nil {
					out, err = exec.Command("mount", "-o", "remount,bind,ro", target).CombinedOutput()
				}
			} else {
				out, err = bindFileMount(source, target)
				if err == nil {
					out, err = makeFileMountReadOnly(target)
				}
			}
			elapsed := float64(time.Since(start)) / float64(time.Millisecond)
			if err != nil {
				t.Fatalf("%s bind: %v %s", mode, err, out)
			}
			var stat unix.Statfs_t
			if err := unix.Statfs(target, &stat); err != nil {
				t.Fatal(err)
			}
			wantFlags := int64(unix.ST_RDONLY | unix.ST_NOSUID | unix.ST_NODEV | unix.ST_NOEXEC)
			if stat.Flags&wantFlags != wantFlags {
				t.Fatalf("%s lost mount restrictions: %#x", mode, stat.Flags)
			}
			if err := os.WriteFile(target, []byte("mutated"), 0o600); !errors.Is(err, unix.EROFS) {
				t.Fatalf("%s allowed a write through the read-only bind: %v", mode, err)
			}
			// Setting the bind read-only must leave its source mount writable.
			if err := os.WriteFile(source, []byte("fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(target); err != nil || string(got) != "fixture" {
				t.Fatalf("%s contents: %q %v", mode, got, err)
			}
			if err := unix.Unmount(target, 0); err != nil {
				t.Fatal(err)
			}
			if round >= 3 {
				samples[mode] = append(samples[mode], elapsed)
			}
		}
	}
	if _, err := bindFileMount(filepath.Join(root, "missing"), target); !errors.Is(err, unix.ENOENT) {
		t.Fatalf("missing source error=%v", err)
	}
	data, err := json.Marshal(samples)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("image_bind_samples_ms=%s", data)
}
