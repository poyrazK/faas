package pgintegration_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReviewStaleUnplannedFailureMustNotNotifyCurrentIntent(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store := basic.(intentTestStore)
		source, _, app := intentFixture(t, store, "report")
		adoption, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil || !adoption.CanApply() {
			t.Fatalf("adopt: %v %+v", err, adoption)
		}
		if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, adoption.Hash); err != nil {
			t.Fatal(err)
		}
		hook, err := store.CreateAccountReleaseWebhookIfUnderQuota(t.Context(), state.AppWebhook{AccountID: source.AccountID,
			Scope: state.AppWebhookScopeAccount, TargetURL: "https://example.test/gitops", SecretSealed: []byte("sealed"),
			EventFilter: []string{"environment.gitops.changed"}, Enabled: true}, api.MustLimitsFor(api.PlanPro))
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		lease, err := store.ClaimEnvironmentGitOps(t.Context(), uuid.NewString(), now, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, source.EnvironmentSlug, "MODE", "newer-console-edit"); err != nil {
			t.Fatal(err)
		}
		current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
		if err != nil || current.IntentVersion == lease.Source.IntentVersion {
			t.Fatalf("intent did not advance: %v %+v", err, current)
		}
		if err := store.FinishEnvironmentGitOps(t.Context(), lease, "failed", json.RawMessage(`{}`), json.RawMessage(`[]`),
			"environment_observation_failed", now, now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		rows, _, err := store.ListAccountReleaseWebhookDeliveries(t.Context(), source.AccountID, hook.ID, 50, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			t.Logf("claimed intent=%d current intent=%d notification=%s", lease.Source.IntentVersion, current.IntentVersion, row.Payload)
		}
		if len(rows) != 0 {
			t.Fatalf("stale pre-plan failure emitted %d current notifications", len(rows))
		}
		fresh, err := store.ClaimEnvironmentGitOps(t.Context(), uuid.NewString(), time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		at := time.Now().UTC()
		if err := store.FinishEnvironmentGitOps(t.Context(), fresh, "failed", json.RawMessage(`{}`), json.RawMessage(`[]`), "environment_observation_failed", at, at.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		rows, _, err = store.ListAccountReleaseWebhookDeliveries(t.Context(), source.AccountID, hook.ID, 50, "")
		if err != nil || len(rows) != 1 {
			t.Fatalf("current observation failure should notify: %+v %v", rows, err)
		}
		var payload api.EnvironmentGitOpsChangedWebhookPayload
		if err := json.Unmarshal(rows[0].Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.PreviousStatus != "unknown" || payload.Status != "unavailable" || payload.ObservedVersion != current.IntentVersion {
			t.Fatalf("stale failure changed the baseline or current identity: %+v", payload)
		}
	})
}
