// Package templates ships the six `faas deploy --template <name>`
// starter projects as an embed.FS so the CLI is a single static
// binary. Precedent: migrations/embed.go:13 — `//go:embed` pulls in
// the sibling subdirectories at compile time.
//
// Each template is a self-contained tar.gz-able directory with the
// minimum surface for a happy-path first deploy: a handler on :8080
// (matches guest-init's expected port, guest/init/main.go), a manifest
// file when needed, and a README that points at the next faas CLI
// command the customer will run.
//
// Note on hello-go: the template ships WITHOUT a go.mod because Go's
// //go:embed refuses to descend into a directory that contains one —
// it treats the file as a module boundary. imaged auto-creates a
// go.mod at build time, so the missing file is invisible to the
// customer. See hello-go/README.md for the full rationale.
package templates

import (
	"archive/tar"
	"compress/gzip"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// FS holds the embedded starter projects. The root is the directory
// this file lives in, so subdirs are accessed by their template name.
//
//go:embed hello-node hello-python hello-go cron-example function-node function-python
var FS embed.FS

// Names is the canonical template list, kept here so the CLI can
// validate --template before touching the embed FS.
var Names = []string{
	"hello-node",
	"hello-python",
	"hello-go",
	"cron-example",
	"function-node",
	"function-python",
}

// Exists reports whether name is a known template.
func Exists(name string) bool {
	if !NameIsValid(name) {
		return false
	}
	for _, n := range Names {
		if n == name {
			return true
		}
	}
	return false
}

// sub returns an fs.FS rooted at the named template. Returns an error
// if the name isn't a known template (so callers don't have to
// re-validate). Internal — public callers use Materialize/TarGz.
func sub(name string) (fs.FS, error) {
	if !Exists(name) {
		return nil, fmt.Errorf("unknown template %q", name)
	}
	return fs.Sub(FS, name)
}

// Materialize copies the template named name into dest. dest should be
// an empty directory (the CLI uses os.MkdirTemp; tests use
// MaterializeForTest). Skips dotfiles — the embed FS shouldn't have
// any today, but if a future template adds one we don't want it
// polluting the customer's repo.
func Materialize(name, dest string) error {
	subFS, err := sub(name)
	if err != nil {
		return err
	}
	return os.CopyFS(dest, subFS)
}

// TarGz materializes the template and writes a tar.gz to dest. The
// top-level directory in the archive is `name/` so `tar -xzf` produces
// a single `name/` folder instead of dumping files into cwd.
func TarGz(name, dest string) error {
	if !Exists(name) {
		return fmt.Errorf("unknown template %q", name)
	}
	rootFS, err := sub(name)
	if err != nil {
		return err
	}
	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("could not create %s: %w", dest, err)
	}
	defer func() { _ = f.Close() }()
	gz := gzip.NewWriter(f)
	defer func() { _ = gz.Close() }()
	tw := tar.NewWriter(gz)
	defer func() { _ = tw.Close() }()

	var entries []string
	if err := fs.WalkDir(rootFS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." || strings.HasPrefix(path.Base(p), ".") {
			if d.IsDir() && p != "." {
				return fs.SkipDir
			}
			return nil
		}
		entries = append(entries, p)
		return nil
	}); err != nil {
		return err
	}
	sort.Strings(entries)
	for _, p := range entries {
		info, err := fs.Stat(rootFS, p)
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = name + "/" + filepath.ToSlash(p)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if err := copyFromFS(tw, rootFS, p); err != nil {
			return err
		}
	}
	return nil
}

func copyFromFS(w io.Writer, root fs.FS, name string) error {
	f, err := root.Open(name)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(w, f)
	return err
}

// MaterializeForTest copies a single template into a fresh tempdir and
// returns the dir + a cleanup func. Tests use this to assert the embed
// FS round-trips through `tar -xzf`.
func MaterializeForTest(name string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "faas-tpl-test-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err := Materialize(name, dir); err != nil {
		cleanup()
		return "", nil, err
	}
	return dir, cleanup, nil
}

// NameIsValid returns true if name contains only characters safe for
// a tar header prefix. Defensive — Names is hard-coded today but the
// CLI's --template is user input.
func NameIsValid(name string) bool {
	if name == "" || strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
		return false
	}
	return true
}
