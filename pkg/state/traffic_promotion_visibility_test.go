// adr: 570
package state

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// A production promotion that makes an internal app public publishes its
// primary hostname, which has no legacy allowance; other settings changes
// and unchanged visibility stay outside the binding analysis.
func TestMemTrafficPromotionVisibility(t *testing.T) {
	m, account, _, app, _ := memTrafficFixture(t)
	m.trafficAppsSuffix = ".apps.example.test"
	internal := api.AppVisibilityInternal
	app, err := m.UpdateApp(t.Context(), app.ID, UpdateAppParams{SetVisibility: true, Visibility: &internal})
	if err != nil {
		t.Fatal(err)
	}
	legacy := publicationLegacyRule(account, app, app.Slug+".apps.example.test")
	m.edgeRules[legacy.ID] = legacy
	public := app
	public.Visibility = api.AppVisibilityPublic
	resized := app
	resized.RAMMB = 512
	for _, tc := range []struct {
		name    string
		changes []promotionWorkloadActivation
		refused bool
	}{
		{"public", []promotionWorkloadActivation{{appID: app.ID, app: public, projectApp: true}}, true},
		{"settings-only", []promotionWorkloadActivation{{appID: app.ID, app: resized, projectApp: true}}, false},
		{"non-production", []promotionWorkloadActivation{{appID: app.ID, app: public, projectApp: false}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := m.checkMemPromotionWorkloadBindingsLocked(t.Context(), account.ID, tc.changes)
			if tc.refused {
				requireRevisionPublicationRefusal(t, err, app.Slug+".apps.example.test")
			} else if err != nil {
				t.Fatalf("unchanged visibility analyzed: %v", err)
			}
		})
	}
	delete(m.edgeRules, legacy.ID)
	if err := m.checkMemPromotionWorkloadBindingsLocked(t.Context(), account.ID, []promotionWorkloadActivation{{appID: app.ID, app: public, projectApp: true}}); err != nil {
		t.Fatalf("visibility change after repair: %v", err)
	}
}
