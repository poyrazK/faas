package financialtest

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestFinancialPreviewCommandOwnership(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s budgetStopStore = state.NewMemStore()
			if backend == "postgres" {
				s, _, _ = financialPostgres(t)
			}
			commands := s.(interface {
				state.AppTaskStore
				state.FinancialPreviewCommandStore
			})
			a := financialAccount(t, s)
			app, err := s.CreateApp(t.Context(), state.App{AccountID: a.ID, Slug: "preview-command", PreviewOfSlug: "production", RAMMB: 128, MaxConcurrency: 5})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:command", Status: state.DeployPending})
			if err != nil {
				t.Fatal(err)
			}
			for _, status := range []state.DeploymentStatus{state.DeployBuilding, state.DeployImaging, state.DeploySnapshotting} {
				if err := s.UpdateDeploymentStatus(t.Context(), dep.ID, status, ""); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.SetDeploymentRootfs(t.Context(), dep.ID, "/tmp/preview-command.ext4", "preview/command.ext4", 4096); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			create := func() state.AppTask {
				t.Helper()
				task, err := commands.CreateAppTask(t.Context(), state.CreateAppTaskParams{AccountID: a.ID, AppID: app.ID, DeploymentID: dep.ID, Kind: state.AppTaskKindManual, Command: []string{"true"}, TimeoutSeconds: 60, MaxOutputBytes: 1024, CreatedAt: now.Add(-time.Minute)})
				if err != nil {
					t.Fatal(err)
				}
				return task
			}
			create()
			task, err := commands.ClaimNextAppTask(t.Context(), "scheduler", now, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			node, err := s.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			r := state.FinancialPreviewCommandRuntime{TaskID: task.ID, LeaseToken: *task.LeaseToken, AccountID: a.ID, AppID: app.ID, DeploymentID: dep.ID, NodeID: node.ID, RAMMB: 128, CPUMillicores: 1000, RecordedAt: now, PrepareUntil: now.Add(time.Minute)}
			if err := commands.RegisterFinancialPreviewCommand(t.Context(), r); err != nil {
				t.Fatal(err)
			}
			if err := commands.RegisterFinancialPreviewCommand(t.Context(), r); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("duplicate preparation accepted: %v", err)
			}
			if rows, err := commands.FinancialPreviewCommands(t.Context(), a.ID, app.ID); err != nil || len(rows) != 1 || rows[0].Phase != "preparing" || rows[0].LeaseToken != r.LeaseToken || !rows[0].PrepareUntil.Equal(r.PrepareUntil.Truncate(time.Microsecond)) && backend == "postgres" {
				t.Fatalf("durable command owner: %+v %v", rows, err)
			}
			if err := commands.ActivateFinancialPreviewCommand(t.Context(), task.ID, uuid.NewString()); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("foreign preparation token: %v", err)
			}
			if err := commands.ActivateFinancialPreviewCommand(t.Context(), task.ID, *task.LeaseToken); err != nil {
				t.Fatal(err)
			}
			pending := create()
			spec := financialBudgetSpec()
			spec.Action = "stop_previews"
			p, err := s.CreateFinancialBudget(t.Context(), a.ID, uuid.NewString(), "editor", spec)
			if err != nil {
				t.Fatal(err)
			}
			month, amount := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC), spec.LimitMillicents
			if err := s.RecordFinancialBudgetEvaluation(t.Context(), a.ID, p.ID, p.Revision, financial.BudgetEvaluation{PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), AsOf: now, EvaluatedAt: now, KnownMillicents: &amount, Fresh: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := commands.ClaimNextAppTask(t.Context(), "scheduler", now, time.Minute); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("queued command bypassed hold: %v", err)
			}
			if _, err := commands.MarkAppTaskRunning(t.Context(), task.ID, *task.LeaseToken, now); err == nil {
				t.Fatal("command dispatched after threshold")
			}
			if row, err := commands.AppTaskByID(t.Context(), a.ID, app.ID, pending.ID); err != nil || row.Status != state.AppTaskQueued || row.AttemptCount != 0 {
				t.Fatalf("held queue was consumed: %+v %v", row, err)
			}
			if err := commands.RetireFinancialPreviewCommand(t.Context(), task.ID, uuid.NewString(), now); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("foreign teardown token: %v", err)
			}
			if err := commands.RetireFinancialPreviewCommand(t.Context(), task.ID, *task.LeaseToken, now); err != nil {
				t.Fatal(err)
			}
			if rows, err := commands.FinancialPreviewCommands(t.Context(), a.ID, app.ID); err != nil || len(rows) != 0 {
				t.Fatalf("confirmed retirement retained capacity: %+v %v", rows, err)
			}
		})
	}
}
