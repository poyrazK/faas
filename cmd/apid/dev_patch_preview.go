package main

import (
	"archive/tar"
	"context"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devpatch"
	"github.com/onebox-faas/faas/pkg/sourcedelta"
	"github.com/onebox-faas/faas/pkg/state"
)

// devPatchPreview reports whether a developer sync could have been applied as
// a live source patch to the deployment that is live right now (ADR-740
// phase 1). It only measures: nothing is delivered to an instance, and a
// failure here never affects the deployment. A nil result means the delta
// could not be inspected, which the normal reconstruction path rejects
// anyway.
func (s *server) devPatchPreview(ctx context.Context, app state.App, deltaPath string, meta devSourceMetadata, sourceRoot string, limits api.Limits) *api.DevPatchPreview {
	if meta.base == "" {
		return &api.DevPatchPreview{Reason: api.DevPatchReasonFullSnapshot}
	}
	var sourceMap *api.DevPatchSourceMap
	if live, err := s.store.LiveDeployment(ctx, app.ID); err == nil && live.BuildID != "" {
		if prov, err := s.store.BuildProvenanceByBuildID(ctx, live.BuildID); err == nil {
			sourceMap = prov.DevPatch
		}
	}
	if sourceMap == nil {
		return &api.DevPatchPreview{Reason: api.DevPatchReasonNoLiveBuild}
	}
	delta, err := openDevSourceArchive(deltaPath)
	if err != nil {
		return nil
	}
	defer func() { _ = delta.Close() }()
	manifest, err := sourcedelta.Inspect(delta, sourceDeltaLimits(limits))
	if err != nil {
		return nil
	}
	preview := devpatch.Evaluate(sourceMap, sourceRoot, devPatchChanges(manifest, meta.deleted))
	return &preview
}

// devPatchChanges lists a delta's entries and deletions in path order so the
// evaluation, including which reason it reports first, is deterministic.
func devPatchChanges(manifest sourcedelta.Manifest, deleted []string) []devpatch.Change {
	changes := make([]devpatch.Change, 0, len(manifest.Entries)+len(deleted))
	for name, entry := range manifest.Entries {
		changes = append(changes, devpatch.Change{
			Path:    name,
			Regular: entry.Type == tar.TypeReg,
			Dir:     entry.Type == tar.TypeDir,
			Size:    entry.Size,
		})
	}
	for _, name := range deleted {
		changes = append(changes, devpatch.Change{Path: name, Deleted: true})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}
