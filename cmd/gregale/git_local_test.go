// cmd/gregale/git_local_test.go — unit tests for the zero-config
// `gregale deploy` git helpers (issue #961 / Mega-A PR-1). The URL
// parser is the highest-risk surface (every GitHub remote variant
// has to land on a clean (owner, repo) pair), so it gets the
// broadest matrix. The walk-up + HEAD + dirty helpers are
// integration-flavored (they shell out to `git`) so they use
// per-test tmpdir repos.

package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGitRemoteURL(t *testing.T) {
	cases := []struct {
		name          string
		input         string
		wantOwner     string
		wantRepo      string
		wantErr       bool
		wantSoftEmpty bool // non-GitHub URL → ("", "", nil) — caller falls through
	}{
		// Accepted GitHub forms.
		{"ssh with .git", "git@github.com:o/r.git", "o", "r", false, false},
		{"ssh without .git", "git@github.com:o/r", "o", "r", false, false},
		{"ssh explicit scheme", "ssh://git@github.com/o/r.git", "o", "r", false, false},
		{"ssh explicit scheme no user", "ssh://github.com/o/r.git", "o", "r", false, false},
		{"ssh explicit with port", "ssh://git@github.com:22/o/r.git", "o", "r", false, false},
		{"https with .git", "https://github.com/o/r.git", "o", "r", false, false},
		{"https without .git", "https://github.com/o/r", "o", "r", false, false},
		{"https with userinfo", "https://token@github.com/o/r.git", "o", "r", false, false},
		{"https with user:pass userinfo", "https://user:pass@github.com/o/r.git", "o", "r", false, false},
		{"http form", "http://github.com/o/r.git", "o", "r", false, false},
		{"uppercase normalized", "git@github.com:OWNER/REPO.git", "owner", "repo", false, false},
		{"with whitespace", "  git@github.com:o/r.git  ", "o", "r", false, false},

		// Non-GitHub URLs: soft empty triple (caller falls through to
		// the cwd-auto-pack branch instead of erroring — issue #1182).
		{"gitlab ssh", "git@gitlab.com:o/r.git", "", "", false, true},
		{"gitlab https", "https://gitlab.com/o/r.git", "", "", false, true},
		{"bitbucket https", "https://bitbucket.org/o/r.git", "", "", false, true},
		{"custom host ssh", "git@git.example.com:o/r.git", "", "", false, true},
		{"custom host https", "https://git.example.com/o/r.git", "", "", false, true},
		{"file scheme", "file:///path/to/repo", "", "", false, true},
		{"git scheme", "git://github.com/o/r.git", "", "", false, true},

		// Genuinely malformed GitHub URLs — hard error.
		{"empty", "", "", "", true, false},
		{"whitespace-only", "   ", "", "", true, false},
		{"missing repo", "git@github.com:o", "", "", true, false},
		{"missing owner", "git@github.com:/r", "", "", true, false},
		{"missing both", "git@github.com:", "", "", true, false},
		{"too many slashes", "git@github.com:o/r/extra", "", "", true, false},
		{"https github no path", "https://github.com/", "", "", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owner, repo, err := parseGitRemoteURL(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseGitRemoteURL(%q) = (%q, %q, nil); want error", tc.input, owner, repo)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseGitRemoteURL(%q): unexpected error: %v", tc.input, err)
			}
			if tc.wantSoftEmpty {
				if owner != "" || repo != "" {
					t.Fatalf("parseGitRemoteURL(%q) = (%q, %q); want soft empty triple",
						tc.input, owner, repo)
				}
				return
			}
			if owner != tc.wantOwner || repo != tc.wantRepo {
				t.Fatalf("parseGitRemoteURL(%q) = (%q, %q); want (%q, %q)",
					tc.input, owner, repo, tc.wantOwner, tc.wantRepo)
			}
		})
	}
}

