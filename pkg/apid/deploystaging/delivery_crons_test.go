package deploystaging

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

func deliveryCronFixture(t *testing.T) (*state.MemStore, state.Account, state.App, *gregalemanifest.Manifest) {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "delivery-cron@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	skip := true
	manifest := &gregalemanifest.Manifest{Triggers: []gregalemanifest.Trigger{
		{App: app.Slug, Kind: gregalemanifest.TriggerKindCron, Schedule: "0 9 * * *", Path: "/report", Timezone: "Europe/Istanbul", SkipIfRunning: &skip},
	}}
	return store, acct, app, manifest
}

// A source-declared cron keeps its timezone and skip_if_running; every one
// used to be staged in UTC (crons audit 2026-11-01).
func TestSourceCronsKeepDeclaredTimezone(t *testing.T) {
	store, acct, app, manifest := deliveryCronFixture(t)
	_, intent, err := PrepareSourceCrons(t.Context(), store, acct, app, manifest)
	if err != nil || intent == nil || len(intent.Desired) != 1 {
		t.Fatalf("intent = %+v, err %v", intent, err)
	}
	if got := intent.Desired[0]; got.Timezone != "Europe/Istanbul" || !got.SkipIfRunning {
		t.Fatalf("staged cron = %+v, want Europe/Istanbul with skip_if_running", got)
	}
}

// A push's cron session comes from its delivery, so githubd retrying the
// delivery stages under the same session; another delivery gets its own.
// Withdrawing an intent frees the app for the next push's crons.
func TestDeliveryCronsSessionFollowsTheDelivery(t *testing.T) {
	store, acct, app, manifest := deliveryCronFixture(t)
	ctx, intent, err := PrepareDeliveryCrons(t.Context(), store, acct, app, manifest, "delivery-1")
	if err != nil || intent == nil {
		t.Fatalf("intent = %+v, err %v", intent, err)
	}
	session := state.DeploymentSubmissionAttempt(ctx)
	if session == "" || session != intent.SessionID || !api.ValidDeploymentAttemptID(session) {
		t.Fatalf("session = %q, intent session %q", session, intent.SessionID)
	}
	retried, _, err := PrepareDeliveryCrons(context.Background(), store, acct, app, manifest, "delivery-1")
	if err != nil || state.DeploymentSubmissionAttempt(retried) != session {
		t.Fatalf("retried session = %q (err %v), want %q", state.DeploymentSubmissionAttempt(retried), err, session)
	}
	other, _, err := PrepareDeliveryCrons(context.Background(), store, acct, app, manifest, "delivery-2")
	if err != nil || state.DeploymentSubmissionAttempt(other) == session {
		t.Fatalf("another delivery reused session %q (err %v)", session, err)
	}
	if err := CancelSourceCrons(ctx, store, acct.ID, app.ID, intent); err != nil {
		t.Fatal(err)
	}
	if _, next, err := PrepareDeliveryCrons(context.Background(), store, acct, app, manifest, "delivery-3"); err != nil || next == nil {
		t.Fatalf("after withdrawal the next push staged %+v, err %v", next, err)
	}
}
