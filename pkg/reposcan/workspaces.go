package reposcan

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"gopkg.in/yaml.v3"
)

// detectWorkspacesImpl enumerates workspace-graph members. A member
// is a workload only if the directory carries a Dockerfile or a
// recognized language marker. Otherwise it is only a workspace graph
// entry and is not buildable on its own.
//
// Recognized workspace sources:
//
//	package.json          top-level "workspaces": ["a", "b/*"]
//	pnpm-workspace.yaml   top-level "packages": ["a", "b/*"]
//	turbo.json            "pipeline" or "$pipeline" object
//	nx.json               "projects" map or array
//	go.work               "use ./module" or "use ( ... )" block
//	Cargo.toml            "[workspace] members" array
//
// Member expansion: each entry is a directory path relative to
// repo root. Glob forms ("packages/*") are expanded (sub-directories
// at the level under the globbed dir become members). The
// expansion is breadth-first, deterministic order via sort.
//
// Pure (no fsys-error propagates to Scan): a missing manifest
// file is a quiet skip.
func detectWorkspacesImpl(fsys fs.FS, includeLibraryMarkers bool) ([]workloadSeed, []string, error) {
	var (
		seeds    []workloadSeed
		warnings []string
		seen     = map[string]bool{}
	)
	add := func(member string, src string) {
		member = strings.TrimRight(strings.TrimPrefix(strings.TrimSpace(member), "./"), "/")
		if member == "" || strings.HasPrefix(member, "..") || !fs.ValidPath(member) {
			return
		}
		if seen[member] {
			return
		}
		seen[member] = true
		seed, runnable, reason := runnableWorkspaceSeed(fsys, member, src, includeLibraryMarkers)
		if !runnable {
			if reason != "" {
				warnings = append(warnings, reason)
			}
			return
		}
		seeds = append(seeds, seed)
	}
	addPatterns := func(patterns []string, src string) error {
		members, err := expandWorkspacePatterns(fsys, patterns)
		if err != nil {
			return fmt.Errorf("reposcan: %s: %w", src, err)
		}
		for _, member := range members {
			add(member, src)
		}
		return nil
	}

	// package.json — workspaces.
	if body, src, err := readFirstValidFile(fsys, []string{namePackageJSON}); err != nil && !isQuiet(err) {
		return nil, nil, err
	} else if body != nil {
		var pj struct {
			Workspaces json.RawMessage `json:"workspaces"`
		}
		if err := json.Unmarshal(body, &pj); err == nil && len(pj.Workspaces) > 0 {
			wsEntries := parseWorkspacesField(pj.Workspaces)
			if err := addPatterns(wsEntries, src); err != nil {
				return nil, nil, err
			}
		}
	}

	// pnpm-workspace.yaml — packages.
	if body, src, err := readFirstValidFile(fsys, []string{namePnpmWorkspace}); err != nil && !isQuiet(err) {
		return nil, nil, err
	} else if body != nil {
		var p struct {
			Packages []string `yaml:"packages"`
		}
		if err := yaml.Unmarshal(body, &p); err == nil {
			if err := addPatterns(p.Packages, src); err != nil {
				return nil, nil, err
			}
		}
	}

	// turbo.json — pipeline or $pipeline.
	if body, src, err := readFirstValidFile(fsys, []string{nameTurboJSON}); err != nil && !isQuiet(err) {
		return nil, nil, err
	} else if body != nil {
		var pj struct {
			Pipeline  map[string]json.RawMessage `json:"pipeline"`
			XPipeline map[string]json.RawMessage `json:"$pipeline"`
		}
		if err := json.Unmarshal(body, &pj); err == nil {
			keys := make([]string, 0, len(pj.Pipeline)+len(pj.XPipeline))
			for k := range pj.Pipeline {
				keys = append(keys, k)
			}
			for k := range pj.XPipeline {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				// Treat pipeline keys as member names; map them onto
				// member paths of either "packages/<k>" (Turbo
				// convention) or the root if no namespaces are
				// in use. The confirm table can show the
				// ambiguity.
				add(k, src)
			}
		}
	}

	// nx.json — projects.
	if body, src, err := readFirstValidFile(fsys, []string{nameNxJSON}); err != nil && !isQuiet(err) {
		return nil, nil, err
	} else if body != nil {
		var pj struct {
			Projects map[string]json.RawMessage `json:"projects"`
		}
		if err := json.Unmarshal(body, &pj); err == nil {
			keys := make([]string, 0, len(pj.Projects))
			for k := range pj.Projects {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				// Nx project keys are arbitrary names, not
				// directory paths. Treat them as the workload
				// name with RootDir="" so a later Tier-3
				// convention reader can pair or the user can
				// resolve via a faas.yaml override (Phase 3+).
				seed, runnable, reason := runnableWorkspaceSeed(fsys, k, src, includeLibraryMarkers)
				if runnable {
					// Nx project keys are the existing workload identity; the
					// filesystem helper derives runnability but must not rename it.
					seed.name = k
					seeds = append(seeds, seed)
				} else if reason != "" {
					warnings = append(warnings, reason)
				}
			}
		}
	}

	// go.work — use ( ... ).
	if body, src, err := readFirstValidFile(fsys, []string{nameGoWork, nameGoWorkSum}); err != nil && !isQuiet(err) {
		return nil, nil, err
	} else if body != nil && strings.HasSuffix(src, ".work") {
		// Skip go.work.sum — it's a hash file.
		if src == "go.work" {
			mods := parseGoWorkUses(string(body))
			for _, m := range mods {
				add(m, src)
			}
		}
	}

	sort.SliceStable(seeds, func(i, j int) bool { return seeds[i].name < seeds[j].name })
	sort.Strings(warnings)
	return seeds, warnings, nil
}

