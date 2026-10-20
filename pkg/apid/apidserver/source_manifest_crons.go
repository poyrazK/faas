package apidserver

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/deploystaging"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

type sourceManifestSkipCronsKey struct{}

func (s *server) stageSourceDeploymentManifest(r *http.Request, acct state.Account, app state.App, m *gregalemanifest.Manifest, rollout state.Deployment, applyTriggers bool) (sourceRefManifestStaged, *api.Problem) {
	ctx := r.Context()
	var intent *api.DeploymentCronStagingRequest
	dark := rollout.TrafficPercentExplicit && rollout.TrafficPercent == 0 && rollout.CanaryTotalSteps == 0
	if dark || rollout.Scope != state.DefaultEnvScope {
		ctx = context.WithValue(ctx, sourceManifestSkipCronsKey{}, true)
	} else if applyTriggers && m != nil {
		store, ok := s.store.(state.DeploymentCronStagingStore)
		if !ok {
			return sourceRefManifestStaged{}, api.ErrCapacity("deployment cron staging is unavailable")
		}
		var err error
		ctx, intent, err = deploystaging.PrepareSourceCrons(ctx, store, acct, app, m)
		if err != nil {
			return sourceRefManifestStaged{}, sourceRefManifestStoreProblem(err, acct.Plan, true)
		}
	}
	// Never fall back to early cron writes if another deploy supersedes the
	// prepared intent between preparation and manifest ingestion.
	ctx = context.WithValue(ctx, sourceManifestSkipCronsKey{}, true)
	staged, problem := s.applySourceRefManifest(ctx, acct, app, m, rollout.Scope, applyTriggers)
	staged.cronIntent = intent
	return staged, problem
}

func (s *server) cancelSourceManifestCrons(ctx context.Context, staged sourceRefManifestStaged) error {
	if staged.cronIntent == nil {
		return nil
	}
	r := *staged.cronIntent
	r.Operation = "cancel"
	_, err := s.store.(state.DeploymentCronStagingStore).ApplyDeploymentCronStaging(ctx, staged.appID, state.DeploymentCronStagingParams{AccountID: staged.accountID, Request: r})
	return err
}
