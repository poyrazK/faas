// adr: 1155 — candidate assessment and customer status feedback contracts.
package pgintegration_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func finishNotificationReport(t *testing.T, store intentTestStore, desired environmentsync.DesiredState, now time.Time, beforeFinish func()) {
	t.Helper()
	lease, err := store.ClaimEnvironmentGitOps(t.Context(), uuid.NewString(), now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := store.ObserveEnvironmentGitOps(t.Context(), lease, desired)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := environmentsync.BuildPlan(desired, observed.State, observed.Owners, environmentsync.PlanOptions{Manager: lease.Source.ID, Revision: lease.Revision.ID,
		CommitSHA: lease.Revision.CommitSHA, Generation: lease.Source.Generation, Now: now, Overrides: observed.Overrides})
	if err != nil {
		t.Fatal(err)
	}
	if beforeFinish != nil {
		beforeFinish()
	}
	status := "converged"
	if plan.HasDrift() {
		status = "drifted"
	}
	if !plan.CanApply() {
		status = "blocked"
	}
	if beforeFinish != nil {
		status = "partial"
	}
	raw, _ := json.Marshal(plan)
	if err := store.FinishEnvironmentGitOps(t.Context(), lease, status, raw, []byte("[]"), "", now, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
}

func TestGitOpsNotificationsDeduplicateRecoverAndIgnoreStaleReports(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store := basic.(intentTestStore)
		source, desired := seed(t, basic)
		app, err := store.CreateApp(t.Context(), state.App{AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: "shop-api", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		set := func(value string) {
			t.Helper()
			if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, source.EnvironmentSlug, "MODE", value); err != nil {
				t.Fatal(err)
			}
		}
		set("production")
		source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("a", 40)))
		if err != nil {
			t.Fatal(err)
		}
		adoption, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil || store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, adoption.Hash) != nil {
			t.Fatal("could not adopt fixture")
		}
		hook, err := store.CreateAccountReleaseWebhookIfUnderQuota(t.Context(), state.AppWebhook{AccountID: source.AccountID, Scope: state.AppWebhookScopeAccount,
			TargetURL: "https://example.test/gitops", SecretSealed: []byte("sealed"), EventFilter: []string{"environment.gitops.changed"}, Enabled: true}, api.MustLimitsFor(api.PlanPro))
		if err != nil {
			t.Fatal(err)
		}
		other, err := store.CreateAccountReleaseWebhookIfUnderQuota(t.Context(), state.AppWebhook{AccountID: source.AccountID, Scope: state.AppWebhookScopeAccount,
			TargetURL: "https://example.test/releases", SecretSealed: []byte("sealed"), EventFilter: []string{"deployment.live"}, Enabled: true}, api.MustLimitsFor(api.PlanPro))
		if err != nil {
			t.Fatal(err)
		}
		check := func(expected int) []state.AppWebhookDelivery {
			t.Helper()
			rows, _, err := store.ListAccountReleaseWebhookDeliveries(t.Context(), source.AccountID, hook.ID, 50, "")
			if err != nil || len(rows) != expected {
				t.Fatalf("notifications=%d want=%d: %v", len(rows), expected, err)
			}
			wrong, _, err := store.ListAccountReleaseWebhookDeliveries(t.Context(), source.AccountID, other.ID, 50, "")
			if err != nil || len(wrong) != 0 {
				t.Fatalf("notification reached a non-subscriber: %v %v", wrong, err)
			}
			return rows
		}
		now := time.Now().UTC().Add(time.Second)
		finishNotificationReport(t, store, desired, now, nil)
		check(0)
		now = now.Add(2 * time.Second)
		finishNotificationReport(t, store, desired, now, func() { set("PRIVATE_DRIFT_VALUE") })
		check(0) // the completed report predates the console change
		now = now.Add(2 * time.Second)
		finishNotificationReport(t, store, desired, now, nil)
		rows := check(1)
		if strings.Contains(string(rows[0].Payload), "PRIVATE_DRIFT_VALUE") || !strings.Contains(string(rows[0].Payload), "variables/MODE") {
			t.Fatalf("alert lost field identity or disclosed values: %s", rows[0].Payload)
		}
		now = now.Add(2 * time.Second)
		finishNotificationReport(t, store, desired, now, nil)
		check(1)
		set("production")
		now = now.Add(2 * time.Second)
		finishNotificationReport(t, store, desired, now, nil)
		rows = check(2)
		found := false
		for _, row := range rows {
			var payload api.EnvironmentGitOpsChangedWebhookPayload
			if json.Unmarshal(row.Payload, &payload) != nil || row.AppID != "" || payload.SourceID != source.ID || payload.ApprovedRevision != source.ApprovedRevisionID || payload.CommitSHA != strings.Repeat("a", 40) {
				t.Fatalf("invalid scoped alert: %s", row.Payload)
			}
			found = found || (payload.PreviousStatus == "drifted" && payload.Status == "in_sync")
		}
		if !found {
			t.Fatal("recovery was not recorded")
		}
		poller := basic.(state.EnvironmentGitSourcePollStore)
		poll := func(result state.EnvironmentGitSourcePollResult) {
			t.Helper()
			now = now.Add(2 * time.Second)
			lease, err := poller.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), now, time.Minute)
			if err != nil || poller.FinishEnvironmentGitSourcePoll(t.Context(), lease, result, now, now.Add(time.Second)) != nil {
				t.Fatalf("source poll: %v", err)
			}
		}
		good := state.EnvironmentGitSourcePollResult{CommitSHA: strings.Repeat("a", 40), Digest: desired.Digest}
		poll(good)
		check(2)
		poll(state.EnvironmentGitSourcePollResult{ErrorCode: "environment_git_source_unavailable"})
		check(3)
		poll(state.EnvironmentGitSourcePollResult{ErrorCode: "environment_git_source_unavailable"})
		check(3)
		poll(good)
		check(4)
	})
}
