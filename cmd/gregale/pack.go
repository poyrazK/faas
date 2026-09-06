package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretscan"
	"github.com/onebox-faas/faas/pkg/whycopy"
)

// framework is the source kind auto-detected from the current directory when
// `gregale deploy` is run with no source flag (issue #313). It intentionally
// mirrors the top-level filename rule in pkg/builderd/detect.go, but is copied
// here rather than imported: importing pkg/builderd would pull the entire
// server stack (DB, scheduler, firecracker) into the CLI binary. The rule is
// small and stable; the two copies are the accepted trade for zero server
// blast radius. The server re-detects authoritatively from the uploaded
// tarball, so this value is only used for CLI UX + the dockerfile flag.
type framework string

const (
	fwNode    framework = "node"
	fwPython  framework = "python"
	fwGo      framework = "go"
	fwDocker  framework = "docker"
	fwUnknown framework = "unknown"
)

// Function runtime literals — declared as constants here so the
// inferFunctionRuntime switch can use named values rather than
// repeating the wire string (which goconst would otherwise flag
// because the runtime names recur across the CLI: --template
// function-node forces "node22", the wire form field carries
// "node22", etc.). The whitelist matches apid's validator at
// cmd/apid/handlers.go:98 (node22 / node24 / python312 /
// python313 / go124 / go124-alpine); this PR only emits the
// runtime values the auto-detect path can infer, but adding a
// new runtime to the map is a follow-up ADR.
const (
	runtimeNode22    = "node22"
	runtimeNode24    = "node24"
	runtimePython312 = "python312"
	runtimePython313 = "python313"
	runtimeGo124     = "go124"
)

// shape is the deploy shape auto-detected from the current directory when
// `gregale deploy` runs with no source flag (issue #737 / ADR-083). A function
// shape means "single handler file at the root, no app markers"; an app shape
// means any app marker (package.json / requirements.txt / go.mod / Dockerfile
// / …) is present at the root. The convention is intentionally narrow:
// a customer with `package.json + handler.js` is unambiguously a Node app,
// and must pass --function to force function mode (otherwise auto-detection
// would silently break every existing Node user).
//
// shapeUnknown fires when the cwd is empty or contains only excluded files
// (.git, .DS_Store, README, dotfiles). The CLI surfaces this as the no-source
// error and lets the customer pick --image, --tarball, --template, --repo,
// or the new --function/--app explicit flags.
type shape int

const (
	shapeApp shape = iota
	shapeFunction
	shapeUnknown
)

// functionHandlerFiles is the closed set of file names that, when present
// alone at the project root, signal a function deploy. The names match the
// template convention at cmd/gregale/templates/function-node/handler.js (and
// its python/go siblings). Anything else falls through to shapeApp or
// shapeUnknown.
var functionHandlerFiles = map[string]bool{
	"handler.js": true,
	"handler.ts": true,
	"handler.py": true,
	"handler.go": true,
}

// defaultExcludeDirs are directory names dropped anywhere in the tree. These
// are build artifacts / VCS metadata that both bloat the tarball past the
// SourceTarballMaxMB cap (pkg/api/limits.go) and are reproduced server-side by
// the builder. Aggressive-but-predictable: other dotfiles (.env, .dockerignore,
// .npmrc, .github/) are deliberately kept.
//
// The set is fixed (no customer config) so a customer reading the docs can
// reason about their tarball contents without consulting a per-customer file.
// Project-level exclusions belong in .gregaleignore (loaded by
// loadGregaleignore below). Issue #1182 added the six "common build
// artifact" dirs (dist/.next/coverage/target/.venv/.cache) so a fresh
// `gregale deploy` from a Next.js / Maven / Cargo / coverage-instrumented
// project doesn't trip the SourceTarballMaxMB cap with garbage the
// builder is going to throw away anyway.
var defaultExcludeDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"__pycache__":  true,
	// Common build / coverage output dirs (issue #1182 §3.5).
	"dist":     true, // npm run build / go build / vite
	".next":    true, // Next.js build output
	"coverage": true, // nyc / istanbul / go test -coverprofile
	"target":   true, // Maven / Cargo / Scala
	".venv":    true, // Python virtualenv
	".cache":   true, // pip / pytest / generic
}

// defaultExcludeFiles are basenames dropped anywhere in the tree (OS/VCS
// metadata that should never become application source).
var defaultExcludeFiles = map[string]bool{
	".DS_Store": true,
	"Thumbs.db": true,
	".git":      true, // linked worktrees use a .git file instead of a directory
}

// appMarker is the closed set of filenames whose presence at the project
// root (or, for the depth-2 hint path, under a single subdirectory) marks
// a directory as containing deployable source for an *app* (Railpack
// framework path). A README.md or dotfile in the same directory is NOT
// a marker — those files don't change the deploy shape.
//
// Single source of truth: detectFramework and detectNestedMarkerHint
// both consult this map, so a new marker (e.g. Cargo.toml for a Rust
// Railpack pipeline that lands in a future ADR) only needs to be added
// here, not in two switches. The marker→framework mapping mirrors
// pkg/builderd/detect.go:73-82 on the server side — the CLI is
// intentionally the lighter view (no Dockerfile priority ordering —
// see detectFramework, which applies Dockerfile-wins as a post-pass).
var appMarker = map[string]framework{
	"package.json":     fwNode,
	"requirements.txt": fwPython,
	"pyproject.toml":   fwPython,
	"pipfile":          fwPython,
	"setup.py":         fwPython,
	"go.mod":           fwGo,
	"dockerfile":       fwDocker,
}

// packEpoch is a fixed modification time stamped on every archive entry so the
// packed tarball is byte-reproducible for a given input (tests depend on this,
// and it avoids leaking local mtimes).
var packEpoch = time.Unix(0, 0)

// gregaleignoreFile is the filename at the packed root whose lines are
// consulted by shouldExclude in addition to defaultExcludeDirs /
// defaultExcludeFiles (issue #1182 §3.5). Lives in the same file as the
// packer because there is exactly one consumer and tests live in
// pack_test.go.
//
// Grammar is a strict subset of .gitignore:
//
//   - blank lines and lines beginning with '#' are ignored
//   - leading '!' negates a previous match (a previously-excluded file
//     can be re-included by a later negated line)
//   - leading '/' anchors the pattern to the packed root (no
//     unanchored equivalent in a non-rooted context)
//   - trailing '/' restricts the pattern to directories
//   - per-segment shell glob: '*' matches any run of non-'/' chars;
//     '?' matches a single non-'/' char. No '**' / no character
//     classes / no bracket expressions in v1.
//
// If the file is absent or unreadable the packer falls through to the
// defaults alone (no regression for customers without a file). Malformed
// lines are skipped silently (matches gitignore: invalid patterns are
// ignored, not fatal).
const gregaleignoreFile = ".gregaleignore"

// gregaleignorePattern is one parsed line. Fields are independent
// (anchor + dirOnly + negate can all be true on the same line).
type gregaleignorePattern struct {
	raw          string
	anchor       bool     // leading '/'
	dirOnly      bool     // trailing '/'
	negate       bool     // leading '!'
	globSegments []string // per-segment glob, split on '/'
}

// loadGregaleignore reads .gregaleignore from srcDir (the packed root)
// and returns the parsed patterns in file order. Missing / unreadable
// file → nil (no patterns → shouldExclude falls through to defaults
// alone). Per-line parse failures are skipped silently.
func loadGregaleignore(srcDir string) []gregaleignorePattern {
	data, err := os.ReadFile(filepath.Join(srcDir, gregaleignoreFile))
	if err != nil {
		return nil
	}
	return parseGregaleignore(data)
}

