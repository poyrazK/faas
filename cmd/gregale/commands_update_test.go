package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/wire"
)

// fakeReleases serves cli-channel pointers and release assets the way
// GitHub's releases/download does. Each release's binary is a shell script
// that prints the version it reports.
type fakeReleases struct {
	t        *testing.T
	stable   string
	latest   string
	reports  map[string]string // tag -> what `version` prints; default "gregale <tag>"
	badSum   map[string]bool
	mu       sync.Mutex
	archives int
}

func (f *fakeReleases) archive(tag string) []byte {
	report, ok := f.reports[tag]
	if !ok {
		report = "gregale " + tag
	}
	script := []byte("#!/bin/sh\necho '" + report + "'\n")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "gregale", Mode: 0o755, Size: int64(len(script)), Typeflag: tar.TypeReg}); err != nil {
		f.t.Fatal(err)
	}
	if _, err := tw.Write(script); err != nil {
		f.t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		f.t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		f.t.Fatal(err)
	}
	return buf.Bytes()
}

func (f *fakeReleases) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	tag, asset := parts[0], parts[1]
	if tag == "cli-channel" {
		pointer := map[string]string{"CLI-STABLE": f.stable, "CLI-LATEST": f.latest}[asset]
		if pointer == "" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintln(w, pointer)
		return
	}
	name := fmt.Sprintf("gregale_%s_%s_%s.tar.gz", strings.TrimPrefix(tag, "v"), updateGOOS, updateGOARCH)
	switch asset {
	case "CLI-SHA256SUMS":
		sum := sha256.Sum256(f.archive(tag))
		digest := hex.EncodeToString(sum[:])
		if f.badSum[tag] {
			digest = strings.Repeat("0", 64)
		}
		_, _ = fmt.Fprintf(w, "%s  %s\n%s  gregale_%s_plan9_amd64.tar.gz\n", digest, name, strings.Repeat("1", 64), strings.TrimPrefix(tag, "v"))
	case name:
		f.mu.Lock()
		f.archives++
		f.mu.Unlock()
		_, _ = w.Write(f.archive(tag))
	default:
		http.NotFound(w, r)
	}
}

type updateRun struct {
	code   int
	stdout string
	stderr string
}

// setupUpdate installs a fake current binary reporting current at a temp
// path and points the updater at a fake release server.
func setupUpdate(t *testing.T, current string, releases *fakeReleases, exeRel string) (exe string, run func(args ...string) updateRun) {
	t.Helper()
	if updateGOOS != "darwin" && updateGOOS != "linux" {
		t.Skip("gregale releases ship darwin and linux only")
	}
	releases.t = t
	srv := httptest.NewServer(releases)
	t.Cleanup(srv.Close)
	t.Setenv("GREGALE_DOWNLOAD_BASE", srv.URL)
	t.Setenv("GREGALE_CHANNEL_BASE", "")

	exe = filepath.Join(t.TempDir(), exeRel)
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldVersion, oldExe, oldOut, oldErr := wire.Version, updateExecutable, osStdout, osStderr
	t.Cleanup(func() {
		wire.Version, updateExecutable, osStdout, osStderr = oldVersion, oldExe, oldOut, oldErr
		resetJSONOutput()
	})
	wire.Version = current
	updateExecutable = func() (string, error) { return exe, nil }
	return exe, func(args ...string) updateRun {
		var stdout, stderr bytes.Buffer
		osStdout, osStderr = &stdout, &stderr
		code := cmdUpdate(args)
		return updateRun{code, stdout.String(), stderr.String()}
	}
}

func readUpdateFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUpdateReplacesBinaryWithNewestStable(t *testing.T) {
	releases := &fakeReleases{stable: "v0.2.0", latest: "v0.3.0-rc.1"}
	exe, run := setupUpdate(t, "v0.1.0", releases, "bin/gregale")
	got := run()
	if got.code != 0 {
		t.Fatalf("exit %d: %s%s", got.code, got.stdout, got.stderr)
	}
	if !strings.Contains(readUpdateFile(t, exe), "gregale v0.2.0") {
		t.Fatalf("binary not replaced with v0.2.0: %q", readUpdateFile(t, exe))
	}
	if !strings.Contains(got.stdout, "from v0.1.0 to v0.2.0") {
		t.Fatalf("stdout = %q", got.stdout)
	}
	if info, err := os.Stat(exe); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("replaced binary is not executable: %v %v", info, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(exe))
	if len(entries) != 1 {
		t.Fatalf("temporary files left beside the binary: %v", entries)
	}
}

