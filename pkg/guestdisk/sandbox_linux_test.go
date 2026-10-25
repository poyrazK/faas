//go:build linux

package guestdisk

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain lets the test binary stand in for vmmd-guestdisk: a sandboxed
// run re-executes it with the helper's arguments.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--uid" {
		os.Exit(SandboxMain(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func sandboxedSession(t *testing.T, img string) *Session {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(t.TempDir(), "scratch")
	if os.Geteuid() == 0 {
		// Root's sandbox masks /tmp, where t.TempDir lives, and runs the
		// tools as the image owner: give it a scratch root and an image
		// outside the masked directories, owned by an unprivileged uid.
		scratch = filepath.Join(sandboxTestRoot(t), "scratch")
		img = moveImage(t, img, sandboxTestRoot(t))
		if err := os.Chown(img, 23456, 23456); err != nil {
			t.Fatal(err)
		}
	}
	ed, err := New(Config{
		Helper:      exe,
		Debugfs:     e2fsTool(t, "debugfs"),
		E2fsck:      e2fsTool(t, "e2fsck"),
		ScratchRoot: scratch,
		AllowOwner:  func(uid, gid int) bool { return uid == 23456 && gid == 23456 },
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := ed.Open(img)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// moveImage copies img into dir: /tmp may be its own filesystem.
func moveImage(t *testing.T, img, dir string) string {
	t.Helper()
	data, err := os.ReadFile(img)
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(dir, "layer.ext4")
	if err := os.WriteFile(moved, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return moved
}

// sandboxTestRoot is a directory outside /tmp for root runs.
func sandboxTestRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/var/lib", "guestdisk-test-")
	if err != nil {
		t.Skipf("no directory outside /tmp for a root sandbox test: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestSandboxedEdit(t *testing.T) {
	img := mkImage(t, func(root string) { write(t, filepath.Join(root, "upper/app/a"), "a") })
	s := sandboxedSession(t, img)
	ctx := context.Background()
	if _, err := s.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteFiles(ctx, []File{{Path: "/upper/etc/faas/secrets.env", Data: []byte("s"), Mode: 0o400}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveAll(ctx, []string{"/upper/app"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DumpTree(ctx, "/upper/etc"); err != nil {
		t.Fatal(err)
	}
	fsckClean(t, s.image)
}

// TestSandboxConfinesFilesystem runs cat in the sandbox against a file the
// ruleset does not cover: Landlock must refuse it.
func TestSandboxConfinesFilesystem(t *testing.T) {
	if LandlockABI() == 0 {
		t.Skip("kernel has no Landlock")
	}
	cat := "/usr/bin/cat"
	if !fileExists(cat) {
		t.Skip("no /usr/bin/cat")
	}
	img := mkImage(t, nil)
	s := sandboxedSession(t, img)
	secret := filepath.Join(sandboxTestRootOrTemp(t), "host-secret")
	if err := os.WriteFile(secret, []byte("host"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := s.run(context.Background(), cat, []string{secret}, nil, runLimits{output: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if res.exit == 0 || strings.Contains(string(res.stdout), "host") {
		t.Fatalf("sandboxed cat read %s: exit %d stdout %q", secret, res.exit, res.stdout)
	}
	// The image itself stays readable.
	res, err = s.run(context.Background(), cat, []string{s.image}, nil, runLimits{output: 64 << 20})
	if err != nil || res.exit != 0 {
		t.Fatalf("sandboxed cat could not read the image: exit %d %v %s", res.exit, err, res.stderr)
	}
}

func sandboxTestRootOrTemp(t *testing.T) string {
	if os.Geteuid() == 0 {
		return sandboxTestRoot(t)
	}
	return t.TempDir()
}

// TestSandboxDropsRoot checks the tool runs as the image owner with no
// network: id prints the credentials and /proc/net/dev lists only lo.
func TestSandboxDropsRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to create namespaces and change uid")
	}
	img := mkImage(t, nil)
	s := sandboxedSession(t, img)
	res, err := s.run(context.Background(), "/usr/bin/id", nil, nil, runLimits{output: 4096})
	if err != nil || res.exit != 0 {
		t.Fatalf("id: exit %d %v %s", res.exit, err, res.stderr)
	}
	if got := strings.TrimSpace(string(res.stdout)); !strings.HasPrefix(got, "uid=23456 gid=23456 ") || strings.Contains(got, "groups=0") {
		t.Fatalf("sandbox credentials = %q", got)
	}
	res, err = s.run(context.Background(), "/usr/bin/cat", []string{"/proc/net/dev"}, nil, runLimits{output: 64 << 10})
	if err != nil || res.exit != 0 {
		t.Fatalf("cat /proc/net/dev: exit %d %v %s", res.exit, err, res.stderr)
	}
	for _, line := range strings.Split(string(res.stdout), "\n")[2:] {
		if name, _, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && name != "lo" {
			t.Fatalf("sandbox sees network interface %q", name)
		}
	}
	res, err = s.run(context.Background(), "/usr/bin/cat", []string{"/proc/self/mountinfo"}, nil, runLimits{output: 1 << 20})
	if err != nil || res.exit != 0 {
		t.Fatalf("cat mountinfo: exit %d %v %s", res.exit, err, res.stderr)
	}
	for _, dir := range sandboxMaskedDirs {
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		masked := false
		for _, line := range strings.Split(string(res.stdout), "\n") {
			fields := strings.Fields(line)
			if len(fields) > 9 && fields[4] == dir && strings.Contains(line, " - tmpfs ") && strings.HasPrefix(fields[5], "ro") {
				masked = true
			}
		}
		if !masked {
			t.Errorf("%s is not masked by a read-only tmpfs in the sandbox", dir)
		}
	}
}

func TestOpenRefusesUnvouchedOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("owner checks apply to root callers")
	}
	img := mkImage(t, nil)
	exe, _ := os.Executable()
	root := sandboxTestRoot(t)
	moved := moveImage(t, img, root)
	ed, err := New(Config{Helper: exe, Debugfs: e2fsTool(t, "debugfs"), E2fsck: e2fsTool(t, "e2fsck"), ScratchRoot: filepath.Join(root, "s")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ed.Open(moved); err == nil || !strings.Contains(err.Error(), "owned by root") {
		t.Fatalf("root-owned image: err = %v", err)
	}
	if err := os.Chown(moved, 23456, 23456); err != nil {
		t.Fatal(err)
	}
	if _, err := ed.Open(moved); err == nil || !strings.Contains(err.Error(), "not an allowed sandbox identity") {
		t.Fatalf("unvouched owner: err = %v", err)
	}
}

func TestHelperRefusesHostMountNamespace(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("masking applies to root")
	}
	if err := maskHostPaths(); err == nil || !strings.Contains(err.Error(), "private mount namespace") {
		t.Fatalf("maskHostPaths in the host namespace: %v", err)
	}
}

func TestParseHelperArgs(t *testing.T) {
	spec, err := parseHelperArgs([]string{"--uid", "5", "--gid", "6", "--image", "/i", "--fsize", "10", "--read", "/r", "--write", "/w", "--require-landlock", "--", "/sbin/debugfs", "-R", "stat /"})
	if err != nil {
		t.Fatal(err)
	}
	if spec.uid != 5 || spec.gid != 6 || spec.image != "/i" || spec.fsize != 10 || spec.readDirs[0] != "/r" || spec.writeDirs[0] != "/w" ||
		!spec.requireLandlock || spec.tool != "/sbin/debugfs" || len(spec.args) != 2 {
		t.Fatalf("spec = %+v", spec)
	}
	for _, bad := range [][]string{
		{"--uid", "5", "--gid", "6", "--image", "/i", "--fsize", "10"},
		{"--uid", "5", "--gid", "6", "--image", "rel", "--fsize", "10", "--", "/t"},
		{"--uid", "5", "--gid", "6", "--image", "/i", "--fsize", "10", "--", "rel"},
		{"--bogus", "1", "--", "/t"},
		{"--uid"},
	} {
		if _, err := parseHelperArgs(bad); err == nil {
			t.Errorf("parseHelperArgs(%q) accepted", bad)
		}
	}
}