// parseGregaleignore parses a .gregaleignore byte slice into patterns.
// Pure function so pack_test.go can drive it without touching the
// filesystem.
func parseGregaleignore(data []byte) []gregaleignorePattern {
	var out []gregaleignorePattern
	sc := bufio.NewScanner(bytes.NewReader(data))
	// Allow long lines; the .gregaleignore spec has no line-length cap
	// (gitignore uses the same ScanLines default and never complains).
	for sc.Scan() {
		// Trim trailing CR (for CRLF inputs) AND surrounding
		// whitespace so blank / whitespace-only lines and
		// trailing-whitespace comments are ignored the same way
		// gitignore treats them.
		line := strings.TrimSpace(strings.TrimRight(sc.Text(), "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p := gregaleignorePattern{}
		if strings.HasPrefix(line, "!") {
			p.negate = true
			line = line[1:]
		}
		if strings.HasPrefix(line, "/") {
			p.anchor = true
			line = line[1:]
		}
		if strings.HasSuffix(line, "/") {
			p.dirOnly = true
			line = strings.TrimSuffix(line, "/")
		}
		if line == "" {
			// Pure "!", "/", or "/" line — ignore (matches gitignore:
			// an empty pattern after stripping flags would match every
			// path, which is never what the customer meant).
			continue
		}
		p.raw = line
		p.globSegments = strings.Split(line, "/")
		out = append(out, p)
	}
	return out
}

// matchGregaleignore reports whether relSlash matches any of the
// patterns, honouring the negation toggle: a later '!pattern' can
// re-include a file that was previously excluded.
//
// relSlash is slash-separated (filepath.ToSlash applied by the caller),
// isDir is true for directory entries. dirOnly patterns only fire on
// directories.
func matchGregaleignore(relSlash string, isDir bool, patterns []gregaleignorePattern) bool {
	matched := false
	for _, p := range patterns {
		if p.dirOnly && !isDir {
			continue
		}
		if matchGregaleignoreOne(relSlash, p) {
			matched = !p.negate
		}
	}
	return matched
}

// matchGregaleignoreOne tests a single pattern against relSlash.
// Anchored patterns only match at the packed root; unanchored patterns
// match at any depth (gitignore's last-segment-match rule).
func matchGregaleignoreOne(relSlash string, p gregaleignorePattern) bool {
	parts := strings.Split(relSlash, "/")
	if p.anchor {
		return globMatchSegments(parts, p.globSegments)
	}
	// Unanchored: try every suffix of the path (the full path, then
	// dropping one leading segment, etc.). This is the gitignore
	// "matches a path component at any level" rule; e.g. `*.log`
	// matches `a/b/c.log` because the suffix `b/c.log` segments
	// match `*.log`.
	for i := 0; i < len(parts); i++ {
		if globMatchSegments(parts[i:], p.globSegments) {
			return true
		}
	}
	return false
}

// globMatchSegments matches a (suffix of) the rel path against the
// pattern's per-segment globs. Uses path/filepath.Match per segment
// so '*' / '?' work but no '**' / bracket classes. Lengths must match
// exactly — `*.log` matches `a.log` but not `a.log.bak`.
func globMatchSegments(parts, pattern []string) bool {
	if len(parts) != len(pattern) {
		return false
	}
	for i, pat := range pattern {
		ok, err := filepath.Match(pat, parts[i])
		if err != nil || !ok {
			return false
		}
	}
	return true
}

// shouldExclude reports whether a slash-separated path relative to the
// packed root (e.g. "node_modules/foo/index.js") should be omitted from
// the archive. patterns is the parsed .gregaleignore list (nil/absent
// → no project-level exclusions).
//
// Order of checks: defaults first (cheap map lookups, no allocation),
// then .gregaleignore (per-entry glob walk). Defaults win over a
// later negated .gregaleignore line — the customer's "include back
// the dist/" un-exclude cannot revive a default-excluded build dir.
// That matches the intent: defaults exist to keep the tarball under
// the source cap, and unblocking them defeats that purpose.
func shouldExclude(relSlashPath string, isDir bool, patterns []gregaleignorePattern) bool {
	base := relSlashPath
	if i := strings.LastIndex(relSlashPath, "/"); i >= 0 {
		base = relSlashPath[i+1:]
	}
	if isDir {
		if defaultExcludeDirs[base] {
			return true
		}
	} else {
		if defaultExcludeFiles[base] {
			return true
		}
		if strings.HasSuffix(base, ".pyc") {
			return true
		}
	}
	return matchGregaleignore(relSlashPath, isDir, patterns)
}

// packExtraExcludeSet converts absolute customer paths (for example an
// explicitly synced .env.dev) into paths relative to the archive root. Paths
// outside the root are ignored because they cannot be packed by this walk.
// The set is deliberately separate from .gregaleignore: these exclusions are
// command-scoped and never mutate the customer's repository configuration.
func packExtraExcludeSet(srcDir string, paths []string) map[string]bool {
	set := make(map[string]bool, len(paths))
	root, err := filepath.Abs(srcDir)
	if err != nil {
		return set
	}
	for _, path := range paths {
		abs, absErr := filepath.Abs(path)
		if absErr != nil {
			continue
		}
		rel, relErr := filepath.Rel(root, abs)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		set[filepath.ToSlash(filepath.Clean(rel))] = true
	}
	return set
}

// detectFramework sniffs the TOP-LEVEL entries of srcDir (no recursion) and
// returns the implied framework. A Dockerfile wins over language markers, in
// lockstep with pkg/builderd/detect.go. Returns fwUnknown when nothing at the
// root identifies the project.
func detectFramework(srcDir string) framework {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fwUnknown
	}
	// Single source of truth: appMarker (issue #744 / ADR-086). A new
	// marker added to the map is picked up here AND by
	// detectNestedMarkerHint without further edits. The Dockerfile-wins
	// post-pass at the end mirrors pkg/builderd/detect.go — when both a
	// Dockerfile and a language marker are present, the Dockerfile wins.
	var hasDocker bool
	var lang framework
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if fw, ok := appMarker[strings.ToLower(e.Name())]; ok {
			if fw == fwDocker {
				hasDocker = true
				continue
			}
			if lang == "" {
				lang = fw
			}
		}
	}
	if hasDocker {
		return fwDocker
	}
	if lang != "" {
		return lang
	}
	return fwUnknown
}

// detectFrameworkVersion is the CLI-side mirror of
// pkg/builderd/detectversion.go. It pre-walks srcDir (the local cwd
// the customer is packing from) and returns the best-effort language
// version declared by the source, or "" if no version marker is found
// or any parser fails. Used by resolveDeployShape's shapeApp banner
// (issue #740 / DEPLOY-PROV-5 / ADR-087) to render
//
//	Detected: app, framework=node, version=22.11.0
//
// BEFORE the multipart POST. The server independently re-derives the
// same value for the build_provenance.framework_version column; the
// CLI banner is purely informational — the operator reads the
// authoritative value via `gregale build provenance <id>`.
//
// Priority order mirrors pkg/builderd/detectversion.go (kept in sync
// intentionally — the CLI is the lighter view; the server is the
// authoritative re-read):
//
//	node    → .nvmrc → package.json::engines.node → ""
//	python  → .python-version → pyproject.toml::requires-python → ""
//	go      → go.mod → "go X.Y" directive → ""
//	docker  → "" (containers pin via FROM; out-of-scope per issue #740)
//
// Any parse error → "". A 64 KB cap per file mirrors the server-side
// bound at pkg/builderd/detectversion.go::maxVersionFileBytes.
func detectFrameworkVersion(srcDir string, fw framework) string {
	switch fw {
	case fwNode:
		if v := cliReadFirstLine(srcDir, ".nvmrc"); v != "" {
			if out := normalizeVersion(stripVersionPrefix(v)); out != "" {
				return out
			}
		}
		if body := cliReadFile(srcDir, "package.json"); body != "" {
			if out := cliVersionFromPackageJSONNode(body); out != "" {
				return out
			}
		}
	case fwPython:
		if v := cliReadFirstLine(srcDir, ".python-version"); v != "" {
			if out := normalizeVersion(stripVersionPrefix(v)); out != "" {
				return out
			}
		}
		if body := cliReadFile(srcDir, "pyproject.toml"); body != "" {
			if out := cliVersionFromPyprojectRequires(body); out != "" {
				return out
			}
		}
	case fwGo:
		if body := cliReadFile(srcDir, "go.mod"); body != "" {
			if out := cliVersionFromGoModDirective(body); out != "" {
				return out
			}
		}
	case fwDocker, fwUnknown:
		// explicit out-of-scope / no anchor
	}
	return ""
}

// funcErrorSuggestion returns the trailing "Detected <fw> project ..."
// snippet for the --function error path, or "" when the snippet
// would be empty / malformed (no version file, no whitelisted
// runtime). Extracted from resolveDeployShape so the conditional is
// readable and so the malformed-runtime case (e.g. node 26 → no
// whitelisted entry → empty --runtime) is impossible.
func funcErrorSuggestion(srcDir string) string {
	fw := detectFramework(srcDir)
	if fw == fwUnknown || fw == fwDocker {
		return ""
	}
	ver := detectFrameworkVersion(srcDir, fw)
	if ver == "" {
		return ""
	}
	rt := runtimeSuggestionFor(fw, ver)
	if rt == "" {
		return ""
	}
	return fmt.Sprintf(
		" Detected %s project (version %s) — try `--runtime %s --handler handler.handler`.",
		fw, ver, rt)
}