func TestUpdatePrereleaseFollowsNewestRelease(t *testing.T) {
	releases := &fakeReleases{stable: "v0.2.0", latest: "v0.3.0-rc.2"}
	exe, run := setupUpdate(t, "v0.3.0-rc.1", releases, "gregale")
	if got := run(); got.code != 0 {
		t.Fatalf("exit %d: %s", got.code, got.stderr)
	}
	if !strings.Contains(readUpdateFile(t, exe), "v0.3.0-rc.2") {
		t.Fatalf("prerelease install did not follow CLI-LATEST: %q", readUpdateFile(t, exe))
	}
}

func TestUpdateFallsBackToLatestWithoutStable(t *testing.T) {
	releases := &fakeReleases{latest: "v0.1.18-rc.337"}
	exe, run := setupUpdate(t, "v0.1.18-rc.300", releases, "gregale")
	if got := run(); got.code != 0 {
		t.Fatalf("exit %d: %s", got.code, got.stderr)
	}
	if !strings.Contains(readUpdateFile(t, exe), "v0.1.18-rc.337") {
		t.Fatalf("binary = %q", readUpdateFile(t, exe))
	}
}

func TestUpdateAlreadyNewestDownloadsNothing(t *testing.T) {
	releases := &fakeReleases{stable: "v0.2.0", latest: "v0.3.0-rc.1"}
	exe, run := setupUpdate(t, "0.2.0", releases, "gregale")
	got := run()
	if got.code != 0 || !strings.Contains(got.stdout, "newest release") {
		t.Fatalf("exit %d stdout %q stderr %q", got.code, got.stdout, got.stderr)
	}
	if releases.archives != 0 || readUpdateFile(t, exe) != "old binary" {
		t.Fatalf("an up-to-date CLI downloaded %d archives", releases.archives)
	}
	// A stable install never moves backwards on its own either.
	wire.Version = "v0.9.0"
	if got := run(); got.code != 0 || releases.archives != 0 {
		t.Fatalf("newer-than-channel install: exit %d, %d archives", got.code, releases.archives)
	}
}

func TestUpdateCheckReportsWithoutInstalling(t *testing.T) {
	releases := &fakeReleases{stable: "v0.2.0"}
	exe, run := setupUpdate(t, "v0.1.0", releases, "gregale")
	jsonOutput = true
	got := run("--check")
	if got.code != 0 {
		t.Fatalf("exit %d: %s", got.code, got.stderr)
	}
	var report updateReport
	if err := json.Unmarshal([]byte(got.stdout), &report); err != nil {
		t.Fatalf("stdout is not JSON: %q", got.stdout)
	}
	if !report.UpdateAvailable || report.Updated || report.Latest != "v0.2.0" || report.Current != "v0.1.0" || report.Channel != "curl" || report.Next != "gregale update" {
		t.Fatalf("report = %+v", report)
	}
	if releases.archives != 0 || readUpdateFile(t, exe) != "old binary" {
		t.Fatal("--check installed something")
	}
}

func TestUpdateChecksumMismatchKeepsOldBinary(t *testing.T) {
	releases := &fakeReleases{stable: "v0.2.0", badSum: map[string]bool{"v0.2.0": true}}
	exe, run := setupUpdate(t, "v0.1.0", releases, "gregale")
	got := run()
	if got.code == 0 || !strings.Contains(got.stderr, "checksum mismatch") {
		t.Fatalf("exit %d stderr %q", got.code, got.stderr)
	}
	if readUpdateFile(t, exe) != "old binary" {
		t.Fatal("binary replaced despite a checksum mismatch")
	}
}

