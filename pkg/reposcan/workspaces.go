package reposcan

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
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

	// nx.json — legacy root projects plus current per-project project.json and
	// package-level nx configuration.
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
				config := nxProjectConfig{Name: k, Root: nxLegacyProjectRoot(k, pj.Projects[k])}
				upsertNxWorkspaceSeed(fsys, &seeds, seen, config, src, includeLibraryMarkers, &warnings)
			}
		}
		projects, discoverErr := discoverNxProjectConfigs(fsys)
		if discoverErr != nil {
			return nil, nil, fmt.Errorf("reposcan: %s: %w", src, discoverErr)
		}
		for _, project := range projects {
			upsertNxWorkspaceSeed(fsys, &seeds, seen, project, project.Source, includeLibraryMarkers, &warnings)
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

	// Cargo.toml — [workspace] members and exclude. Cargo exclusions are
	// evaluated after members so a broad member glob cannot add them back.
	if body, src, err := readFirstValidFile(fsys, []string{"Cargo.toml"}); err != nil && !isQuiet(err) {
		return nil, nil, err
	} else if body != nil {
		var manifest struct {
			Workspace struct {
				Members []string `toml:"members"`
				Exclude []string `toml:"exclude"`
			} `toml:"workspace"`
		}
		if _, err := toml.Decode(string(body), &manifest); err == nil && len(manifest.Workspace.Members) > 0 {
			patterns := append([]string(nil), manifest.Workspace.Members...)
			for _, excluded := range manifest.Workspace.Exclude {
				patterns = append(patterns, "!"+excluded)
			}
			if err := addPatterns(patterns, src); err != nil {
				return nil, nil, err
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
	name := workspaceWorkloadName(fsys, member)
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

func workspaceWorkloadName(fsys fs.FS, member string) string {
	declared := ""
	if body, err := fs.ReadFile(fsys, path.Join(member, namePackageJSON)); err == nil {
		var manifest struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(body, &manifest) == nil {
			declared = manifest.Name
		}
	}
	if declared == "" {
		if body, err := fs.ReadFile(fsys, path.Join(member, "Cargo.toml")); err == nil {
			var manifest struct {
				Package struct {
					Name string `toml:"name"`
				} `toml:"package"`
			}
			if _, err := toml.Decode(string(body), &manifest); err == nil {
				declared = manifest.Package.Name
			}
		}
	}
	if declared == "" {
		declared = path.Base(member)
	}
	return canonicalWorkspaceWorkloadName(declared, member)
}

func canonicalWorkspaceWorkloadName(declared, member string) string {
	if strings.TrimSpace(declared) == "" {
		declared = path.Base(member)
	}
	raw := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(declared, "@")))
	var normalized strings.Builder
	lastHyphen := false
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			normalized.WriteRune(r)
			lastHyphen = false
			continue
		}
		if !lastHyphen && normalized.Len() > 0 {
			normalized.WriteByte('-')
			lastHyphen = true
		}
	}
	slug := strings.Trim(normalized.String(), "-")
	if len(slug) < 3 {
		parent := path.Base(path.Dir(member))
		if parent != "." && parent != "/" && parent != "" {
			slug = canonicalWorkspaceWorkloadName(parent+"-"+slug, "")
		} else {
			slug = strings.Trim("app-"+slug, "-")
		}
	}
	if len(slug) > 40 {
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(raw+"\x00"+member)))
		prefix := strings.TrimRight(slug[:31], "-")
		slug = prefix + "-" + digest[:8]
	}
	return slug
}

type nxTargetConfig struct {
	Command string `json:"command"`
	Options struct {
		Command  string   `json:"command"`
		Commands []string `json:"commands"`
	} `json:"options"`
}

type nxProjectConfig struct {
	Name        string                    `json:"name"`
	Root        string                    `json:"root"`
	ProjectType string                    `json:"projectType"`
	Targets     map[string]nxTargetConfig `json:"targets"`
	Source      string                    `json:"-"`
}

func nxLegacyProjectRoot(name string, raw json.RawMessage) string {
	var direct string
	if json.Unmarshal(raw, &direct) == nil && direct != "" {
		return normalizeWorkspaceMember(direct)
	}
	var config nxProjectConfig
	if json.Unmarshal(raw, &config) == nil && config.Root != "" {
		return normalizeWorkspaceMember(config.Root)
	}
	return normalizeWorkspaceMember(name)
}

