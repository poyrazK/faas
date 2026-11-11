package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/manifest"
	"github.com/onebox-faas/faas/pkg/releaseinstall"
)

// stageVerifiedRelease writes a release whose manifest matches its binaries
// and points current at it.
func stageVerifiedRelease(t *testing.T, releasesRoot, gitSHA string) {
	t.Helper()
	bin := releaseinstall.BinDir(releasesRoot, gitSHA)
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range manifest.SortedHostKeys() {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("binary "+name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m, err := releaseinstall.Build(releasesRoot, gitSHA, "sha256:"+strings.Repeat("a", 64), time.Unix(1_700_000_000, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := releaseinstall.Write(releasesRoot, m); err != nil {
		t.Fatal(err)
	}
	current := releaseinstall.CurrentSymlink(releasesRoot)
	_ = os.Remove(current)
	if err := os.Symlink(releaseinstall.BundleRoot(releasesRoot, gitSHA), current); err != nil {
		t.Fatal(err)
	}
}

// fakeProc writes a /proc with one exe symlink per pid. A target ending in
// " (deleted)" is written as the kernel shows a replaced binary.
func fakeProc(t *testing.T, exes map[string]string) string {
	t.Helper()
	proc := t.TempDir()
	for pid, exe := range exes {
		if err := os.MkdirAll(filepath.Join(proc, pid), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(exe, filepath.Join(proc, pid, "exe")); err != nil {
			t.Fatal(err)
		}
	}
	return proc
}

func TestReleaseVerify(t *testing.T) {
	const sha = "1111111111111111111111111111111111111111"
	const old = "2222222222222222222222222222222222222222"
	daemon := manifest.SortedHostKeys()[0]
	cases := []struct {
		name   string
		mutate func(t *testing.T, root string)
		exes   func(root string) map[string]string
		want   string // "" for OK, else a substring of a failure
	}{
		{
			name: "clean",
			exes: func(root string) map[string]string {
				return map[string]string{
					"10": filepath.Join(releaseinstall.BinDir(root, sha), daemon),
					"11": "/usr/bin/sleep",
					"12": filepath.Join(filepath.Dir(root), "prestage", sha, "imaged"),
					"13": filepath.Join(filepath.Dir(root), "cloudflared", "2026.10.0", "cloudflared"),
				}
			},
		},
		{
			name: "binary changed on disk",
			mutate: func(t *testing.T, root string) {
				if err := os.WriteFile(filepath.Join(releaseinstall.BinDir(root, sha), daemon), []byte("tampered"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			want: "release " + sha,
		},
		{
			name: "process runs a replaced binary",
			exes: func(root string) map[string]string {
				return map[string]string{"10": filepath.Join(releaseinstall.BinDir(root, sha), daemon) + " (deleted)"}
			},
			want: "replaced or removed after it started",
		},
		{
			name: "process outside any release",
			exes: func(root string) map[string]string {
				return map[string]string{"10": filepath.Join(filepath.Dir(root), "hotfixes", "vmmd")}
			},
			want: "outside any installed release",
		},
		{
			name: "process from an older release that no longer verifies",
			mutate: func(t *testing.T, root string) {
				stageVerifiedRelease(t, root, old)
				stageVerifiedRelease(t, root, sha)
				if err := os.WriteFile(filepath.Join(releaseinstall.BinDir(root, old), daemon), []byte("tampered"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			exes: func(root string) map[string]string {
				return map[string]string{"10": filepath.Join(releaseinstall.BinDir(root, old), daemon)}
			},
			want: "from release " + old,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "faas", "releases")
			stageVerifiedRelease(t, root, sha)
			if tc.mutate != nil {
				tc.mutate(t, root)
			}
			exes := map[string]string{}
			if tc.exes != nil {
				exes = tc.exes(root)
			}
			report := releaseVerify(root, fakeProc(t, exes))
			joined := strings.Join(report.Failures, "\n")
			if tc.want == "" {
				if !report.OK() || report.ManifestDigest == "" || report.Processes != 1 {
					t.Fatalf("clean host reported %+v", report)
				}
				return
			}
			if report.OK() || !strings.Contains(joined, tc.want) {
				t.Fatalf("report %+v, want a failure containing %q", report, tc.want)
			}
		})
	}
}

func TestReleaseVerifyMetricsCarryTheSignedDigest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "faas", "releases")
	const sha = "3333333333333333333333333333333333333333"
	stageVerifiedRelease(t, root, sha)
	other := filepath.Join(t.TempDir(), "faas", "releases")
	stageVerifiedRelease(t, other, sha)
	a, b := releaseVerify(root, t.TempDir()), releaseVerify(other, t.TempDir())
	if !a.OK() || a.ManifestDigest != b.ManifestDigest {
		t.Fatalf("two hosts with one release disagree: %+v vs %+v", a, b)
	}
	body := releaseVerifyMetrics(a, time.Unix(42, 0))
	for _, want := range []string{
		"faas_release_integrity_ok 1\n",
		"faas_release_integrity_failures 0\n",
		"faas_release_integrity_checked_timestamp_seconds 42\n",
		`faas_release_manifest_info{release="` + sha + `",digest="` + a.ManifestDigest + `"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %q:\n%s", want, body)
		}
	}
	failed := releaseVerifyMetrics(releaseVerifyReport{Release: sha, Failures: []string{"x"}}, time.Unix(42, 0))
	if !strings.Contains(failed, "faas_release_integrity_ok 0\n") || strings.Contains(failed, "faas_release_manifest_info") {
		t.Errorf("failed run metrics:\n%s", failed)
	}
}
