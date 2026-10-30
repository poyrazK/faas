package markers

import (
	"encoding/json"
	"regexp"
	"strings"
)

var (
	pipfilePythonRe = regexp.MustCompile(`(?m)^\s*python_(?:full_)?version\s*=\s*["']([^"']+)["']`)
	misePythonRe    = regexp.MustCompile(`(?m)^\s*(?:tools\.)?["']?python["']?\s*=\s*(.+?)\s*$`)
)

// PythonVersionSpecsAtRoot returns every Python version declaration a source
// makes, as written: the first line of .python-version and runtime.txt,
// Pipfile's python_version or python_full_version, a python entry in
// .tool-versions, mise.toml or .mise.toml, and packages.python in
// railpack.json. A value this reader cannot unpack (a TOML array or table)
// is returned raw, so a caller that must not replace a declared version sees
// one it does not recognize.
func PythonVersionSpecsAtRoot(path, sourceRoot string) []string {
	files := readTarballFilesAtRoot(path, sourceRoot,
		".python-version", "runtime.txt", "Pipfile", ".tool-versions", "mise.toml", ".mise.toml", "railpack.json")
	var specs []string
	add := func(spec string) {
		if spec = strings.TrimSpace(spec); spec != "" {
			specs = append(specs, spec)
		}
	}
	add(stripLines(files[".python-version"]))
	add(stripLines(files["runtime.txt"]))
	if m := pipfilePythonRe.FindStringSubmatch(files["Pipfile"]); m != nil {
		add(m[1])
	}
	for _, line := range strings.Split(files[".tool-versions"], "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "python" {
			add(fields[1]) // the first listed version is the default
		}
	}
	for _, name := range []string{"mise.toml", ".mise.toml"} {
		if m := misePythonRe.FindStringSubmatch(files[name]); m != nil {
			add(unquoteTOMLString(m[1]))
		}
	}
	add(railpackPackageVersion(files["railpack.json"], "python"))
	return specs
}

// RailpackPackageVersionAtRoot returns the version railpack.json pins for a
// package ("node", "python"), or "". A non-string value is returned raw.
func RailpackPackageVersionAtRoot(path, sourceRoot, pkg string) string {
	return railpackPackageVersion(readTarballFileAtRoot(path, "railpack.json", sourceRoot), pkg)
}

func railpackPackageVersion(body, pkg string) string {
	var config struct {
		Packages map[string]json.RawMessage `json:"packages"`
	}
	if strings.TrimSpace(body) == "" || json.Unmarshal([]byte(body), &config) != nil {
		return ""
	}
	raw, ok := config.Packages[pkg]
	if !ok {
		return ""
	}
	var version string
	if json.Unmarshal(raw, &version) != nil {
		return string(raw)
	}
	return version
}

func unquoteTOMLString(value string) string {
	for _, quote := range []string{`"`, `'`} {
		if inner, ok := strings.CutPrefix(value, quote); ok {
			if inner, ok = strings.CutSuffix(inner, quote); ok && !strings.ContainsAny(inner, `"'`) {
				return inner
			}
		}
	}
	return value
}