// cliReadFile reads up to 64 KB of the named file in srcDir and returns
// its contents. Returns "" on any error so the caller treats the file
// as not-present. A 64 KB cap mirrors the server-side bound.
func cliReadFile(srcDir, name string) string {
	const maxBytes = 64 * 1024
	path := filepath.Join(srcDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(data) > maxBytes {
		return ""
	}
	return string(data)
}

// cliReadFirstLine returns the first non-blank, non-comment trimmed
// line of the named file. "" on any error or all-blank file.
func cliReadFirstLine(srcDir, name string) string {
	body := cliReadFile(srcDir, name)
	if body == "" {
		return ""
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line
	}
	return ""
}

// stripVersionPrefix strips a leading "v" from a version string. The
// rest of the version is matched by the per-parser regex; that's how
// "v22.11.0" turns into "22.11.0" without forcing a strict semver
// parse.
func stripVersionPrefix(s string) string {
	return strings.TrimPrefix(strings.TrimSpace(s), "v")
}

// versionLikeCLI is the dotted-version matcher used by both the
// node and python parsers. The .python-version file commonly writes
// "3.11" (two-component) so the regex accepts X.Y or X.Y.Z. Mirrors
// pkg/builderd/detectversion.go::semverLike.
var versionLikeCLI = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?`)

// normalizeVersion extracts the first dotted version (X.Y or X.Y.Z)
// from s. Mirrors pkg/builderd/detectversion.go::normalizeSemver —
// the server kept two named variants (semverLike / pythonSemverLike)
// despite byte-identical regexes, so the CLI does too for symmetry,
// and uses one shared regex to avoid duplication.
func normalizeVersion(s string) string {
	return versionLikeCLI.FindString(s)
}

// cliVersionFromPackageJSONNode mirrors pkg/builderd's
// versionFromPackageJSONNode. Only the bare-version-extraction path is
// copied; the server has the full encoding/json path because the
// CLI-side path runs on untrusted customer source files.
func cliVersionFromPackageJSONNode(body string) string {
	var pkg struct {
		Engines struct {
			Node json.RawMessage `json:"node"`
		} `json:"engines"`
	}
	if err := json.Unmarshal([]byte(body), &pkg); err != nil {
		return ""
	}
	if len(pkg.Engines.Node) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(pkg.Engines.Node, &s); err != nil {
		return ""
	}
	s = strings.TrimSpace(s)
	// Strip the leading operator. Order matters: 2-char prefixes
	// (>=, <=) come first so they're not eaten by the single-char
	// match.
	for _, op := range []string{">=", "<=", ">", "<", "^", "~", "="} {
		if strings.HasPrefix(s, op) {
			s = strings.TrimPrefix(s, op)
			break
		}
	}
	return normalizeVersion(strings.TrimSpace(s))
}

// cliVersionFromPyprojectRequires mirrors the server-side regex
// parser. We keep the regex identical so the server-side test cases
// translate 1:1 to the CLI side.
func cliVersionFromPyprojectRequires(body string) string {
	re := regexp.MustCompile(`(?i)requires-python\s*=\s*["']([^"']+)["']`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	val := strings.TrimSpace(m[1])
	for _, op := range []string{">=", "<=", "==", "!=", ">", "<", "~=", "^", "="} {
		if strings.HasPrefix(val, op) {
			val = strings.TrimPrefix(val, op)
			break
		}
	}
	if comma := strings.Index(val, ","); comma >= 0 {
		val = strings.TrimSpace(val[:comma])
	}
	return normalizeVersion(val)
}

// cliVersionFromGoModDirective mirrors the server-side regex.
func cliVersionFromGoModDirective(body string) string {
	re := regexp.MustCompile(`(?m)^\s*go\s+(\d+\.\d+(?:\.\d+)?)\s*$`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return m[1]
}

// runtimeSuggestionFor maps a (framework, version) pair to the
// closest whitelisted runtime name on the wire (cmd/apid/handlers.go:98).
// Returns "" when no version is found or the framework is not
// supported — the caller falls back to the bare error.
//
// Fallback policy: when the marker's version is older than the
// lowest whitelisted runtime (e.g. node20 → node22, python3.11 →
// python312) we still suggest the closest available — the explicit
// --runtime flag the customer types MUST be whitelisted, and the
// marker's intent ("I want node 20") is best served by the closest
// available. When the version is NEWER than the highest whitelisted
// runtime (e.g. node26 → highest is node24, hypothetical python3.15
// → highest is python313) we still suggest the highest available —
// refusing to lie about a non-existent runtime is moot when the
// closest is the highest whitelisted entry the customer can actually
// type. Returning "" only on a totally unparseable version.
func runtimeSuggestionFor(fw framework, ver string) string {
	if ver == "" {
		return ""
	}
	majorMinor := strings.SplitN(ver, ".", 3)
	if len(majorMinor) < 2 {
		return ""
	}
	major, err := strconv.Atoi(majorMinor[0])
	if err != nil {
		return ""
	}
	// The whitelist (cmd/apid/handlers.go:98) is the source of truth:
	// node22, node24, python312, python313, go124.
	switch fw {
	case fwNode:
		switch {
		case major >= 24:
			return runtimeNode24
		case major >= 22:
			return runtimeNode22
		case major >= 1:
			// node18, node20, etc. → still suggest the lowest
			// available (node22). Major >= 1 covers every realistic
			// Node source — "node0" is not a thing.
			return runtimeNode22
		}
	case fwPython:
		if major >= 3 {
			// 3.13 → python313, 3.11 → python312 (still in
			// whitelist). 3.10, 3.11, 3.12, 3.13 all map to the
			// closest available. We require minor for the past-
			// 3.13 disambiguation.
			minor, mErr := strconv.Atoi(majorMinor[1])
			if mErr != nil {
				return ""
			}
			if minor >= 13 {
				return runtimePython313
			}
			return runtimePython312
		}
	case fwGo:
		if major >= 1 {
			// 1.24 → go124. The whitelist only has one Go runtime
			// today; a 1.25+ suggestion would point to a runtime
			// that doesn't exist yet, so we require minor >= 24
			// exactly.
			minor, mErr := strconv.Atoi(majorMinor[1])
			if mErr != nil {
				return ""
			}
			if minor >= 24 {
				return runtimeGo124
			}
		}
	}
	return ""
}

// detectNestedMarkerHint returns true if srcDir contains at least one app
// marker within 2 subdirectory levels of the project root, capturing the
// common monorepo layout (apps/web/package.json, services/api/go.mod,
// libs/x/pyproject.toml, frontend/package.json). Used by resolveDeployShape's
// shapeUnknown branch (issue #744 / ADR-086) to surface a "looks like a
// workspace" hint pointing the customer at `gregale scan --path .` instead
// of opening an issue.
//
// Cheap on purpose: a recursive os.ReadDir per top-level subdir, capped at
// depth 2, no file contents read. Excluded subdirs (defaultExcludeDirs —
// node_modules, .git, vendor, __pycache__ — plus dotfile dirs) are skipped
// at every depth so a stray node_modules/x/package.json does not
// false-positive as a workspace.
//
// Depth-3 monorepos (e.g. apps/services/api/package.json — three
// subdirectory levels deep) are now visible (walkForMarkers maxDepth
// bumped from 1 to 2 in commit issue #1182 §P1 follow-up) so the
// hint fires for `apps/services/api/package.json` too. Depth-4+
// remains out of scope — those belong to `gregale scan` (pkg/reposcan),
// which already handles them via the workspaces_extra_test.go
// "monorepo" fixture. The CLI hint is just a pointer at the next
// step.
func detectNestedMarkerHint(srcDir string) bool {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if isExcludedSubdir(e.Name()) {
			continue
		}
		// walkForMarkers(d1, 2) recurses two levels into each top-level
		// subdir, which puts us at depth 3 from the project root — files
		// at apps/web/package.json are seen; files at
		// apps/services/api/package.json (depth 3) are now seen;
		// files at apps/web/services/api/package.json (depth 4) are
		// still out of scope.
		if walkForMarkers(filepath.Join(srcDir, e.Name()), 2) {
			return true
		}
	}
	return false
}

// walkForMarkers recurses at most maxDepth levels below dir looking for an
// app marker. Returns true on the first hit. Excluded subdirs
// (defaultExcludeDirs + dotfiles) are skipped at every level so a real
// workspace isn't false-positive-masked by a sibling node_modules tree.
func walkForMarkers(dir string, maxDepth int) bool {
	if maxDepth < 0 {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			if isExcludedSubdir(name) {
				continue
			}
			if walkForMarkers(filepath.Join(dir, name), maxDepth-1) {
				return true
			}
			continue
		}
		if _, ok := appMarker[strings.ToLower(name)]; ok {
			return true
		}
	}
	return false
}

// isExcludedSubdir reports whether a subdirectory should be skipped at any
// depth during the nested-marker hint walk (issue #744 / ADR-086). The
// defaultExcludeDirs set covers build artifacts and VCS metadata; dotfile
// dirs (e.g. .vscode, .github) are also skipped so a repo with .vscode/
// doesn't confuse the marker sniff.
func isExcludedSubdir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	return defaultExcludeDirs[strings.ToLower(name)]
}

// NestedMarkerHintError wraps a deploy-shape error to carry the
// "looks like a workspace — try: gregale scan --path ." hint through
// printErr (issue #744 / ADR-086). The wrapped err is the original
// shapeUnknown message; Hint is the additional customer-facing line.
// errors.As extracts the hint in printErr so JSON-mode consumers can
// programmatically detect it without parsing free-form stderr text.
type NestedMarkerHintError struct {
	Dir  string
	Hint string
	Err  error
}

func (e *NestedMarkerHintError) Error() string { return e.Err.Error() }
func (e *NestedMarkerHintError) Unwrap() error { return e.Err }

// detectShape sniffs the TOP-LEVEL entries of srcDir (no recursion) and returns
// the deploy shape (issue #737 / ADR-083). The rule:
//
//   - shapeFunction: exactly one of {handler.js, handler.ts, handler.py,
//     handler.go} at the root AND none of the app markers (package.json /
//     requirements.txt / pyproject.toml / Pipfile / setup.py / go.mod /
//     Dockerfile). A README.md and dotfiles are ignored — most repos have
//     them and they don't change the shape.
//   - shapeApp: any app marker present at the root. App markers always win
//     over a co-located handler.* — a customer with `package.json +
//     handler.js` is unambiguously a Node app and must pass --function to
//     override.
//   - shapeUnknown: cwd is empty, missing, or contains only excluded files.
//
// The detector is intentionally minimal: it mirrors the top-level sniff rule
// the server's pkg/builderd/detect.go:41-95 applies to the uploaded tarball,
// so a CLI-detected shape matches what builderd will see on the other end.
// Files only ever seen during the build (node_modules, .git, __pycache__,
// vendor) are NOT app markers — the framework detector only counts the
// "primary" files, and so does shape.
func detectShape(srcDir string) shape {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return shapeUnknown
	}
	var (
		hasAppMarker bool
		handlerFile  string // name of the single handler.* if present
	)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Excluded files (build artifacts, OS junk) are not markers —
		// they don't change the shape. Match the same set defaultExcludeFiles
		// uses so behaviour is consistent with what gets packed.
		if defaultExcludeFiles[name] {
			continue
		}
		// Dotfiles (other than .git, which is excluded above) and
		// README* are ignored. The customer reads a README on GitHub,
		// not in a deployable function — and dotfiles (.env,
		// .dockerignore, .npmrc) are common and not shape-changing.
		if strings.HasPrefix(name, ".") || strings.HasPrefix(strings.ToLower(name), "readme") {
			continue
		}
		switch strings.ToLower(name) {
		// Keep in sync with detectFramework's app-marker switch and
		// pkg/builderd/detect.go:73-82. If you change one, change all.
		case "package.json", "requirements.txt", "pyproject.toml",
			"pipfile", "setup.py", "go.mod", "dockerfile":
			hasAppMarker = true
		}
		if functionHandlerFiles[strings.ToLower(name)] {
			// Second handler.* wins means the shape is ambiguous —
			// fall through to shapeApp (any co-located handler that
			// isn't a single, named handler.js signals "this is a
			// project, not a function").
			if handlerFile != "" {
				hasAppMarker = true
				continue
			}
			handlerFile = strings.ToLower(name)
		}
	}
	switch {
	case hasAppMarker:
		return shapeApp
	case handlerFile != "":
		return shapeFunction
	default:
		return shapeUnknown
	}
}

