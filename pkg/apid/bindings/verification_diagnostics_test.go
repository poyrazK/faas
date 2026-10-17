package bindings

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func diagnosticHasCode(out api.BindingVerificationRunDiagnostics, code string) bool {
	for _, b := range out.Blockers {
		if b.Code == code {
			return true
		}
	}
	return false
}
func diagnosticHasRecovery(out api.BindingVerificationRunDiagnostics, action string) bool {
	for _, a := range out.Recovery {
		if a.Action == action {
			return true
		}
	}
	return false
}
func TestVerificationDiagnosticsReadOnlyAndPublicChanges(t *testing.T) {
	for _, cause := range []string{"unchanged", "policy", "same_revision_requirements", "configuration", "binding", "generation", "protected_fields", "partial", "forbidden"} {
		t.Run(cause, func(t *testing.T) {
			b, m, i, p := verificationBackendFixture(t)
			ctx := t.Context()
			run, err := b.Create(ctx, newReceiptRequest(*i, *p))
			if err != nil {
				t.Fatal(err)
			}
			b.CaptureTask = func(context.Context, string, api.BindingVerificationPlanStep, time.Time) (*state.CreateAppTaskParams, error) {
				t.Error("diagnostics captured private commands")
				return nil, state.ErrConflict
			}
			code := ""
			switch cause {
			case "policy":
				p.Revision++
				code = "policy_changed"
			case "same_revision_requirements":
				p.ObjectStorageRequirements = map[string]string{"ASSETS": "read_write"}
				code = "policy_changed"
			case "configuration":
				stamp := time.Now().Add(-time.Millisecond)
				i.RuntimeFreshness.ConfigChangedAt = &stamp
				code = "configuration_changed"
			case "binding":
				i.Bindings = nil
				code = "binding_selection_changed"
			case "generation":
				i.Bindings[0].CredentialGeneration = new(int64(2))
				code = "credential_generation_changed"
			case "protected_fields":
				i.Bindings[0].HTTPSURL = "https://private-origin-secret-value.invalid"
				p.Reason = "private-reason-secret-value"
				code = "binding_metadata_changed"
			case "partial":
				i.Complete = false
				i.Issues = []api.BindingInventoryIssue{{Type: "service", Code: "unavailable", Message: "private-provider-secret-value"}}
				code = "inventory_unavailable"
			case "forbidden":
				b.DiagnosticsAccess = func(api.BindingVerificationRun) error {
					return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Forbidden", "No domain access.")
				}
			}
			diagnostic, err := b.Diagnose(ctx, run.ID)
			if cause == "forbidden" {
				if !diagnosticForbidden(err) {
					t.Fatalf("forbidden metadata returned: %+v %v", diagnostic, err)
				}
				return
			}
			if err != nil || diagnostic.RunID != run.ID || !diagnostic.ExpiresAt.Equal(run.ExpiresAt) {
				t.Fatalf("diagnose: %+v %v", diagnostic, err)
			}
			if code != "" && !diagnosticHasCode(diagnostic, code) {
				t.Fatalf("missing %s: %+v", code, diagnostic)
			}
			if cause == "unchanged" && !diagnosticHasRecovery(diagnostic, "resume") {
				t.Fatalf("missing continuation: %+v", diagnostic)
			}
			if cause == "partial" && (diagnostic.Complete || !diagnosticHasRecovery(diagnostic, "retry_diagnostics") || len(diagnostic.Changes) != 0) {
				t.Fatalf("partial inventory treated as drift: %+v", diagnostic)
			}
			if code != "" && cause != "partial" && !diagnosticHasRecovery(diagnostic, "review_new_run") {
				t.Fatalf("drift did not require review: %+v", diagnostic)
			}
			data, _ := json.Marshal(diagnostic)
			if strings.Contains(string(data), "secret-value") {
				t.Fatalf("private values exposed: %s", data)
			}
			stored, err := m.GetBindingVerificationRun(ctx, b.AccountID, b.AppID, run.ID)
			if err != nil || stored.Status != "pending" || stored.Steps[0].ResourceID != "" || !stored.ExpiresAt.Equal(run.ExpiresAt) {
				t.Fatalf("diagnostics mutated receipt: %+v %v", stored, err)
			}
			tasks, err := m.ListAppTasks(ctx, b.AccountID, b.AppID, 10, 0)
			if err != nil || len(tasks) != 0 {
				t.Fatalf("diagnostics admitted tasks: %v %v", tasks, err)
			}
		})
	}
}
func TestVerificationDiagnosticsPendingFailureAckAndExpiry(t *testing.T) {
	b, m, i, p := verificationBackendFixture(t)
	ctx := t.Context()
	run, err := b.Create(ctx, newReceiptRequest(*i, *p))
	if err != nil {
		t.Fatal(err)
	}
	step, err := b.Admit(ctx, run.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	diagnostic, err := b.Diagnose(ctx, run.ID)
	if err != nil || !diagnosticHasCode(diagnostic, "resource_pending") || !diagnosticHasRecovery(diagnostic, "resume") {
		t.Fatalf("pending: %+v %v", diagnostic, err)
	}
	claimed, err := m.ClaimNextAppTask(ctx, "diagnostics", time.Now(), time.Minute)
	if err != nil || claimed.LeaseToken == nil {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	exit := 1
	if _, err = m.CompleteAppTask(ctx, state.CompleteAppTaskParams{ID: step.ResourceID, LeaseToken: *claimed.LeaseToken, Status: state.AppTaskFailed, FailureCode: new("probe_failed"), FailureMessage: new("private-provider-secret-value"), ExitCode: &exit, StdoutTail: "private-guest-secret-value", FinishedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	diagnostic, err = b.Diagnose(ctx, run.ID)
	if err != nil || !diagnosticHasCode(diagnostic, "resource_failed") || !diagnosticHasRecovery(diagnostic, "review_new_run") {
		t.Fatalf("failed: %+v %v", diagnostic, err)
	}
	data, _ := json.Marshal(diagnostic)
	if strings.Contains(string(data), "private-guest") {
		t.Fatal("guest output exposed")
	}
	base := diagnosticBase(api.BindingVerificationRun{ID: run.ID, App: b.App, Scope: run.Scope, Status: "expired", ExpiresAt: time.Now().Add(-time.Second), Steps: run.Steps})
	finishDiagnostics(&base, run)
	if !diagnosticHasCode(base, "run_expired") || diagnosticHasRecovery(base, "resume") {
		t.Fatalf("expired receipt suggested resume: %+v", base)
	}
	// Acknowledgement is separate from task-guest proof and must remain explicit.
	p.RequireApplicationAck = true
	i.Bindings = append(i.Bindings, api.AppBindingInventoryItem{Type: api.BindingTypePostgres, Name: "database", Binding: "DATABASE_URL", Scope: run.Scope, Access: "read", State: "active", CredentialConfigured: new(true), CredentialGeneration: new(int64(1))})
	fresh := run
	fresh.Policy = *p
	fresh.Inventory = *i
	out := diagnosticBase(fresh)
	applyPublicDiagnostics(&out, fresh, *i, *p)
	finishDiagnostics(&out, fresh)
	found := false
	for _, blocker := range out.Blockers {
		found = found || blocker.Recovery == "wait_for_application_ack"
	}
	if !found {
		t.Fatalf("missing acknowledgement explanation: %+v", out)
	}
	if _, err = b.Diagnose(ctx, "not-a-uuid"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid receipt accepted: %v", err)
	}
}
func TestProjectVerificationDiagnosticsMembershipAndReadOnly(t *testing.T) {
	b, input, inventories := graphBackendFixture(t)
	ctx := t.Context()
	run, err := b.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	memberFactory := b.Member
	b.Member = func(ctx context.Context, name, id string) (VerificationRunBackend, error) {
		member, err := memberFactory(ctx, name, id)
		member.CaptureTask = func(context.Context, string, api.BindingVerificationPlanStep, time.Time) (*state.CreateAppTaskParams, error) {
			t.Error("graph diagnostics captured private commands")
			return nil, state.ErrConflict
		}
		return member, err
	}
	diagnostic, err := b.Diagnose(ctx, run.ID)
	if err != nil || len(diagnostic.Members) != 2 || !diagnostic.Complete {
		t.Fatalf("graph: %+v %v", diagnostic, err)
	}
	for name, member := range diagnostic.Members {
		if member.App != name || member.RunID != run.Members[name].ID || !member.ExpiresAt.Equal(run.ExpiresAt) {
			t.Fatalf("member changed: %+v", member)
		}
	}
	inventories["graph-first"].Bindings[0].Name = "different-service"
	diagnostic, err = b.Diagnose(ctx, run.ID)
	if err != nil || !diagnosticHasCode(diagnostic.Members["graph-first"], "binding_selection_changed") {
		t.Fatalf("member drift missing: %+v %v", diagnostic, err)
	}
	store := b.Store.(*state.MemStore)
	if _, err = store.CreateApp(ctx, state.App{AccountID: b.AccountID, ProjectID: b.Project.ID, Slug: "added-member", Type: state.AppTypeFunction}); err != nil {
		t.Fatal(err)
	}
	diagnostic, err = b.Diagnose(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, blocker := range diagnostic.Blockers {
		found = found || blocker.Code == "project_membership_changed"
	}
	if !found {
		t.Fatalf("missing membership drift: %+v", diagnostic)
	}
	saved, err := store.GetProjectBindingVerificationRun(ctx, b.AccountID, b.Project.ID, b.Environment, run.ID)
	if err != nil || saved.Status != "pending" || !saved.ExpiresAt.Equal(run.ExpiresAt) {
		t.Fatalf("diagnostics mutated graph: %+v %v", saved, err)
	}
}
