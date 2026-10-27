package apidserver

import (
	"context"
	"net/http"
	"net/http/httptest"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// InstallCapabilityGate installs the production capability gate on a
// source-ref test server.
func (e sourceRefTestEnv) InstallCapabilityGate() error {
	return e.srv.InstallCapabilityGate()
}

// PromotionApprovalProblemForTest runs the promotion approval check for a
// promotion of apps into toEnvironment (ADR-20261020085706 amendment 1).
func (s *server) PromotionApprovalProblemForTest(ctx context.Context, apps []state.App, project, toEnvironment string, protected bool) *api.Problem {
	plan := projectEnvironmentPromotionPlan{Apps: map[string]state.App{}}
	plan.Preview.ProjectSlug = project
	plan.Preview.ApprovalRequired = protected
	for _, app := range apps {
		plan.Apps[app.Slug] = app
	}
	return s.promotionApprovalProblem(ctx, plan, toEnvironment)
}

// ProtectedPromotionApprovalProblemForTest holds a protected production
// environment's promotion approval, given by approver, to the apps' policies
// for a promotion by promoter (ADR-20261020085706 amendment 2).
func (s *server) ProtectedPromotionApprovalProblemForTest(ctx context.Context, apps []state.App, toEnvironment, approver, promoter string) *api.Problem {
	plan := projectEnvironmentPromotionPlan{Apps: map[string]state.App{}}
	plan.Preview.ApprovalRequired = true
	for _, app := range apps {
		plan.Apps[app.Slug] = app
	}
	return s.protectedPromotionApprovalProblem(ctx, plan, toEnvironment, approver, promoter)
}

// RenderReleaseSafetyForTest renders the release safety dashboard page for
// acct's app slug.
func (s *server) RenderReleaseSafetyForTest(acct state.Account, slug string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/dashboard/apps/"+slug+"/release-safety", nil)
	r.SetPathValue("slug", slug)
	r = r.WithContext(WithAccount(r.Context(), acct))
	rec := httptest.NewRecorder()
	s.renderReleaseSafetyDashboard(rec, r)
	return rec
}

// GitOpsReleaseHoldForTest names what holds activating graph, or "".
func (s *server) GitOpsReleaseHoldForTest(ctx context.Context, lease state.EnvironmentGitOpsLease, graph state.EnvironmentWorkloadGraph) (string, error) {
	b := &environmentGitOpsBackend{server: s}
	return b.gitOpsReleaseHold(ctx, lease, graph)
}
