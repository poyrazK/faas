// spec: §4.6, §4.8
package rootfs

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
)

func TestFullRootfsLaunch(t *testing.T) {
	knownPATH := "/usr/bin:/app/bin"
	defaultPATH := ""
	relativePATH := ".:bin::/nonexistent"
	noAbsolutePATH := ".:bin:"
	cases := []struct {
		name    string
		command string
		cwd     string
		path    *string
		want    string
	}{
		{name: "scratch binary does not need a shell", command: "/app/server"},
		{name: "guest proc command deferred", command: "/proc/self/exe"},
		{name: "guest temporary path deferred", command: "/tmp/generated-command"},
		{name: "companion mount command deferred", command: api.FullRootfsSidecarMountPath + "/helper/upper/bin/tool"},
		{name: "symlink to guest proc deferred", command: "/app/proc-command"},
		{name: "shell form needs image shell", command: "/bin/sh", want: "missing path"},
		{name: "missing absolute", command: "/bin/absent", want: "missing path"},
		{name: "non executable", command: "/app/config", want: "no execute permission"},
		{name: "directory", command: "/app", want: "not a regular executable"},
		{name: "trailing slash", command: "/app/server/", want: "not a regular executable"},
		{name: "relative command", command: "./server", cwd: "/app"},
		{name: "relative command default cwd", command: "app/server"},
		{name: "relative command with missing cwd", command: "./server", cwd: "/absent", want: "missing path"},
		{name: "absolute command allows guest created cwd", command: "/app/server", cwd: "/tmp"},
		{name: "PATH skips earlier non executable", command: "server", path: &knownPATH},
		{name: "default PATH", command: "tool", path: &defaultPATH},
		{name: "missing PATH command", command: "absent", path: &knownPATH, want: "no executable"},
		{name: "PATH does not search cwd", command: "server", cwd: "/app", path: &relativePATH, want: "no executable"},
		{name: "PATH without absolute directories uses guest root fallback", command: "server", path: &noAbsolutePATH},
		{name: "unknown PATH deferred", command: "runtime-command"},
		{name: "merged usr absolute link", command: "/bin/tool"},
		{name: "relative symlink", command: "/app/link"},
		{name: "symlink cwd parent traversal", command: "../server", cwd: "/work"},
		{name: "missing component parent traversal", command: "/absent/../app/server", want: "missing path"},
		{name: "file component parent traversal", command: "/app/config/../server", want: "invalid symlink"},
		{name: "symlink cycle", command: "/loop", want: "invalid symlink"},
		{name: "host absolute symlink never used", command: "/host-link", want: "missing path"},
		{name: "host relative symlink never used", command: "/host-relative-link", want: "missing path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for name, mode := range map[string]os.FileMode{
				"app/server": 0o755, "app/config": 0o644, "usr/bin/server": 0o644, "usr/bin/tool": 0o755,
				"app/bin/server": 0o755, "server": 0o755,
			} {
				dst := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
					t.Fatal(err)
				}
				// Deliberately not a runnable binary: validation must only stat it.
				if err := os.WriteFile(dst, []byte("untrusted image bytes"), mode); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Mkdir(filepath.Join(root, "app/sub"), 0o755); err != nil {
				t.Fatal(err)
			}
			// These image contents disappear beneath procfs. Neither a direct
			// command nor a symlink into /proc may be rejected based on them.
			if err := os.Mkdir(filepath.Join(root, "proc"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("/absent-image-path", filepath.Join(root, "proc/self")); err != nil {
				t.Fatal(err)
			}
			// /usr/bin/env exists on our Unix assembler hosts and is not beneath a
			// guest mount. A t.TempDir target would be deferred on Linux /tmp.
			hostFile := "/usr/bin/env"
			if _, err := os.Stat(hostFile); err != nil {
				t.Fatal(err)
			}
			for name, target := range map[string]string{
				"bin": "/usr/bin", "app/link": "server", "work": "/app/sub", "loop": "loop", "app/proc-command": "/proc/self/exe",
				"host-link": hostFile, "host-relative-link": "../../../../../../../../.." + hostFile,
			} {
				if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
					t.Fatal(err)
				}
			}
			m := api.AppManifest{Entrypoint: []string{tc.command, "argument-must-not-leak"}, WorkingDir: tc.cwd}
			err := validateFullRootfsLaunch(root, m, tc.path)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, oci.ErrImageManifestInvalid) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want image_manifest_invalid containing %q", err, tc.want)
			}
			for _, forbidden := range []string{root, hostFile, "argument-must-not-leak", knownPATH} {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("error exposes %q: %v", forbidden, err)
				}
			}
		})
	}
}

