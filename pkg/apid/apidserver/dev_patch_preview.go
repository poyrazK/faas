package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/apid/devloop"
	"github.com/onebox-faas/faas/pkg/db"
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
// deployment. It reports whether the patch was published. Errors are
// logged; the deployment itself already succeeded.
func (s *server) recordDevPatchSource(ctx context.Context, app state.App, deploymentID, sourcePath, sourceRoot string, plan devPatchPlan) bool {
	store, ok := s.store.(state.DevSourcePatchStore)
	if !ok {
		return false
	}
	watch := devloop.LoadWatch(ctx, s.store, app.ID) != nil
	if !plan.inspect {
		s.devLoopMetrics.ObservePatchPlan(devloop.PatchUninspected, "", watch)
		return false
	}
	if err := store.RecordDevSourceManifest(ctx, state.DevSourceManifest{
		DeploymentID: deploymentID, AppID: app.ID, SourceRoot: sourceRoot,
		Entries: devPatchEntriesToState(plan.manifest),
	}, devSourceManifestsKept); err != nil {
		s.log.Warn("developer source manifest not recorded", "app_id", app.ID, "error", err)
	}
	switch {
	case plan.preview == nil:
		return false
	case !plan.preview.Eligible:
		s.devLoopMetrics.ObservePatchPlan(devloop.PatchIneligible, plan.preview.Reason, watch)
		return false
	case plan.preview.ChangedPaths == 0:
		s.devLoopMetrics.ObservePatchPlan(devloop.PatchNoChanges, "", watch)
		return false
	case !devPatchDeliveryEnabled():
		s.devLoopMetrics.ObservePatchPlan(devloop.PatchDeliveryDisabled, "", watch)
		return false
	}
	if err := s.publishDevPatch(ctx, store, app, sourcePath, sourceRoot, plan); err != nil {
		s.log.Warn("developer live patch not published", "app_id", app.ID, "error", err)
		s.devLoopMetrics.ObservePatchPlan(devloop.PatchPublishFailed, "", watch)
		return false
	}
	s.devLoopMetrics.ObservePatchPlan(devloop.PatchPublished, "", watch)
	return true
}

// settleDeferredDevBuild finishes ADR-20261021221906's decision once the patch is in:
// a published patch keeps the build deferred and tells the CLI until when;
// otherwise the build starts now, since the edit has not reached the
// instance.
func (s *server) settleDeferredDevBuild(ctx context.Context, app state.App, deploymentID string, notBefore time.Time, published bool, preview *api.DevPatchPreview) {
	if notBefore.IsZero() {
		return
	}
	if published && preview != nil {
		preview.BuildDeferredUntil = &notBefore
		s.devLoopMetrics.ObserveBuildDeferral(devloop.BuildDeferred)
		return
	}
	s.startDeferredDevBuild(ctx, app, deploymentID)
}

// startDeferredDevBuilds starts every deferred build of the app now: a
// live patch failed to apply, so the build is what delivers the edit.
func (s *server) startDeferredDevBuilds(ctx context.Context, app state.App) {
	deferred, ok := s.store.(state.DeferredBuildStore)
	if !ok {
		return
	}
	ids, err := deferred.DeferredDeployments(ctx, app.ID)
	if err != nil {
		s.log.Warn("deferred developer builds not listed; they start when their window ends", "app_id", app.ID, "error", err)
		return
	}
	for _, id := range ids {
		s.startDeferredDevBuild(ctx, app, id)
	}
}

// startDeferredDevBuild makes one deferred build claimable now and tells
// builderd.
func (s *server) startDeferredDevBuild(ctx context.Context, app state.App, deploymentID string) {
	deferred, ok := s.store.(state.DeferredBuildStore)
	if !ok {
		return
	}
	build, found, err := deferred.StartDeferredBuild(ctx, deploymentID)
	if err != nil {
		s.log.Warn("deferred developer build not started; it starts when its window ends", "app_id", app.ID, "deployment_id", deploymentID, "error", err)
		return
	}
	if found {
		s.devLoopMetrics.ObserveBuildDeferral(devloop.BuildStarted)
		apidsource.NotifyBuildQueued(ctx, s.notif, s.log, build, app.ID, "")
	}
}

// cancelSupersededDevBuilds cancels the app's other deferred developer
// builds before they run (ADR-20261021221906): the sync just enqueued carries their
// source, and CreateDeployment does not supersede a deployment whose build
// is already queued. A build that started meanwhile is cancelled too; the
// newer sync's build replaces it.
func (s *server) cancelSupersededDevBuilds(ctx context.Context, acct state.Account, app state.App, keepDeploymentID string) {
	deferred, ok := s.store.(state.DeferredBuildStore)
	if !ok {
		return
	}
	ids, err := deferred.DeferredDeployments(ctx, app.ID)
	if err != nil {
		s.log.Warn("deferred developer builds not listed; they build when their window ends", "app_id", app.ID, "error", err)
		return
	}
	for _, id := range ids {
		if id == keepDeploymentID {
			continue
		}
		_, cancelledBuilds, err := s.store.CancelDeploymentTx(ctx, id, acct.ID, state.CancelReasonSystem)
		if err != nil {
			if !errors.Is(err, state.ErrNotFound) && !errors.Is(err, state.ErrInvalidStateTransition) {
				s.log.Warn("superseded developer build not cancelled", "app_id", app.ID, "deployment_id", id, "error", err)
			}
			continue
		}
		s.devLoopMetrics.ObserveBuildDeferral(devloop.BuildSuperseded)
		for _, buildID := range cancelledBuilds {
			raw, _ := json.Marshal(buildChangedPayload{BuildID: buildID, DeploymentID: id, Status: "cancelled", Reason: string(state.CancelReasonSystem), Cascade: true})
			if err := s.notif.Notify(ctx, db.NotifyBuildChanged, string(raw)); err != nil {
				s.log.Warn("superseded developer build: build_changed notify", "build", buildID, "error", err)
			}
		}
	}
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
