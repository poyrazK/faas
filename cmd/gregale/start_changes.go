package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/safetext"
)

// Source comparisons stay in memory. Recovery records contain neither file
// contents nor hashes. This is a local review, not a remote source inventory.
type startSourceSnapshot struct {
	root  string
	files map[string]startSourceFile
}

type startSourceFile struct {
	mode   os.FileMode
	digest [sha256.Size]byte
}

func (r *startRunner) prepareSourceReview() error {
	snapshot, err := r.snapshotSource(r.ctx)
	if err != nil {
		return err
	}
	r.reviewedSource = snapshot
	if selected, err := filepath.EvalSymlinks(r.session.SourcePath); err == nil && selected != snapshot.root {
		_, _ = fmt.Fprintf(osStdout, "  Build context  %s (workspace)\n", startResponseText(snapshot.root))
	}
	if r.projectUpdates {
		renderStartSourceChanges(r.sourceBaseline, snapshot)
	}
	return nil
}

func (r *startRunner) snapshotSource(ctx context.Context) (*startSourceSnapshot, error) {
	limits, ok := api.LimitsFor(api.Plan(r.account.Plan))
	if !ok {
		return nil, errors.New("source review needs a recognized account plan")
	}
	root := r.session.SourcePath
	if repoRoot, err := gitRootFromCwd(root); err == nil {
		contextRoot, _, workspace, err := resolveWorkspaceContext(repoRoot, root)
		if err != nil {
			return nil, fmt.Errorf("resolve source review build context: %w", err)
		}
		if workspace {
			root = contextRoot
		}
	}
	return readStartSourceSnapshot(ctx, root, int64(limits.SourceTarballMaxMB)<<20, r.secretsFile)
}

func (r *startRunner) verifyReviewedSource(ctx context.Context) error {
	if r.reviewedSource == nil {
		return nil
	}
	current, err := r.snapshotSource(ctx)
	if err != nil {
		return fmt.Errorf("recheck reviewed source: %w", err)
	}
	if current.root != r.reviewedSource.root || !maps.Equal(current.files, r.reviewedSource.files) {
		return errors.New("source files changed after the preview; review the new files before deploying")
	}
	return nil
}

func readStartSourceSnapshot(ctx context.Context, source string, maxBytes int64, excludes ...string) (*startSourceSnapshot, error) {
	root, err := filepath.EvalSymlinks(source)
	if err != nil {
		return nil, fmt.Errorf("resolve source review directory: %w", err)
	}
	var selectedPaths []string
	for _, path := range excludes {
		if path != "" {
			selectedPaths = append(selectedPaths, path)
		}
	}
	patterns, excluded := loadGregaleignore(root), packExtraExcludeSet(root, selectedPaths)
	snapshot := &startSourceSnapshot{root: root, files: make(map[string]startSourceFile)}
	entries, remaining := 0, maxBytes
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if excluded[rel] || shouldExclude(rel, entry.IsDir(), patterns) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		entries++
		if entries > api.SourceArchiveMaxEntries {
			return sourceArchiveEntryLimitError(entries)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("source file %q must be a regular file", rel)
		}
		file, size, err := hashStartSourceFile(path, remaining)
		if err != nil {
			return fmt.Errorf("review file %q: %w", rel, err)
		}
		snapshot.files[rel], remaining = file, remaining-size
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("review project source: %w", err)
	}
	return snapshot, nil
}

func hashStartSourceFile(path string, maxBytes int64) (startSourceFile, int64, error) {
	var result startSourceFile
	f, err := openCustomerFile(path)
	if err != nil {
		return result, 0, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return result, 0, fmt.Errorf("inspect source file: %w", err)
	}
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(f, maxBytes+1))
	if err != nil {
		return result, 0, fmt.Errorf("hash source file: %w", err)
	}
	if size > maxBytes {
		return result, 0, fmt.Errorf("source file exceeds the remaining %d-byte source budget (observed at least %d bytes); see https://gregale.dev/docs/deploy-from-github", maxBytes, size)
	}
	result.mode = info.Mode().Perm()
	copy(result.digest[:], hash.Sum(nil))
	return result, size, nil
}

type startSourceChange struct{ path, action string }

func startSourceChanges(previous, current *startSourceSnapshot) []startSourceChange {
	var changes []startSourceChange
	for path, file := range current.files {
		action := "added"
		if previous != nil && previous.root == current.root {
			if before, exists := previous.files[path]; exists {
				if before == file {
					continue
				}
				action = "modified"
			}
		} else {
			action = "included"
		}
		changes = append(changes, startSourceChange{path, action})
	}
	if previous != nil && previous.root == current.root {
		for path := range previous.files {
			if _, exists := current.files[path]; !exists {
				changes = append(changes, startSourceChange{path, "deleted"})
			}
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].path < changes[j].path })
	return changes
}

func renderStartSourceChanges(previous, current *startSourceSnapshot) {
	label := "Changes since the previous source review (file names only)"
	if previous == nil || previous.root != current.root {
		label = "Earlier source comparison unavailable; reviewing current files (file names only)"
	}
	_, _ = fmt.Fprintln(osStdout, "\n"+label)
	changes := startSourceChanges(previous, current)
	if len(changes) == 0 {
		_, _ = fmt.Fprintln(osStdout, "  No included file changes detected. Review configuration before deploying again.")
	}
	for _, change := range changes[:min(len(changes), 40)] {
		_, _ = fmt.Fprintf(osStdout, "  %-8s %q\n", change.action, safetext.Truncate(change.path, 160))
	}
	if len(changes) > 40 {
		_, _ = fmt.Fprintf(osStdout, "  ... %d more file changes\n", len(changes)-40)
	}
}
