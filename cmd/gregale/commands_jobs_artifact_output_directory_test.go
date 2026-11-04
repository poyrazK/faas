package main_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// symlinkedTempDir returns base/real and base/link, where link is a symlink
// to real, the shape of macOS /tmp and $TMPDIR or a symlinked checkout.
func symlinkedTempDir(t *testing.T) (base, real, link string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	base = t.TempDir()
	real, link = filepath.Join(base, "real"), filepath.Join(base, "link")
	if err := os.Mkdir(real, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	return base, real, link
}

func sameDirectory(t *testing.T, root *os.Root, path string) bool {
	t.Helper()
	opened, err := root.Stat(".")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return os.SameFile(opened, want)
}

// Output directories the user names may pass through symlinks they own:
// every component of /tmp/out on macOS used to be refused.
func TestArtifactOutputDirectoryFollowsTheUsersSymlinks(t *testing.T) {
	_, real, link := symlinkedTempDir(t)

	missing := filepath.Join(link, "results", "run")
	directory, err := main.InspectArtifactOutputDirectory(missing)
	if err != nil {
		t.Fatalf("inspect %s: %v", missing, err)
	}
	defer directory.Close()
	if want := []string{filepath.Join(link, "results"), missing}; !reflect.DeepEqual(directory.Planned(), want) {
		t.Fatalf("planned = %q, want %q in the caller's spelling", directory.Planned(), want)
	}
	if err := directory.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !sameDirectory(t, directory.Root(), filepath.Join(real, "results", "run")) {
		t.Fatal("created output directory is not under the symlink's target")
	}

	// An --out-dir that is itself a symlink to an existing directory.
	existing, err := main.InspectArtifactOutputDirectory(link)
	if err != nil {
		t.Fatalf("inspect %s: %v", link, err)
	}
	defer existing.Close()
	if existing.Root() == nil || len(existing.Planned()) != 0 || !sameDirectory(t, existing.Root(), real) {
		t.Fatalf("symlinked existing directory: root=%v planned=%q", existing.Root(), existing.Planned())
	}
}

// What the CLI creates or the server names must stay a real directory, so a
// symlink planted there cannot redirect writes out of the output directory.
func TestArtifactOutputDirectoryKeepsCLIAndServerNamesReal(t *testing.T) {
	base, real, link := symlinkedTempDir(t)
	elsewhere := filepath.Join(base, "elsewhere")
	if err := os.Mkdir(elsewhere, 0o750); err != nil {
		t.Fatal(err)
	}

	t.Run("project state directory", func(t *testing.T) {
		project := filepath.Join(real, "planted")
		if err := os.Mkdir(project, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, filepath.Join(project, ".gregale")); err != nil {
			t.Fatal(err)
		}
		if _, err := main.InspectArtifactOutputDirectory(filepath.Join(link, "planted", ".gregale")); err == nil {
			t.Fatal("a symlinked .gregale directory was adopted")
		}
		// A real .gregale under a symlinked checkout is fine.
		if err := os.MkdirAll(filepath.Join(real, "checkout", ".gregale"), 0o750); err != nil {
			t.Fatal(err)
		}
		directory, err := main.InspectArtifactOutputDirectory(filepath.Join(link, "checkout", ".gregale"))
		if err != nil {
			t.Fatalf("real .gregale under a symlinked checkout: %v", err)
		}
		directory.Close()
	})

	t.Run("dangling symlink", func(t *testing.T) {
		if err := os.Symlink(filepath.Join(base, "missing"), filepath.Join(real, "dangling")); err != nil {
			t.Fatal(err)
		}
		if _, err := main.InspectArtifactOutputDirectory(filepath.Join(link, "dangling", "out")); err == nil {
			t.Fatal("a dangling symlink was accepted as an output component")
		}
	})

	t.Run("component swapped before creation", func(t *testing.T) {
		target := filepath.Join(link, "fresh", "out")
		directory, err := main.InspectArtifactOutputDirectory(target)
		if err != nil {
			t.Fatal(err)
		}
		defer directory.Close()
		if err := os.Symlink(elsewhere, filepath.Join(real, "fresh")); err != nil {
			t.Fatal(err)
		}
		if err := directory.Create(context.Background()); err == nil {
			t.Fatal("creation adopted a symlink that appeared after inspection")
		}
		if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
			t.Fatalf("wrote through the planted symlink: %v", entries)
		}
	})

	t.Run("server-named task directory", func(t *testing.T) {
		out := filepath.Join(real, "out")
		if err := os.MkdirAll(filepath.Join(out, "task-1"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, filepath.Join(out, "task-0")); err != nil {
			t.Fatal(err)
		}
		directory, err := main.InspectArtifactOutputDirectory(filepath.Join(link, "out"))
		if err != nil {
			t.Fatal(err)
		}
		defer directory.Close()
		if root, err := main.OpenArtifactTaskDirectory(directory.Root(), "task-0"); err == nil {
			_ = root.Close()
			t.Fatal("a symlinked task directory was opened")
		}
		root, err := main.OpenArtifactTaskDirectory(directory.Root(), "task-1")
		if err != nil {
			t.Fatalf("real task directory: %v", err)
		}
		_ = root.Close()
	})
}

// `job defaults clear --dry-run` inspected <cwd>/.gregale and refused a
// working directory reached through a symlink.
func TestJobDefaultsClearPreviewFromSymlinkedDirectory(t *testing.T) {
	_, real, link := symlinkedTempDir(t)
	project := filepath.Join(link, "project")
	if err := os.Mkdir(filepath.Join(real, "project"), 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	var stdout, stderr bytes.Buffer
	oldOut, oldErr := *main.OsStdout, *main.OsStderr
	*main.OsStdout, *main.OsStderr = &stdout, &stderr
	defer func() { *main.OsStdout, *main.OsStderr = oldOut, oldErr }()
	code := main.Run([]string{"job", "defaults", "clear", "--dry-run"})
	if code != 0 || !strings.Contains(stdout.String(), "nothing to clear") {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
}
