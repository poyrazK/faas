package imaged

// adr: 435. A source producer binds conversion, not observed adoption or boot.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/state"
)

type sourceBuildRootfsBinding struct {
	Approval   state.BuildExportPublication `json:"-"`
	Base       state.BaseImageProducer      `json:"-"`
	IntentHash string                       `json:"-"`
}

func (h *Handler) prepareSourceBuildRootfsBinding(ctx context.Context, app state.App, dep state.Deployment, approval state.BuildExportPublication) (sourceBuildRootfsBinding, error) {
	binding := sourceBuildRootfsBinding{Approval: approval}
	hash, err := state.SourceBuildRootfsIntentHash(app, dep)
	if err != nil {
		return binding, err
	}
	binding.IntentHash = hash
	_, runtime := state.SourceBuildRootfsKind(app, dep)
	binding.Base, err = h.runtimeDefaultBaseProducer(ctx, runtime)
	if errors.Is(err, state.ErrNotFound) {
		selected := app
		selected.Runtime = runtime
		if err := h.ensureDeploymentRuntimeBase(ctx, selected); err != nil {
			return binding, err
		}
		binding.Base, err = h.runtimeDefaultBaseProducer(ctx, runtime)
	}
	return binding, err
}

func (h *Handler) publishSourceRootfs(ctx context.Context, app state.App, dep state.Deployment, key string, result rootfs.BuildResult, binding *sourceBuildRootfsBinding) error {
	path := h.appsRootPath(app.Slug, dep.ID)
	if binding == nil {
		return h.setDeploymentRootfs(ctx, dep.ID, path, key, result.ContentBytes)
	}
	store, ok := h.store.(state.SourceBuildRootfsStore)
	if !ok {
		return fmt.Errorf("imaged: durable source rootfs store unavailable")
	}
	kind, runtime := state.SourceBuildRootfsKind(app, dep)
	if result.ImageKey != key || result.ArtifactDigest == "" || result.ArtifactBytes <= 0 || result.GuestInitDigest != binding.Base.Input.GuestInitDigest {
		return fmt.Errorf("imaged: source conversion output binding missing or mismatched")
	}
	if err := h.checkProducedRootfs(ctx, key, result); err != nil {
		return err
	}
	current, err := h.runtimeDefaultBaseProducer(ctx, runtime)
	if err != nil {
		return err
	}
	if current.ID != binding.Base.ID || current.InputHash != binding.Base.InputHash {
		return state.ErrApplicationStandardRuntimeStale
	}
	parent := binding.Approval
	_, err = store.PublishSourceBuildRootfs(ctx, state.SourceBuildRootfsInput{
		ID: uuid.NewString(), PublicationID: parent.ID, PublicationHash: parent.InputHash,
		AccountID: app.AccountID, OrgID: app.OrgID, AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope,
		Kind: kind, Runtime: runtime, IntentHash: binding.IntentHash, StorageKey: key, RootfsPath: path, ContentBytes: result.ContentBytes,
		ArtifactDigest: result.ArtifactDigest, ArtifactBytes: result.ArtifactBytes, GuestInitDigest: result.GuestInitDigest, RunnerDigest: result.RunnerDigest,
		BaseProducerID: binding.Base.ID, BaseInputHash: binding.Base.InputHash, LayoutVersion: state.SourceBuildRootfsLayout,
	})
	return err
}
