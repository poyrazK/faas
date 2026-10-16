package bindings

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (b ProjectVerificationRunBackend) Diagnose(ctx context.Context, id string) (api.ProjectBindingVerificationRunDiagnostics, error) {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil {
		return api.ProjectBindingVerificationRunDiagnostics{}, state.ErrInvalidArgument
	}
	id = parsed.String()
	r, err := b.Store.GetProjectBindingVerificationRun(ctx, b.AccountID, b.Project.ID, b.Environment, id)
	if err != nil {
		return api.ProjectBindingVerificationRunDiagnostics{}, err
	}
	out := api.ProjectBindingVerificationRunDiagnostics{RunID: r.ID, Project: r.Project, Environment: r.Environment, Status: r.Status, CheckedAt: time.Now().UTC(), ExpiresAt: r.ExpiresAt, Complete: true, Members: map[string]api.BindingVerificationRunDiagnostics{}, Blockers: []api.BindingVerificationDiagnosticBlocker{}, Changes: []api.BindingVerificationDifference{}, Recovery: []api.BindingVerificationRecoveryAction{}}
	if r.Status == "cancelled" || r.Status == "expired" {
		out.Blockers = append(out.Blockers, diagnosticBlocker("run_"+r.Status, "The original graph receipt is terminal; its deadline cannot be renewed.", "review_new_run"))
	}
	runs := []api.BindingVerificationRun{}
	for _, name := range graphVerificationApps(r) {
		original := r.Members[name]
		member, err := b.Member(ctx, name, original.Policy.AppID)
		var diagnostic api.BindingVerificationRunDiagnostics
		observed := original
		if err == nil {
			observed, err = member.Read(ctx, original.ID)
		}
		if err == nil {
			diagnostic, err = member.diagnoseRunWithBudget(ctx, observed, out.Budget)
		}
		if err != nil {
			if diagnosticForbidden(err) {
				return out, err
			}
			diagnostic = api.BindingVerificationRunDiagnostics{RunID: original.ID, GraphRunID: r.ID, App: name, DeploymentID: original.DeploymentID, Scope: original.Scope, Status: original.Status, CheckedAt: out.CheckedAt, ExpiresAt: original.ExpiresAt, Complete: false, Blockers: []api.BindingVerificationDiagnosticBlocker{}, Changes: []api.BindingVerificationDifference{}, Recovery: []api.BindingVerificationRecoveryAction{}}
			code, recovery := "member_unavailable", "retry_diagnostics"
			if diagnosticChanged(err) {
				recovery = "review_new_run"
			}
			diagnostic.Blockers = append(diagnostic.Blockers, diagnosticBlocker(code, "The original graph member could not be read.", recovery))
			finishDiagnostics(&diagnostic, original)
		}
		if diagnostic.RunID != original.ID || diagnostic.GraphRunID != r.ID || diagnostic.DeploymentID != original.DeploymentID || !diagnostic.ExpiresAt.Equal(r.ExpiresAt) {
			return out, fmt.Errorf("graph diagnostic selection changed")
		}
		out.Members[name] = diagnostic
		if out.Budget == nil && diagnostic.Budget != nil {
			out.Budget = diagnostic.Budget
		}
		out.Complete = out.Complete && diagnostic.Complete
		out.Blockers = append(out.Blockers, diagnostic.Blockers...)
		out.Changes = append(out.Changes, diagnostic.Changes...)

		runs = append(runs, observed)
	}
	apps, err := b.Catalog.AppsForProject(ctx, b.AccountID, b.Project.ID)
	if err != nil {
		out.Complete = false
		out.Blockers = append(out.Blockers, diagnosticBlocker("graph_inventory_unavailable", "Current project membership could not be read.", "retry_diagnostics"))
	} else {
		names := map[string]bool{}
		for _, app := range apps {
			names[app.Slug] = true
		}
		changed := len(names) != len(r.Members)
		for name := range r.Members {
			changed = changed || !names[name]
		}
		if changed {
			out.Blockers = append(out.Blockers, diagnosticBlocker("project_membership_changed", "The current project members differ from the reviewed graph.", "review_new_run"))
			out.Changes = append(out.Changes, api.BindingVerificationDifference{Field: "project_member_count", Reviewed: strconv.Itoa(len(r.Members)), Current: strconv.Itoa(len(names))})
			for name := range r.Members {
				if !names[name] {
					out.Changes = append(out.Changes, api.BindingVerificationDifference{App: name, Field: "project_membership", Reviewed: "true", Current: "false"})
				}
			}
		}
	}
	if err = b.confirmReviewManifest(ctx, r.Manifest); err != nil {
		if diagnosticForbidden(err) {
			return out, err
		}
		code, recovery := "graph_review_unavailable", "retry_diagnostics"
		if diagnosticChanged(err) {
			code, recovery = "graph_selection_changed", "review_new_run"
		} else {
			out.Complete = false
		}
		out.Blockers = append(out.Blockers, diagnosticBlocker(code, "The reviewed graph routing, policies, targets or candidate eligibility could not be reconfirmed.", recovery))
	}
	if r.Status == "pending" {
		if err = b.Store.CheckProjectBindingVerificationRun(ctx, b.AccountID, b.Project.ID, b.Environment, id); err != nil {
			code, recovery := "graph_admission_check_unavailable", "retry_diagnostics"
			if diagnosticChanged(err) {
				code, recovery = "graph_admission_snapshot_changed", "review_new_run"
			} else {
				out.Complete = false
			}
			out.Blockers = append(out.Blockers, diagnosticBlocker(code, "The original graph admission fences are not confirmed current.", recovery))
		}
	}
	out.Recovery = diagnosticRecovery(out.Blockers, runs)
	sort.SliceStable(out.Blockers, func(i, j int) bool {
		a, b := out.Blockers[i], out.Blockers[j]
		return a.App+a.Type+a.Name+a.Binding+a.Code < b.App+b.Type+b.Name+b.Binding+b.Code
	})
	sort.SliceStable(out.Changes, func(i, j int) bool {
		a, b := out.Changes[i], out.Changes[j]
		return a.App+a.Type+a.Name+a.Binding+a.Field < b.App+b.Type+b.Name+b.Binding+b.Field
	})
	return out, nil
}
