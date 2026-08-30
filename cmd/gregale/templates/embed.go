// Package templates ships the thirteen `gregale deploy --template <name>`
// starter projects as an embed.FS so the CLI is a single static
// binary. Precedent: migrations/embed.go:13 — `//go:embed` pulls in
// the sibling subdirectories at compile time.
//
// Each template is a self-contained tar.gz-able directory with the
// minimum surface for a happy-path first deploy: a handler on :8080
// (matches guest-init's expected port, guest/init/main.go), a manifest
// file when needed, and a README that points at the next gregale CLI
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
//go:embed hello-node hello-python hello-go cron-example function-node function-python function-go s3-uploader slack-bot rest-api-postgres cron-worker webhook-receiver ai-chat
var FS embed.FS

// Names is the canonical template list, kept here so the CLI can
// validate --template before touching the embed FS. The seven
// "hello/function" scaffolds ship with `gregale deploy`; the six
// stateless-contract scaffolds (Wave 0 PR-B + Move 1 PR-A) are
// scaffolded by `gregale init` (commands_init.go) and tell the
// customer which managed service to plug in. Names must stay in
// lockstep with the //go:embed directive above — adding a template
// means a new entry in BOTH places.
var Names = []string{
	"hello-node",
	"hello-python",
	"hello-go",
	"cron-example",
	"function-node",
	"function-python",
	"function-go",
	"s3-uploader",
	"slack-bot",
	"rest-api-postgres",
	"cron-worker",
	"webhook-receiver",
	"ai-chat",
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
// MaterializeForTest). Delegates to os.CopyFS — no dotfile filtering,
// no header munging. TarGz is the path that wraps the result for the
// accept-time tarball scan; TarGz is the one that skips dotfiles.
func Materialize(name, dest string) error {
	subFS, err := sub(name)
	if err != nil {
		return err
	}
	if err := os.CopyFS(dest, subFS); err != nil {
		return err
	}
	if name == "hello-go" || name == "function-go" {
		modPath := filepath.Join(dest, "go.mod")
		if _, err := os.Stat(modPath); os.IsNotExist(err) {
			_ = os.WriteFile(modPath, []byte("module "+name+"\n\ngo 1.24\n"), 0o644)
		}
	}
	return nil
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
	if name == "hello-go" || name == "function-go" {
		modContent := []byte("module " + name + "\n\ngo 1.24\n")
		hdr := &tar.Header{
			Name:     name + "/go.mod",
			Mode:     0o644,
			Size:     int64(len(modContent)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(modContent); err != nil {
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
	dir, err := os.MkdirTemp("", "gregale-tpl-test-*")
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

// CategoryFor returns the customer-facing group label for a template
// name. Used by `gregale init --list` (commands_init.go) to bucket the
// 13 templates by what the customer is trying to do, not by alphabetical
// order. Recognised categories:
//
//	"hello"              — first-touch smoke tests (3)
//	"function"           — generic runtimes the customer customises (3)
//	"stateless-contract" — managed-service scaffolds that BYO credentials (5)
//	"ai"                 — LLM-facing scaffolds that BYO keys (1)
//	""                   — unknown / not in Names
//
// The grouping mirrors the template sections in the public docs site:
// hello/function are the "just run it" tier, stateless-contract
// scaffolds are the "BYO managed service" tier, ai is the LLM tier.
// Adding a template means a new entry in BOTH Names AND this switch.
func CategoryFor(name string) string {
	switch name {
	case "hello-node", "hello-python", "hello-go":
		return "hello"
	case "function-node", "function-python", "function-go", "cron-example":
		return "function"
	case "s3-uploader", "slack-bot", "rest-api-postgres", "cron-worker", "webhook-receiver":
		return "stateless-contract"
	case "ai-chat":
		return "ai"
	}
	return ""
}

// CategoryOrder is the canonical order in which `gregale init --list`
// prints categories. Pins against accidental reorders in CategoryFor.
var CategoryOrder = []string{"hello", "function", "stateless-contract", "ai"}