// inferFunctionRuntime picks the apid runtime + wire handler for a
// function-shaped repo. The runtime is keyed on the handler file's
// extension; the wire handler is the literal `handler.handler` value
// that imaged's function-layer manifest rewrites to /app/<runtime>.js
// (per the convention at cmd/gregale/templates/function-node/handler.js
// and defaultTemplateHandler at cmd/gregale/commands2.go:48). The bool
// is false when the cwd lacks a single, recognised handler file —
// callers should fall back to shapeUnknown in that case.
//
// This is the load-bearing helper that wires detectShape into the
// cmdDeployTarball flow: detectShape picks shapeFunction, then
// inferFunctionRuntime fills in runtime + handler for the multipart
// form. Both default to the same convention the function-* templates
// force today, so an existing function customer who runs
// `gregale deploy` against a hand-written handler.js gets the exact
// same wire shape they would have got via
// `gregale --template function-node --tarball ...`.
func inferFunctionRuntime(srcDir string) (runtime, handler string, ok bool) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return "", "", false
	}
	var picked string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if functionHandlerFiles[strings.ToLower(e.Name())] {
			if picked != "" {
				return "", "", false // ambiguous — multiple handlers
			}
			picked = strings.ToLower(e.Name())
		}
	}
	if picked == "" {
		return "", "", false
	}
	switch strings.ToLower(picked) {
	case "handler.js", "handler.ts":
		return runtimeNode22, defaultTemplateHandler, true
	case "handler.py":
		return runtimePython312, defaultTemplateHandler, true
	case "handler.go":
		return runtimeGo124, defaultTemplateHandler, true
	}
	return "", "", false
}

// defaultZeroConfigSourceCapMB is the conservative per-plan floor used when
// the CLI can't resolve the customer's plan (Whoami round-trip failed or
// skipped). The server rejects anything above 100 MB on Free/Hobby (see
// pkg/api/limits.go: SourceTarballMaxMB). The floor is the safest choice
// because a zero-config abort on Free is much better UX than a slow upload
// that ends in a 413. Customers on Pro/Scale who exceed 100 MB but fit within
// 250 MB should pass --tarball of a hand-built archive.
//
// Production callers (commands2.go) resolve the per-plan cap via
// api.MustLimitsFor(plan).SourceTarballMaxMB and thread it through; this
// constant is the floor for the cmd/gregale/pack_test.go harness where the
// plan is unknown.
const defaultZeroConfigSourceCapMB = 100

// packDirToTarGz walks srcDir and writes a gzipped tar archive to destPath. The
// archive's single top-level directory is filepath.Base(srcDir), preserving the
// invariant apid's validateTarballShape depends on (one project root). Symlinks,
// hardlinks and device nodes are rejected — apid rejects them too, so failing
// fast in the CLI is strictly better UX. Regular files are streamed with a fixed
// mtime for reproducibility, and each file is read through a LimitReader
// capped at capMB so a single runaway file aborts early instead of
// materialising its full size into the tar. After the archive is written the
// on-disk size is re-checked against the cap (gzip compression can change the
// byte count either way). Returns the count of regular files archived.
//
// capMB is the per-plan upload cap resolved by the caller from
// api.MustLimitsFor(plan).SourceTarballMaxMB. cmd/gregale's tests pass
// defaultZeroConfigSourceCapMB as a stable floor so the per-file / total-size
// tests have a deterministic budget.
//
// envOverride, when non-nil, is a rel-path → redacted-bytes map applied to
// the matching entry before it's written to the tarball. Used by the
// --secret-scan default path: pkg/secretscan.ScanEnvContent flags
// every line in a .env* file whose value matches a known credential pattern
// or exceeds the Shannon-entropy floor, and the caller passes the file's
// redacted content here so the offending values never enter the archive.
// Pass nil from callers that don't run the scan (e.g. the unit-test
// harness, or `gregale deploy --secret-scan=off`).
//
// The gzip→tar→walk shape mirrors cmd/gregale/templates/embed.go:TarGz.
func packDirToTarGz(srcDir, destPath string, capMB int, envOverride map[string][]byte, extraExcludes ...string) (regularFileCount int, err error) {
	return packDirToTarGzWithRoot(srcDir, destPath, capMB, envOverride, filepath.Base(srcDir), nil, extraExcludes...)
}

// packDirToTarGzFlat writes the directory contents relative to the archive
// root, without the historical single-directory transport wrapper. Workspace
// deploys use this shape so source_root is stable across Git HEAD archives and
// working-tree archives: both describe paths from the repository root.
func packDirToTarGzFlat(srcDir, destPath string, capMB int, envOverride map[string][]byte, extraExcludes ...string) (regularFileCount int, err error) {
	return packDirToTarGzWithRoot(srcDir, destPath, capMB, envOverride, "", nil, extraExcludes...)
}

