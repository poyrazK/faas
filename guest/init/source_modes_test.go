package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestBuildSourceModesPreserveExecutablesAndSkipSymlinks(t *testing.T) {
	root := t.TempDir()
	for _, file := range []struct {
		name string
		mode os.FileMode
		want os.FileMode
	}{{"build.sh", 0o755, 0o777}, {"package.json", 0o644, 0o666}, {"private-tool", 0o700, 0o766}, {"apps/api/build.sh", 0o755, 0o777}, {"packages/shared/generate.sh", 0o700, 0o766}} {
		path := filepath.Join(root, file.name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("source"), file.mode); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "external-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", filepath.Join(root, "dangling-link")); err != nil {
		t.Fatal(err)
	}
	if err := prepareBuildSourceModes(root); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]os.FileMode{"build.sh": 0o777, "package.json": 0o666, "private-tool": 0o766, "apps/api/build.sh": 0o777, "packages/shared/generate.sh": 0o766, "apps/api": 0o777, "packages/shared": 0o777} {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s: mode=%o want=%o", name, info.Mode().Perm(), want)
		}
	}
	info, err := os.Stat(outside)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("symlink target changed to %o", info.Mode().Perm())
	}
}

// The CLI packs every entry at the Unix epoch; build tools that copy only
// newer sources skip such files, so extraction moves them to a stable date.
func TestBuildSourceModesMoveEpochTimes(t *testing.T) {
	root := t.TempDir()
	epoch, kept := filepath.Join(root, "src/main/resources/data/tags.json"), filepath.Join(root, "pom.xml")
	for _, path := range []string{epoch, kept} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	zero := time.Unix(0, 0)
	for _, path := range []string{epoch, filepath.Dir(epoch)} {
		if err := os.Chtimes(path, zero, zero); err != nil {
			t.Fatal(err)
		}
	}
	recent := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(kept, recent, recent); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "current")
	if err := os.Symlink("pom.xml", link); err != nil {
		t.Fatal(err)
	}
	zeroTV := unix.NsecToTimeval(0)
	if err := unix.Lutimes(link, []unix.Timeval{zeroTV, zeroTV}); err != nil {
		t.Fatal(err)
	}
	if err := prepareBuildSourceModes(root); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil {
		t.Fatal(err)
	} else if !info.ModTime().Equal(buildSourceStableTime) {
		t.Errorf("symlink mtime %s, want %s", info.ModTime().UTC(), buildSourceStableTime)
	}
	if buildSourceStableTime.In(time.FixedZone("UTC-12", -12*3600)).Year() < 1980 {
		t.Errorf("stable time %s is before 1980 west of UTC; ZIP writers reject it", buildSourceStableTime)
	}
	for path, want := range map[string]time.Time{epoch: buildSourceStableTime, filepath.Dir(epoch): buildSourceStableTime, kept: recent} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(want) {
			t.Errorf("%s: mtime %s, want %s", path, info.ModTime().UTC(), want)
		}
	}
}
