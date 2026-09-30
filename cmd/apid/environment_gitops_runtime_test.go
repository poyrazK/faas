package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

type gitOpsRuntimeNotifier struct {
	*gitOpsFleetNotifier
	requests []state.EnvironmentGitOpsRuntimeRequest
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
	worker.Now = func() time.Time { return time.Now().Add(4 * time.Second) }
	if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
		t.Fatalf("ready recovery: %v %v", worked, err)
	}
	current, err = store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
	if err != nil || current.AppliedRevisionID != source.ApprovedRevisionID || len(runtimeNotifier.requests) != 1 {
		t.Fatalf("ready runtime did not converge: %+v %v", current, err)
	}
}
