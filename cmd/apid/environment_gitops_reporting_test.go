// adr: 568 — continuous reports retain intent and effects through outage and restart.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func reportGitOpsSource(t *testing.T, store *state.MemStore, source state.EnvironmentGitSource) state.EnvironmentGitSource {
	t.Helper()
	mode := "report"
	source, err := store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID,
		state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Mode: mode})
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func waitGitOpsReport(t *testing.T, store *state.MemStore, source state.EnvironmentGitSource, previousID string) state.EnvironmentGitOpsRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		runs, err := store.ListEnvironmentGitOpsRuns(ctx, source.AccountID, source.ID, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) == 1 && runs[0].CompletedAt != nil && runs[0].ID != previousID {
			return runs[0]
		}
		select {
		case <-ctx.Done():
			t.Fatal("report controller did not publish a completed attempt")
		case <-ticker.C:
		}
	}
}

func TestEnvironmentGitDriftReportingRequiresExplicitOptIn(t *testing.T) {
	for _, setting := range []string{"", "false", "1"} {
		t.Run(setting, func(t *testing.T) {
			srv, store, source, _ := gitOpsBackendFixture(t, 1, &gitOpsFleetNotifier{})
			source = reportGitOpsSource(t, store, source)
			stop := srv.startEnvironmentGitDriftReporting(t.Context(), func(string) string { return setting })
			stop()
			runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 10)
			if err != nil || len(runs) != 0 {
				t.Fatalf("disabled reporter claimed intent: %+v %v", runs, err)
			}
		})
	}
}

func TestEnvironmentGitDriftReportingFromApidSurvivesGitOutageAndRestart(t *testing.T) {
	notifier := &gitOpsFleetNotifier{nodes: []string{"node-a"}}
	srv, store, source, apps := gitOpsBackendFixture(t, 1, notifier)
	source = reportGitOpsSource(t, store, source)
	poll, err := store.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), time.Now().Add(time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishEnvironmentGitSourcePoll(t.Context(), poll, state.EnvironmentGitSourcePollResult{ErrorCode: "environment_git_source_unavailable"}, time.Now(), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	stop := srv.startEnvironmentGitDriftReporting(t.Context(), func(string) string { return "true" })
	t.Cleanup(stop)
	first := waitGitOpsReport(t, store, source, "")
	stop()
	if first.Status != "drifted" || string(first.Steps) != "[]" {
		t.Fatalf("Git outage hid observed drift: %+v", first)
	}
	rows, err := store.ListAppEnvInScope(t.Context(), source.AccountID, apps[0].ID, "production")
	if err != nil || len(rows) != 1 || rows[0].Value != "console" {
		t.Fatalf("report changed approved intent: %+v %v", rows, err)
	}
	if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, apps[0].ID, "production", "MODE", "second-console-edit"); err != nil {
		t.Fatal(err)
	}
	fresh := newServer(store, srv.log, "gregale.dev", notifier)
	stopFresh := fresh.startEnvironmentGitDriftReporting(t.Context(), func(string) string { return "true" })
	t.Cleanup(stopFresh)
	second := waitGitOpsReport(t, store, source, first.ID)
	stopFresh()
	if second.Status != "drifted" || second.RevisionID != source.ApprovedRevisionID {
		t.Fatalf("restart lost approved definition: %+v", second)
	}
	account, err := store.AccountByID(t.Context(), source.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	response := gitOpsHandlerRequest(t, fresh, account, http.MethodGet, "", nil, fresh.getEnvironmentGitOps)
	var status api.EnvironmentGitOpsStatusResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil || len(status.Runs) < 2 || status.Runs[0].ID != second.ID || status.Source.SourceErrorCode != "environment_git_source_unavailable" || status.Source.AppliedRevisionID != "" {
		t.Fatalf("authenticated status lost report or separate source outage: %d %s", response.Code, response.Body.String())
	}
	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	if len(notifier.published) != 0 {
		t.Fatalf("report sent policy mutation: %+v", notifier.published)
	}
}

func TestEnvironmentGitDriftReportingLeavesEnforceEffectsPending(t *testing.T) {
	notifier := &gitOpsFleetNotifier{nodes: []string{"node-a"}, failApply: true}
	srv, store, source, _ := gitOpsBackendFixture(t, 1, notifier)
	worker := gitOpsBackendWorker(srv, store)
	if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
		t.Fatalf("failed enforce attempt: %v %v", worked, err)
	}
	source = reportGitOpsSource(t, store, source)
	lease, err := store.ClaimEnvironmentGitOpsMode(t.Context(), "report", "effect-inspection", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.PendingEnvironmentGitOpsEffects(t.Context(), lease)
	if err != nil || len(before) != 1 {
		t.Fatalf("pending enforcement: %+v %v", before, err)
	}
	backend := worker.Backend.(*environmentGitOpsBackend)
	if err := backend.RecoverEffects(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishEnvironmentGitOps(t.Context(), lease, "drifted", json.RawMessage(`{}`), json.RawMessage(`[]`), "", time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	notifier.mu.Lock()
	published := len(notifier.published)
	notifier.failApply = false
	notifier.mu.Unlock()
	stop := srv.startEnvironmentGitDriftReporting(t.Context(), func(string) string { return "true" })
	t.Cleanup(stop)
	run := waitGitOpsReport(t, store, source, lease.RunID)
	stop()
	if run.Status != "drifted" || run.ErrorCode != "environment_runtime_unacknowledged" {
		t.Fatalf("equal intent hid pending enforcement: %+v", run)
	}
	inspect, err := store.ClaimEnvironmentGitOpsMode(t.Context(), "report", "after-report", time.Now().Add(time.Minute), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	after, err := store.PendingEnvironmentGitOpsEffects(t.Context(), inspect)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("report recovered or modified enforce effect: %+v %+v %v", before, after, err)
	}
	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	if len(notifier.published) != published {
		t.Fatal("report replayed enforce fleet notifications")
	}
}
