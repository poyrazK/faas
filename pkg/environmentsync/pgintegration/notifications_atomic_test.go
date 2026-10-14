// adr: 1155 — candidate assessment and customer status feedback contracts.
package pgintegration_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGitOpsNotificationTransactionAndRestart(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	source, desired, _ := intentFixture(t, store, "report")
	hook, err := store.CreateAccountReleaseWebhookIfUnderQuota(t.Context(), state.AppWebhook{AccountID: source.AccountID, Scope: state.AppWebhookScopeAccount,
		TargetURL: "https://example.test/gitops-atomic", SecretSealed: []byte("sealed"), EventFilter: []string{"environment.gitops.changed"}, Enabled: true}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Second)
	lease, err := store.ClaimEnvironmentGitOps(t.Context(), uuid.NewString(), now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := store.ObserveEnvironmentGitOps(t.Context(), lease, desired)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := environmentsync.BuildPlan(desired, observed.State, observed.Owners, environmentsync.PlanOptions{Manager: source.ID, Revision: lease.Revision.ID, CommitSHA: lease.Revision.CommitSHA, Generation: source.Generation, Now: now})
	if err != nil || plan.CanApply() {
		t.Fatalf("fixture must have adoption blockers: %+v %v", plan, err)
	}
	raw, _ := json.Marshal(plan)
	if _, err := pool.Exec(t.Context(), "ALTER TABLE app_webhook_deliveries ADD CONSTRAINT reject_gitops_test CHECK (event <> 'environment.gitops.changed')"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishEnvironmentGitOps(t.Context(), lease, "blocked", raw, []byte("[]"), "", now, now.Add(time.Second)); err == nil {
		t.Fatal("notification insert failure did not roll back report")
	}
	var baseline string
	if err := pool.QueryRow(t.Context(), "SELECT notification_state::text FROM environment_git_sources WHERE id=$1", source.ID).Scan(&baseline); err != nil || baseline != "{}" {
		t.Fatalf("failed delivery consumed baseline: %s %v", baseline, err)
	}
	runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 10)
	if err != nil || len(runs) != 1 || runs[0].CompletedAt != nil {
		t.Fatalf("failed delivery committed report: %+v %v", runs, err)
	}
	if _, err := pool.Exec(t.Context(), "ALTER TABLE app_webhook_deliveries DROP CONSTRAINT reject_gitops_test"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishEnvironmentGitOps(t.Context(), lease, "blocked", raw, []byte("[]"), "", now, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	restarted := state.NewPgStore(pool)
	finishNotificationReport(t, restarted, desired, now.Add(2*time.Second), nil)
	rows, _, err := restarted.ListAccountReleaseWebhookDeliveries(t.Context(), source.AccountID, hook.ID, 10, "")
	if err != nil || len(rows) != 1 || !strings.Contains(string(rows[0].Payload), "blocked") {
		t.Fatalf("restart duplicated or lost delivery: %+v %v", rows, err)
	}
	claimed, err := restarted.ClaimDueAppWebhookDeliveries(t.Context(), 10, now.Add(3*time.Second))
	if err != nil || len(claimed) != 1 || claimed[0].AppID != "" {
		t.Fatalf("account-only delivery was not claimable: %+v %v", claimed, err)
	}
}
