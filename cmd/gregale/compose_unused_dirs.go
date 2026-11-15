package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"gopkg.in/yaml.v3"
)

// composeUnusedTopLevelDirs returns the top-level directories of root that
// the Compose file at root never points into: no build context or
// Dockerfile, env_file, secret or config file, include or extends file lives
// under them. Repositories such as microservices-demo carry kubernetes, helm,
// istio, terraform and docs trees beside src/, and packing them pushed the
// project archive past the entry limit although no service builds from them.
// It returns nil when there is no readable Compose file or when a service
// builds from the root itself, since every directory may then be used.
func composeUnusedTopLevelDirs(root string) []string {
	file := composeFileIn(root)
	if file == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		return nil
	}
	var doc map[string]any
	if yaml.Unmarshal(raw, &doc) != nil {
		return nil
	}
	used := map[string]bool{}
	whole := false
	mark := func(rel string) {
		rel = strings.TrimSpace(rel)
		if rel == "" || strings.Contains(rel, "://") || strings.Contains(rel, "$") {
			// A URL context names no local directory; an interpolated path
			// cannot be resolved here, so keep everything.
			if strings.Contains(rel, "$") {
				whole = true
			}
			return
		}
		clean := path.Clean(filepath.ToSlash(rel))
		if path.IsAbs(clean) || strings.HasPrefix(clean, "../") || clean == ".." {
			return
		}
		if clean == "." {
			whole = true
			return
		}
		used[strings.SplitN(clean, "/", 2)[0]] = true
	}
	markAll := func(v any, key string) {
		switch x := v.(type) {
		case string:
			mark(x)
		case []any:
			for _, item := range x {
				switch y := item.(type) {
				case string:
					mark(y)
				case map[string]any:
					if p, ok := y[key].(string); ok {
						mark(p)
					}
					if ps, ok := y[key].([]any); ok {
						for _, p := range ps {
							if s, ok := p.(string); ok {
								mark(s)
							}
						}
					}
				}
			}
		case map[string]any:
			if p, ok := x[key].(string); ok {
				mark(p)
			}
		}
	}
	services, _ := doc["services"].(map[string]any)
	for _, raw := range services {
		service, _ := raw.(map[string]any)
		switch build := service["build"].(type) {
		case string:
			mark(build)
		case map[string]any:
			context, _ := build["context"].(string)
			if context == "" {
				context = "."
			}
			mark(context)
			if dockerfile, ok := build["dockerfile"].(string); ok {
				mark(path.Join(filepath.ToSlash(context), filepath.ToSlash(dockerfile)))
			}
		}
		markAll(service["env_file"], "path")
		markAll(service["extends"], "file")
	}
	for _, section := range []string{"secrets", "configs"} {
		entries, _ := doc[section].(map[string]any)
		for _, raw := range entries {
			if entry, ok := raw.(map[string]any); ok {
				if p, ok := entry["file"].(string); ok {
					mark(p)
				}
			}
		}
	}
	markAll(doc["include"], "path")
	if whole {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var unused []string
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || strings.HasPrefix(name, ".") || used[name] {
			continue
		}
		unused = append(unused, name)
	}
	slices.Sort(unused)
	return unused
}

// dropComposeUnusedDirs removes from an over-limit archive the entries under
// top-level directories the Compose file never points into, and says which
// ones it left out. Archives within the limit are never changed, so repos
// that deploy today keep exactly the same source.
func dropComposeUnusedDirs(srcDir string, entries []packEntry) []packEntry {
	unused := composeUnusedTopLevelDirs(srcDir)
	if len(unused) == 0 {
		return entries
	}
	kept := entries[:0:0]
	for _, entry := range entries {
		top := strings.SplitN(filepath.ToSlash(entry.rel), "/", 2)[0]
		if slices.Contains(unused, top) {
			continue
		}
		kept = append(kept, entry)
	}
	if len(kept) == len(entries) {
		return entries
	}
	_, _ = fmt.Fprintf(osStderr, "Source has more than %d entries; left out %s, which no Compose service builds from or reads. List them in .gregaleignore to skip this step.\n",
		api.SourceArchiveMaxEntries, strings.Join(unused, ", "))
	return kept
}
