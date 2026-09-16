package reposcan

import (
	"io/fs"
	"sort"
	"strings"
)

// conventionDirs is the documented Tier-3 directory convention
// (impl plan §3). Each top-level subdirectory whose NAME appears
// here is a candidate; each member subdirectory is a workload iff
// it carries a Dockerfile or a language marker.
//
// When two convention dirs both match (e.g. a `services/foo` AND
// an `apps/foo`), BOTH are emitted as separate seeds. They have
// different `rootDir` values, so mergeByKey treats them as two
// distinct workloads — the (RootDir, Name) merge key is what
// keeps them separate. The order here matters only for the
// iteration order of `fs.ReadDir` results, which Go standardises
// to alphabetical regardless.
var conventionDirs = []string{"services", "apps", "packages", "cmd"}

// detectConventionImpl walks each present convention dir and emits
// one workloadSeed per runnable member. Members without a runnable target are
// skipped so package libraries do not become public HTTP applications.
//
// Tier-3 result is always class=ClassUnknown (Phase 4
// characterization corrects the hint). RootDir is the full member
// path (e.g. "services/auth"), so a Tier-1 compose that pins the
// same path stays a different merge key from a different member.
func detectConventionImpl(fsys fs.FS) ([]workloadSeed, []string, error) {
	var (
		seeds    []workloadSeed
		warnings []string
		seen     = map[string]bool{}
	)
	for _, top := range conventionDirs {
		info, err := fs.Stat(fsys, top)
		if err != nil || !info.IsDir() {
			continue
		}
		entries, err := fs.ReadDir(fsys, top)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			member := top + "/" + e.Name()
			if seen[member] {
				continue
			}
			seen[member] = true
			name := e.Name()
			if name == "" || strings.HasPrefix(name, ".") || name == "*" {
				continue
			}
			seed, runnable, reason := runnableWorkspaceSeed(fsys, member, "convention", false)
			if !runnable {
				if reason != "" {
					warnings = append(warnings, reason)
				}
				continue
			}
			seeds = append(seeds, seed)
		}
	}
	// Also handle the bare-repo Tier-4 floor; pushed into Scan()
	// itself rather than this detector because the floor is a
	// tier-wide concern.
	sort.SliceStable(seeds, func(i, j int) bool { return seeds[i].name < seeds[j].name })
	sort.Strings(warnings)
	return seeds, warnings, nil
}
