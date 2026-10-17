// adr: 1349
package state

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRehearsalNetworkPolicyRequiresFreshAppsAndSurvivesCleanup(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newEffectFixture(t, backend)
			expires := time.Now().Add(time.Hour)
			app, err := f.base.CreateApp(t.Context(), App{AccountID: f.account.ID, Slug: "network-" + uuid.NewString()[:8], Type: AppTypeApp, RAMMB: 128, PreviewOfSlug: "source", PreviewPrState: PreviewPrStateOpen, PreviewExpiresAt: &expires})
			if err != nil {
				t.Fatal(err)
			}
			member := ScenarioTestMember{AppID: app.ID, Workload: "primary", WebhookMode: ScenarioWebhookCaptureMode, NetworkMode: ScenarioNetworkContained}
			bad := member
			bad.WebhookMode = ""
			if err := f.base.RegisterScenarioTestMembers(t.Context(), f.account.ID, actionCaptureTestRun, []ScenarioTestMember{bad}); !errors.Is(err, ErrConflict) {
				t.Fatalf("unbounded policy: %v", err)
			}
			if err := f.base.RegisterScenarioTestMembers(t.Context(), f.account.ID, actionCaptureTestRun, []ScenarioTestMember{member}); err != nil {
				t.Fatal(err)
			}
			got, err := f.base.ScenarioTestMemberByApp(t.Context(), app.ID)
			if err != nil || got.NetworkMode != ScenarioNetworkContained {
				t.Fatalf("enrollment: %+v %v", got, err)
			}
			if _, err := f.base.SoftDeleteAppCascade(t.Context(), app.ID); err != nil {
				t.Fatal(err)
			}
			if err := f.base.DeleteScenarioTestMembers(t.Context(), f.account.ID, actionCaptureTestRun); err != nil {
				t.Fatal(err)
			}
			run, err := f.base.ScenarioRehearsalNetworkRun(t.Context(), app.ID)
			if err != nil || run != actionCaptureTestRun {
				t.Fatalf("cleanup removed containment: %q %v", run, err)
			}
			if run, err := f.base.ScenarioRehearsalNetworkRun(t.Context(), f.app.ID); err != nil || run != "" {
				t.Fatalf("ordinary app policy: %q %v", run, err)
			}
			if f.pool != nil {
				if _, err := f.pool.Exec(t.Context(), `UPDATE scenario_test_action_policies SET network_mode='' WHERE app_id=$1`, app.ID); err == nil {
					t.Fatal("retained policy could be downgraded")
				}
			}
		})
	}
}
