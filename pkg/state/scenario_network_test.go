// adr: 20261017201005
package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRehearsalNetworkPolicyRequiresFreshAppsAndSurvivesCleanup(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := state.NewEffectFixture(t, backend)
			expires := time.Now().Add(time.Hour)
			app, err := (*f.Base()).CreateApp(t.Context(), state.App{AccountID: (*f.Account()).ID, Slug: "network-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, PreviewOfSlug: "source", PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
			if err != nil {
				t.Fatal(err)
			}
			member := state.ScenarioTestMember{AppID: app.ID, Workload: "primary", WebhookMode: state.ScenarioWebhookCaptureMode, NetworkMode: state.ScenarioNetworkContained}
			bad := member
			bad.WebhookMode = ""
			if err := (*f.Base()).RegisterScenarioTestMembers(t.Context(), (*f.Account()).ID, state.ActionCaptureTestRun, []state.ScenarioTestMember{bad}); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("unbounded policy: %v", err)
			}
			if err := (*f.Base()).RegisterScenarioTestMembers(t.Context(), (*f.Account()).ID, state.ActionCaptureTestRun, []state.ScenarioTestMember{member}); err != nil {
				t.Fatal(err)
			}
			got, err := (*f.Base()).ScenarioTestMemberByApp(t.Context(), app.ID)
			if err != nil || got.NetworkMode != state.ScenarioNetworkContained {
				t.Fatalf("enrollment: %+v %v", got, err)
			}
			if _, err := (*f.Base()).SoftDeleteAppCascade(t.Context(), app.ID); err != nil {
				t.Fatal(err)
			}
			if err := (*f.Base()).DeleteScenarioTestMembers(t.Context(), (*f.Account()).ID, state.ActionCaptureTestRun); err != nil {
				t.Fatal(err)
			}
			run, err := (*f.Base()).ScenarioRehearsalNetworkRun(t.Context(), app.ID)
			if err != nil || run != state.ActionCaptureTestRun {
				t.Fatalf("cleanup removed containment: %q %v", run, err)
			}
			if run, err := (*f.Base()).ScenarioRehearsalNetworkRun(t.Context(), (*f.App()).ID); err != nil || run != "" {
				t.Fatalf("ordinary app policy: %q %v", run, err)
			}
			if (*f.Pool()) != nil {
				if _, err := (*f.Pool()).Exec(t.Context(), `UPDATE scenario_test_action_policies SET network_mode='' WHERE app_id=$1`, app.ID); err == nil {
					t.Fatal("retained policy could be downgraded")
				}
			}
		})
	}
}