// packDirToTarGzWithRoot is the implementation shared by the legacy wrapped
// packer and the flat repository-context packer. buildOnlyFiles contains
// generated regular files keyed relative to srcDir. They are written only to
// the archive and never materialized in the customer's source directory.
func packDirToTarGzWithRoot(srcDir, destPath string, capMB int, envOverride map[string][]byte, archiveRoot string, buildOnlyFiles map[string][]byte, extraExcludes ...string) (regularFileCount int, err error) {

	// Load .gregaleignore once (before the walk) so shouldExclude sees
	// the same patterns for every entry. Missing file → nil → no
	// project-level exclusions (defaults still apply).
	gitignorePatterns := loadGregaleignore(srcDir)
	extraExcludeSet := packExtraExcludeSet(srcDir, extraExcludes)

	f, err := os.Create(destPath)
	if err != nil {
		return 0, fmt.Errorf("create archive %s: %w", destPath, err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	defer func() {
		// Defer must close in tar→gzip→file order so gzip's trailer flushes
		// to disk before the file's Close. Idempotent: Close on a
		// previously-closed writer returns an error we ignore.
		_ = tw.Close()
		_ = gz.Close()
		_ = f.Close()
	}()

	// Collect first, sort, then write — a deterministic archive order makes
	// the output reproducible (same input → same bytes).
	type entry struct {
		abs  string
		rel  string // slash-separated, relative to srcDir (no root prefix)
		info os.FileInfo
	}
	var entries []entry
	walkErr := filepath.WalkDir(srcDir, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if p == srcDir {
			return nil
		}
		rel, rerr := filepath.Rel(srcDir, p)
		if rerr != nil {
			return rerr
		}
		relSlash := filepath.ToSlash(rel)
		if extraExcludeSet[relSlash] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if shouldExclude(relSlash, d.IsDir(), gitignorePatterns) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		mode := info.Mode()
		if mode&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink %q is not allowed in source tarballs (apid rejects symlink entries) — remove the symlink or pass --tarball of an archive you built yourself", relSlash)
		}
		if !d.IsDir() && !mode.IsRegular() {
			return fmt.Errorf("refusing to pack irregular file %q (device/socket/pipe)", relSlash)
		}
		entries = append(entries, entry{abs: p, rel: relSlash, info: info})
		return nil
	})
	if walkErr != nil {
		return 0, fmt.Errorf("walk %s: %w", srcDir, walkErr)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })
	capBytes := int64(capMB) * 1024 * 1024
	var totalUncompressed int64
	existingEntries := make(map[string]bool, len(entries))
	for _, e := range entries {
		existingEntries[e.rel] = true
	}

	for _, e := range entries {
		hdr, herr := tar.FileInfoHeader(e.info, "")
		if herr != nil {
			return 0, fmt.Errorf("header for %s: %w", e.rel, herr)
		}
		hdr.Name = e.rel
		if archiveRoot != "" {
			hdr.Name = archiveRoot + "/" + e.rel
		}
		if e.info.IsDir() {
			hdr.Name += "/"
		}
		hdr.ModTime = packEpoch
		hdr.AccessTime = time.Time{}
		hdr.ChangeTime = time.Time{}
		if e.info.IsDir() {
			if err := tw.WriteHeader(hdr); err != nil {
				return 0, fmt.Errorf("write header %s: %w", hdr.Name, err)
			}
			continue
		}
		// envOverride path: the secret-scan pass may have rewritten this
		// entry's bytes to redact credential-shaped lines (Stripe keys,
		// GitHub PATs, AWS access keys, etc.). When the rel path is in the
		// override map, write those bytes instead of reading from disk.
		// The override is consumed in-place (delete the key) so a single
		// caller can't accidentally re-pack the same override into two
		// destinations — the secret-scan pass is per-archive.
		if data, ok := envOverride[e.rel]; ok {
			hdr.Size = int64(len(data))
			if totalUncompressed > capBytes-hdr.Size {
				return 0, fmt.Errorf("uncompressed source is over the %d MB zero-config cap; trim large files or pass --tarball of a hand-built archive", capMB)
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return 0, fmt.Errorf("write header %s: %w", hdr.Name, err)
			}
			if _, err := tw.Write(data); err != nil {
				return 0, fmt.Errorf("write body %s: %w", hdr.Name, err)
			}
			totalUncompressed += hdr.Size
			delete(envOverride, e.rel)
			regularFileCount++
			continue
		}
		if e.info.Size() > capBytes-totalUncompressed {
			if e.info.Size() > capBytes {
				return 0, fmt.Errorf("refusing to pack %s: %d bytes > %d MB per-file cap (untracked large file? pass --tarball of a hand-built archive)",
					filepath.Base(e.abs), e.info.Size(), capMB)
			}
			return 0, fmt.Errorf("uncompressed source is over the %d MB zero-config cap; trim large files or pass --tarball of a hand-built archive", capMB)
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return 0, fmt.Errorf("write header %s: %w", hdr.Name, err)
		}
		written, err := copyRegular(tw, e.abs, capMB)
		if err != nil {
			return 0, err
		}
		totalUncompressed += written
		if totalUncompressed > capBytes {
			return 0, fmt.Errorf("uncompressed source is over the %d MB zero-config cap; trim large files or pass --tarball of a hand-built archive", capMB)
		}
		regularFileCount++
	}

	// Generated build markers belong in the transport archive but not in the
	// customer's project. Go function templates use this path for go.mod: a
	// local module marker would make the next zero-config deploy look like an
	// app, while the remote framework detector requires it to select Go.
	virtualNames := make([]string, 0, len(buildOnlyFiles))
	for rel := range buildOnlyFiles {
		rel = filepath.ToSlash(rel)
		if rel == "." || rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
			return 0, fmt.Errorf("invalid build-only archive path %q", rel)
		}
		if existingEntries[rel] {
			return 0, fmt.Errorf("build-only archive path %q collides with source file", rel)
		}
		virtualNames = append(virtualNames, rel)
	}
	sort.Strings(virtualNames)
	for _, rel := range virtualNames {
		data := buildOnlyFiles[rel]
		if int64(len(data)) > capBytes-totalUncompressed {
			return 0, fmt.Errorf("uncompressed source is over the %d MB zero-config cap; trim large files or pass --tarball of a hand-built archive", capMB)
		}
		name := rel
		if archiveRoot != "" {
			name = archiveRoot + "/" + rel
		}
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(data)),
			Typeflag: tar.TypeReg,
			ModTime:  packEpoch,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return 0, fmt.Errorf("write header %s: %w", hdr.Name, err)
		}
		if _, err := tw.Write(data); err != nil {
			return 0, fmt.Errorf("write body %s: %w", hdr.Name, err)
		}
		totalUncompressed += hdr.Size
		regularFileCount++
	}
	// Final size check. Close gzip→tar before statting so the on-disk size
	// is the actual packed size (gzip writes its trailer at Close). Close
	// is idempotent — the defer above also calls them on any return path.
	if err := tw.Close(); err != nil {
		_ = gz.Close()
		return 0, fmt.Errorf("close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return 0, fmt.Errorf("close gzip: %w", err)
	}
	st, err := os.Stat(destPath)
	if err != nil {
		return 0, fmt.Errorf("stat packed tarball: %w", err)
	}
	if st.Size() > capBytes {
		return 0, fmt.Errorf("packed cwd is %d MB, over the %d MB zero-config cap; trim large files or pass --tarball of a hand-built archive",
			st.Size()/(1024*1024), capMB)
	}
	return regularFileCount, nil
}

// copyRegular streams one regular file into the tar writer. It routes through
// openCustomerFile (commands5.go) — the vetted symlink-safe boundary — rather
// than a bare os.Open, both to satisfy the cmd/gregale os.Open tripwire and
// because the walked paths are customer-supplied (TOCTOU: a path Lstat'd as
// regular during the walk could be swapped for a symlink before we read it).
//
// The stream is wrapped in a LimitReader at capMB so a single runaway file
// (a 2 GB raw dataset committed by accident) aborts early with a clear
// error instead of streaming gigabytes through gzip→tar. capMB is the
// per-plan upload cap resolved by the caller from
// api.MustLimitsFor(plan).SourceTarballMaxMB.
func copyRegular(tw *tar.Writer, abs string, capMB int) (int64, error) {
	f, err := openCustomerFile(abs)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", abs, err)
	}
	defer func() { _ = f.Close() }()
	// Wrap the source in a LimitReader so a runaway file (a 2 GB raw
	// dataset committed by accident) aborts with at most capBytes+1
	// bytes streamed into the tar before we reject it. Reading +1
	// disambiguates "file is exactly capBytes" (allowed) from "file is
	// strictly larger" (rejected). The prior implementation did
	// io.Copy(tw, f) without a cap and only checked the size after the
	// entire file had already been streamed into gzip — a 2 GB file
	// would pay the full CPU/IO cost before the error surfaced.
	capBytes := int64(capMB) * 1024 * 1024
	lr := io.LimitReader(f, capBytes+1)
	n, err := io.Copy(tw, lr)
	if err != nil {
		return n, fmt.Errorf("copy %s: %w", abs, err)
	}
	if n > capBytes {
		return n, fmt.Errorf("refusing to pack %s: %d bytes > %d MB per-file cap (untracked large file? pass --tarball of a hand-built archive)",
			filepath.Base(abs), n, capMB)
	}
	return n, nil
}

// buildCreateRequest stamps the issue #737 / ADR-083 fields onto the
// CreateAppRequest the CLI hands to apid. Lives in commands2.go (next
// to the caller) so the function-testable wire-shape contract stays
// in one file; tests in commands2_test.go exercise it directly.

// resolveDeployShape runs the cwd detector and emits the customer-visible
// "Detected: …" line for issue #737 / ADR-083. The print goes BEFORE the
// multipart POST so the customer's first response from the CLI is the
// deploy shape. The explicit --function / --app flags short-circuit the
// detector (see the mutex check in cmdDeployTarball); this helper assumes
// they have already been mutex-checked. On shapeUnknown, an actionable
// error is returned — the caller turns it into a customer-visible
// printErr. The returned (runtime, handler) are non-empty only on the
// shapeFunction path; on shapeApp they are empty (server-side Railpack
// auto-detects). Allocated to live in pack.go (next to detectShape /
// inferFunctionRuntime) so the unit test exercises both the wire
// contract and the print line in one place.
//
// The print is suppressed when jsonOutput is true: the §3.2 --json
// contract requires stdout to be a single parseable JSON object, so
// a freeform "Detected: …" line would corrupt `gregale deploy --json
// | jq`. The shape is still resolved (the wire shape is the same
// either way); only the customer-visible banner is gated.
func resolveDeployShape(srcDir string, explicitFunction, explicitApp, jsonOutput bool, displayOverride ...string) (shape, string, string, error) {
	detected := detectShape(srcDir)
	if explicitFunction {
		detected = shapeFunction
	} else if explicitApp {
		detected = shapeApp
	}
	switch detected {
	case shapeUnknown:
		baseErr := fmt.Errorf(
			"no deployable source found in %s: expected package.json, requirements.txt / pyproject.toml / "+
				"Pipfile / setup.py, go.mod, or Dockerfile at the project root for an *app*, "+
				"OR a single handler.{js,ts,py,go} for a *function* — "+
				"or pass --image, --tarball, --template, --repo, --function, or --app",
			filepath.Base(srcDir))
		// Issue #744 / ADR-086: if the cwd has app markers at depth 1 (a
		// common monorepo layout — apps/web/package.json, services/api/go.mod),
		// surface a customer-visible hint pointing at `gregale scan --path .`.
		// The hint is wrapped in NestedMarkerHintError so printErr can route
		// it to stderr without corrupting --json stdout. Customers with deep
		// (depth-3+) monorepos still get the bare error — that's fine, the
		// reposcan tree handles them via `gregale scan` regardless.
		if detectNestedMarkerHint(srcDir) {
			return shapeUnknown, "", "", &NestedMarkerHintError{
				Dir: filepath.Base(srcDir),
				Hint: fmt.Sprintf(
					"Hint: found app markers under subdirectories of %s — this looks like a workspace (monorepo). "+
						"Run `gregale scan --path .` to decompose it into per-app plans, then apply them individually.",
					filepath.Base(srcDir)),
				Err: baseErr,
			}
		}
		return shapeUnknown, "", "", baseErr
	case shapeFunction:
		rt, hnd, ok := inferFunctionRuntime(srcDir)
		if !ok {
			// Issue #740 / DEPLOY-PROV-5 / ADR-087: when the user
			// passed --function on a directory that has an app
			// marker's version file but no handler.{js,ts,py,go} at
			// the root, surface the marker's version as a runtime
			// suggestion so the error message is actionable. The
			// suggestion is best-effort: when no version file is
			// present, OR the version maps to no whitelisted
			// runtime (e.g. an unreleased future major), we fall
			// back to the bare error rather than emit a malformed
			// `--runtime  --handler ...` arg.
			suggestion := funcErrorSuggestion(srcDir)
			return shapeUnknown, "", "", fmt.Errorf(
				"--function requires a single handler.{js,ts,py,go} at the project root; "+
					"found zero or ambiguous handler files in %s.%s",
				filepath.Base(srcDir), suggestion)
		}
		// The return values stay inferred so callers can fill only flags the
		// customer omitted. The optional display values let cmdDeploy show an
		// explicit --runtime / --handler in the banner, matching the values it
		// will actually send on the wire.
		displayRuntime, displayHandler := rt, hnd
		if len(displayOverride) > 0 && displayOverride[0] != "" {
			displayRuntime = displayOverride[0]
		}
		if len(displayOverride) > 1 && displayOverride[1] != "" {
			displayHandler = displayOverride[1]
		}
		// Issue #961 / Mega-A PR-2: surface the class (function/app)
		// alongside the runtime + handler so the banner matches the
		// BuildPlan field on the DeploymentResponse.
		if !jsonOutput {
			PrintOK(osStdout, "Detected: function, runtime=%s, handler=%s, class=function", displayRuntime, displayHandler)
		}
		return shapeFunction, rt, hnd, nil
	case shapeApp:
		fw := detectFramework(srcDir)
		// Issue #740 / DEPLOY-PROV-5 / ADR-087: mirror the
		// server-side parser in the CLI banner. The version is
		// informational; the server independently re-derives it
		// for build_provenance.framework_version.
		ver := detectFrameworkVersion(srcDir, fw)
		// Issue #961 / Mega-A PR-2: add `class=app` so the banner
		// mirrors the auto-detected BuildPlan field on the deployment
		// response. Mirrors the `class=function` line in the function
		// branch above.
		if !jsonOutput {
			if ver != "" {
				PrintOK(osStdout, "Detected: app, framework=%s, version=%s, class=app", fw, ver)
			} else {
				PrintOK(osStdout, "Detected: app, framework=%s, class=app", fw)
			}
		}
		return shapeApp, "", "", nil
	}
	return shapeUnknown, "", "", fmt.Errorf("internal: resolveDeployShape fell through")
}

