package bindingrun

import (
	"context"
	"errors"
	"github.com/poyrazK/faas/sdk/go/internal/api"
	"testing"
	"time"
)

type diagnosticClient struct {
	*fakeClient
	appRead      func(context.Context) (api.BindingVerificationRunDiagnostics, error)
	graphRead    func(context.Context) (api.ProjectBindingVerificationRunDiagnostics, error)
	calls        int
	initialError error
}

func (f *diagnosticClient) GetBindingVerificationRunDiagnostics(ctx context.Context, _, _ string) (api.BindingVerificationRunDiagnostics, error) {
	f.calls++
	return f.appRead(ctx)
}
func (f *diagnosticClient) GetProjectBindingVerificationRunDiagnostics(ctx context.Context, _, _, _ string) (api.ProjectBindingVerificationRunDiagnostics, error) {
	f.calls++
	return f.graphRead(ctx)
}
func (f *diagnosticClient) ResumeBindingVerificationRun(ctx context.Context, app, id string) (api.BindingVerificationRun, error) {
	if f.initialError != nil {
		return api.BindingVerificationRun{}, f.initialError
	}
	return f.fakeClient.ResumeBindingVerificationRun(ctx, app, id)
}
func appDiagnostic(r api.BindingVerificationRun) api.BindingVerificationRunDiagnostics {
	return api.BindingVerificationRunDiagnostics{RunID: r.ID, GraphRunID: r.GraphRunID, App: r.App, DeploymentID: r.DeploymentID, Scope: r.Scope, Status: "pending", CheckedAt: time.Now().UTC(), ExpiresAt: r.ExpiresAt, Complete: false, Blockers: []api.BindingVerificationDiagnosticBlocker{{Code: "policy_changed", Message: "Policy changed.", Recovery: "review_new_run"}}, Changes: []api.BindingVerificationDifference{}, Recovery: []api.BindingVerificationRecoveryAction{{Action: "review_new_run", Message: "Review a new receipt."}}}
}
func TestRunnerAutomaticDiagnosticsPreserveCauseAndSelection(t *testing.T) {
	for _, mode := range []string{"success", "unavailable", "wrong_run", "wrong_deployment", "wrong_deadline", "skipped", "cancelled", "initial_failure"} {
		t.Run(mode, func(t *testing.T) {
			fake := newFake(contractCase{Actions: []string{"probe"}, Fault: "failed"})
			selected := *fake.root.app
			cause := errors.New("original network error")
			client := &diagnosticClient{fakeClient: fake}
			client.appRead = func(context.Context) (api.BindingVerificationRunDiagnostics, error) {
				out := appDiagnostic(selected)
				switch mode {
				case "unavailable":
					return out, errors.New("private diagnostic error")
				case "wrong_run":
					out.RunID = "99999999-9999-4999-8999-999999999999"
				case "wrong_deployment":
					out.DeploymentID = "99999999-9999-4999-8999-999999999999"
				case "wrong_deadline":
					out.ExpiresAt = out.ExpiresAt.Add(time.Second)
				}
				return out, nil
			}
			ctx, cancel := context.WithCancel(testContext(t))
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			if mode == "initial_failure" {
				client.initialError = cause
			}
			_, err := RunApp(ctx, client, selected.App, selected.ID, Options{PollInterval: time.Millisecond, SkipDiagnostics: mode == "skipped"})
			var recovery *RecoveryError
			if !errors.As(err, &recovery) || recovery.RunID != selected.ID {
				t.Fatalf("recovery: %v", err)
			}
			expected := mode == "success" || mode == "initial_failure"
			if (recovery.Diagnostics != nil) != expected {
				t.Fatalf("diagnostics: %+v", recovery)
			}
			if mode == "initial_failure" && !errors.Is(err, cause) {
				t.Fatal("lost original cause")
			}
			if mode == "cancelled" || mode == "skipped" {
				if client.calls != 0 {
					t.Fatal("diagnosed a stopped call")
				}
			}
			if fake.admits > 1 {
				t.Fatal("lookup admitted replacement work")
			}
		})
	}
}
func TestRunnerAutomaticGraphDiagnostics(t *testing.T) {
	fake := newFake(contractCase{Graph: true, Actions: []string{"probe", "probe"}, Fault: "failed"})
	selected := *fake.root.graph
	client := &diagnosticClient{fakeClient: fake}
	client.graphRead = func(context.Context) (api.ProjectBindingVerificationRunDiagnostics, error) {
		members := map[string]api.BindingVerificationRunDiagnostics{}
		for name, r := range selected.Members {
			members[name] = appDiagnostic(r)
		}
		return api.ProjectBindingVerificationRunDiagnostics{RunID: selected.ID, Project: selected.Project, Environment: selected.Environment, Status: "pending", CheckedAt: time.Now(), ExpiresAt: selected.ExpiresAt, Members: members, Complete: true}, nil
	}
	_, err := RunProject(testContext(t), client, selected.Project, selected.Environment, selected.ID, Options{PollInterval: time.Millisecond})
	var recovery *RecoveryError
	if !errors.As(err, &recovery) || recovery.ProjectDiagnostics == nil || len(recovery.ProjectDiagnostics.Members) != 2 || client.calls != 1 {
		t.Fatalf("graph diagnostics: %+v %v", recovery, err)
	}
}
func TestRunnerAutomaticDiagnosticsBoundedAndCancellation(t *testing.T) {
	for _, callbackFailure := range []bool{false, true} {
		fake := newFake(contractCase{Actions: []string{"probe"}, Fault: "failed"})
		client := &diagnosticClient{fakeClient: fake}
		client.appRead = func(ctx context.Context) (api.BindingVerificationRunDiagnostics, error) {
			<-ctx.Done()
			return api.BindingVerificationRunDiagnostics{}, ctx.Err()
		}
		ctx, cancel := context.WithTimeout(testContext(t), 80*time.Millisecond)
		defer cancel()
		options := Options{PollInterval: time.Millisecond}
		if callbackFailure {
			options.OnProgress = func(Progress) error { return errors.New("checkpoint failed") }
		}
		start := time.Now()
		_, err := RunApp(ctx, client, "api", fake.root.id(), options)
		if err == nil || time.Since(start) > time.Second {
			t.Fatalf("lookup delayed failure: %v", err)
		}
		if callbackFailure && client.calls != 0 {
			t.Fatal("diagnosed callback failure")
		}
		if !callbackFailure && (client.calls != 1 || errors.Is(err, context.DeadlineExceeded)) {
			t.Fatalf("lookup masked the original resource error: %v", err)
		}
	}
}