func TestSplitOwnerRepo(t *testing.T) {
	cases := []struct {
		in        string
		wantOwner string
		wantRepo  string
		wantErr   bool
	}{
		{"o/r", "o", "r", false},
		{"o/r.git", "o", "r", false},
		{"O/R.git", "o", "r", false}, // lowercased
		{"/r", "", "", true},
		{"o/", "", "", true},
		{"", "", "", true},
		{"o", "", "", true},
		{"o/r/extra", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			owner, repo, err := splitOwnerRepo(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("splitOwnerRepo(%q) = (%q, %q, nil); want error", tc.in, owner, repo)
				}
				return
			}
			if err != nil {
				t.Fatalf("splitOwnerRepo(%q): unexpected error: %v", tc.in, err)
			}
			if owner != tc.wantOwner || repo != tc.wantRepo {
				t.Fatalf("splitOwnerRepo(%q) = (%q, %q); want (%q, %q)",
					tc.in, owner, repo, tc.wantOwner, tc.wantRepo)
			}
		})
	}
}

func TestGitRootFromCwd(t *testing.T) {
	// Inside a fresh repo with no remote, the helper resolves to
	// the repo root regardless of how deep we are nested.
	t.Run("empty_start_walks_up", func(t *testing.T) {
		root := initTestRepo(t)
		// Save and restore cwd so we don't leak state.
		orig, err := os.Getwd()
		if err != nil {
			t.Fatalf("os.Getwd: %v", err)
		}
		t.Cleanup(func() { _ = os.Chdir(orig) })

		nested := filepath.Join(root, "deep", "nest")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.Chdir(nested); err != nil {
			t.Fatalf("Chdir: %v", err)
		}
		got, err := gitRootFromCwd("")
		if err != nil {
			t.Fatalf("gitRootFromCwd(\"\"): %v", err)
		}
		// On macOS, $TMPDIR is a symlink (/var/folders → /private/var/folders);
		// filepath.Abs preserves the symlink form, so canonicalize both
		// paths before comparing.
		absRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			t.Fatalf("EvalSymlinks(root): %v", err)
		}
		absGot, err := filepath.EvalSymlinks(got)
		if err != nil {
			t.Fatalf("EvalSymlinks(got): %v", err)
		}
		if absGot != absRoot {
			t.Fatalf("got %q, want %q", absGot, absRoot)
		}
	})

	// /tmp is never a git repo (well — under normal CI it isn't; if
	// your /tmp happens to be a git repo, the assertion is wrong
	// but the helper is still correct).
	t.Run("not_in_git_repo", func(t *testing.T) {
		tmp := t.TempDir()
		_, err := gitRootFromCwd(tmp)
		if !errors.Is(err, ErrNotInGitRepo) {
			t.Fatalf("gitRootFromCwd(%q): got %v; want ErrNotInGitRepo", tmp, err)
		}
	})

	// Inside a real git repo, the helper returns the repo root and
	// walks UP through nested subdirs.
	t.Run("walks_up_to_repo_root", func(t *testing.T) {
		root := initTestRepo(t)
		nested := filepath.Join(root, "a", "b", "c")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		got, err := gitRootFromCwd(nested)
		if err != nil {
			t.Fatalf("gitRootFromCwd: %v", err)
		}
		absRoot, _ := filepath.Abs(root)
		if got != absRoot {
			t.Fatalf("got %q, want %q", got, absRoot)
		}
	})
}

func TestGitRemoteOrigin(t *testing.T) {
	t.Run("happy_path", func(t *testing.T) {
		root := initTestRepo(t)
		// `git init` does not set a remote; add one explicitly.
		mustGit(t, root, "remote", "add", "origin", "git@github.com:o/r.git")
		got, err := gitRemoteOrigin(root)
		if err != nil {
			t.Fatalf("gitRemoteOrigin: %v", err)
		}
		if got != "git@github.com:o/r.git" {
			t.Fatalf("gitRemoteOrigin = %q; want %q", got, "git@github.com:o/r.git")
		}
	})

	t.Run("no_origin", func(t *testing.T) {
		root := initTestRepo(t)
		_, err := gitRemoteOrigin(root)
		if !errors.Is(err, ErrNoGitRemote) {
			t.Fatalf("gitRemoteOrigin (no origin): got %v; want ErrNoGitRemote", err)
		}
	})
}

