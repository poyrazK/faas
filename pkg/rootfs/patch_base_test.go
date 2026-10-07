package rootfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

func requireE2fsprogs(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"mkfs.ext4", "debugfs", "e2fsck"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed; skipping real-image patch test", tool)
		}
	}
}

// patchFixture builds a small base ext4 the way BuildBase does (mkfs -d of a
// staging tree) with PID 1 at sbinDir/init, and /sbin symlinked to sbinDir
// when they differ.
func patchFixture(t *testing.T, sbinDir string, init []byte) string {
	t.Helper()
	staging := t.TempDir()
	if err := os.MkdirAll(filepath.Join(staging, sbinDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if sbinDir != "sbin" {
		if err := os.Symlink(sbinDir, filepath.Join(staging, "sbin")); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(staging, sbinDir, "init"), init, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(staging, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "etc", "passwd"), []byte("root:x:0:0::/root:/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(t.TempDir(), "base.ext4")
	if err := (wire.ExecRunner{}).Run(context.Background(), MkfsCommand(staging, image, 16)); err != nil {
		t.Fatalf("mkfs: %v", err)
	}
	return image
}

func writeGuestInit(t *testing.T, body []byte) (string, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "guest-init")
	if err := os.WriteFile(p, body, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	return p, hex.EncodeToString(sum[:])
}

func readStored(t *testing.T, be storage.StorageBackend, key string) []byte {
	t.Helper()
	rc, err := be.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func dumpInit(t *testing.T, image, initPath string) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "init")
	if err := (wire.ExecRunner{}).Run(context.Background(), []string{"debugfs", "-R", "dump " + initPath + " " + out, image}); err != nil {
		t.Fatalf("dump: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read dumped init: %v", err)
	}
	return b
}

func TestPatchBaseGuestInitReplacesPID1(t *testing.T) {
	requireE2fsprogs(t)
	t.Setenv("FAAS_BASE_TMP_ROOT", t.TempDir())
	for _, tc := range []struct {
		name     string
		sbinDir  string
		initPath string
	}{
		{name: "sbin directory", sbinDir: "sbin", initPath: "/sbin/init"},
		{name: "usrmerged sbin", sbinDir: "usr/sbin", initPath: "/usr/sbin/init"},
		{name: "sbin into usr/bin", sbinDir: "usr/bin", initPath: "/usr/bin/init"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := patchFixture(t, tc.sbinDir, bytes.Repeat([]byte("old-init"), 4096))
			before, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			// A larger binary must fit in the blocks the old one frees.
			newInit := bytes.Repeat([]byte("new-guest-init"), 8192)
			gi, sum := writeGuestInit(t, newInit)
			be, err := storage.NewLocalStorageBackend(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			b := NewBuilder(wire.ExecRunner{})
			res, err := b.PatchBaseGuestInit(context.Background(), BasePatchInput{
				SourceImage: source, GuestInitPath: gi, GuestInitSHA256: sum,
				Storage: be, StorageKey: "base/runner-test-amd64.ext4",
			})
			if err != nil {
				t.Fatalf("PatchBaseGuestInit: %v", err)
			}
			if res.ImageKey != "base/runner-test-amd64.ext4" || res.SizeBytes != int64(len(before)) {
				t.Fatalf("result = %+v, want key and source size %d", res, len(before))
			}
			after, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("source image was modified; the patch must work on a copy")
			}
			published := filepath.Join(t.TempDir(), "published.ext4")
			if err := os.WriteFile(published, readStored(t, be, res.ImageKey), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := dumpInit(t, published, tc.initPath); !bytes.Equal(got, newInit) {
				t.Fatalf("published %s holds %d bytes, want the new guest-init", tc.initPath, len(got))
			}
			if err := (wire.ExecRunner{}).Run(context.Background(), []string{"e2fsck", "-fn", published}); err != nil {
				t.Fatalf("published image fails e2fsck: %v", err)
			}
		})
	}
}

// Two nodes patching the same published bytes must publish identical bytes,
// or ADR-510 refuses cross-node restores until ADR-567 convergence runs.
func TestPatchBaseGuestInitIsDeterministic(t *testing.T) {
	requireE2fsprogs(t)
	t.Setenv("FAAS_BASE_TMP_ROOT", t.TempDir())
	source := patchFixture(t, "usr/sbin", []byte("old-init"))
	gi, sum := writeGuestInit(t, []byte("new-guest-init"))
	var outputs [][]byte
	for i := 0; i < 2; i++ {
		if i > 0 {
			// debugfs stamps wall-clock seconds unless the fake time holds.
			time.Sleep(1100 * time.Millisecond)
		}
		be, err := storage.NewLocalStorageBackend(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := NewBuilder(wire.ExecRunner{}).PatchBaseGuestInit(context.Background(), BasePatchInput{
			SourceImage: source, GuestInitPath: gi, GuestInitSHA256: sum,
			Storage: be, StorageKey: "base/runner-test-amd64.ext4",
		}); err != nil {
			t.Fatalf("patch %d: %v", i, err)
		}
		outputs = append(outputs, readStored(t, be, "base/runner-test-amd64.ext4"))
	}
	if !bytes.Equal(outputs[0], outputs[1]) {
		t.Fatal("patching the same image twice produced different bytes")
	}
}

func TestPatchBaseGuestInitRefusesUnexpectedImages(t *testing.T) {
	requireE2fsprogs(t)
	t.Setenv("FAAS_BASE_TMP_ROOT", t.TempDir())
	gi, sum := writeGuestInit(t, []byte("new-guest-init"))

	symlinkInit := func(t *testing.T) string {
		staging := t.TempDir()
		if err := os.MkdirAll(filepath.Join(staging, "sbin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("/bin/busybox", filepath.Join(staging, "sbin", "init")); err != nil {
			t.Fatal(err)
		}
		image := filepath.Join(t.TempDir(), "base.ext4")
		if err := (wire.ExecRunner{}).Run(context.Background(), MkfsCommand(staging, image, 8)); err != nil {
			t.Fatal(err)
		}
		return image
	}
	for _, tc := range []struct {
		name   string
		source func(t *testing.T) string
		sum    string
		want   error
	}{
		{name: "init is a symlink", source: symlinkInit, sum: sum, want: ErrBasePatchUnsupported},
		{name: "digest does not match the binary", source: func(t *testing.T) string {
			return patchFixture(t, "sbin", []byte("old"))
		}, sum: strings.Repeat("0", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			be, err := storage.NewLocalStorageBackend(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			_, err = NewBuilder(wire.ExecRunner{}).PatchBaseGuestInit(context.Background(), BasePatchInput{
				SourceImage: tc.source(t), GuestInitPath: gi, GuestInitSHA256: tc.sum,
				Storage: be, StorageKey: "base/runner-test-amd64.ext4",
			})
			if err == nil {
				t.Fatal("PatchBaseGuestInit succeeded, want an error")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if _, getErr := be.Get(context.Background(), "base/runner-test-amd64.ext4"); getErr == nil {
				t.Fatal("a refused patch published an image")
			}
		})
	}
}

type runOnly struct{}

func (runOnly) Run(context.Context, []string) error { return nil }

func TestPatchBaseGuestInitNeedsAnOutputRunner(t *testing.T) {
	_, err := NewBuilder(runOnly{}).PatchBaseGuestInit(context.Background(), BasePatchInput{})
	if !errors.Is(err, ErrBasePatchUnsupported) {
		t.Fatalf("err = %v, want ErrBasePatchUnsupported", err)
	}
}

func TestParseDebugfsStat(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		want debugfsInode
	}{
		{
			name: "regular",
			out: "Inode: 559   Type: regular    Mode:  0755   Flags: 0x80000\n" +
				"Generation: 0    Version: 0x00000000:00000000\n" +
				"User:   992   Group:   988   Project:     0   Size: 64837456\n",
			want: debugfsInode{Type: "regular", Mode: "0755", User: "992", Group: "988", Size: 64837456},
		},
		{
			name: "fast symlink",
			out: "Inode: 12   Type: symlink    Mode:  0777   Flags: 0x0\n" +
				"User:     0   Group:     0   Project:     0   Size: 8\n" +
				"Fast link dest: \"usr/sbin\"\n",
			want: debugfsInode{Type: "symlink", Mode: "0777", User: "0", Group: "0", Size: 8, LinkDest: "usr/sbin"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDebugfsStat(tc.out)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
	if _, err := parseDebugfsStat(""); !errors.Is(err, ErrBasePatchUnsupported) {
		t.Fatalf("empty output err = %v, want ErrBasePatchUnsupported", err)
	}
}

func TestBasePatchInputRejectsUnsafePaths(t *testing.T) {
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	valid := BasePatchInput{
		SourceImage: "/srv/fc/base/a.ext4", GuestInitPath: "/opt/faas/init",
		GuestInitSHA256: strings.Repeat("a", 64), Storage: be, StorageKey: "base/a.ext4",
	}
	if err := validateBasePatchInput(valid); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	for name, mutate := range map[string]func(*BasePatchInput){
		"space in guest-init path": func(in *BasePatchInput) { in.GuestInitPath = "/opt/faas/my init" },
		"quote in source path":     func(in *BasePatchInput) { in.SourceImage = `/srv/fc/"a.ext4` },
		"relative source path":     func(in *BasePatchInput) { in.SourceImage = "a.ext4" },
		"newline in guest-init":    func(in *BasePatchInput) { in.GuestInitPath = "/opt/init\nrm /etc" },
		"uppercase digest":         func(in *BasePatchInput) { in.GuestInitSHA256 = strings.Repeat("A", 64) },
		"missing storage":          func(in *BasePatchInput) { in.Storage = nil },
	} {
		in := valid
		mutate(&in)
		if err := validateBasePatchInput(in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