func discoverNxProjectConfigs(fsys fs.FS) ([]nxProjectConfig, error) {
	var projects []nxProjectConfig
	err := fs.WalkDir(fsys, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		base := path.Base(name)
		switch base {
		case "project.json":
			body, err := fs.ReadFile(fsys, name)
			if err != nil {
				return err
			}
			var project nxProjectConfig
			if !decodeWorkspaceJSON(body, &project) {
				return nil
			}
			if project.Root == "" {
				project.Root = path.Dir(name)
			}
			project.Root = normalizeWorkspaceMember(project.Root)
			project.Source = name
			projects = append(projects, project)
		case namePackageJSON:
			body, err := fs.ReadFile(fsys, name)
			if err != nil {
				return err
			}
			var manifest struct {
				Name string          `json:"name"`
				Nx   json.RawMessage `json:"nx"`
			}
			if !decodeWorkspaceJSON(body, &manifest) || len(manifest.Nx) == 0 || string(manifest.Nx) == "null" {
				return nil
			}
			var project nxProjectConfig
			if !decodeWorkspaceJSON(manifest.Nx, &project) {
				return nil
			}
			if project.Name == "" {
				project.Name = manifest.Name
			}
			if project.Root == "" {
				project.Root = path.Dir(name)
			}
			project.Root = normalizeWorkspaceMember(project.Root)
			project.Source = name + "#nx"
			projects = append(projects, project)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(projects, func(i, j int) bool {
		if projects[i].Root != projects[j].Root {
			return projects[i].Root < projects[j].Root
		}
		return projects[i].Source < projects[j].Source
	})
	return projects, nil
}

// decodeWorkspaceJSON treats unrelated malformed workspace metadata as an
// unsupported discovery hint. Authoritative manifest parsing is handled by
// the dedicated detectors, which return customer-facing syntax errors.
func decodeWorkspaceJSON(body []byte, dst any) bool {
	return json.Unmarshal(body, dst) == nil
}

func normalizeWorkspaceMember(member string) string {
	member = strings.TrimSpace(member)
	for strings.HasPrefix(member, "./") {
		member = strings.TrimPrefix(member, "./")
	}
	member = strings.TrimRight(member, "/")
	if member == "." {
		return ""
	}
	return member
}

func nxTargetCommand(targets map[string]nxTargetConfig) (string, Class) {
	for _, candidate := range []struct {
		Name  string
		Class Class
	}{{"serve", ClassHTTP}, {"start", ClassHTTP}, {"dev", ClassHTTP}, {"worker", ClassWorker}} {
		target, ok := targets[candidate.Name]
		if !ok {
			continue
		}
		command := strings.TrimSpace(target.Command)
		if command == "" {
			command = strings.TrimSpace(target.Options.Command)
		}
		if command == "" && len(target.Options.Commands) == 1 {
			command = strings.TrimSpace(target.Options.Commands[0])
		}
		if command != "" {
			return command, candidate.Class
		}
	}
	return "", ""
}

func upsertNxWorkspaceSeed(
	fsys fs.FS,
	seeds *[]workloadSeed,
	seen map[string]bool,
	project nxProjectConfig,
	source string,
	includeLibraryMarkers bool,
	warnings *[]string,
) {
	root := normalizeWorkspaceMember(project.Root)
	if root == "" || strings.HasPrefix(root, "..") || !fs.ValidPath(root) {
		return
	}
	name := canonicalWorkspaceWorkloadName(project.Name, root)
	command, class := nxTargetCommand(project.Targets)
	seed, runnable, reason := runnableWorkspaceSeed(fsys, root, source, includeLibraryMarkers)
	if command != "" {
		if !runnable {
			seed = workloadSeed{rootDir: root, source: source + ": " + root}
		}
		seed.command = []string{command}
		seed.commandShell = true
		seed.class = class
		runnable = true
	}
	if !runnable {
		if reason != "" {
			*warnings = append(*warnings, reason)
		}
		return
	}
	seed.name = name
	for i := range *seeds {
		if (*seeds)[i].rootDir != root {
			continue
		}
		if command != "" {
			(*seeds)[i].command = append([]string(nil), seed.command...)
			(*seeds)[i].commandShell = true
			(*seeds)[i].class = class
		}
		(*seeds)[i].name = name
		return
	}
	seen[root] = true
	*seeds = append(*seeds, seed)
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