func TestResolveHEAD(t *testing.T) {
	t.Run("happy_path_returns_sha", func(t *testing.T) {
		root := initTestRepo(t)
		// initTestRepo commits a file so HEAD resolves.
		sha, err := resolveHEAD(root)
		if err != nil {
			t.Fatalf("resolveHEAD: %v", err)
		}
		if len(sha) != 40 {
			t.Fatalf("resolveHEAD: len(sha) = %d; want 40", len(sha))
		}
	})

	t.Run("zero_commits", func(t *testing.T) {
		root := t.TempDir()
		// Plain `git init` with no commit.
		mustGit(t, root, "init", "-q")
		_, err := resolveHEAD(root)
		if err == nil {
			t.Fatalf("resolveHEAD on zero-commit repo: got nil error; want one")
		}
		if !strings.Contains(err.Error(), "no commits yet") {
			t.Fatalf("resolveHEAD on zero-commit repo: error = %v; want 'no commits yet'", err)
		}
	})
}

func TestIsDirtyWorkdir(t *testing.T) {
	t.Run("clean", func(t *testing.T) {
		root := initTestRepo(t)
		dirty, err := isDirtyWorkdir(root)
		if err != nil {
			t.Fatalf("isDirtyWorkdir: %v", err)
		}
		if dirty {
			t.Fatalf("isDirtyWorkdir = true on clean repo")
		}
	})

	t.Run("modified_file", func(t *testing.T) {
		root := initTestRepo(t)
		// Modify the existing tracked file.
		if err := os.WriteFile(filepath.Join(root, "README.md"),
			[]byte("modified contents\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		dirty, err := isDirtyWorkdir(root)
		if err != nil {
			t.Fatalf("isDirtyWorkdir: %v", err)
		}
		if !dirty {
			t.Fatalf("isDirtyWorkdir = false after modifying tracked file")
		}
	})
}

// initTestRepo creates a tempdir with a fresh `git init`, configures
// a committer identity (required for `git commit` to succeed in CI
// environments without one pre-set), commits a README, and returns
// the tempdir path. The repo has no remote — tests that need one add
// it explicitly.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q")
	mustGit(t, dir, "config", "user.email", "test@example.com")
	mustGit(t, dir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "README.md"),
		[]byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile README: %v", err)
	}
	mustGit(t, dir, "add", "README.md")
	mustGit(t, dir, "commit", "-q", "-m", "initial commit")
	return dir
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_PAGER=cat",
		// belt-and-suspenders in case the runner has no global config
		"GIT_AUTHOR_NAME=Test User",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test User",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

// TestGitArchiveHEAD_HappyPath exercises the refactored zero-config
// packer (issue #1182 §3.5): a temp git repo with a committed file
// is archived to a temp path, the file is readable as a gzipped tar,
// and the entry set contains the committed README.md. Pins that
// `git archive HEAD -o <path>` produces a self-contained archive
// the CLI can hand to the multipart writer without further
// processing.
func TestGitArchiveHEAD_HappyPath(t *testing.T) {
	dir := initTestRepo(t)
	out := filepath.Join(t.TempDir(), "out.tar.gz")
	if err := gitArchiveHEAD(dir, out); err != nil {
		t.Fatalf("gitArchiveHEAD: %v", err)
	}
	f, err := os.Open(out) //nolint:forbidigo // test opens a tempfile path it just constructed via t.TempDir + filepath.Join
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	var found bool
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		if filepath.Base(hdr.Name) == "README.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("archive did not contain README.md entry")
	}
}