// autoPackCwd is the zero-config entry point: it packs srcDir into a fresh temp
// tarball, detects the framework, and returns everything the deploy path needs.
// The caller owns the returned path and must os.Remove it. On any error the
// temp file (if created) is removed before returning. fileCount is the count
// of regular files archived — NOT the server-side entry count, which includes
// directory entries (see cmd/apid/deploy_inputs.go:maxSourceFiles).
//
// envOverride is the rel-path → redacted-bytes map produced by the
// secret-scan pass (see scanAndRedactEnvFiles). Pass nil to skip the scan
// entirely — used by `gregale deploy --secret-scan=off` and by callers
// that already vetted the inputs (cmd/e2e harness, pack_test.go).
func autoPackCwd(srcDir string, capMB int, envOverride map[string][]byte) (tarballPath string, fw framework, fileCount int, err error) {
	return autoPackSource(srcDir, srcDir, false, capMB, envOverride)
}

// autoPackSource packs packDir while detecting the build framework from
// detectDir. The distinction matters for workspace deploys: the repository is
// the BuildKit context, but the selected member remains the source root that
// determines the framework and working directory.
func autoPackSource(detectDir, packDir string, flat bool, capMB int, envOverride map[string][]byte, extraExcludes ...string) (tarballPath string, fw framework, fileCount int, err error) {
	// Error-explanations cluster (spec §6.4 amendment 1): warn-only
	// preflight that lifts the cluster's source-side hints via the
	// whycopy catalog. Hints are printed after the deploy summary by
	// the caller (cmdDeploy). The preflight does NOT fail the deploy.
	//
	// Cluster A: when --doctor-strict already ran runDoctorChecks on
	// srcDir (commands2.go:1097), the same loopback-bind + arch-mismatch
	// scans are already in the doctor's report. Skip the second
	// filepath.Walk per check to avoid ~2× the file-system overhead
	// on the customer's hot path. The doctor is fail-fast on errors
	// but still emits warn-only hints for these two — the post-deploy
	// summary remains useful for callers without --doctor-strict.
	for _, hint := range runPackPreflight(detectDir, doctorPreflightRan) {
		PrintWarn(osStderr, "%s", hint)
	}

	f, err := os.CreateTemp("", "gregale-cwd-*.tar.gz")
	if err != nil {
		return "", fwUnknown, 0, fmt.Errorf("create temp tarball: %w", err)
	}
	path := f.Name()
	_ = f.Close()

	buildOnlyFiles, err := functionGoBuildOnlyFiles(detectDir, packDir)
	if err != nil {
		_ = os.Remove(path)
		return "", fwUnknown, 0, err
	}

	var n int
	if flat {
		n, err = packDirToTarGzWithRoot(packDir, path, capMB, envOverride, "", buildOnlyFiles, extraExcludes...)
	} else {
		n, err = packDirToTarGzWithRoot(packDir, path, capMB, envOverride, filepath.Base(packDir), buildOnlyFiles, extraExcludes...)
	}
	if err != nil {
		_ = os.Remove(path)
		return "", fwUnknown, 0, err
	}
	return path, detectFramework(detectDir), n, nil
}

const functionGoBuildModule = "module gregale-function\n\ngo 1.24\n"

// functionGoBuildOnlyFiles returns the framework marker required by the remote
// Go builder when detectDir is a marker-free Go function. The path is relative
// to packDir so workspace deployments place go.mod beside the selected
// handler.go while retaining the full repository as build context.
func functionGoBuildOnlyFiles(detectDir, packDir string) (map[string][]byte, error) {
	if detectShape(detectDir) != shapeFunction {
		return nil, nil
	}
	runtime, _, ok := inferFunctionRuntime(detectDir)
	if !ok || runtime != runtimeGo124 {
		return nil, nil
	}
	rel, err := filepath.Rel(packDir, filepath.Join(detectDir, "go.mod"))
	if err != nil {
		return nil, fmt.Errorf("resolve Go function build module path: %w", err)
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return nil, fmt.Errorf("go function source %s is outside pack root %s", detectDir, packDir)
	}
	return map[string][]byte{rel: []byte(functionGoBuildModule)}, nil
}

// runPackPreflight scans the cwd for the 2 source-side failure modes
// the cluster's runtime detectors (commit 7-13) catch post-deploy,
// returning a slice of warn-only hints. The hints do NOT fail the
// deploy (per spec §6.4 amendment 1: preflight is warn-only); they're
// printed after the deploy summary so the customer can fix them
// before the next deploy. The whycopy catalog is the source of truth
// for hint prose — same path as cmdDoctor and the post-failure
// renderer.
//
// Cluster A: when skipDoctorScan is true, the loopback-bind and
// arch-mismatch scans are skipped because `gregale deploy --doctor-strict`
// already ran runDoctorChecks on the same srcDir. The deploy wire-in
// (commands2.go:1097) sets `doctorPreflightRan = true` before
// autoPackCwd fires. Saves 2× filepath.Walk traversals on the
// customer's repo on the --doctor-strict hot path.
//
// 2 checks:
//
//  1. Loopback bind: app.listen("127.0.0.1"...) or bind("127.0.0.1")
//     patterns in source — would trip app_loopback_bound post-deploy.
//  2. Arch mismatch: tarball contains a Mach-O / ARM aarch64 binary
//     — would trip app_arch_mismatch post-deploy (ENOEXEC in the
//     build VM's linux/amd64 kernel).
//
// The PORT-unset check is intentionally OMITTED: the runtime
// contract auto-provides PORT=8080, and scanning source for
// `process.env.PORT` doesn't catch the failure mode (it's a
// MISSING-listener failure, not a missing-env failure). The
// runtime detector catches the real case via app_not_listening;
// preflighting it here would produce false positives for every
// customer who hardcodes 8080 in their app.
//
// Errors during the scan (permission denied, etc.) are silently
// swallowed: a hard error here would block the deploy on noise.
// The customer already gets the runtime prose from the catalog
// if the deploy then fails.
// doctorPreflightRan is set by the --doctor-strict deploy wire-in
// (commands2.go:1097) before autoPackCwd fires, to signal that
// runDoctorChecks has already walked srcDir. runPackPreflight
// consults it to skip the loopback-bind and arch-mismatch scans
// and avoid the double-walk on the --doctor-strict hot path
// (review finding F7). Defaults to false — callers that don't set
// it (e.g. older test paths) get the original walk behaviour.
var doctorPreflightRan bool

func runPackPreflight(srcDir string, skipDoctorScan bool) []string {
	hints := []string{}
	if skipDoctorScan {
		// --doctor-strict already walked srcDir via runDoctorChecks
		// (commands2.go:1097). The doctor's loopback-bind and arch
		// checks emit error-class findings that exit 1 before this
		// point, so any surviving warn-only noise from those two
		// would be redundant. The remaining preflight paths (none
		// today; reserved for future checks) would still run below.
		return hints
	}
	if hit := preflightLoopbackBind(srcDir); hit != "" {
		hints = append(hints, hit)
	}
	if hit := preflightArchMismatch(srcDir); hit != "" {
		hints = append(hints, hit)
	}
	return hints
}

