package fcvm

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// spec: §11
//
// drive1 is tenant-writable: the guest writes it at runtime and the customer
// image seeds it. vmmd mounts it on the host as root before boot, so a link
// the tenant planted must never carry a pre-boot write — or the full-rootfs
// marker read — outside the mount.

const hostSentinel = "root:x:0:0:root:/root:/bin/sh\n"

// plantHostFile returns a host directory holding a sentinel file that a
// successful escape would overwrite.
func plantHostFile(t *testing.T) (dir, file string) {
	t.Helper()
	dir = t.TempDir()
	file = filepath.Join(dir, "passwd")
	if err := os.WriteFile(file, []byte(hostSentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, file
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func TestPreBootWritesNeverLeaveTheDrive(t *testing.T) {
	resolverIP := netip.MustParseAddr("10.200.0.1")
	cases := []struct {
		name  string
		plant func(t *testing.T, mountRoot, hostDir, hostFile string)
		write func(mountRoot string) error
		// wantWritten is the drive-relative file a successful write leaves
		// behind; empty means the write must fail closed.
		wantWritten string
	}{
		{
			name: "env.json is a symlink to a host file",
			plant: func(t *testing.T, mountRoot, _, hostFile string) {
				mustSymlink(t, hostFile, filepath.Join(mountRoot, apiEnvPath))
			},
			write:       func(mp string) error { return writeAPIEnv(mp, []byte(`{"K":"v"}`)) },
			wantWritten: apiEnvPath,
		},
		{
			name: "secrets.env is a symlink to a host file",
			plant: func(t *testing.T, mountRoot, _, hostFile string) {
				mustSymlink(t, hostFile, filepath.Join(mountRoot, secretsEnvPath))
			},
			write:       func(mp string) error { return writeSecretsEnv(mp, []byte(`{"sealed":"x"}`)) },
			wantWritten: secretsEnvPath,
		},
		{
			name: "etc/faas is a symlink to a host directory",
			plant: func(t *testing.T, mountRoot, hostDir, _ string) {
				mustSymlink(t, hostDir, filepath.Join(mountRoot, "upper", "etc", "faas"))
			},
			write: func(mp string) error { return writeSecretsEnv(mp, []byte(`{"sealed":"x"}`)) },
		},
		{
			name: "workload env directory is a symlink to a host directory",
			plant: func(t *testing.T, mountRoot, hostDir, _ string) {
				mustSymlink(t, hostDir, filepath.Join(mountRoot, workloadEnvPath))
			},
			write: func(mp string) error { return writeWorkloadEnv(mp, "worker", []byte(`{"K":"v"}`)) },
		},
		{
			name: "etc is a symlink to a host directory for the resolver",
			plant: func(t *testing.T, mountRoot, hostDir, _ string) {
				mustSymlink(t, hostDir, filepath.Join(mountRoot, "upper", "etc"))
			},
			write: func(mp string) error { return writeServiceDiscoveryResolver(mp, resolverIP) },
		},
		{
			name: "full-rootfs marker is reached through a symlinked etc",
			plant: func(t *testing.T, mountRoot, hostDir, _ string) {
				// The host directory holds a valid marker; following the
				// link would switch vmmd to the full-rootfs layout and
				// write etc/faas/env.json straight into the host dir.
				marker := filepath.Join(hostDir, strings.TrimPrefix(api.FullRootfsMarkerPath, "/etc/"))
				if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(marker, []byte(api.FullRootfsMarkerValue), 0o644); err != nil {
					t.Fatal(err)
				}
				mustSymlink(t, hostDir, filepath.Join(mountRoot, "etc"))
			},
			write: func(mp string) error { return writeAPIEnv(mp, []byte(`{"K":"v"}`)) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mountRoot := t.TempDir()
			hostDir, hostFile := plantHostFile(t)
			tc.plant(t, mountRoot, hostDir, hostFile)
			before := listDir(t, hostDir)

			err := tc.write(mountRoot)

			if got, _ := os.ReadFile(hostFile); string(got) != hostSentinel {
				t.Fatalf("host file overwritten through a tenant symlink: %q", got)
			}
			if after := listDir(t, hostDir); strings.Join(after, ",") != strings.Join(before, ",") {
				t.Fatalf("host directory changed: before %v after %v", before, after)
			}
			if tc.wantWritten == "" {
				if err == nil {
					t.Fatalf("write through an escaping symlink succeeded; want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("write: %v", err)
			}
			info, err := os.Lstat(filepath.Join(mountRoot, tc.wantWritten))
			if err != nil || !info.Mode().IsRegular() {
				t.Fatalf("%s is not a regular file on the drive after the write (err=%v)", tc.wantWritten, err)
			}
		})
	}
}

// A link that stays inside the drive is replaced, not followed: the write
// must land at its own path without rewriting whatever the link named.
func TestPreBootWriteReplacesInDriveSymlink(t *testing.T) {
	mountRoot := t.TempDir()
	other := filepath.Join(mountRoot, "upper", "app", "config.json")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("tenant"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, "../../app/config.json", filepath.Join(mountRoot, apiEnvPath))

	if err := writeAPIEnv(mountRoot, []byte(`{"K":"v"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got, _ := os.ReadFile(other); string(got) != "tenant" {
		t.Fatalf("link target rewritten: %q", got)
	}
	info, err := os.Lstat(filepath.Join(mountRoot, apiEnvPath))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o400 {
		t.Fatalf("env.json = %v (err=%v), want a regular 0400 file", info, err)
	}
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// The builder drive is written by untrusted build code; vmmd exports it as
// root. build-done.json must be a bounded regular file on the drive, never a
// link to a host file (or an endless device such as /dev/zero, which made
// os.ReadFile grow until vmmd was OOM-killed).
func TestReadBuildDoneRefusesLinksAndOversize(t *testing.T) {
	cases := []struct {
		name  string
		plant func(t *testing.T, mountRoot, hostFile string)
		want  string // "" = must not be read
	}{
		{
			name: "regular manifest",
			plant: func(t *testing.T, mountRoot, _ string) {
				p := filepath.Join(mountRoot, buildDoneRel)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(`{"exit_code":0}`), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: `{"exit_code":0}`,
		},
		{
			name: "symlink to a host file",
			plant: func(t *testing.T, mountRoot, hostFile string) {
				mustSymlink(t, hostFile, filepath.Join(mountRoot, buildDoneRel))
			},
		},
		{
			name: "symlink to an endless device",
			plant: func(t *testing.T, mountRoot, _ string) {
				mustSymlink(t, "/dev/zero", filepath.Join(mountRoot, buildDoneRel))
			},
		},
		{
			name: "parent directory links to the host",
			plant: func(t *testing.T, mountRoot, hostFile string) {
				hostDir := filepath.Dir(hostFile)
				if err := os.WriteFile(filepath.Join(hostDir, "build-done.json"), []byte(hostSentinel), 0o600); err != nil {
					t.Fatal(err)
				}
				mustSymlink(t, hostDir, filepath.Join(mountRoot, "upper", "etc", "faas"))
			},
		},
		{
			name: "oversize manifest",
			plant: func(t *testing.T, mountRoot, _ string) {
				p := filepath.Join(mountRoot, buildDoneRel)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, make([]byte, maxBuildDoneBytes+1), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mountRoot := t.TempDir()
			_, hostFile := plantHostFile(t)
			tc.plant(t, mountRoot, hostFile)
			data, ok := readBuildDone(mountRoot)
			if tc.want == "" {
				if ok {
					t.Fatalf("read %d bytes that must not be read: %.40q", len(data), data)
				}
				return
			}
			if !ok || string(data) != tc.want {
				t.Fatalf("readBuildDone = %q ok=%v, want %q", data, ok, tc.want)
			}
		})
	}
}

func TestBuildOutDirRefusesSymlinkedComponents(t *testing.T) {
	mountRoot := t.TempDir()
	hostDir, _ := plantHostFile(t)
	if err := os.MkdirAll(filepath.Join(hostDir, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, hostDir, filepath.Join(mountRoot, "upper", "build"))
	if dir, ok := buildOutDir(mountRoot); ok {
		t.Fatalf("buildOutDir followed a symlinked component to %s", dir)
	}

	real := t.TempDir()
	if err := os.MkdirAll(filepath.Join(real, "upper", "build", "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	if dir, ok := buildOutDir(real); !ok || dir != filepath.Join(real, "upper", "build", "out") {
		t.Fatalf("buildOutDir(real) = %q ok=%v", dir, ok)
	}
}

// copyTree exports builder output as root. Only directories and regular
// files are copied: a recreated symlink hands builderd a path into the host
// (image.tar -> another build's export), and opening a FIFO or device node
// blocks or reads the host device.
func TestCopyTreeDropsLinksAndSpecialFiles(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	hostDir, hostFile := plantHostFile(t)
	if err := os.MkdirAll(filepath.Join(src, "cache", "blobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "cache", "blobs", "layer"), []byte("blob"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, hostFile, filepath.Join(src, "image.tar"))
	mustSymlink(t, hostDir, filepath.Join(src, "cache", "host"))
	if err := syscall.Mkfifo(filepath.Join(src, "fifo"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyTree(src, dst, 1<<20); err != nil {
		t.Fatalf("copyTree: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(dst, "cache", "blobs", "layer")); err != nil || string(got) != "blob" {
		t.Fatalf("regular file not exported: %q err=%v", got, err)
	}
	for _, rel := range []string{"image.tar", "cache/host", "fifo"} {
		if _, err := os.Lstat(filepath.Join(dst, rel)); !os.IsNotExist(err) {
			t.Errorf("%s exported (err=%v); want it dropped", rel, err)
		}
	}
}