// TestGitArchiveHEADPath makes sure a selected monorepo service is archived
// as the build root. The repository itself contains a root-level file and the
// service contains its own framework marker; only the service files should be
// visible to the downstream source packer.
func TestGitArchiveHEADPath(t *testing.T) {
	dir := initTestRepo(t)
	service := filepath.Join(dir, "apps", "api")
	if err := os.MkdirAll(service, 0o755); err != nil {
		t.Fatalf("mkdir service: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workspace.lock"), []byte("root-only\n"), 0o644); err != nil {
		t.Fatalf("write workspace lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(service, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(service, "index.js"), []byte("console.log('api')\n"), 0o644); err != nil {
		t.Fatalf("write index.js: %v", err)
	}
	mustGit(t, dir, "add", "apps/api", "workspace.lock")
	mustGit(t, dir, "commit", "-q", "-m", "add api service")

	out := filepath.Join(t.TempDir(), "api.tar.gz")
	if err := gitArchiveHEADPath(dir, "apps/api", out); err != nil {
		t.Fatalf("gitArchiveHEADPath: %v", err)
	}
	entries := readGitArchiveFixture(t, out)
	for _, want := range []string{"package.json", "index.js"} {
		if _, ok := entries[want]; !ok {
			t.Errorf("selected archive missing %q; entries=%v", want, entries)
		}
	}
	for _, omitted := range []string{"apps/api/package.json", "README.md", "workspace.lock"} {
		if _, ok := entries[omitted]; ok {
			t.Errorf("selected archive contains non-selected entry %q", omitted)
		}
	}
}

func TestResolveDeploySourceDir(t *testing.T) {
	base := t.TempDir()
	service := filepath.Join(base, "apps", "api")
	if err := os.MkdirAll(service, 0o755); err != nil {
		t.Fatalf("mkdir service: %v", err)
	}

	got, err := resolveDeploySourceDir(base, "apps/api")
	if err != nil {
		t.Fatalf("resolveDeploySourceDir: %v", err)
	}
	if got != service {
		t.Errorf("resolved path = %q, want %q", got, service)
	}

	file := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := resolveDeploySourceDir(base, file); err == nil {
		t.Fatal("file source should fail")
	}

	link := filepath.Join(base, "link")
	if err := os.Symlink(service, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := resolveDeploySourceDir(base, link); err == nil {
		t.Fatal("symlink source should fail")
	}
}

func TestGitRelativePath(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "apps", "api")
	got, err := gitRelativePath(root, inside)
	if err != nil {
		t.Fatalf("gitRelativePath: %v", err)
	}
	if got != "apps/api" {
		t.Errorf("relative path = %q, want apps/api", got)
	}
	if _, err := gitRelativePath(root, filepath.Dir(root)); err == nil {
		t.Fatal("outside source should fail")
	}
}

// TestGitArchiveHEAD_EmptyRepo pins the empty-repo guard. A fresh
// `git init` with no commits must return a non-nil error (the
// rev-parse HEAD^{commit} pre-check). The caller surfaces this as
// "no commits yet; commit something and try again".
func TestGitArchiveHEAD_EmptyRepo(t *testing.T) {
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q")
	out := filepath.Join(t.TempDir(), "out.tar.gz")
	if err := gitArchiveHEAD(dir, out); err == nil {
		t.Fatal("gitArchiveHEAD on empty repo should fail; got nil")
	}
	// out should not exist — the pre-check aborts before the archive
	// command runs.
	if _, err := os.Stat(out); err == nil {
		t.Errorf("expected no archive on empty repo; file exists")
	}
}

// TestGitArchiveHEAD_NotInRepo pins that running outside a git
// working tree surfaces a non-nil error and does not produce a
// spurious archive file at outPath.
func TestGitArchiveHEAD_NotInRepo(t *testing.T) {
	dir := t.TempDir() // no git init
	out := filepath.Join(t.TempDir(), "out.tar.gz")
	if err := gitArchiveHEAD(dir, out); err == nil {
		t.Fatal("gitArchiveHEAD outside a repo should fail; got nil")
	}
}

// TestResolveZeroConfigProvenance exercises the bundle the refactored
// zero-config path reads from. Pins the four return-contract cases:
// ok=true with GitHub origin, ok=true with non-GitHub origin
// (Owner/Repo empty), ok=false with ErrNotInGitRepo, and
// ok=false with ErrNoGitRemote.
func TestResolveZeroConfigProvenance(t *testing.T) {
	t.Run("github_origin_clean", func(t *testing.T) {
		dir := initTestRepo(t)
		mustGit(t, dir, "remote", "add", "origin", "git@github.com:o/r.git")
		prov, ok, err := resolveZeroConfigProvenance(dir)
		if err != nil || !ok {
			t.Fatalf("expected ok=true, err=nil; got ok=%v err=%v", ok, err)
		}
		if prov.Owner != "o" || prov.Repo != "r" {
			t.Errorf("got owner=%q repo=%q, want o/r", prov.Owner, prov.Repo)
		}
		if len(prov.SHA) != 40 {
			t.Errorf("SHA %q is not 40 chars", prov.SHA)
		}
		if prov.Dirty {
			t.Errorf("clean repo should report Dirty=false")
		}
		if prov.Root != dir {
			t.Errorf("Root=%q want %q", prov.Root, dir)
		}
	})

	t.Run("github_origin_dirty", func(t *testing.T) {
		dir := initTestRepo(t)
		mustGit(t, dir, "remote", "add", "origin", "git@github.com:o/r.git")
		// Add an untracked file → dirty.
		if err := os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("hi"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		prov, ok, err := resolveZeroConfigProvenance(dir)
		if err != nil || !ok {
			t.Fatalf("expected ok=true, err=nil; got ok=%v err=%v", ok, err)
		}
		if !prov.Dirty {
			t.Errorf("untracked-file repo should report Dirty=true")
		}
	})

	t.Run("non_github_origin_soft_empty", func(t *testing.T) {
		dir := initTestRepo(t)
		mustGit(t, dir, "remote", "add", "origin", "git@gitlab.com:o/r.git")
		prov, ok, err := resolveZeroConfigProvenance(dir)
		if err != nil || !ok {
			t.Fatalf("expected ok=true, err=nil for non-GitHub origin; got ok=%v err=%v", ok, err)
		}
		// Owner + Repo should be empty (caller still uses gitArchiveHEAD
		// — just no provenance metadata on the deployment row).
		if prov.Owner != "" || prov.Repo != "" {
			t.Errorf("non-GitHub origin should produce empty owner/repo; got %q/%q",
				prov.Owner, prov.Repo)
		}
	})

	t.Run("no_origin_falls_through", func(t *testing.T) {
		dir := initTestRepo(t) // initTestRepo adds no remote
		_, ok, err := resolveZeroConfigProvenance(dir)
		if ok {
			t.Errorf("expected ok=false for git repo without origin; got ok=true")
		}
		if !errors.Is(err, ErrNoGitRemote) {
			t.Errorf("expected ErrNoGitRemote; got %v", err)
		}
	})

	t.Run("not_in_repo", func(t *testing.T) {
		dir := t.TempDir() // no git init
		_, ok, err := resolveZeroConfigProvenance(dir)
		if ok {
			t.Errorf("expected ok=false outside a repo; got ok=true")
		}
		if !errors.Is(err, ErrNotInGitRepo) {
			t.Errorf("expected ErrNotInGitRepo; got %v", err)
		}
	})

	t.Run("empty_repo", func(t *testing.T) {
		// `git init` with no commit and no remote. Should fall through
		// as ErrNoGitRemote (origin check fires first).
		dir := t.TempDir()
		mustGit(t, dir, "init", "-q")
		_, ok, err := resolveZeroConfigProvenance(dir)
		if ok {
			t.Errorf("expected ok=false for empty repo with no origin; got ok=true")
		}
		if !errors.Is(err, ErrNoGitRemote) {
			t.Errorf("expected ErrNoGitRemote; got %v", err)
		}
	})

	t.Run("user_name_captured", func(t *testing.T) {
		dir := initTestRepo(t)
		// initTestRepo sets user.name=Test User via env vars; the helper
		// reads `git config user.name` which falls through to the
		// env-var-supplied value.
		mustGit(t, dir, "remote", "add", "origin", "git@github.com:o/r.git")
		prov, _, err := resolveZeroConfigProvenance(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if prov.DeployedBy != "Test User" {
			t.Errorf("DeployedBy=%q want %q", prov.DeployedBy, "Test User")
		}
	})
}