// preflightLoopbackBind surfaces the app_loopback_bound hint before
// the failed wake. Returns the whycopy hint string when the
// pattern is found; empty otherwise.
func preflightLoopbackBind(srcDir string) string {
	sources := scanSource(srcDir, loopbackBindRegex, 1)
	if len(sources) == 0 {
		return ""
	}
	p := whycopy.Decorate(&api.Problem{}, api.CodeAppLoopbackBound, nil)
	return p.Hint
}

// preflightArchMismatch surfaces the app_arch_mismatch hint when
// the cwd contains a Mach-O or ARM aarch64 binary.
func preflightArchMismatch(srcDir string) string {
	sources := scanSource(srcDir, archMismatchRegex, 1)
	if len(sources) == 0 {
		return ""
	}
	p := whycopy.Decorate(&api.Problem{}, api.CodeAppArchMismatch, nil)
	return p.Hint
}

// envFileBaseNames is the set of file names that count as "env files" for
// the purpose of the secret-scan pass. We scan exactly these patterns —
// not arbitrary *.env* — because scanning every file in the source tree
// for credential patterns would produce noise (compiled binaries, lock
// files, generated code). The list mirrors the conventional dotenv
// spelling (.env, .env.local, .env.production, etc.) plus the explicit
// docker-compose env_file convention.
var envFileBaseNames = map[string]bool{
	".env":             true,
	".env.local":       true,
	".env.development": true,
	".env.production":  true,
	".env.test":        true,
	".env.staging":     true,
	".env.example":     true,
	".env.sample":      true,
	".env.template":    true,
	".env.defaults":    true,
	"env":              true, // bare 'env' file is uncommon but used by some frameworks
}

// isEnvFileBase reports whether a slash-separated rel path's basename is a
// dotenv-style file the scanner should walk. Files inside excluded
// directories (.git, node_modules, etc.) are skipped by the caller before
// we ever see them, so this is purely a basename check.
func isEnvFileBase(relSlash string) bool {
	base := relSlash
	if i := strings.LastIndex(relSlash, "/"); i >= 0 {
		base = relSlash[i+1:]
	}
	if envFileBaseNames[base] {
		return true
	}
	// Globs like ".env.<anything>" that aren't in the explicit list above
	// (e.g. .env.production.us-east-1) still count — the dotenv convention
	// is open-ended.
	if strings.HasPrefix(base, ".env.") {
		return true
	}
	return false
}

// secretScanMode is the closed enum for the --secret-scan flag. v1 (PR #862)
// shipped on/off; v2 adds strict (abort on any finding) and source-tree
// (scan non-.env files too). The aliases are accepted by
// parseSecretScanFlag below.
type secretScanMode int

const (
	modeOff secretScanMode = iota
	modeWarn
	modeStrict
	modeSourceTree
)

func (m secretScanMode) String() string {
	switch m {
	case modeOff:
		return "off"
	case modeWarn:
		return "warn"
	case modeStrict:
		return "strict"
	case modeSourceTree:
		return "source-tree"
	default:
		return "unknown"
	}
}

// isScanEnabled reports whether the mode produces any scan pass. modeStrict
// and modeSourceTree both run the scan; the difference is what they do
// AFTER a finding (abort vs warn vs continue).
func (m secretScanMode) isScanEnabled() bool { return m != modeOff }

// isSourceTree reports whether the mode also walks non-.env files. Only
// modeSourceTree returns true; everything else is .env-only (PR #862's
// scope).
func (m secretScanMode) isSourceTree() bool { return m == modeSourceTree }

// isStrict reports whether the mode should abort (exit 1) when any
// finding is produced.
func (m secretScanMode) isStrict() bool { return m == modeStrict }

// parseSecretScanFlag normalises a --secret-scan value (string from the
// flag parser) to the secretScanMode used by both `gregale deploy` and
// `gregale env push`. Accepts the common on/off spellings (on/off,
// true/false, 1/0, yes/no) for backward compatibility with v1 (PR #862),
// plus the new v2 spellings (`strict`, `source-tree`, `tree`,
// `src-tree`). Returns a non-nil error for any other value so a typo
// (--secret-scan=warn, --secret-scan=yes-please) fails fast at flag parse
// time rather than silently defaulting.
//
// Shared between commands2.go (deploy) and commands5.go (env push). The
// literal values are lifted to named consts so goconst (golangci-lint
// v2.4.0) does not flag the repeated string literals. Mirrors the
// requireSignedTrue / requireSignedFalse pattern in
// commands_app_security.go:50-59 and forceDefaultFalse in
// commands_sign_keys_test.go:36.
const (
	secretScanOn   = "on"
	secretScanOff  = "off"
	secretScanYes  = "yes"
	secretScanNo   = "no"
	secretScanOne  = "1"
	secretScanZero = "0"

	secretScanStrict      = "strict"
	secretScanSourceTree  = "source-tree"
	secretScanSourceTree2 = "tree"
	secretScanSourceTree3 = "src-tree"
)

func parseSecretScanFlag(raw string) (secretScanMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case secretScanOff, requireSignedFalse, secretScanZero, secretScanNo:
		return modeOff, nil
	case secretScanOn, requireSignedTrue, secretScanOne, secretScanYes:
		return modeWarn, nil
	case secretScanStrict:
		return modeStrict, nil
	case secretScanSourceTree, secretScanSourceTree2, secretScanSourceTree3:
		return modeSourceTree, nil
	default:
		return modeOff, fmt.Errorf("--secret-scan must be 'on'/'off'/'strict'/'source-tree' (got %q)", raw)
	}
}

// scanAndRedactEnvFiles walks srcDir once, finds every dotenv-style file,
// runs pkg/secretscan against it, and returns:
//   - overrides: rel-path → rewritten bytes, where offending lines are
//     replaced by "<KEY>=<REDACTED # secret detected: stripe_live>".
//     Empty for files where every line was a secret (the file is still
//     included in the archive with that placeholder body so the customer's
//     app doesn't see a missing .env at boot).
//   - findings:  every secretscan.Finding produced, with the same
//     File path as the override key so the caller can render one warning
//     line per finding to stderr.
//
// The function is total: a srcDir with no env files returns (nil, nil).
// An unredactable file (read error) is returned as a finding-with-error
// rather than panicking; the caller decides whether that's a hard stop.
//
// mode == modeOff is the fast-path escape hatch (--secret-scan=off): the
// walk still happens but no files are scanned and no overrides are
// produced, so the archive's bytes are identical to the pre-PR behaviour.
//
// mode == modeSourceTree additionally calls scanAndRedactTreeFiles and
// merges its findings + overrides into the same return values. The two
// passes share an accumulator so callers don't have to distinguish which
// pass produced which finding.
//
// mode == modeStrict short-circuits on the FIRST finding and returns a
// *StrictSecretScanError so the caller's printErr path renders a unified
// 422 envelope + per-finding details.
func scanAndRedactEnvFiles(srcDir string, mode secretScanMode) (overrides map[string][]byte, findings []secretscan.Finding, err error) {
	if !mode.isScanEnabled() {
		return nil, nil, nil
	}
	// errs accumulates per-file failures so a transient open/read error on
	// the first .env file doesn't poison the whole walk. errors.Join at
	// the end returns every failure to the caller; partial success is
	// still surfaced via overrides/findings.
	var errs []error
	walkErr := filepath.WalkDir(srcDir, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(srcDir, p)
		if rerr != nil {
			return rerr
		}
		relSlash := filepath.ToSlash(rel)
		if !isEnvFileBase(relSlash) {
			return nil
		}
		// openCustomerFile enforces the cmd/gregale os.Open tripwire and
		// is symlink-safe (commands5.go). env files are customer-supplied
		// so we route through the vetted boundary.
		f, ferr := openCustomerFile(p)
		if ferr != nil {
			errs = append(errs, fmt.Errorf("open %s: %w", relSlash, ferr))
			return nil
		}
		data, rerr := io.ReadAll(io.LimitReader(f, treeScanMaxBytes+1))
		_ = f.Close()
		if rerr != nil {
			errs = append(errs, fmt.Errorf("read %s: %w", relSlash, rerr))
			return nil
		}
		if int64(len(data)) > treeScanMaxBytes {
			return nil
		}
		fileFindings := secretscan.ScanEnvContent(relSlash, data)
		if len(fileFindings) == 0 {
			return nil
		}
		findings = append(findings, fileFindings...)
		if overrides == nil {
			overrides = make(map[string][]byte)
		}
		overrides[relSlash] = redactEnvContent(data, fileFindings)
		return nil
	})
	if walkErr != nil {
		errs = append(errs, fmt.Errorf("walk %s: %w", srcDir, walkErr))
	}
	// Source-tree pass: same accumulator, walks non-.env files too.
	if mode.isSourceTree() {
		treeOverrides, treeFindings, treeErr := scanAndRedactTreeFiles(srcDir)
		if treeErr != nil {
			errs = append(errs, treeErr)
		}
		for k, v := range treeOverrides {
			if overrides == nil {
				overrides = make(map[string][]byte)
			}
			overrides[k] = v
		}
		findings = append(findings, treeFindings...)
	}
	// Strict mode: surface as a typed error so the CLI's printErr path
	// emits a unified 422 envelope + per-finding details. We return ALL
	// findings (not just the first) so the customer sees the full list
	// and can fix every line in one pass.
	if mode.isStrict() && len(findings) > 0 {
		return overrides, findings, &StrictSecretScanError{Findings: findings, Hint: "move detected secrets to `gregale secrets set` (see " + cliDocsURL + ")"}
	}
	return overrides, findings, errors.Join(errs...)
}

