package imaged

// Issue #299 scan gate: a scan the scanner cannot see must fail closed.
// Grype skips tmpfs under /dev (and /run, /sys, ...), so a view staged under
// a /dev/shm TMPDIR catalogued nothing and returned a clean result.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/scanview"
)

func TestScannerSkippedRootMatchesSyftContainment(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/dev/shm/gregale-e2e-tmp/imaged-scan-view-1", "/dev"},
		{"/dev", "/dev"},
		{"/devices/imaged-scan-view-1", ""},
		{"/run/user/1000/imaged-scan-view-1", "/run"},
		{"/var/lock/imaged-scan-view-1", "/var/lock"},
		{"/sys/fs/cgroup", "/sys"},
		{"/proc/1/root", "/proc"},
		{"/srv/fc/base/imaged-scan-view-1", ""},
		{"/var/lib/faas/imaged/tmp/imaged-scan-view-1", ""},
		{"/tmp/imaged-scan-view-1", ""},
		{"/", ""},
	}
	for _, tc := range cases {
		got, ok := scannerSkippedRoot(tc.path)
		if got != tc.want || ok != (tc.want != "") {
			t.Errorf("scannerSkippedRoot(%q) = %q, %v; want %q", tc.path, got, ok, tc.want)
		}
	}
}

func TestDefaultGrypeDispatchRefusesInvisibleView(t *testing.T) {
	source := scanViewSourceFixture(t)
	parent, err := filepath.EvalSymlinks(filepath.Dir(source))
	if err != nil {
		t.Fatal(err)
	}
	saved := scannerSkippedDirs
	scannerSkippedDirs = []string{parent}
	t.Cleanup(func() { scannerSkippedDirs = saved })
	expected, err := scanview.Snapshot(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	bin, record := viewScannerFixture(t, "")

	result, err := runGrypeImpl(t.Context(), bin, source)
	if result != nil || !errors.Is(err, errScanDirInvisible) {
		t.Fatalf("scan of an invisible view = %+v, %v; want errScanDirInvisible", result, err)
	}
	if _, statErr := os.Stat(record); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("the scanner ran against a view it cannot see")
	}
	entries, err := os.ReadDir(filepath.Dir(source))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "imaged-scan-view-") {
			t.Fatal("refused view was not removed:", entry.Name())
		}
	}
	if after, err := scanview.Snapshot(t.Context(), source); err != nil || after != expected {
		t.Fatal("refusal changed the input filesystem", err)
	}
}

func TestCheckScannerVisibleResolvesSymlinks(t *testing.T) {
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, skipped := scannerSkippedRoot(real); skipped {
		t.Skipf("TMPDIR %s is itself invisible to the scanner", real)
	}
	if err := checkScannerVisible(dir); err != nil {
		t.Fatalf("disk-backed directory refused: %v", err)
	}
	// /var/run and /var/lock reach /run through symlinks; the real path
	// decides, not the spelling.
	link := filepath.Join(dir, "into-dev")
	if err := os.Symlink("/dev", link); err != nil {
		t.Fatal(err)
	}
	if err := checkScannerVisible(link); !errors.Is(err, errScanDirInvisible) {
		t.Fatalf("symlink into /dev = %v; want errScanDirInvisible", err)
	}
}
