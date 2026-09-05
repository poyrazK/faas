package markers

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/sourcecontext"
	"github.com/onebox-faas/faas/pkg/tarball"
)

// maxVersionFileBytes caps every version-marker file we read.
// The parsers only need a handful of bytes; a 64 KB bound
// refuses the OOM-by-tarball attack and keeps the parser O(1)
// on real source. Used by both the FS and the tarball paths.
const maxVersionFileBytes = 64 * 1024

// VersionFromFS returns the source-declared language version for
// the given framework, or "" if no version marker is found or
// any parser fails. Used by the CLI's
// "Detected: app, framework=node, version=22.11.0" banner
// (issue #740 / DEPLOY-PROV-5 / ADR-087). Symmetric with
// VersionFromTarball on identical inputs (parity contract).
//
// Per-framework priority order (mirrors ADR-087 §4):
//
//	node    → .nvmrc → package.json::engines.node → ""
//	python  → .python-version → pyproject.toml::requires-python → ""
//	go      → go.mod → "go X.Y" directive → ""
//	docker  → "" (containers pin via FROM; out-of-scope)
//	unknown → "" (no anchor)
//
// Any parse failure → "". A 64 KB cap per file (maxVersionFileBytes)
// refuses the OOM-by-source attack.
func VersionFromFS(fsys fs.FS, fw Framework) string {
	switch fw {
	case FrameworkNode:
		if nvmrc := readFSFile(fsys, ".nvmrc"); nvmrc != "" {
			if v := normalizeSemver(stripLines(nvmrc)); v != "" {
				return v
			}
		}
		if pkg := readFSFile(fsys, "package.json"); pkg != "" {
			if v := versionFromPackageJSONNode(pkg); v != "" {
				return v
			}
		}
	case FrameworkPython:
		if pyver := readFSFile(fsys, ".python-version"); pyver != "" {
			if v := normalizePythonVersion(stripLines(pyver)); v != "" {
				return v
			}
		}
		if pyproj := readFSFile(fsys, "pyproject.toml"); pyproj != "" {
			if v := versionFromPyprojectRequires(pyproj); v != "" {
				return v
			}
		}
	case FrameworkGo:
		if gomod := readFSFile(fsys, "go.mod"); gomod != "" {
			if v := versionFromGoModDirective(gomod); v != "" {
				return v
			}
		}
	case FrameworkDocker, FrameworkUnknown:
		// explicit out-of-scope / no anchor
	}
	return ""
}

// VersionFromTarball is the server-side mirror: opens a gzipped
// tarball at path and returns the inferred version. Symmetric
// with VersionFromFS so the two answers agree on the same
// input. Errors are intentionally swallowed: parse failures
// are not failures of the build — the column is observability,
// not a build-pipeline input.
//
//nolint:forbidigo // path is the apid-spooled tarball; same trust rationale as DetectFromTarball above.
func VersionFromTarball(path string, fw Framework) string {
	return VersionFromTarballAtRoot(path, fw, "")
}

// VersionFromTarballAtRoot is the workspace-aware mirror of
// VersionFromTarball. sourceRoot is relative to the logical archive root;
// empty means the legacy archive root.
func VersionFromTarballAtRoot(path string, fw Framework, sourceRoot string) string {
	switch fw {
	case FrameworkNode:
		if nvmrc := readTarballFileAtRoot(path, ".nvmrc", sourceRoot); nvmrc != "" {
			if v := normalizeSemver(stripLines(nvmrc)); v != "" {
				return v
			}
		}
		if pkg := readTarballFileAtRoot(path, "package.json", sourceRoot); pkg != "" {
			if v := versionFromPackageJSONNode(pkg); v != "" {
				return v
			}
		}
	case FrameworkPython:
		if pyver := readTarballFileAtRoot(path, ".python-version", sourceRoot); pyver != "" {
			if v := normalizePythonVersion(stripLines(pyver)); v != "" {
				return v
			}
		}
		if pyproj := readTarballFileAtRoot(path, "pyproject.toml", sourceRoot); pyproj != "" {
			if v := versionFromPyprojectRequires(pyproj); v != "" {
				return v
			}
		}
	case FrameworkGo:
		if gomod := readTarballFileAtRoot(path, "go.mod", sourceRoot); gomod != "" {
			if v := versionFromGoModDirective(gomod); v != "" {
				return v
			}
		}
	case FrameworkDocker, FrameworkUnknown:
		// explicit out-of-scope / no anchor
	}
	return ""
}

// readFSFile reads up to maxVersionFileBytes of the named file
// from fsys. Returns "" on any error or oversized entry so the
// caller treats the file as "not present" — the build pipeline
// never depends on version detection. Errors are intentionally
// swallowed: parse failures are not failures of the build.
func readFSFile(fsys fs.FS, name string) string {
	f, err := fsys.Open(name)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	if st.Size() > maxVersionFileBytes {
		return "" // refuse OOM; treat as missing
	}
	lr := io.LimitReader(f, maxVersionFileBytes+1)
	buf, err := io.ReadAll(lr)
	if err != nil {
		return ""
	}
	if len(buf) > maxVersionFileBytes {
		return ""
	}
	return string(buf)
}

