package builderd

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// maxVersionFileBytes caps every version-marker file we read out of the
// tarball. The parsers only need a handful of bytes; a 64 KB bound
// refuses the OOM-by-tarball attack and keeps the parser O(1) on real
// source. Mirrors the size cap at the customer-facing CLI mirror in
// cmd/gregale/pack.go::detectFrameworkVersion.
const maxVersionFileBytes = 64 * 1024

// detectVersion reads the source tarball at path and returns the
// inferred language version for the given framework, or "" if no
// version can be determined. The function is purely additive: every
// parser returns "" on any parse error (malformed JSON, missing
// fields, comments-only pyproject.toml) so a bad source file never
// breaks the build. The build pipeline never reads this value — it
// exists for operator observability (ADR-087).
//
// Per-framework priority order:
//
//	node    → .nvmrc → package.json::engines.node → ""
//	python  → .python-version → pyproject.toml::requires-python → ""
//	go      → go.mod → "go X.Y" directive → ""
//	docker  → "" (containers pin via FROM; explicit out-of-scope per
//	           the issue #740 body — only the four marker sources
//	           mentioned in the issue are surfaced)
//	unknown → "" (no framework to anchor the parser)
//
// Dockerfile FROM parsing is intentionally out of scope. Operators
// who want stricter pinning can set FAAS_DEPLOY_BASE_REF_<RUNTIME>
// on the control plane (ADR-052).
//
//nolint:forbidigo // path is the apid-spooled tarball; same rationale as Detect above.
func detectVersion(path string, fw Framework) (string, error) {
	switch fw {
	case FrameworkNode:
		nvmrc, err := readTarFile(path, ".nvmrc")
		if err == nil && nvmrc != "" {
			if v := normalizeSemver(stripLines(nvmrc)); v != "" {
				return v, nil
			}
		}
		pkg, err := readTarFile(path, "package.json")
		if err == nil && pkg != "" {
			if v := versionFromPackageJSONNode(pkg); v != "" {
				return v, nil
			}
		}
	case FrameworkPython:
		pyver, err := readTarFile(path, ".python-version")
		if err == nil && pyver != "" {
			if v := normalizePythonVersion(stripLines(pyver)); v != "" {
				return v, nil
			}
		}
		pyproj, err := readTarFile(path, "pyproject.toml")
		if err == nil && pyproj != "" {
			if v := versionFromPyprojectRequires(pyproj); v != "" {
				return v, nil
			}
		}
	case FrameworkGo:
		gomod, err := readTarFile(path, "go.mod")
		if err == nil && gomod != "" {
			if v := versionFromGoModDirective(gomod); v != "" {
				return v, nil
			}
		}
	case FrameworkDocker, FrameworkUnknown:
		// explicit out-of-scope / no anchor
	}
	return "", nil
}

// readTarFile extracts the top-level file named entryName from the
// gzipped tarball at path and returns its contents (capped at
// maxVersionFileBytes). Returns "" on any error so the caller treats
// the file as "not present" — the build pipeline never depends on
// version detection.
func readTarFile(path, entryName string) (string, error) {
	//nolint:forbidigo // path is the apid-spooled tarball; same trust rationale as Detect above.
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open: %w", err)
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	needle := strings.ToLower(entryName)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("tar: %w", err)
		}
		// Top-level only — nested configs (e.g. apps/web/.nvmrc) are
		// not part of the source-marker scan. Mirrors Detector.Detect.
		if strings.Contains(hdr.Name, "/") {
			continue
		}
		if strings.ToLower(hdr.Name) != needle {
			continue
		}
		// Cap read. io.LimitReader silently truncates at N; we use
		// ReadAll with a hard cap so a 1 GB file doesn't OOM the
		// build goroutine.
		lr := io.LimitReader(tr, maxVersionFileBytes+1)
		buf, err := io.ReadAll(lr)
		if err != nil {
			return "", fmt.Errorf("read: %w", err)
		}
		if len(buf) > maxVersionFileBytes {
			return "", nil // treat as missing
		}
		return string(buf), nil
	}
}

// stripLines returns the first non-empty trimmed line of s. .nvmrc and
// .python-version are single-line files in practice; we accept
// multi-line but only consider the first line.
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

// semverLike is a permissive dotted-version matcher: 1.2.3, 22.11.0,
// 1.24. Optional pre-release suffix is stripped. The first match is
// captured so callers see the bare version.
var semverLike = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?`)

// pythonSemverLike matches dotted versions of the form 3.11.0 / 3.11.
// Defaults to 3-component, but the regex accepts 2-component because
// .python-version commonly writes "3.11" without patch.
var pythonSemverLike = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?`)

// normalizeSemver strips a leading "v" and extracts the first dotted
// version it can find. Returns "" on no match.
func normalizeSemver(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if m := semverLike.FindString(s); m != "" {
		return m
	}
	return ""
}

// normalizePythonVersion strips a leading "v" and returns the first
// dotted version. .python-version files are commonly just "3.11."
func normalizePythonVersion(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if m := pythonSemverLike.FindString(s); m != "" {
		return m
	}
	return ""
}

// versionFromPackageJSONNode parses engines.node from a package.json
// body. Strips common operators (>=, ^, ~) and returns the bare
// version. Returns "" if engines.node is missing or not a string.
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
	// engines.node can be a string ("22.11.0") or a range (">=22.11.0").
	// We accept string only — a non-string is a malformed manifest.
	var s string
	if err := json.Unmarshal(pkg.Engines.Node, &s); err != nil {
		return ""
	}
	s = strings.TrimSpace(s)
	// Strip the leading operator (^, ~, >=, >, <=, <, =).
	for _, op := range []string{">=", "<=", ">", "<", "^", "~", "="} {
		if strings.HasPrefix(s, op) {
			s = strings.TrimPrefix(s, op)
			break
		}
	}
	s = strings.TrimSpace(s)
	return normalizeSemver(s)
}

// versionFromPyprojectRequires pulls the first dotted version out of
// the requires-python = ">=3.11" line. Strips the operator prefix.
var pyprojectRequiresRe = regexp.MustCompile(`(?i)requires-python\s*=\s*["']([^"']+)["']`)

// versionFromPyprojectRequires parses the bare string version. We do
// NOT parse the full PEP 508 spec — a strict "==3.13" pin is reported
// as a best-effort because the build pipeline never reads this value.
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
	// PEP 508 allows things like "3.11.0,<3.13" — we take the first
	// comma-separated token, then normalize.
	if comma := strings.Index(val, ","); comma >= 0 {
		val = strings.TrimSpace(val[:comma])
	}
	return normalizePythonVersion(val)
}

// goModDirectiveRe matches the leading "go X.Y" or "go X.Y.Z"
// directive in a go.mod file.
var goModDirectiveRe = regexp.MustCompile(`(?m)^\s*go\s+(\d+\.\d+(?:\.\d+)?)\s*$`)

// versionFromGoModDirective returns the first "go X.Y" line's version.
// Returns "" if no such directive is present.
func versionFromGoModDirective(body string) string {
	m := goModDirectiveRe.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return m[1]
}
