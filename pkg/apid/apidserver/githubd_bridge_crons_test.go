package apidserver_test

import (
	"testing"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// A GitHub push stages the crons its gregale.yaml declares for the app, as a
// source deploy of the same commit does; pushes ignored them (crons audit
// 2026-11-01). A PR preview, another environment and a release staged without
// traffic stage none.
func TestGitHubPushStagesDeclaredCrons(t *testing.T) {
	manifest := func(slug string) *gregalemanifest.Manifest {
		return &gregalemanifest.Manifest{Triggers: []gregalemanifest.Trigger{
			{App: slug, Kind: gregalemanifest.TriggerKindCron, Schedule: "0 3 * * *", Path: "/nightly", Timezone: "Europe/Istanbul"},
		}}
	}
	for _, tc := range []struct {
		name   string
		kind   state.DeploymentKind
		scope  string
		staged bool
		want   bool
	}{
		{"push to production", state.DeploymentKindGitHub, "", false, true},
		{"pull request preview", state.DeploymentKindPreview, "", false, false},
		{"push to another environment", state.DeploymentKindGitHub, "staging", false, false},
		{"release staged without traffic", state.DeploymentKindGitHub, "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			acct, err := store.CreateAccount(t.Context(), "push-crons@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "push-crons"})
			if err != nil {
				t.Fatal(err)
			}
			req := &githubdpb.EnqueueBuildRequest{AccountId: acct.ID, AppId: app.ID, DeliveryId: "delivery-1", DeploymentScope: tc.scope}
			ctx, intent, problem := apidserver.XPrepareDeliveryCrons(t.Context(), store, req, acct, app, manifest(app.Slug), tc.kind, tc.staged)
			if problem != nil {
				t.Fatalf("problem: %s", problem.Detail)
			}
			if got := intent != nil; got != tc.want {
				t.Fatalf("staged crons = %v, want %v", got, tc.want)
			}
			if !tc.want {
				return
			}
			if session := state.DeploymentSubmissionAttempt(ctx); session != intent.SessionID {
				t.Fatalf("deploy context session = %q, want the intent's %q", session, intent.SessionID)
			}
			if len(intent.Desired) != 1 || intent.Desired[0].Path != "/nightly" || intent.Desired[0].Timezone != "Europe/Istanbul" {
				t.Fatalf("staged crons = %+v", intent.Desired)
			}
		})
	}
}