// parseWorkspacesField turns package.json's "workspaces" field
// (which can be a string array OR an object with "packages"/"nohoist")
// into a flat list of directory paths.
func parseWorkspacesField(raw json.RawMessage) []string {
	// Try array form first.
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	var obj struct {
		Packages []string `json:"packages"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj.Packages
	}
	return nil
}

// parseGoWorkUses extracts module paths from a go.work file. The Go
// workspace grammar accepts both direct use directives and parenthesized
// use blocks; modfile also keeps unrelated directives out of the result.
//
//	use (
//	    ./services/api
//	    ./services/worker
//	)
//
// Each ./ prefix is stripped.
func parseGoWorkUses(body string) []string {
	wf, err := modfile.ParseWork("go.work", []byte(body), nil)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(wf.Use))
	for _, use := range wf.Use {
		if use == nil {
			continue
		}
		member := strings.TrimPrefix(use.Path, "./")
		if member != "" {
			out = append(out, member)
		}
	}
	return out
}

func expandWorkspacePatterns(fsys fs.FS, patterns []string) ([]string, error) {
	var directories []string
	if err := fs.WalkDir(fsys, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && name != "." {
			directories = append(directories, name)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Strings(directories)
	selected := make(map[string]bool)
	for _, raw := range patterns {
		pattern := strings.TrimSpace(raw)
		exclude := strings.HasPrefix(pattern, "!")
		if exclude {
			pattern = strings.TrimSpace(strings.TrimPrefix(pattern, "!"))
		}
		pattern = strings.TrimPrefix(pattern, "./")
		pattern = strings.TrimRight(pattern, "/")
		if pattern == "" || strings.HasPrefix(pattern, "/") || strings.Contains(pattern, "\\") {
			return nil, fmt.Errorf("invalid workspace pattern %q", raw)
		}
		invalidPath := false
		for _, segment := range strings.Split(pattern, "/") {
			if segment == ".." || segment == "." || segment == "" {
				invalidPath = true
				break
			}
		}
		if invalidPath {
			continue
		}
		matcher, err := compileWorkspacePattern(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid workspace pattern %q: %w", raw, err)
		}
		for _, directory := range directories {
			if !matcher.MatchString(directory) {
				continue
			}
			if exclude {
				delete(selected, directory)
			} else {
				selected[directory] = true
			}
		}
	}
	members := make([]string, 0, len(selected))
	for member := range selected {
		members = append(members, member)
	}
	sort.Strings(members)
	return members, nil
}

func compileWorkspacePattern(pattern string) (*regexp.Regexp, error) {
	if strings.ContainsAny(pattern, "[]{}()") {
		return nil, fmt.Errorf("unsupported glob operator")
	}
	var expression strings.Builder
	expression.WriteByte('^')
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				if i+2 < len(pattern) && pattern[i+2] == '/' {
					expression.WriteString("(?:[^/]+/)*")
					i += 2
					continue
				}
				expression.WriteString(".*")
				i++
			} else {
				expression.WriteString("[^/]*")
			}
		case '?':
			expression.WriteString("[^/]")
		default:
			expression.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	expression.WriteByte('$')
	return regexp.Compile(expression.String())
}

func runnableWorkspaceSeed(fsys fs.FS, member, src string, includeLibraryMarkers bool) (workloadSeed, bool, string) {
	name := path.Base(member)
	if name == "" || name == "." {
		return workloadSeed{}, false, ""
	}
	base := workloadSeed{name: name, rootDir: member, source: src + ": " + member}
	for _, dockerfile := range []string{nameDockerfile, nameDockerfileLower} {
		if info, err := fs.Stat(fsys, path.Join(member, dockerfile)); err == nil && !info.IsDir() {
			return base, true, ""
		}
	}
	packagePath := path.Join(member, namePackageJSON)
	if body, err := fs.ReadFile(fsys, packagePath); err == nil {
		var manifest struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(body, &manifest) == nil {
			for _, candidate := range []struct {
				name  string
				class Class
			}{{"start", ClassHTTP}, {"serve", ClassHTTP}, {"worker", ClassWorker}} {
				if command := strings.TrimSpace(manifest.Scripts[candidate.name]); command != "" {
					base.command = []string{command}
					base.commandShell = true
					base.class = candidate.class
					return base, true, ""
				}
			}
		}
		if includeLibraryMarkers {
			return base, true, ""
		}
		return workloadSeed{}, false, "reposcan: " + src + ": " + member + " is a package library with no runnable start target — skipping"
	}
	for _, marker := range []string{"pyproject.toml", "go.mod", "Cargo.toml", "pom.xml"} {
		if info, err := fs.Stat(fsys, path.Join(member, marker)); err == nil && !info.IsDir() {
			return base, true, ""
		}
	}
	return workloadSeed{}, false, ""
}

func rangeLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Split(s, "\n")
}

// isQuiet classifies readFirstValidFile errors as expected
// (no-such-file) so workspace detectors don't propagate them to
// Scan().
func isQuiet(err error) bool {
	if err == nil {
		return false
	}
	// readFirstValidFile never returns (nil, non-nil) under
	// normal use; any non-nil error is "expected, skip".
	return true
}