// redactEnvContent rewrites the env file so every line that produced a
// Finding has its value replaced by a placeholder. Lines are matched by
// their 1-indexed Line so a multi-finding file redacts only the offending
// lines, leaving clean KEY=VALUE pairs intact (this matters for
// stripe_test false-positives in mixed dev/prod env files).
//
// The placeholder preserves the original KEY so the customer's app can
// still boot without crashing on a missing env var; the comment makes the
// redaction self-documenting in the deployed tarball.
func redactEnvContent(data []byte, findings []secretscan.Finding) []byte {
	bad := make(map[int]string, len(findings))
	for _, f := range findings {
		bad[f.Line] = f.Provider
	}
	var out bytes.Buffer
	lineNo := 1
	// Trailing-newline policy: if the input ends with \n we preserve
	// exactly that. If it does NOT end with \n, the output also lacks
	// one. We only emit \n BETWEEN lines (after every consumed chunk).
	// The previous shape appended \n unconditionally, which inflated
	// files with no trailing newline (e.g. "A\nB" → "A\nB\n") and
	// emitted "\n" for empty input — both of which changed shell `source
	// .env` semantics in subtle ways.
	hasTrailing := len(data) > 0 && data[len(data)-1] == '\n'
	first := true
	writeSep := func() {
		if !first {
			out.WriteByte('\n')
		}
		first = false
	}
	for {
		i := bytes.IndexByte(data, '\n')
		var line []byte
		if i < 0 {
			line = data
			data = nil
		} else {
			line = data[:i]
			data = data[i+1:]
		}
		if provider, hit := bad[lineNo]; hit {
			eq := bytes.IndexByte(line, '=')
			if eq >= 0 {
				key := bytes.TrimSpace(line[:eq])
				key = bytes.TrimPrefix(key, []byte("export "))
				writeSep()
				fmt.Fprintf(&out, "%s=<REDACTED # secret detected: %s>", key, provider)
			} else {
				// No '=' — keep the line as-is. Should be impossible given
				// the scan matched it, but defensive: never crash the
				// packer on a malformed line we can't safely rewrite.
				writeSep()
				out.Write(line)
			}
		} else {
			writeSep()
			out.Write(line)
		}
		lineNo++
		if len(data) == 0 {
			if hasTrailing {
				out.WriteByte('\n')
			}
			return out.Bytes()
		}
	}
}

// formatSecretScanWarnings was an early string-slice renderer that lived
// here while the CLI was being wired up. It was replaced by
// renderSecretScanWarnings in commands2.go, which has direct access to
// the CLI's PrintWarn + osStderr. Removed in favour of the single
// renderer so a future contributor changing the message format only
// touches one place. The two-line output shape is documented inline
// at renderSecretScanWarnings.

// treeScanMaxBytes caps the per-file size we'll read when running a
// source-tree scan. The cap exists so a 200-MiB customer-uploaded
// artefact (e.g. a bundled video or a compiled binary with a .ts
// extension) doesn't OOM the CLI. The cap is read-only — files larger
// than the cap are SKIPPED, not truncated, because a truncated PEM
// block can fail to match the armour pattern and silently slip
// through. The same cap is enforced server-side in
// cmd/apid/secretscan.go so the two paths agree on what is scanned.
const treeScanMaxBytes = 1 << 20

// scanAndRedactTreeFiles walks srcDir once for non-.env files and runs
// pkg/secretscan against every text-shaped file. Returns the same shape
// as scanAndRedactEnvFiles (overrides + findings) so the caller can
// merge them into a single accumulator.
//
// Behaviour contract:
//
//   - Files already classified as env files (isEnvFileBase) are SKIPPED;
//     the env pass handles them with the .env-shaped placeholder and
//     would otherwise double-emit findings.
//   - Files inside an excluded subdir (defaultExcludeDirs, e.g.
//     .git, node_modules, vendor, __pycache__) are skipped — same as
//     the tarball walk.
//   - Files larger than treeScanMaxBytes (1 MiB) are skipped (a
//     truncated read can drop a multi-line PEM in the middle).
//   - Binary files (IsTextFile == false) are skipped — NUL-byte probe
//   - http.DetectContentType per pkg/secretscan/textfile.go.
//   - openCustomerFile enforces the cmd/gregale os.Open tripwire
//     (commands5.go) so a symlinked source file doesn't exfiltrate
//     unrelated host paths.
//
// Files that produce at least one finding have their contents
// rewritten via redactFileContent; clean files do not appear in the
// overrides map (the original is shipped as-is from disk).
func scanAndRedactTreeFiles(srcDir string) (overrides map[string][]byte, findings []secretscan.Finding, err error) {
	var errs []error
	walkErr := filepath.WalkDir(srcDir, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			// Skip excluded subtrees at any depth.
			if p != srcDir && isExcludedSubdir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(srcDir, p)
		if rerr != nil {
			return rerr
		}
		relSlash := filepath.ToSlash(rel)
		// Skip env files — the env pass has already handled them with
		// the right placeholder shape. Re-scanning would re-emit
		// findings and double-report.
		if isEnvFileBase(relSlash) {
			return nil
		}
		// Cheap size gate before opening: stat first, bail out early
		// on anything over the cap. d.Info() returns *FileInfo; for
		// symlinks it follows the link so d.IsDir() already filtered
		// those.
		info, ierr := d.Info()
		if ierr != nil {
			errs = append(errs, fmt.Errorf("stat %s: %w", relSlash, ierr))
			return nil
		}
		if info.Size() > treeScanMaxBytes {
			return nil
		}
		f, ferr := openCustomerFile(p)
		if ferr != nil {
			errs = append(errs, fmt.Errorf("open %s: %w", relSlash, ferr))
			return nil
		}
		// Read with an io.LimitReader so a maliciously-sized entry that
		// slipped past the stat gate (race) still can't OOM us. The
		// cap is enforced strictly — we drop the file on overflow
		// rather than scanning a truncated copy.
		data, rerr := io.ReadAll(io.LimitReader(f, treeScanMaxBytes+1))
		_ = f.Close()
		if rerr != nil {
			errs = append(errs, fmt.Errorf("read %s: %w", relSlash, rerr))
			return nil
		}
		if int64(len(data)) > treeScanMaxBytes {
			return nil
		}
		// Text-file gate: NUL-byte probe + http.DetectContentType.
		// Skipping binaries keeps the false-positive rate sane.
		if !secretscan.IsTextFile(relSlash, data) {
			return nil
		}
		fileFindings := secretscan.ScanFile(relSlash, data)
		if len(fileFindings) == 0 {
			return nil
		}
		findings = append(findings, fileFindings...)
		if overrides == nil {
			overrides = make(map[string][]byte)
		}
		overrides[relSlash] = redactFileContent(data, fileFindings)
		return nil
	})
	if walkErr != nil {
		errs = append(errs, fmt.Errorf("walk %s: %w", srcDir, walkErr))
	}
	return overrides, findings, errors.Join(errs...)
}

// redactFileContent rewrites a non-env source file so that every line
// that produced a Finding is replaced by a safe placeholder. Unlike
// redactEnvContent (which preserves the original KEY in KEY=VALUE form),
// this helper has no canonical separator to recover; instead it emits a
// self-documenting XML-comment-style placeholder that's valid in every
// common text format (.json, .yaml, .ts, .go, .py, .toml, .html).
//
// JSON: a stray `<!-- -->` is invalid JSON. To stay parse-safe for
// JSON files we emit a sentinel key instead: `"REDACTED_KEY_<n>": "..."
// ` with a comment-style rationale string. Files where the redacted line
// is itself inside a string literal (e.g. an `aws_access_key_id: AKIA...`
// YAML entry) get the inline-comment placeholder because YAML comments
// are universal; the loader just sees an empty string after stripping.
//
// Why a single shape across formats: customers shouldn't have to
// understand which placeholder is safe in which file. The placeholder
// is a single XML-style comment that a JSON parser will reject only
// if it lands inside a string literal, in which case the file was
// already broken (a JSON file with literal HTML in a string value).
// The redactor intentionally leaves the file parse-broken so the
// customer sees the failure at build time and investigates, rather
// than shipping a half-redacted artefact whose deployment then fails
// for an unrelated reason.
//
// The 1-indexed line numbers from the scanner map 1:1 to the file's
// line breaks; comments and blank lines are handled by the upstream
// scanner (skip), so the rewriting pass is straightforward.
func redactFileContent(data []byte, findings []secretscan.Finding) []byte {
	bad := make(map[int]string, len(findings))
	for _, f := range findings {
		bad[f.Line] = f.Provider
	}
	var out bytes.Buffer
	lineNo := 1
	hasTrailing := len(data) > 0 && data[len(data)-1] == '\n'
	first := true
	writeSep := func() {
		if !first {
			out.WriteByte('\n')
		}
		first = false
	}
	for {
		i := bytes.IndexByte(data, '\n')
		var line []byte
		if i < 0 {
			line = data
			data = nil
		} else {
			line = data[:i]
			data = data[i+1:]
		}
		if provider, hit := bad[lineNo]; hit {
			writeSep()
			// XML-style comment placeholder. Same shape regardless
			// of source format so customers see one consistent
			// marker across their tree.
			fmt.Fprintf(&out, "<!-- REDACTED secret detected: %s -->", provider)
		} else {
			writeSep()
			out.Write(line)
		}
		lineNo++
		if len(data) == 0 {
			if hasTrailing {
				out.WriteByte('\n')
			}
			return out.Bytes()
		}
	}
}