func TestBuildFullRootfsLaunchChecksMergedLayersBeforePublishing(t *testing.T) {
	cases := []struct {
		name      string
		upper     []entry
		lowerMode int64
		valid     bool
	}{
		{name: "lower executable retained", upper: []entry{{name: "app/data", body: "data"}}, valid: true},
		{name: "whiteout removes executable", upper: []entry{{name: "app/.wh.server"}}},
		{name: "opaque directory removes executable", upper: []entry{{name: "app/.wh..wh..opq"}}},
		{name: "replacement loses execute bit", upper: []entry{{name: "app/server", body: "replacement"}}},
		{name: "replacement gains execute bit", lowerMode: 0o644, upper: []entry{{name: "app/server", body: "replacement", mode: 0o755}}, valid: true},
		{name: "replacement restores executable", upper: []entry{{name: "app/.wh.server"}, {name: "app/server", body: "replacement", mode: 0o755}}, valid: true},
		{name: "replacement directory", upper: []entry{{name: "app/.wh.server"}, {name: "app/server", typeflag: tar.TypeDir}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			gi := filepath.Join(t.TempDir(), "guest-init")
			if err := os.WriteFile(gi, []byte("init"), 0o755); err != nil {
				t.Fatal(err)
			}
			run := &mkfsFakeRunner{fill: []byte("ext4")}
			be := newTestStorage(t)
			key := "apps/launch/dep.ext4"
			lowerMode := tc.lowerMode
			if lowerMode == 0 {
				lowerMode = 0o755
			}
			_, err := NewBuilder(run).BuildFullRootfs(context.Background(), BuildFullRootfsInput{
				Layers: []io.Reader{
					gzLayer(t, []entry{{name: "app/server", body: "untrusted", mode: lowerMode}}),
					gzLayer(t, tc.upper),
				},
				Manifest: api.AppManifest{Entrypoint: []string{"/app/server"}}, GuestInitPath: gi,
				Plan: api.PlanHobby, Storage: be, StorageKey: key,
			})
			if tc.valid {
				if err != nil || len(run.argv) == 0 {
					t.Fatalf("valid build: err=%v mkfs=%v", err, run.argv)
				}
			} else {
				if !errors.Is(err, oci.ErrImageManifestInvalid) || len(run.argv) != 0 {
					t.Fatalf("invalid build: err=%v mkfs=%v", err, run.argv)
				}
				if code, ok := oci.SentinelToCode(err); !ok || code != api.CodeImageManifestInvalid {
					t.Fatalf("deployment code = %q, known=%v", code, ok)
				}
				if r, getErr := be.Get(context.Background(), key); getErr == nil {
					_ = r.Close()
					t.Fatal("invalid artifact was published")
				}
			}
			entries, readErr := os.ReadDir(tmp)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("builder staging leaked: %v, err=%v", entries, readErr)
			}
		})
	}
}

func TestFullRootfsLaunchNeverUsesHostPATH(t *testing.T) {
	host := "/bin"
	if _, err := os.Stat(filepath.Join(host, "sh")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", host)
	imagePATH := host
	err := validateFullRootfsLaunch(t.TempDir(), api.AppManifest{Entrypoint: []string{"sh"}}, &imagePATH)
	if !errors.Is(err, oci.ErrImageManifestInvalid) || strings.Contains(err.Error(), host) {
		t.Fatalf("host PATH influenced launch validation: %v", err)
	}
}

func TestLayerReplacementPreservesLowerHardlink(t *testing.T) {
	root := t.TempDir()
	if err := ApplyLayerGz(root, gzLayer(t, []entry{
		{name: "app/server", body: "lower", mode: 0o755},
		{name: "app/alias", typeflag: tar.TypeLink, linkname: "app/server"},
	})); err != nil {
		t.Fatal(err)
	}
	if err := ApplyLayerGz(root, gzLayer(t, []entry{{name: "app/server", body: "upper", mode: 0o644}})); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]struct {
		body string
		mode os.FileMode
	}{"app/server": {"upper", 0o644}, "app/alias": {"lower", 0o755}} {
		p := filepath.Join(root, name)
		body, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != want.body || info.Mode().Perm() != want.mode {
			t.Fatalf("%s = %q mode %o, want %q mode %o", name, body, info.Mode().Perm(), want.body, want.mode)
		}
	}
}
