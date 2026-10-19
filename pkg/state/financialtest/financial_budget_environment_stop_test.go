package financialtest

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestFinancialBudgetEnvironmentStopIsolation(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s budgetStopStore = state.NewMemStore()
			if backend == "postgres" {
				s, _, _ = financialPostgres(t)
			}
			a := financialAccount(t, s)
			project, err := s.CreateProject(t.Context(), state.Project{AccountID: a.ID, Slug: "budget-environments", ScanSource: state.ProjectScanSourceSingle})
			if err != nil {
				t.Fatal(err)
			}
			stage, err := s.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: a.ID, ProjectID: project.ID, Slug: "stage-a"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: a.ID, ProjectID: project.ID, Slug: "stage-b"}); err != nil {
				t.Fatal(err)
			}
			app, err := s.CreateApp(t.Context(), state.App{AccountID: a.ID, ProjectID: project.ID, WorkloadName: "preview", Slug: "budget-stage-preview", PreviewOfSlug: "production", RAMMB: 128, MaxConcurrency: 2})
			if err != nil {
				t.Fatal(err)
			}
			deploy := func(app state.App, scope string) state.Deployment {
				t.Helper()
				d, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage, ImageDigest: "sha256:scope"})
				if err != nil {
					t.Fatal(err)
				}
				return d
			}
			selected, sibling := deploy(app, "stage-a"), deploy(app, "stage-b")
			spec := financialBudgetSpec()
			spec.Action, spec.Scope = "stop_previews", financial.BudgetScope{Kind: "environment", ID: stage.ID}
			p, err := s.CreateFinancialBudget(t.Context(), a.ID, uuid.NewString(), "editor", spec)
			if err != nil {
				t.Fatal(err)
			}
			now, amount := time.Now().UTC(), spec.LimitMillicents
			month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
			if err := s.RecordFinancialBudgetEvaluation(t.Context(), a.ID, p.ID, p.Revision, financial.BudgetEvaluation{PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), AsOf: now, EvaluatedAt: now, KnownMillicents: &amount, Fresh: true}); err != nil {
				t.Fatal(err)
			}
			if err := state.CheckFinancialBudgetAdmission(t.Context(), s, app, selected.ID, now); !errors.Is(err, state.ErrFinancialBudgetHeld) {
				t.Fatalf("selected runtime environment escaped hold: %v", err)
			}
			if err := state.CheckFinancialBudgetAdmission(t.Context(), s, app, sibling.ID, now); err != nil {
				t.Fatalf("sibling environment was held: %v", err)
			}
			foreign, err := s.CreateProject(t.Context(), state.Project{AccountID: a.ID, Slug: "other-budget-project", ScanSource: state.ProjectScanSourceSingle})
			if err != nil {
				t.Fatal(err)
			}
			other, err := s.CreateApp(t.Context(), state.App{AccountID: a.ID, ProjectID: foreign.ID, WorkloadName: "preview", Slug: "other-stage-preview", PreviewOfSlug: "production", RAMMB: 128, MaxConcurrency: 2})
			if err != nil {
				t.Fatal(err)
			}
			if err := state.CheckFinancialBudgetAdmission(t.Context(), s, other, uuid.NewString(), now); err != nil {
				t.Fatalf("unrelated project required this environment's runtime identity: %v", err)
			}
		})
	}
}
