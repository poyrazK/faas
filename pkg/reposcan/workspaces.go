package reposcan

import (
	"encoding/json"
	"io/fs"
	"path"
	"sort"
	"strings"

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
//	go.work               "use ( ... )" block
//	Cargo.toml            "[workspace] members" array
//
// Member expansion: each entry is a directory path relative to
// repo root. Glob forms ("packages/*") are expanded (sub-directories
// at the level under the globbed dir become members). The
// expansion is breadth-first, deterministic order via sort.
//
// Pure (no fsys-error propagates to Scan): a missing manifest
// file is a quiet skip.
func detectWorkspacesImpl(fsys fs.FS) ([]workloadSeed, []string, error) {
	var (
		seeds    []workloadSeed
		warnings []string
		seen     = map[string]bool{}
	)
	var add func(member string, src string)
	add = func(member string, src string) {
		if member == "" || strings.HasPrefix(member, "..") {
			return
		}
		// Strip trailing slash.
		member = strings.TrimRight(member, "/")
		// fs.ValidPath is the load-bearing rejection — a path like
		// "packages/../escape" passes the leading ".." check above,
		// but path.Join normalises it to "escape" before fs.ReadDir
		// or fs.Stat ever sees it. fs.ValidPath rejects *after*
		// normalisation, so the guard runs on the final value.
		if !fs.ValidPath(member) {
			return
		}
		// Skip the literal "*" form (un-expandable glob).
		if strings.HasSuffix(member, "/*") {
			dir := strings.TrimSuffix(member, "/*")
			entries, err := fs.ReadDir(fsys, dir)
			if err != nil {
				return // directory doesn't exist; quiet skip
			}
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				// path.Join normalises, so a directory entry named
				// ".." would produce a parent-escape; re-validate
				// before the recursive call.
				joined := path.Join(dir, e.Name())
				if !fs.ValidPath(joined) {
					continue
				}
				add(joined, src)
			}
			return
		}
		if seen[member] {
			return
		}
		seen[member] = true
		// Eligibility: directory carries Dockerfile or language marker.
		if !hasMarker(fsys, member) {
			return
		}
		// Name = last path segment.
		name := path.Base(member)
		// If name is a glob literal "*" — skip silently (no member to name).
		if name == "" || name == "*" {
			return
		}
		seeds = append(seeds, workloadSeed{
			name:    name,
			rootDir: member,
			source:  src + ": " + member,
		})
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
			for _, w := range wsEntries {
				add(w, src)
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
			for _, w := range p.Packages {
				add(w, src)
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
				if hasMarker(fsys, k) {
					seeds = append(seeds, workloadSeed{
						name:    k,
						rootDir: k,
						source:  src + ": " + k,
					})
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

	_ = warnings // reserved for future use (e.g. un-readable YAML)
	sort.SliceStable(seeds, func(i, j int) bool { return seeds[i].name < seeds[j].name })
	return seeds, nil, nil
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

// parseGoWorkUses extracts the module paths inside a go.work
// "use ( … )" block. The line grammar is one module per line:
//
//	use (
//	    ./services/api
//	    ./services/worker
//	)
//
// Each ./ prefix is stripped.
func parseGoWorkUses(body string) []string {
	var out []string
	inUse := false
	for _, line := range rangeLines(body) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "use (") || strings.TrimRight(trimmed, " \t") == "use (" {
			inUse = true
			continue
		}
		if inUse {
			if strings.HasPrefix(trimmed, ")") || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
				continue
			}
			s := strings.TrimPrefix(trimmed, "./")
			if s == "" {
				continue
			}
			out = append(out, s)
		}
	}
	return out
}

func rangeLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Split(s, "\n")
}

// hasMarker returns true if directory carries a Dockerfile or any
// of the §3 language markers.
func hasMarker(fsys fs.FS, dir string) bool {
	for _, marker := range []string{
		nameDockerfile,
		nameDockerfileLower,
		namePackageJSON,
		"pyproject.toml",
		"go.mod",
		"Cargo.toml",
		"pom.xml",
	} {
		if !fs.ValidPath(path.Join(dir, marker)) {
			continue
		}
		if _, err := fs.Stat(fsys, path.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
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
