package rootfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// An image's layers keep absolute symlinks verbatim, so every host operation
// after extraction must resolve guest paths inside staging. These tests plant
// links to "host" paths outside staging and check that imaged neither reads
// nor writes through them (spec §11).

const hostSecret = "AGE-SECRET-KEY-1HOSTSECRETMUSTNOTLEAK"

// hostFixture returns a host directory outside staging and a secret file in it.
func hostFixture(t *testing.T) (dir, secret string) {
	t.Helper()
	dir = t.TempDir()
	secret = filepath.Join(dir, "fleet.age")
	if err := os.WriteFile(secret, []byte("# created: 2026-10-24\n"+hostSecret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, secret
}

func plantLink(t *testing.T, staging, guestPath, target string) {
	t.Helper()
	p := filepath.Join(staging, filepath.FromSlash(guestPath))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
}

// assertHostUntouched fails when anything but the secret appeared in dir or
// the secret changed.
func assertHostUntouched(t *testing.T, dir, secret string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(secret) {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("host directory gained entries: %v", names)
	}
	got, err := os.ReadFile(secret)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), hostSecret) {
		t.Fatalf("host secret was overwritten: %q", got)
	}
}

func TestParseStagingPasswdDoesNotReadHostFiles(t *testing.T) {
	_, secret := hostFixture(t)
	staging := t.TempDir()
	plantLink(t, staging, "etc/passwd", secret)

	entries, err := parseStagingPasswd(staging)
	if err != nil {
		if strings.Contains(err.Error(), hostSecret) {
			t.Fatalf("passwd parse error leaks the host file: %v", err)
		}
		t.Fatalf("parseStagingPasswd = %v; a link out of the image must resolve inside staging and find nothing", err)
	}
	if entries != nil {
		t.Fatalf("parseStagingPasswd read %v through a link to a host file", entries)
	}
}

