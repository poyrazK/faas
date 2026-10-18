package builderd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/buildpublisher"
	"github.com/onebox-faas/faas/pkg/state"
)

// An opt-in context never falls back to the legacy, cross-app cache. Missing
// canonical proof or signing support is an ordinary VM-build decision.
func gitOpsReuseScope(app state.App, dep state.Deployment) (scope string, optedIn bool) {
	frozen, err := dep.ScopedWorkloadRuntime()
	if err != nil || frozen == nil || frozen.Source == nil || !frozen.Source.ReuseUnchangedBuild {
		return "", false
	}
	if frozen.SourceArchive == nil || frozen.SourceArchive.ContextFormat != api.EnvironmentGitBuildContextFormat ||
		frozen.SourceArchive.SHA256 != dep.SourceSHA256 || frozen.Source.BuildArtifactID != "" ||
		frozen.Source.Kind == "image" || len(dep.Sidecars) != 0 || frozen.BuiltArtifact != nil {
		return "", true
	}
	intent, err := state.SourceBuildRootfsIntentHash(app, dep)
	if err != nil {
		return "", true
	}
	data, err := json.Marshal(struct {
		Account, App, Provider, Repository, Intent, ContextFormat string
		RepositoryID                                              int64
		Source                                                    any
	}{app.AccountID, app.ID, frozen.SourceProvider, frozen.SourceRepository, intent, frozen.SourceArchive.ContextFormat, frozen.SourceRepositoryID, frozen.Source})
	if err != nil {
		return "", true
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), true
}

// The cache's local digest sidecar is insufficient for automatic reuse. Bind
// the leased OCI bytes to an approved export, its original source and build
// recipe, and the origin's currently selected signed runtime/base producers.
// All uncertainty is a miss. The target still publishes its own signed export,
// converts its own rootfs and passes its own graph qualification and activation.
func (b *Builderd) trustedGitOpsCacheEntry(ctx context.Context, app state.App, dep state.Deployment, recipe BuildCacheRecipe, environment BuildEnvironment, cached CacheEntry) bool {
	limits, known := api.LimitsFor(recipe.Plan)
	if !known || cached.Bytes <= 0 || (cached.Bytes+(1<<20)-1)>>20 > int64(limits.AppLayerMaxMB) {
		return false
	}
	publications, pubOK := b.store.(state.BuildExportPublicationStore)
	producers, producerOK := b.store.(state.DeploymentRuntimeProducerInputStore)
	if !pubOK || !producerOK || b.buildPublisher == nil || recipe.GitOpsReuseScope == "" || cached.Toolchain.OriginDeploymentID == dep.ID {
		return false
	}
	origin, err := b.store.DeploymentByID(ctx, cached.Toolchain.OriginDeploymentID)
	if err != nil || origin.DeletedAt != nil || (origin.Status != state.DeployLive && origin.Status != state.DeploySnapshotting) || origin.AppID != app.ID || origin.BuildID != cached.Toolchain.OriginBuildID || origin.SourceSHA256 != dep.SourceSHA256 || len(origin.Sidecars) != 0 {
		return false
	}
	originApp, err := state.AppForDeploymentRuntime(app, origin)
	if err != nil {
		return false
	}
	scope, opted := gitOpsReuseScope(originApp, origin)
	if !opted || scope != recipe.GitOpsReuseScope || origin.SourceRoot != dep.SourceRoot {
		return false
	}
	provenance, err := b.store.BuildProvenanceByBuildID(ctx, origin.BuildID)
	if err != nil || provenance.SourceSHA256 != dep.SourceSHA256 || provenance.BaseDigest != environment.BaseDigest || provenance.RuntimeBaseRef != recipe.RuntimeBaseRef ||
		provenance.BuildkitVer != cached.Toolchain.BuildkitVer || provenance.RailpackVer != cached.Toolchain.RailpackVer || !reflect.DeepEqual(provenance.DevPatch, cached.Toolchain.DevPatch) {
		return false
	}
	digest, bytes, err := buildpublisher.MeasureExport(ctx, cached.Path)
	if err != nil || bytes != cached.Bytes {
		return false
	}
	proof, err := publications.GetFreshBuildExportPublication(ctx, app.AccountID, app.ID, origin.ID, origin.BuildID)
	if err != nil {
		return false
	}
	claims := proof.Input.Claims
	if claims.SourceSHA256 != dep.SourceSHA256 || claims.ExportDigest != digest || claims.ExportBytes != bytes ||
		claims.AccountID != canonicalBuildID(app.AccountID) || claims.AppID != canonicalBuildID(app.ID) || claims.DeploymentID != canonicalBuildID(origin.ID) || claims.BuildID != canonicalBuildID(origin.BuildID) {
		return false
	}
	// This last shared trust fence rechecks the complete selected base and
	// source publisher set together, after measuring the leased bytes.
	_, err = producers.GetFreshDeploymentRuntimeProducerInputs(ctx, app.AccountID, app.ID, origin.ID)
	return err == nil
}
