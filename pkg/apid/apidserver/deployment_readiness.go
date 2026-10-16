package apidserver

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/deployreadiness"
	"github.com/onebox-faas/faas/pkg/apid/deploystaging"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) checkProjectDeployment(w http.ResponseWriter, r *http.Request, account state.Account) {
	sender, ok := s.routeProbes.(deploystaging.ProjectCriticalProbeSender)
	deployreadiness.Handle(w, r, account, s.store, deployreadiness.Config{
		ServiceAdditionsEnabled: s.atomicServiceAdditionsEnabled,
		WorkerReleasesEnabled:   s.atomicWorkerReleaseEnabled, ProgressiveEnabled: s.projectCanariesEnabled, SecretsEnabled: s.projectReleaseSecretsEnabled,
		CriticalProbesConfigured: ok && sender.Configured() && s.notif != nil,
		PromotionPreview: func(ctx context.Context, account state.Account, project string, request api.ProjectDeploymentCheckRequest) (api.ProjectEnvironmentPromotionPreviewResponse, *api.Problem) {
			plan, problem := s.buildProjectEnvironmentPromotionPlan(ctx, account, project, request.FromEnvironment, request.Environment, request.SyncConfig, request.Verify, request.Migrate, request.Workers)
			return plan.Preview, problem
		},
	})
}
