// Customer `gregale doctor` file-set scoping (issue #2669).
//
// doctor walks the directory it is pointed at. `gregale deploy` does not
// always upload that directory: with an `origin` remote and no
// --source=worktree it uploads `git archive HEAD` (or HEAD:<path>), which
// contains committed files only. A .gitignore'd fixture, local binary or
// nested worktree is therefore never deployed, and reporting it as a blocking
// finding is a false positive (#2669).
//
// The scope here narrows doctor's walks to the file set the selected packer
// would actually produce:
//
//   - head (and auto with an origin remote) uploads the committed HEAD tree,
//     so paths Git reports as ignored are skipped. Git answers the question
//     with .gitignore files at every level, .git/info/exclude and
//     core.excludesFile, using its own grammar. Re-implementing that lookup
//     in doctor would answer a different question (#2669 review).
//   - worktree, a repository without an origin, and a plain directory upload
//     the files on disk through loadPackRules, which never reads .gitignore
//     (docs/deploys.md:14-16). Every file there can ship, so doctor keeps
//     scanning every file.
//
// A tracked file that also matches .gitignore is not reported by
// `git ls-files --others`, so it stays in scope: `git archive` ships it.
package main

import (
	"fmt"
	"path"
	"strings"
)

// doctorScanScope is the set of paths the selected packer omits from the
// upload, keyed by slash path relative to the scanned directory. A nil scope
// means packaging reads the working tree and doctor must scan everything.
type doctorScanScope struct {
	ignoredFiles map[string]bool
	ignoredDirs  map[string]bool
}

// allows reports whether packaging can ship rel (relative to the scan root).
func (s *doctorScanScope) allows(rel string, isDir bool) bool {
	if s == nil {
		return true
	}
	if rel == "." || rel == "" {
		return true
	}
	if isDir {
		return !s.ignoredDirs[rel]
	}
	if s.ignoredFiles[rel] {
		return false
	}
	// A file directly under a collapsed ignored directory is covered by the
	// walker's SkipDir, but a scope used without the walker must not leak it.
	for dir := path.Dir(rel); dir != "." && dir != "/"; dir = path.Dir(dir) {
		if s.ignoredDirs[dir] {
			return false
		}
	}
	return true
}

// doctorPackagingScope resolves the local source policy exactly as deploy
// does (deploymentCheckSource, deploy_source_files.go) and returns the
// exclusion set for that policy. mode is doctor's --source value:
// deploySourceWorktree returns a nil scope, deploySourceHEAD requires a
// repository with a commit, and deploySourceAuto follows deploy's default
// (HEAD when the repository has an origin remote, worktree otherwise).
//
// Any uncertainty resolves to the nil scope: doctor then scans more files
// than packaging may ship, never fewer.
func doctorPackagingScope(dir string, mode deploySourceMode) (*doctorScanScope, error) {
	committed, err := doctorPackagingUsesHead(dir, mode)
	if err != nil || !committed {
		return nil, err
	}
	return gitIgnoredScope(dir), nil
}

// doctorPackagingUsesHead mirrors deploy's choice between the committed
// packer and loadPackRules.
func doctorPackagingUsesHead(dir string, mode deploySourceMode) (bool, error) {
	switch mode {
	case deploySourceWorktree:
		return false, nil
	case deploySourceHEAD:
		if _, err := resolveExplicitHeadProvenance(dir); err != nil {
			return false, fmt.Errorf("--source=head requires a Git repository with a commit: %w", err)
		}
		return true, nil
	}
	// deploySourceAuto: deploy falls through to loadPackRules without an
	// origin remote, in a non-git directory, and when Git metadata cannot be
	// read. Scanning the working tree is the conservative answer for all
	// three.
	_, ok, _ := resolveZeroConfigProvenance(dir)
	return ok, nil
}

// gitIgnoredScope asks git which paths under dir the committed packer omits.
//
// `git ls-files --others --ignored --exclude-standard --directory` lists
// untracked paths ignored by .gitignore / .git/info/exclude /
// core.excludesFile, collapsing wholly-ignored directories. Tracked files are
// absent from --others even when a pattern matches, which is what
// `git archive` ships. --full-name keeps output repository-relative; the
// scan-root prefix is stripped so keys match the walker's filepath.Rel.
//
// Returns nil when git cannot answer (no commit, no git binary, an
// unreadable repository, the bounded timeout): doctor then scans the
// working tree instead of hiding a file that may ship.
func gitIgnoredScope(dir string) *doctorScanScope {
	prefix, err := gitScanPrefix(dir)
	if err != nil {
		return nil
	}
	out, err := runGitCmd(dir, "ls-files", "--full-name", "--others", "--ignored", "--exclude-standard", "--directory", "-z")
	if err != nil {
		return nil
	}
	scope := &doctorScanScope{ignoredFiles: map[string]bool{}, ignoredDirs: map[string]bool{}}
	for _, entry := range strings.Split(out, "\x00") {
		rel, ok := strings.CutPrefix(entry, prefix)
		if !ok || rel == "" {
			// Outside the scanned directory (or a path git did not express
			// relative to the repository root): leave it in scope.
			continue
		}
		if strings.HasSuffix(rel, "/") {
			if ignored := strings.TrimSuffix(rel, "/"); ignored != "" {
				scope.ignoredDirs[ignored] = true
			}
			continue
		}
		scope.ignoredFiles[rel] = true
	}
	return scope
}

// gitScanPrefix returns dir's prefix inside its repository ("apps/api/"), or
// "" at the repository root. The leading path components are what
// --full-name prepends to every ls-files entry.
func gitScanPrefix(dir string) (string, error) {
	out, err := runGitCmd(dir, "rev-parse", "--show-prefix")
	if err != nil {
		return "", err
	}
	prefix := strings.TrimRight(out, "\r\n")
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return prefix, nil
}