func TestParseStagingPasswdFollowsLinksInsideTheImage(t *testing.T) {
	staging := t.TempDir()
	if err := os.MkdirAll(filepath.Join(staging, "usr", "share", "base"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "usr", "share", "base", "passwd"), []byte("app:x:1000:1000::/home/app:/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Absolute targets resolve against the image root, as the guest sees them.
	plantLink(t, staging, "etc/passwd", "/usr/share/base/passwd")

	entries, err := parseStagingPasswd(staging)
	if err != nil || entries["app"].Uid != 1000 {
		t.Fatalf("parseStagingPasswd = %v, %v; want the image's own passwd through its link", entries, err)
	}
}

func TestStagingWritesStayInsideStaging(t *testing.T) {
	runner := filepath.Join(t.TempDir(), "faas-runner")
	if err := os.WriteFile(runner, []byte("runner"), 0o755); err != nil {
		t.Fatal(err)
	}
	guestInit := filepath.Join(t.TempDir(), "init")
	if err := os.WriteFile(guestInit, []byte("guest-init"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		// links maps guest paths to targets; "HOSTDIR" and "SECRET" are
		// replaced with the host fixture paths.
		links map[string]string
		run   func(staging string) error
		// inside is a guest path that must hold the written file afterwards,
		// resolved inside staging.
		inside string
	}{
		{"manifest through an etc link", map[string]string{"etc": "HOSTDIR"}, func(staging string) error {
			return InjectManifest(staging, api.AppManifest{Entrypoint: []string{"/app/server"}, Port: 8080})
		}, "/etc/faas/app.json"},
		{"manifest onto a final link", map[string]string{"etc/faas/app.json": "SECRET"}, func(staging string) error {
			return InjectManifest(staging, api.AppManifest{Entrypoint: []string{"/app/server"}, Port: 8080})
		}, "/etc/faas/app.json"},
		{"guest-init through an sbin link", map[string]string{"sbin": "HOSTDIR"}, func(staging string) error {
			return InjectGuestInit(staging, guestInit)
		}, "/sbin/init"},
		{"guest-init onto a final link", map[string]string{"sbin/init": "SECRET"}, func(staging string) error {
			return InjectGuestInit(staging, guestInit)
		}, "/sbin/init"},
		{"function runner onto a final link", map[string]string{"usr/local/bin/faas-runner": "SECRET"}, func(staging string) error {
			return InjectFunctionRunner(staging, runner)
		}, "/usr/local/bin/faas-runner"},
		{"passwd table through an etc/faas link", map[string]string{"etc/faas": "HOSTDIR"}, func(staging string) error {
			return writePasswdTable(staging, map[string]PasswdEntry{"app": {Name: "app", Uid: 1000, Gid: 1000}}, 16)
		}, passwdTablePath},
		{"full-rootfs marker onto a final link", map[string]string{api.FullRootfsMarkerPath[1:]: "SECRET"}, func(staging string) error {
			return writeFullRootfsMarker(staging)
		}, api.FullRootfsMarkerPath},
		{"workload manifest through an etc link", map[string]string{"etc": "HOSTDIR"}, func(staging string) error {
			return InjectWorkloadManifest(staging, "worker", api.AppManifest{Entrypoint: []string{"/app/worker"}, Port: 9090})
		}, filepath.Join(api.SidecarWorkloadManifestPath, "worker", "workload.json")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hostDir, secret := hostFixture(t)
			staging := t.TempDir()
			for guestPath, target := range tc.links {
				target = strings.NewReplacer("HOSTDIR", hostDir, "SECRET", secret).Replace(target)
				plantLink(t, staging, guestPath, target)
			}
			if err := tc.run(staging); err != nil {
				t.Fatal(err)
			}
			assertHostUntouched(t, hostDir, secret)
			p, info, err := lstatStaging(staging, tc.inside)
			if err != nil || !info.Mode().IsRegular() {
				t.Fatalf("%s: %v, %v; want a regular file inside staging", tc.inside, info, err)
			}
			if rel, err := filepath.Rel(staging, p); err != nil || strings.HasPrefix(rel, "..") {
				t.Fatalf("%s resolved to %s, outside staging", tc.inside, p)
			}
		})
	}
}

func TestEnsureBaseMountpointsStayInsideStaging(t *testing.T) {
	hostDir, secret := hostFixture(t)
	staging := t.TempDir()
	plantLink(t, staging, "sys", hostDir)
	if err := ensureBaseMountpoints(staging); err != nil {
		t.Fatal(err)
	}
	assertHostUntouched(t, hostDir, secret)
}

func TestNormalizeFunctionHandlerDoesNotCopyHostFiles(t *testing.T) {
	_, secret := hostFixture(t)
	staging := t.TempDir()
	plantLink(t, staging, "app/server", secret)

	err := NormalizeFunctionHandlerFrom(staging, "/app/server", "/app/handler")
	if err == nil {
		t.Fatal("a handler source linking out of the image was copied")
	}
	if data, readErr := os.ReadFile(filepath.Join(staging, "app", "handler")); readErr == nil && strings.Contains(string(data), hostSecret) {
		t.Fatal("the host file was copied into the image")
	}
}

func TestApplyTarballDoesNotUnpackThroughAnAppLink(t *testing.T) {
	hostDir, secret := hostFixture(t)
	dir := t.TempDir()
	staging := filepath.Join(dir, "staging")
	plantLink(t, staging, "app", hostDir)
	tarball := filepath.Join(dir, "src.tar.gz")
	writeGzTar(t, tarball, "handler.js", []byte("export const handler = async () => ({});"))

	if err := ApplyTarball(staging, tarball, 1024*1024); err != nil {
		t.Fatal(err)
	}
	assertHostUntouched(t, hostDir, secret)
}

func TestParsePasswdErrorsDoNotQuoteTheLine(t *testing.T) {
	_, err := ParsePasswd(strings.NewReader(hostSecret + "\n"))
	if err == nil {
		t.Fatal("a malformed line parsed")
	}
	if strings.Contains(err.Error(), hostSecret) {
		t.Fatalf("parse error quotes the line: %v", err)
	}
}
