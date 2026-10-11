package apidsource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/state"
)

// enqueueProjectImage publishes directly to imaged. The source archive was
// used to validate project declarations; there is no source build to publish.
func enqueueProjectImage(ctx context.Context, store Store, notif Notifier, p EnqueueParams) (EnqueueResult, error) {
	if !api.ValidProjectImage(p.ImageRef) || p.ImagePort < 0 || p.ImagePort > 65535 {
		return EnqueueResult{}, fmt.Errorf("apidsource: invalid project image or port")
	}
	// Source builds publish operation definitions before their durable build
	// becomes claimable. Image deployments are claimable at row creation, so
	// source-defined operations need an atomic image admission path first.
	if len(p.OperationDefinitions) > 0 {
		return EnqueueResult{}, api.ErrSourceInvalid("project image workloads cannot publish source-defined operations")
	}
	profile, err := frameworkprofile.CaptureImageRuntime(p.ImageCommand, p.ImageHealthcheck)
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("apidsource: capture project image runtime: %w", err)
	}
	id := uuid.NewString()
	if p.DeliveryID != "" {
		id, _ = githubDeliveryIDs(p.DeliveryID, p.AppID)
	}
	input := state.Deployment{
		ID: id, AppID: p.AppID, Kind: state.DeploymentKindImage,
		ImageDigest: p.ImageRef, Status: state.DeployPending, Scope: p.Scope,
		SourceURL: p.SourceURL, CommitSHA: p.CommitSHA,
		GitHubSourceRef: p.GitHubSourceRef, GitHubInstallationID: p.GitHubInstallationID,
		DeployedByUserID: p.ActorUserID, DeployedVia: p.ActorVia,
		DeployedFromIP: p.ActorFromIP, PusherLogin: p.ActorPusherLogin,
		Reason: p.Reason, Tag: p.Tag, DeployedBy: p.DeployedBy, PRNumber: p.PRNumber,
		ReleaseCommand: append([]string(nil), p.ReleaseCommand...), ReleaseCommandShell: p.ReleaseCommandShell,
		OverridePort: p.ImagePort, FullRootfsAllowAuto: p.FullRootfsAllowAuto,
		Workflows:       append(json.RawMessage(nil), p.Workflows...),
		InferredProfile: profile,
	}
	if p.ServiceRollout {
		input.RolloutState = "rolling_out"
		input.TrafficPercentExplicit = true
		now := time.Now().UTC()
		input.RolloutStartedAt = &now
	}
	d, err := createProjectImageDeployment(ctx, store, p, input)
	if err != nil && p.DeliveryID != "" && errors.Is(err, state.ErrConflict) {
		if reader, ok := store.(interface {
			DeploymentByID(context.Context, string) (state.Deployment, error)
		}); ok {
			d, err = reader.DeploymentByID(ctx, id)
			if err == nil && (d.AppID != p.AppID || d.Kind != state.DeploymentKindImage) {
				err = fmt.Errorf("project image deployment id collision")
			}
		}
	}
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("apidsource: create image deployment: %w", err)
	}
	payload, _ := json.Marshal(map[string]string{
		"kind": "image", "status": string(d.Status), "app_id": p.AppID,
		"deployment_id": d.ID, "to": d.ID,
	})
	// The pending deployment is durable; imaged recovery rediscovers it if
	// the notification is lost, just as it does for direct image deployments.
	if err := notif.Notify(ctx, db.NotifyDeploymentChanged, string(payload)); err != nil {
		p.Log.Warn("apidsource: notify project image (imaged will recover)", "deployment", d.ID, "err", err)
	}
	return EnqueueResult{DeploymentID: d.ID}, nil
}

func createProjectImageDeployment(ctx context.Context, store Store, p EnqueueParams, input state.Deployment) (state.Deployment, error) {
	if p.Activity != nil {
		if activityStore, ok := store.(state.OrgActivityDeploymentMutationStore); ok {
			activity := *p.Activity
			id, err := uuid.Parse(input.ID)
			if err != nil {
				return state.Deployment{}, err
			}
			activity.DeploymentID = &id
			activity.SourceType, activity.SourceID = "deployment.requested", input.ID
			d, _, err := activityStore.CreateDeploymentWithActivity(ctx, input, activity)
			return d, err
		}
	}
	return store.CreateDeployment(ctx, input)
}