func TestUpdateRefusesBinaryReportingAnotherVersion(t *testing.T) {
	releases := &fakeReleases{stable: "v0.2.0", reports: map[string]string{"v0.2.0": "gregale v0.1.9"}}
	exe, run := setupUpdate(t, "v0.1.0", releases, "gregale")
	got := run()
	if got.code == 0 || !strings.Contains(got.stderr, "unchanged") {
		t.Fatalf("exit %d stderr %q", got.code, got.stderr)
	}
	if readUpdateFile(t, exe) != "old binary" {
		t.Fatal("binary replaced by one reporting the wrong version")
	}
	entries, _ := os.ReadDir(filepath.Dir(exe))
	if len(entries) != 1 {
		t.Fatalf("temporary files left beside the binary: %v", entries)
	}
}

func TestUpdateExplicitVersionMayDowngrade(t *testing.T) {
	releases := &fakeReleases{stable: "v0.2.0"}
	exe, run := setupUpdate(t, "v0.2.0", releases, "gregale")
	if got := run("--version", "0.1.5"); got.code != 0 {
		t.Fatalf("exit %d: %s", got.code, got.stderr)
	}
	if !strings.Contains(readUpdateFile(t, exe), "gregale v0.1.5") {
		t.Fatalf("binary = %q", readUpdateFile(t, exe))
	}
	if got := run("--version", "v0.1.5/../x"); got.code == 0 {
		t.Fatal("a tag that is not a release tag was accepted")
	}
}

func TestUpdateNPMInstallPrintsNPMCommand(t *testing.T) {
	releases := &fakeReleases{stable: "v0.2.0"}
	exe, run := setupUpdate(t, "v0.1.0", releases, "lib/node_modules/@gregale/cli-darwin-arm64/bin/gregale")
	got := run()
	if got.code == 0 || !strings.Contains(got.stderr, "npm install -g gregale@0.2.0") {
		t.Fatalf("exit %d stderr %q", got.code, got.stderr)
	}
	if releases.archives != 0 || readUpdateFile(t, exe) != "old binary" {
		t.Fatal("updated files npm owns")
	}
}

func TestUpdateDevBuildNeedsExplicitVersion(t *testing.T) {
	releases := &fakeReleases{stable: "v0.2.0"}
	exe, run := setupUpdate(t, "dev", releases, "gregale")
	if got := run(); got.code == 0 || !strings.Contains(got.stderr, "--version") {
		t.Fatalf("exit %d stderr %q", got.code, got.stderr)
	}
	if readUpdateFile(t, exe) != "old binary" {
		t.Fatal("dev build replaced without --version")
	}
	if got := run("--version", "v0.2.0"); got.code != 0 || !strings.Contains(readUpdateFile(t, exe), "v0.2.0") {
		t.Fatalf("exit %d stderr %q", got.code, got.stderr)
	}
}

func TestUpdateRefusesPlainHTTPOffLoopback(t *testing.T) {
	for base, ok := range map[string]bool{
		"https://github.com/poyrazK/faas/releases/download": true,
		"http://127.0.0.1:8080":                             true,
		"http://localhost:8080/x":                           true,
		"http://github.com/poyrazK/faas/releases/download":  false,
		"ftp://example.com":                                 false,
		"not a url":                                         false,
	} {
		if err := updateCheckBase(base); (err == nil) != ok {
			t.Errorf("updateCheckBase(%q) = %v, want ok=%v", base, err, ok)
		}
	}
}

func TestUpdateChecksumFor(t *testing.T) {
	digest := strings.Repeat("ab", 32)
	sums := []byte(digest + "  gregale_0.2.0_linux_amd64.tar.gz\n" + digest + " *gregale_0.2.0_darwin_arm64.tar.gz\nshort  gregale_0.2.0_linux_arm64.tar.gz\n")
	for name, ok := range map[string]bool{
		"gregale_0.2.0_linux_amd64.tar.gz":  true,
		"gregale_0.2.0_darwin_arm64.tar.gz": true,
		"gregale_0.2.0_linux_arm64.tar.gz":  false,
		"gregale_0.2.0":                     false,
	} {
		if got, found := updateChecksumFor(sums, name); found != ok || (ok && got != digest) {
			t.Errorf("updateChecksumFor(%q) = %q, %v", name, got, found)
		}
	}
}
