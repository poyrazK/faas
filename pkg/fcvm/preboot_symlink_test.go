package fcvm

import (
	"context"
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
// image seeds it. A link the tenant planted must never carry a pre-boot
// write, or the full-rootfs marker read, anywhere but the file's own path.
// The sandboxed editor never follows links (pkg/guestdisk tests that on real
// ext4); these tests hold the stagers to the same rules through the
// directory model, where an escape would reach the host.

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

// stageOnDir runs one staging session's inspection and write against a
// directory drive.
func stageOnDir(root string, files ...driveFile) error {
	img := &dirDriveImage{root: root}
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.path
	}
	layout, err := inspectDrive(context.Background(), img, paths, nil)
	if err != nil {
		return err
	}
	return stageDriveFiles(context.Background(), img, layout, files)
}

func resolverFile(t *testing.T) driveFile {
	t.Helper()
	files, _, err := preBootFileWriters(nil, nil, nil, "10.200.0.1", false)
	if err != nil || len(files) != 1 {
		t.Fatalf("resolver writer: %v %v", files, err)
	}
	return files[0]
}

func TestPreBootWritesNeverLeaveTheDrive(t *testing.T) {
	cases := []struct {
		name  string
		plant func(t *testing.T, mountRoot, hostDir, hostFile string)
		file  driveFile
		// wantWritten is the drive-relative file a successful write leaves
		// behind; empty means the write must fail closed.
		wantWritten string
	}{
		{
			name: "env.json is a symlink to a host file",
			plant: func(t *testing.T, mountRoot, _, hostFile string) {
				mustSymlink(t, hostFile, filepath.Join(mountRoot, apiEnvPath))
			},
			file:        apiEnvFile([]byte(`{"K":"v"}`)),
			wantWritten: apiEnvPath,
		},
		{
			name: "secrets.env is a symlink to a host file",
			plant: func(t *testing.T, mountRoot, _, hostFile string) {
				mustSymlink(t, hostFile, filepath.Join(mountRoot, secretsEnvPath))
			},
			file:        secretsEnvFile([]byte(`{"sealed":"x"}`)),
			wantWritten: secretsEnvPath,
		},
		{
			name: "etc/faas is a symlink to a host directory",
			plant: func(t *testing.T, mountRoot, hostDir, _ string) {
				mustSymlink(t, hostDir, filepath.Join(mountRoot, "upper", "etc", "faas"))
			},
			file: secretsEnvFile([]byte(`{"sealed":"x"}`)),
		},
		{
			name: "workload env directory is a symlink to a host directory",
			plant: func(t *testing.T, mountRoot, hostDir, _ string) {
				mustSymlink(t, hostDir, filepath.Join(mountRoot, workloadEnvPath))
			},
			file: workloadEnvFile("worker", []byte(`{"K":"v"}`)),
		},
		{
			name: "etc is a symlink to a host directory for the resolver",
			plant: func(t *testing.T, mountRoot, hostDir, _ string) {
				mustSymlink(t, hostDir, filepath.Join(mountRoot, "upper", "etc"))
			},
			file: resolverFile(t),
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
			file: apiEnvFile([]byte(`{"K":"v"}`)),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mountRoot := t.TempDir()
			hostDir, hostFile := plantHostFile(t)
			tc.plant(t, mountRoot, hostDir, hostFile)
			before := listDir(t, hostDir)

			err := stageOnDir(mountRoot, tc.file)

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

	if err := stageOnDir(mountRoot, apiEnvFile([]byte(`{"K":"v"}`))); err != nil {
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

const buildDoneRel = "upper/etc/faas/build-done.json"

// The builder drive is written by untrusted build code. build-done.json must
// be a bounded regular file on the drive, never a link to a host file (or an
// endless device such as /dev/zero, which once made a read grow until vmmd
// was OOM-killed).
func TestExportBuildDoneRefusesLinksAndOversize(t *testing.T) {
	cases := []struct {
		name  string
		plant func(t *testing.T, mountRoot, hostFile string)
		want  string // "" = must not be exported
	}{
		{
			name: "regular manifest",
			plant: func(t *testing.T, mountRoot, _ string) {
				mustWriteFile(t, filepath.Join(mountRoot, buildDoneRel), []byte(`{"exit_code":0}`))
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
				mustWriteFile(t, filepath.Join(mountRoot, buildDoneRel), make([]byte, maxBuildDoneBytes+1))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mountRoot := t.TempDir()
			_, hostFile := plantHostFile(t)
			tc.plant(t, mountRoot, hostFile)
			exportDir := t.TempDir()
			if err := exportBuildDrive(context.Background(), &dirDriveImage{root: mountRoot}, exportDir, 1<<20); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(exportDir, "build-done.json"))
			if tc.want == "" {
				if err == nil {
					t.Fatalf("exported %d bytes that must not be read: %.40q", len(data), data)
				}
				return
			}
			if err != nil || string(data) != tc.want {
				t.Fatalf("build-done.json = %q (%v), want %q", data, err, tc.want)
			}
		})
	}
}

func TestExportRefusesSymlinkedBuildOutComponents(t *testing.T) {
	mountRoot := t.TempDir()
	hostDir, _ := plantHostFile(t)
	mustWriteFile(t, filepath.Join(hostDir, "out", "image.tar"), []byte("host"))
	mustSymlink(t, hostDir, filepath.Join(mountRoot, "upper", "build"))
	exportDir := t.TempDir()
	if err := exportBuildDrive(context.Background(), &dirDriveImage{root: mountRoot}, exportDir, 1<<20); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(exportDir, "build")); !os.IsNotExist(err) {
		t.Fatalf("export followed a symlinked component (err=%v)", err)
	}

	real := t.TempDir()
	mustWriteFile(t, filepath.Join(real, "upper", "build", "out", "image.tar"), []byte("oci"))
	exportDir = t.TempDir()
	if err := exportBuildDrive(context.Background(), &dirDriveImage{root: real}, exportDir, 1<<20); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(exportDir, "build", "out", "image.tar")); err != nil || string(got) != "oci" {
		t.Fatalf("image.tar = %q (%v)", got, err)
	}
	if _, err := os.Lstat(filepath.Join(exportDir, ".guestdisk-dump")); !os.IsNotExist(err) {
		t.Fatalf("dump staging left in the export (err=%v)", err)
	}
}

// Builder output reaches builderd as root-owned files. Only directories and
// regular files are moved: a recreated symlink hands builderd a path into
// the host (image.tar -> another build's export), and opening a FIFO or
// device node blocks or reads the host device. Oversize files are skipped.
func TestMoveTreeDropsLinksSpecialAndOversizeFiles(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	hostDir, hostFile := plantHostFile(t)
	mustWriteFile(t, filepath.Join(src, "cache", "blobs", "layer"), []byte("blob"))
	mustWriteFile(t, filepath.Join(src, "huge"), make([]byte, 64))
	mustSymlink(t, hostFile, filepath.Join(src, "image.tar"))
	mustSymlink(t, hostDir, filepath.Join(src, "cache", "host"))
	if err := syscall.Mkfifo(filepath.Join(src, "fifo"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := moveTree(src, dst, 32); err != nil {
		t.Fatalf("moveTree: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(dst, "cache", "blobs", "layer")); err != nil || string(got) != "blob" {
		t.Fatalf("regular file not exported: %q err=%v", got, err)
	}
	for _, rel := range []string{"image.tar", "cache/host", "fifo", "huge"} {
		if _, err := os.Lstat(filepath.Join(dst, rel)); !os.IsNotExist(err) {
			t.Errorf("%s exported (err=%v); want it dropped", rel, err)
		}
	}
	if got, _ := os.ReadFile(hostFile); string(got) != hostSentinel {
		t.Fatal("host file changed")
	}
}
