package conformance

import (
	"testing"

	"github.com/google/uuid"
)

// testScenarioRehearsalReadsForUnenrolledApp pins the not-enrolled half of
// the rehearsal containment reads (#4892): an app with no scenario action
// policy has no contained network run, and webhook delivery for it, or for a
// webhook that does not exist, is not blocked. Both stores must agree,
// because these reads gate every application boot and outbound delivery.
func testScenarioRehearsalReadsForUnenrolledApp(t *testing.T, fx *Fixture) {
	t.Helper()
	run, err := fx.Store.ScenarioRehearsalNetworkRun(fx.Ctx, fx.App.ID)
	if err != nil || run != "" {
		t.Fatalf("ScenarioRehearsalNetworkRun(unenrolled) = %q, %v; want empty run", run, err)
	}
	missingWebhook := uuid.NewString()
	for _, appID := range []string{fx.App.ID, ""} {
		blocked, err := fx.Store.ScenarioWebhookDeliveryBlocked(fx.Ctx, appID, missingWebhook)
		if err != nil || blocked {
			t.Fatalf("ScenarioWebhookDeliveryBlocked(%q, missing webhook) = %v, %v; want false", appID, blocked, err)
		}
	}
}
