package apidserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/deployreadiness"
	"github.com/onebox-faas/faas/pkg/apid/deploystaging"
	"github.com/onebox-faas/faas/pkg/apid/projectaccess"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) checkProjectDeployment(w http.ResponseWriter, r *http.Request, account state.Account) {
	if grant, shared := projectaccess.From(r.Context()); shared && grant.Project.ID == "" {
		// The company has no project with this slug yet; a namespace match is
		// someone's personal project and must not be checked under company access.
		if _, err := s.store.ProjectBySlug(r.Context(), account.ID, r.PathValue("slug")); err == nil {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Project slug unavailable", "a project outside this organization already uses this slug"))
			return
		} else if !errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrCapacity("could not check the project slug"))
			return
		}
	}
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
