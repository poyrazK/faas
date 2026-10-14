package apidserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

type gitOpsRuntimeNotifier struct {
	*gitOpsFleetNotifier
	requests []state.EnvironmentGitOpsRuntimeRequest
}

func TestEnvironmentGitOpsBackendConvergesConfigurationOnlyEnvironment(t *testing.T) {
	srv, store, account, project, _ := newProjectLifecycleFixture(t)
	if _, err := store.UpdateProjectBinding(t.Context(), account.ID, project.ID, "example/shop", "main", 42); err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateEnvironmentGitSource(t.Context(), account.ID, project.ID, "production", state.EnvironmentGitSourceSpec{
		RepositoryID: 123, InstallationID: 42, Repository: "example/shop", Ref: "refs/heads/main",
		ManifestPath: "environments/production.yaml", Mode: "enforce", ApprovalPolicy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion,
		Project: "shop", Environment: "production", Workloads: map[string]api.EnvironmentWorkload{},
		Configuration: map[string]json.RawMessage{"LOG_LEVEL": json.RawMessage(`"info"`)}})
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), state.ApproveEnvironmentRevision{AccountID: account.ID,
		SourceID: source.ID, CommitSHA: strings.Repeat("a", 40), Desired: desired, ApprovedBy: "test-owner"})
	if err != nil {
		t.Fatal(err)
	}
	worker := gitOpsBackendWorker(srv, store)
	if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
		t.Fatalf("configuration-only reconciliation: worked=%t err=%v", worked, err)
	}
	current, err := store.EnvironmentGitSource(t.Context(), account.ID, project.ID, "production")
	if err != nil || current.AppliedRevisionID != source.ApprovedRevisionID {
		t.Fatalf("configuration-only revision did not converge: %+v %v", current, err)
	}
	config, err := store.ProjectEnvironmentConfigLatest(t.Context(), account.ID, project.ID, "production")
	var values map[string]string
	if err != nil || json.Unmarshal(config.Values, &values) != nil || values["LOG_LEVEL"] != "info" {
		t.Fatalf("configuration-only revision did not publish its reviewed value: %+v %v", config, err)
	}
	runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), account.ID, source.ID, 1)
	if err != nil || len(runs) != 1 || runs[0].Status != "converged" {
		t.Fatalf("configuration-only run: %+v %v", runs, err)
	}
}

func (n *gitOpsRuntimeNotifier) Notify(ctx context.Context, channel, raw string) error {
	if channel != db.NotifyRuntimeConfigRestart {
		return n.gitOpsFleetNotifier.Notify(ctx, channel, raw)
	}
	var request state.EnvironmentGitOpsRuntimeRequest
	if err := json.Unmarshal([]byte(raw), &request); err != nil {
		return err
	}
	n.mu.Lock()
	n.requests = append(n.requests, request)
	n.mu.Unlock()
	return nil
}

func TestEnvironmentGitOpsBackendWaitsForRuntimeReadinessAfterIntentAndFleetConverge(t *testing.T) {
	notifier := &gitOpsFleetNotifier{nodes: []string{"node-a"}}
	srv, store, source, apps := gitOpsBackendFixture(t, 1, notifier)
	runtimeNotifier := &gitOpsRuntimeNotifier{gitOpsFleetNotifier: notifier}
	srv.notif = runtimeNotifier
	app := apps[0]
	deployment, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "production",
		Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("c", 64), Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	old, err := store.CreateInstance(t.Context(), app.ID, deployment.ID, string(state.StateWaking), 512, "node-a", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateInstanceState(t.Context(), old.ID, string(state.StateRunning)); err != nil {
		t.Fatal(err)
	}
	worker := gitOpsBackendWorker(srv, store)
	if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
		t.Fatalf("apply: %v %v", worked, err)
	}
	runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 1)
	if err != nil || len(runs) != 1 || runs[0].Status != "partial" || runs[0].ErrorCode != "environment_runtime_unacknowledged" {
		t.Fatalf("intent/fleet success hid stale runtime: %+v %v", runs, err)
	}
	if len(runtimeNotifier.requests) != 1 || runtimeNotifier.requests[0].AppID != app.ID || runtimeNotifier.requests[0].Scope != "production" {
		t.Fatalf("runtime handoff lost scope: %+v", runtimeNotifier.requests)
	}
	if err := store.UpdateInstanceState(t.Context(), old.ID, string(state.StateStopped)); err != nil {
		t.Fatal(err)
	}
	fresh, err := store.CreateInstance(t.Context(), app.ID, deployment.ID, string(state.StateWaking), 512, "node-a", runtimeNotifier.requests[0].WakeID)
	if err != nil {
		t.Fatal(err)
	}
	// A fresh apid must recover from durable state while the candidate is
	// booting; notification acceptance alone cannot publish applied_revision.
	freshServer := newServer(store, srv.log, "gregale.dev", runtimeNotifier)
	freshServer.WithEdgeRuleFleetRequired(true)
	worker = gitOpsBackendWorker(freshServer, store)
	worker.Now = func() time.Time { return time.Now().Add(2 * time.Second) }
	if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
		t.Fatalf("candidate recovery: %v %v", worked, err)
	}
	current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
	if err != nil || current.AppliedRevisionID != "" || len(runtimeNotifier.requests) != 1 {
		t.Fatalf("booting candidate was reported applied or refreshed twice: %+v %v", current, err)
	}
	if err := store.UpdateInstanceState(t.Context(), fresh.ID, string(state.StateRunning)); err != nil {
		t.Fatal(err)
	}
	boundary, stamped, err := state.RuntimeConfigChangedAtForScope(t.Context(), store, app.ID, deployment.Scope)
	if err != nil || !stamped {
		t.Fatalf("runtime boundary: %v %v", stamped, err)
	}
	variables, err := store.ListAppEnvInScope(t.Context(), app.AccountID, app.ID, deployment.Scope)
	if err != nil {
		t.Fatal(err)
	}
	inputs := state.RuntimeConfigInputs{Scope: deployment.Scope, Boundary: boundary, Variables: map[string]string{}, AllSecrets: true}
	for _, row := range variables {
		inputs.Variables[row.Key] = row.Value
		if row.UpdatedAt.After(inputs.Boundary) {
			inputs.Boundary = row.UpdatedAt
		}
	}
	if err := store.RecordInstanceRuntimeConfigReceipt(t.Context(), fresh.ID, fresh.WakeID, inputs); err != nil {
		t.Fatal(err)
	}
	worker.Now = func() time.Time { return time.Now().Add(4 * time.Second) }
	if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
		t.Fatalf("ready recovery: %v %v", worked, err)
	}
	current, err = store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
	if err != nil || current.AppliedRevisionID != "" || len(runtimeNotifier.requests) != 1 {
		t.Fatalf("runtime receipt bypassed workload qualification: %+v %v", current, err)
	}
}
