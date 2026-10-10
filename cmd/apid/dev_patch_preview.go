package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devpatch"
	"github.com/onebox-faas/faas/pkg/sourcedelta"
	"github.com/onebox-faas/faas/pkg/state"
)

// devPatchDeliveryEnv is the operator switch for ADR-740 phase 2. Without it
// apid only reports previews; with it, eligible syncs also store a patch that
// vmmd serves to the live developer instances.
const devPatchDeliveryEnv = "FAAS_DEV_PATCH_DELIVERY"

// devSourceManifestsKept bounds stored manifests per developer app, in
// addition to the live deployment's, which is never pruned.
const devSourceManifestsKept = 8

func devPatchDeliveryEnabled() bool { return os.Getenv(devPatchDeliveryEnv) == "1" }

// devPatchPlan is the cumulative difference between the live build's source
// and a newly uploaded developer source.
type devPatchPlan struct {
	preview  *api.DevPatchPreview
	manifest sourcedelta.Manifest
	inspect  bool
	liveID   string
	include  map[string]bool // archive paths of changed regular files
	deleted  []string        // source-root-relative removed paths
}

// planDevPatch compares a complete developer source archive with the source
// of the deployment that is live right now (ADR-740). A failure here never
// affects the deployment; it only changes what the preview reports.
func (s *server) planDevPatch(ctx context.Context, app state.App, sourcePath, sourceRoot string, limits api.Limits) devPatchPlan {
	var plan devPatchPlan
	archive, err := openDevSourceArchive(sourcePath)
	if err != nil {
		return plan
	}
	plan.manifest, err = sourcedelta.Inspect(archive, sourceDeltaLimits(limits))
	_ = archive.Close()
	if err != nil {
		return plan
	}
	plan.inspect = true
	store, ok := s.store.(state.DevSourcePatchStore)
	if !ok {
		return plan
	}
	reason := func(reason string) devPatchPlan {
		plan.preview = &api.DevPatchPreview{Reason: reason}
		return plan
	}
	live, err := s.store.LiveDeployment(ctx, app.ID)
	if err != nil || live.BuildID == "" {
		return reason(api.DevPatchReasonNoLiveBuild)
	}
	prov, err := s.store.BuildProvenanceByBuildID(ctx, live.BuildID)
	if err != nil || prov.DevPatch == nil {
		return reason(api.DevPatchReasonNoLiveBuild)
	}
	base, err := store.DevSourceManifest(ctx, live.ID)
	if err != nil || base.SourceRoot != sourceRoot {
		return reason(api.DevPatchReasonNoBaseManifest)
	}
	changes := devpatch.Diff(devPatchEntriesFromState(base.Entries), devPatchEntriesFromManifest(plan.manifest))
	preview := devpatch.Evaluate(prov.DevPatch, sourceRoot, changes)
	plan.preview = &preview
	if !preview.Eligible {
		return plan
	}
	plan.liveID = live.ID
	plan.include = map[string]bool{}
	root := strings.Trim(sourceRoot, "/")
	for _, change := range changes {
		rel, inside := devpatch.Relative(root, change.Path)
		switch {
		case !inside || rel == "":
		case change.Deleted:
			plan.deleted = append(plan.deleted, rel)
		case change.Regular:
			plan.include[change.Path] = true
		}
	}
	return plan
}

// recordDevPatchSource stores the new deployment's manifest and, when
// delivery is enabled and the sync is eligible, the patch for the live
// deployment. Errors are logged; the deployment itself already succeeded.
func (s *server) recordDevPatchSource(ctx context.Context, app state.App, deploymentID, sourcePath, sourceRoot string, plan devPatchPlan) {
	store, ok := s.store.(state.DevSourcePatchStore)
	if !ok {
		return
	}
	if !plan.inspect {
		s.devLoopMetrics.observePatchPlan(devPatchOutcomeUninspected, "")
		return
	}
	if err := store.RecordDevSourceManifest(ctx, state.DevSourceManifest{
		DeploymentID: deploymentID, AppID: app.ID, SourceRoot: sourceRoot,
		Entries: devPatchEntriesToState(plan.manifest),
	}, devSourceManifestsKept); err != nil {
		s.log.Warn("developer source manifest not recorded", "app_id", app.ID, "error", err)
	}
	switch {
	case plan.preview == nil:
		return
	case !plan.preview.Eligible:
		s.devLoopMetrics.observePatchPlan(devPatchOutcomeIneligible, plan.preview.Reason)
		return
	case plan.preview.ChangedPaths == 0:
		s.devLoopMetrics.observePatchPlan(devPatchOutcomeNoChanges, "")
		return
	case !devPatchDeliveryEnabled():
		s.devLoopMetrics.observePatchPlan(devPatchOutcomeDeliveryDisabled, "")
		return
	}
	if err := s.publishDevPatch(ctx, store, app, sourcePath, sourceRoot, plan); err != nil {
		s.log.Warn("developer live patch not published", "app_id", app.ID, "error", err)
		s.devLoopMetrics.observePatchPlan(devPatchOutcomePublishFailed, "")
		return
	}
	s.devLoopMetrics.observePatchPlan(devPatchOutcomePublished, "")
}

func (s *server) publishDevPatch(ctx context.Context, store state.DevSourcePatchStore, app state.App, sourcePath, sourceRoot string, plan devPatchPlan) error {
	archive, err := openDevSourceArchive(sourcePath)
	if err != nil {
		return err
	}
	defer func() { _ = archive.Close() }()
	content, digest, err := devpatch.BuildArchive(archive, sourceRoot, plan.include, api.DevPatchMaxBytes)
	if err != nil {
		return err
	}
	if plan.liveID == "" {
		return errors.New("developer live patch has no base deployment")
	}
	created, err := store.CreateDevSourcePatch(ctx, state.DevSourcePatch{
		AppID: app.ID, BaseDeploymentID: plan.liveID, ImageDir: api.DevPatchImageDir,
		Archive: content, Deleted: plan.deleted, Digest: digest,
		ExpiresAt: time.Now().UTC().Add(api.DevSourceCacheTTL),
	})
	if err != nil {
		return err
	}
	plan.preview.Generation = created.Generation
	return nil
}

func devPatchEntriesFromManifest(manifest sourcedelta.Manifest) map[string]devpatch.Entry {
	out := make(map[string]devpatch.Entry, len(manifest.Entries))
	for name, entry := range manifest.Entries {
		out[name] = devpatch.Entry{Type: entry.Type, Mode: entry.Mode, Size: entry.Size, Digest: entry.Digest}
	}
	return out
}

func devPatchEntriesFromState(entries map[string]state.DevSourceEntry) map[string]devpatch.Entry {
	out := make(map[string]devpatch.Entry, len(entries))
	for name, entry := range entries {
		out[name] = devpatch.Entry{Type: entry.Type, Mode: entry.Mode, Size: entry.Size, Digest: entry.Digest}
	}
	return out
}

func devPatchEntriesToState(manifest sourcedelta.Manifest) map[string]state.DevSourceEntry {
	out := make(map[string]state.DevSourceEntry, len(manifest.Entries))
	for name, entry := range manifest.Entries {
		out[name] = state.DevSourceEntry{Type: entry.Type, Mode: entry.Mode, Size: entry.Size, Digest: entry.Digest}
	}
	return out
}
