package main

import (
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/sourcecontext"
)

// resolveWorkspaceContext returns the repository root and the selected
// member's path when sourceDir is an explicitly selected workspace workload.
// The caller can then upload the repository as the build context while keeping
// the selected member as the build working directory.
//
// Workspace detection intentionally reuses reposcan's graph parser instead of
// maintaining a second list of package.json/pnpm/go.work conventions in the
// deploy command. A directory must be both declared by a workspace manifest
// and carry a build marker; unmarked workspace directories do not cause the
// upload scope to expand.
func resolveWorkspaceContext(repoRoot, sourceDir string) (contextRoot, sourceRoot string, ok bool, err error) {
	rel, err := gitRelativePath(repoRoot, sourceDir)
	if err != nil {
		return "", "", false, err
	}
	root, err := sourcecontext.StorageRoot(rel)
	if err != nil {
		return "", "", false, err
	}
	if root == "" {
		return "", "", false, nil
	}

	members, err := reposcan.WorkspaceMemberPaths(os.DirFS(repoRoot))
	if err != nil {
		return "", "", false, err
	}
	for _, member := range members {
		memberRoot, memberErr := sourcecontext.StorageRoot(member)
		if memberErr != nil {
			continue
		}
		if memberRoot == root {
			abs, absErr := filepath.Abs(repoRoot)
			if absErr != nil {
				return "", "", false, absErr
			}
			return filepath.Clean(abs), root, true, nil
		}
	}
	return "", "", false, nil
}
