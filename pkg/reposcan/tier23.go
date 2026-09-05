package reposcan

import (
	"io/fs"
	"sort"
)

// detectWorkspaces returns one workloadSeed per workspace-graph
// member that also carries a Dockerfile or language marker. A
// member with no build marker is not a workload — it is only a
// workspace graph entry.
// Implementation lives in workspaces.go.
//
// Recognized workspace manifests (impl plan §3):
//   - package.json  (top-level "workspaces": [...])
//   - pnpm-workspace.yaml (top-level "packages": [...])
//   - turbo.json    (pipeline / $pipeline form)
//   - nx.json       (projects)
//   - go.work       (use ( ... ))
//   - Cargo.toml    ([workspace] members) [out of scope this phase]
//
// Each workspace member has its directory treated as RootDir —
// the merge rule later pairs on (RootDir, Name) so a workspace
// "packages/auth" with auth-name does NOT collide with a Tier-3
// "services/auth" convention of the same name.
//
// Pure: no fsys error escalates to Scan. A missing manifest file
// is a quiet skip.
func detectWorkspaces(fsys fs.FS) ([]workloadSeed, []string, error) {
	return detectWorkspacesImpl(fsys)
}

// WorkspaceMemberPaths returns the repository-relative paths of workspace
// members that carry a build marker. It is the public, path-only view of the
// same workspace graph used by Scan. Members without a marker are omitted so
// a deploy selected with --path expands to a repository context only when it
// points at a buildable workspace member.
func WorkspaceMemberPaths(fsys fs.FS) ([]string, error) {
	seeds, _, err := detectWorkspacesImpl(fsys)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(seeds))
	paths := make([]string, 0, len(seeds))
	for _, seed := range seeds {
		if seed.rootDir == "" {
			continue
		}
		if _, ok := seen[seed.rootDir]; ok {
			continue
		}
		seen[seed.rootDir] = struct{}{}
		paths = append(paths, seed.rootDir)
	}
	sort.Strings(paths)
	return paths, nil
}

// detectConvention scans the documented Tier-3 root subdirectories
// (services/, apps/, packages/, cmd/) and emits one workloadSeed
// per member that carries a Dockerfile or language marker. Tier 3
// is exactly the directory-shape heuristic; it's why the Phase 3
// confirm table exists (always ask before provisioning).
func detectConvention(fsys fs.FS) ([]workloadSeed, []string, error) {
	return detectConventionImpl(fsys)
}
