// Package releaseretention keeps the bounded rollback window for immutable
// on-host release directories. Release installs are additive, so without a
// retention policy every deployment eventually consumes the runner or host
// root filesystem even though only the active release and a small rollback
// window are usable.
package releaseretention

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultKeepPrevious is the number of non-active releases retained after a
// successful deployment. The active release is always retained separately.
const DefaultKeepPrevious = 3

// Report describes a retention pass. Removed entries are release IDs, not
// arbitrary paths; the implementation only removes direct, SHA-named child
// directories of the configured releases root.
type Report struct {
	Current string   `json:"current"`
	Kept    []string `json:"kept"`
	Removed []string `json:"removed"`
}

type candidate struct {
	id      string
	path    string
	modTime time.Time
}

// Prune removes old release directories while preserving the active release
// and keepPrevious newest inactive releases. Only direct children named as a
// 40-character lowercase Git SHA are eligible. Unknown files, symlinks, and
// legacy/non-SHA directories are left untouched.
//
// The current symlink is resolved and validated before any deletion. If it is
// malformed or points outside releasesRoot, the function fails closed. The
// active target is re-read before each deletion so a concurrent install cannot
// cause the newly active release to be removed.
func Prune(releasesRoot, currentPath string, keepPrevious int) (Report, error) {
	if strings.TrimSpace(releasesRoot) == "" {
		return Report{}, errors.New("releaseretention: empty releases root")
	}
	if strings.TrimSpace(currentPath) == "" {
		return Report{}, errors.New("releaseretention: empty current path")
	}
	if keepPrevious < 0 {
		return Report{}, errors.New("releaseretention: keepPrevious must not be negative")
	}

	root, err := filepath.Abs(releasesRoot)
	if err != nil {
		return Report{}, fmt.Errorf("releaseretention: resolve releases root: %w", err)
	}
	if info, statErr := os.Stat(root); statErr != nil {
		return Report{}, fmt.Errorf("releaseretention: stat releases root: %w", statErr)
	} else if !info.IsDir() {
		return Report{}, fmt.Errorf("releaseretention: releases root is not a directory: %s", root)
	}
	current, err := currentReleaseID(root, currentPath)
	if err != nil {
		return Report{}, err
	}
	report := Report{Current: current}

	entries, err := os.ReadDir(root)
	if err != nil {
		return report, fmt.Errorf("releaseretention: read releases root: %w", err)
	}
	candidates := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		// Never recurse through an archive, symlink, or staging entry. The
		// retention contract is deliberately limited to direct SHA dirs.
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() || !validReleaseID(entry.Name()) {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return report, fmt.Errorf("releaseretention: stat release %s: %w", entry.Name(), infoErr)
		}
		candidates = append(candidates, candidate{
			id:      entry.Name(),
			path:    filepath.Join(root, entry.Name()),
			modTime: info.ModTime(),
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].modTime.Equal(candidates[j].modTime) {
			return candidates[i].id > candidates[j].id
		}
		return candidates[i].modTime.After(candidates[j].modTime)
	})

	previousKept := 0
	for _, item := range candidates {
		if item.id == current || previousKept < keepPrevious {
			report.Kept = append(report.Kept, item.id)
			if item.id != current {
				previousKept++
			}
			continue
		}

		latestCurrent, currentErr := currentReleaseID(root, currentPath)
		if currentErr != nil {
			return report, currentErr
		}
		if latestCurrent != current {
			return report, fmt.Errorf("releaseretention: current release changed during prune from %q to %q", current, latestCurrent)
		}
		if item.id == latestCurrent {
			report.Kept = append(report.Kept, item.id)
			continue
		}
		if err := os.RemoveAll(item.path); err != nil {
			return report, fmt.Errorf("releaseretention: remove release %s: %w", item.id, err)
		}
		report.Removed = append(report.Removed, item.id)
	}
	return report, nil
}

func currentReleaseID(root, currentPath string) (string, error) {
	link, err := filepath.Abs(currentPath)
	if err != nil {
		return "", fmt.Errorf("releaseretention: resolve current path: %w", err)
	}
	target, err := os.Readlink(link)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("releaseretention: read current symlink: %w", err)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(link), target)
	}
	rel, err := filepath.Rel(root, filepath.Clean(target))
	if err != nil {
		return "", fmt.Errorf("releaseretention: resolve current target: %w", err)
	}
	// Keep a legacy/non-SHA active directory too. Retention only removes
	// SHA-named candidates, but an old installation must never be deleted just
	// because its directory predates the current naming contract.
	if rel == "." || filepath.Dir(rel) != "." {
		return "", fmt.Errorf("releaseretention: current symlink target %q is not a direct release", target)
	}
	return rel, nil
}

func validReleaseID(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
