package apidserver

import (
	"context"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/deploystaging"
	"github.com/onebox-faas/faas/pkg/state"
)

type projectDeploymentCronPlan struct {
	plan deploystaging.ProjectCronPlan
	err  error
}

func (s *server) planProjectDeploymentCrons(ctx context.Context, acct state.Account, apps, added, changed []state.App, crons []planCron) ([]state.App, map[string]projectDeploymentCronPlan) {
	plans := make(map[string]projectDeploymentCronPlan, len(apps))
	touched := make(map[string]bool)
	for _, app := range append(append([]state.App{}, added...), changed...) {
		touched[app.ID] = true
	}
	out := append([]state.App{}, changed...)
	for _, app := range apps {
		desired := []api.DeploymentCronConfig{}
		for _, cron := range crons {
			if strings.EqualFold(cron.WorkloadName, app.WorkloadName) {
				desired = append(desired, api.DeploymentCronConfig{Schedule: cron.Schedule, Path: cron.Path, Enabled: cron.Enabled})
			}
		}
		store, ok := s.store.(state.DeploymentCronStagingStore)
		entry := projectDeploymentCronPlan{}
		if !ok {
			entry.err = state.ErrInvalidArgument
		} else {
			entry.plan, entry.err = deploystaging.PlanProjectCrons(ctx, store, acct.ID, app.ID, desired)
		}
		if touched[app.ID] {
			entry.plan.Reuse = nil
		}
		plans[app.ID] = entry
		if !touched[app.ID] && (entry.err != nil || entry.plan.Changed) {
			out = append(out, app)
			touched[app.ID] = true
		}
	}
	return out, plans
}

func (s *server) prepareProjectDeploymentCrons(ctx context.Context, acct state.Account, appID string, plan projectDeploymentCronPlan) error {
	limits, _ := api.LimitsFor(acct.Plan)
	_, err := s.store.(state.DeploymentCronStagingStore).ApplyDeploymentCronStaging(ctx, appID, state.DeploymentCronStagingParams{AccountID: acct.ID, Request: plan.plan.Request, Limits: limits})
	return err
}

func (s *server) readProjectDeploymentConfiguration(ctx context.Context, accountID, deploymentID string) *api.DeploymentConfigurationStatus {
	if store, ok := s.store.(state.DeploymentConfigurationStore); ok {
		config, err := store.DeploymentConfiguration(ctx, accountID, deploymentID)
		if err == nil {
			return &config
		}
		s.log.Warn("apid: read project deployment configuration failed", "deployment_id", deploymentID, "err", err)
	}
	return nil
}
