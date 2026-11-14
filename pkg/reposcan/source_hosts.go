// adr: 20261112223000

package reposcan

import (
	"bufio"
	"bytes"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Source-host detection bounds: a scan reads at most this much source per
// workload, so a large repository costs a bounded walk, never a full read.
const (
	sourceHostMaxFiles     = 400
	sourceHostMaxFileBytes = 256 << 10
	sourceHostMaxPerWork   = 5
)

var sourceHostExtensions = map[string]bool{
	".py": true, ".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".go": true, ".cs": true,
	".java": true, ".kt": true, ".rb": true, ".php": true, ".rs": true, ".ex": true, ".exs": true,
}

var sourceHostSkipDirs = map[string]bool{
	"node_modules": true, "vendor": true, ".git": true, "dist": true, "build": true, "target": true,
	"bin": true, "obj": true, "__pycache__": true, ".venv": true, "venv": true, "test": true, "tests": true,
	"__tests__": true, "spec": true, "testdata": true, "fixtures": true,
}

// composeHostName is a service name Docker resolves as a host: `db` is one,
// though it is too short for an app slug.
var composeHostName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// envLookup marks a line that reads the host from the environment; a
// literal there is a fallback default, which is fine on Gregale once the
// variable is set.
var envLookup = regexp.MustCompile(`(?i)getenv|environ|process\.env|GetEnvironmentVariable|ENV\[|System\.getenv|env::var|os\.LookupEnv`)

// hostPatterns returns the patterns that find name used as a network host:
// host="redis", postgres://u:p@db/x, Server=db;, "redis:6379", Connect("redis").
func hostPatterns(name string) []*regexp.Regexp {
	n := regexp.QuoteMeta(name)
	return []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bhost(?:name)?["']?\s*[:=]\s*["']` + n + `["']`),
		regexp.MustCompile(`(?i)(?:^|[;"'\s])(?:server|host|data source|addr|address)\s*=\s*` + n + `\s*[;"']`),
		regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://(?:[^"'\s@/]*@)?` + n + `(?::\d+)?(?:[/"'\s?]|$)`),
		regexp.MustCompile(`["']` + n + `:\d{2,5}["']`),
		regexp.MustCompile(`\(\s*["']` + n + `["']\s*\)`),
	}
}

// detectSourceHosts records, for each source-built workload, where its code
// dials another service by its Compose name (ADR-20261112223000 §1). Docker
// resolves those names on the project network; Gregale does not, so the
// code must read the address from the environment instead.
func detectSourceHosts(fsys fs.FS, workloads []Workload, managed []Managed) {
	names := map[string]bool{}
	for _, w := range workloads {
		names[strings.ToLower(w.Name)] = true
	}
	for _, m := range managed {
		names[strings.ToLower(m.Name)] = true
	}
	patterns := map[string][]*regexp.Regexp{}
	for name := range names {
		if composeHostName.MatchString(name) {
			patterns[name] = hostPatterns(name)
		}
	}
	for i := range workloads {
		w := &workloads[i]
		if w.Image != "" {
			continue
		}
		root := path.Clean(w.RootDir)
		if root == "" || !fs.ValidPath(root) {
			root = "."
		}
		self := strings.ToLower(w.Name)
		w.SourceHosts = scanSourceHosts(fsys, root, func(name string) bool { return name != self }, patterns)
	}
}

func scanSourceHosts(fsys fs.FS, root string, other func(string) bool, patterns map[string][]*regexp.Regexp) []api.PlanSourceHost {
	order := make([]string, 0, len(patterns))
	for name := range patterns {
		if other(name) {
			order = append(order, name)
		}
	}
	if len(order) == 0 {
		return nil
	}
	sort.Strings(order)
	var found []api.PlanSourceHost
	files := 0
	_ = fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && (sourceHostSkipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if !sourceHostExtensions[path.Ext(p)] || strings.Contains(path.Base(p), "_test.") || strings.Contains(path.Base(p), ".test.") {
			return nil
		}
		if files++; files > sourceHostMaxFiles || len(found) >= sourceHostMaxPerWork {
			return fs.SkipAll
		}
		info, err := d.Info()
		if err != nil || info.Size() > sourceHostMaxFileBytes {
			return nil
		}
		body, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil
		}
		scanner := bufio.NewScanner(bytes.NewReader(body))
		scanner.Buffer(make([]byte, 0, 64<<10), sourceHostMaxFileBytes)
		for line := 1; scanner.Scan(); line++ {
			text := scanner.Text()
			trimmed := strings.TrimSpace(text)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") || envLookup.MatchString(text) {
				continue
			}
			for _, name := range order {
				if !strings.Contains(strings.ToLower(text), name) {
					continue
				}
				for _, re := range patterns[name] {
					if re.MatchString(text) {
						found = append(found, api.PlanSourceHost{Host: name, File: p, Line: line})
						break
					}
				}
				if len(found) >= sourceHostMaxPerWork {
					return fs.SkipAll
				}
			}
		}
		return nil
	})
	return found
}
