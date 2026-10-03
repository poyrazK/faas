package imaged

// adr: 435. Consume company-approved source bytes without creating runtime authority.

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/buildpublisher"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) approvedSourceExport(ctx context.Context, app state.App, dep state.Deployment) (*state.BuildExportPublication, error) {
	required := app.RequireSigned || app.SecurityPolicy.RequiresSignedImage()
	store, ok := h.store.(state.BuildExportPublicationStore)
	if !ok {
		if required {
			return nil, fmt.Errorf("imaged: source publisher store unavailable")
		}
		return nil, nil
	}
	present, err := store.HasBuildExportPublication(ctx, app.AccountID, app.ID, dep.ID)
	if err != nil {
		return nil, err
	}
	if !present {
		if required {
			return nil, fmt.Errorf("imaged: required source publisher approval missing")
		}
		return nil, nil
	}
	build, err := h.store.BuildByDeployment(ctx, dep.ID)
	if err != nil {
		return nil, err
	}
	value, err := store.GetFreshBuildExportPublication(ctx, app.AccountID, app.ID, dep.ID, build.ID)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func (h *Handler) consumeSourceBuild(ctx context.Context, app state.App, dep state.Deployment, acct state.Account) (err error) {
	approval, err := h.approvedSourceExport(ctx, app, dep)
	if err != nil {
		return fmt.Errorf("imaged: source publisher approval: %w", err)
	}
	if approval == nil {
		if err := h.convertSourceBuild(ctx, app, dep, acct); err != nil {
			return err
		}
		return h.recheckSourceApproval(ctx, app, dep, nil)
	}
	if (app.Type == state.AppTypeFunction || app.Runtime != "") && !h.runtimeBaseStagingEnabled {
		return fmt.Errorf("imaged: approved function conversion requires produced runtime staging")
	}
	snapshot, err := buildpublisher.SnapshotExport(ctx, dep.RootfsPath, approval.Input.Claims.ExportDigest, approval.Input.Claims.ExportBytes)
	if err != nil {
		return fmt.Errorf("imaged: snapshot approved source export: %w", err)
	}
	defer func() { err = errors.Join(err, snapshot.Close()) }()
	consumed := dep
	consumed.RootfsPath = snapshot.Path()
	if err := h.convertSourceBuild(ctx, app, consumed, acct); err != nil {
		return err
	}
	return h.recheckSourceApproval(ctx, app, dep, approval)
}

func (h *Handler) convertSourceBuild(ctx context.Context, app state.App, dep state.Deployment, acct state.Account) error {
	if app.Type == state.AppTypeFunction || app.Runtime != "" {
		return h.buildFunctionLayer(ctx, app, dep, acct)
	}
	return h.buildLocalOCIAppLayer(ctx, app, dep, acct)
}

func (h *Handler) recheckSourceApproval(ctx context.Context, app state.App, dep state.Deployment, prior *state.BuildExportPublication) error {
	current, err := h.store.AppByID(ctx, app.ID)
	if err != nil {
		return err
	}
	fresh, err := h.approvedSourceExport(ctx, current, dep)
	if err != nil {
		return err
	}
	if (prior == nil) != (fresh == nil) || prior != nil && prior.InputHash != fresh.InputHash {
		return state.ErrApplicationStandardRuntimeStale
	}
	return nil
}