// readTarballFile extracts the top-level file named entryName
// from the gzipped tarball at path and returns its contents
// (capped at maxVersionFileBytes). Returns "" on any error or
// oversized entry. Mirrors the original
// pkg/builderd/detectversion.go::readTarFile verbatim — the only
// change is the function name to disambiguate from readFSFile.
func readTarballFile(path, entryName string) string {
	return readTarballFileAtRoot(path, entryName, "")
}

func readTarballFileAtRoot(path, entryName, sourceRoot string) string {
	root, err := sourcecontext.Normalize(sourceRoot)
	if err != nil {
		return ""
	}
	logicalRoot, err := tarball.ResolveSourceRoot(path, sourceRoot)
	if err != nil {
		return ""
	}
	//nolint:forbidigo // path is the apid-spooled tarball; same trust rationale as DetectFromTarball.
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return ""
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	needle := strings.ToLower(strings.TrimPrefix(entryName, "./"))
	if root != sourcecontext.DefaultRoot {
		needle = strings.ToLower(logicalRoot + "/" + strings.TrimPrefix(entryName, "./"))
	} else if logicalRoot != "" {
		needle = strings.ToLower(logicalRoot + "/" + needle)
	}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return ""
		}
		if err != nil {
			return ""
		}
		// Ignore archive metadata and directories. The shared root prefix
		// above promotes only the one transport wrapper used by GitHub
		// codeload; ordinary nested configs remain excluded.
		switch hdr.Typeflag {
		case tar.TypeDir, tar.TypeXHeader, tar.TypeXGlobalHeader,
			tar.TypeGNULongName, tar.TypeGNULongLink:
			continue
		}
		name := strings.ToLower(strings.TrimPrefix(hdr.Name, "./"))
		if name != needle {
			continue
		}
		// Cap read. io.LimitReader silently truncates at N; we use
		// ReadAll with a hard cap so a 1 GB file doesn't OOM the
		// build goroutine.
		lr := io.LimitReader(tr, maxVersionFileBytes+1)
		buf, err := io.ReadAll(lr)
		if err != nil {
			return ""
		}
		if len(buf) > maxVersionFileBytes {
			return "" // treat as missing
		}
		return string(buf)
	}
}

// stripLines returns the first non-empty trimmed line of s.
// .nvmrc and .python-version are single-line files in practice;
// we accept multi-line but only consider the first non-blank,
// non-comment line.
func stripLines(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line
	}
	return ""
}

// versionLike is the dotted-version matcher used by both the
// node and python normalizers. The .python-version file
// commonly writes "3.11" (two-component) so the regex accepts
// X.Y or X.Y.Z. The matcher's first hit is returned, so a body
// like "engines.node >= 22.11.0" yields "22.11.0".
var versionLike = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?`)

// normalizeSemver strips a leading "v" and extracts the first
// dotted version it can find. Returns "" on no match.
func normalizeSemver(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	return versionLike.FindString(s)
}

// normalizePythonVersion strips a leading "v" and returns the
// first dotted version. .python-version files are commonly just
// "3.11." Kept as a named entry point for symmetry — the
// implementation is identical to normalizeSemver today.
func normalizePythonVersion(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	return versionLike.FindString(s)
}

// versionFromPackageJSONNode parses engines.node from a
// package.json body. Strips common operators (>=, ^, ~) and
// returns the bare version. Returns "" if engines.node is
// missing or not a string.
func versionFromPackageJSONNode(body string) string {
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
	// engines.node can be a string ("22.11.0") or a range
	// (">=22.11.0"). We accept string only — a non-string is a
	// malformed manifest.
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
	s = strings.TrimSpace(s)
	return normalizeSemver(s)
}

// pyprojectRequiresRe matches the leading
//
//	requires-python = ">=3.11"
//
// line in a pyproject.toml body. The capture group is the bare
// string after the equals sign, with quotes stripped.
var pyprojectRequiresRe = regexp.MustCompile(`(?i)requires-python\s*=\s*["']([^"']+)["']`)

// versionFromPyprojectRequires parses the bare string version.
// We do NOT parse the full PEP 508 spec — a strict "==3.13" pin
// is reported as a best-effort because the build pipeline never
// reads this value.
func versionFromPyprojectRequires(body string) string {
	m := pyprojectRequiresRe.FindStringSubmatch(body)
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
	val = strings.TrimSpace(val)
	// PEP 508 allows things like "3.11.0,<3.13" — we take the
	// first comma-separated token, then normalize.
	if comma := strings.Index(val, ","); comma >= 0 {
		val = strings.TrimSpace(val[:comma])
	}
	return normalizePythonVersion(val)
}

// goModDirectiveRe matches the leading "go X.Y" or "go X.Y.Z"
// directive in a go.mod file.
var goModDirectiveRe = regexp.MustCompile(`(?m)^\s*go\s+(\d+\.\d+(?:\.\d+)?)\s*$`)

// versionFromGoModDirective returns the first "go X.Y" line's
// version. Returns "" if no such directive is present.
func versionFromGoModDirective(body string) string {
	m := goModDirectiveRe.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return m[1]
}
