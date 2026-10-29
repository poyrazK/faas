// adr: 20261023115741 — the template catalog offers only starters an account
// can deploy: event-worker declares event_triggers, which the events preview
// refuses with capability_not_released until the account is enrolled.
package apidserver_test

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

func TestTemplateCatalogFollowsCapabilities(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	srv := *e.S()
	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	names := srv.TemplateNames(*e.Acct())
	if slices.Contains(names, "event-worker") {
		t.Fatalf("templates with events off = %v, want no event-worker", names)
	}
	if !slices.Contains(names, "cron-worker") {
		t.Fatalf("templates = %v, want the released cron-worker", names)
	}
	// event_triggers belong to event-subscriptions, a child of events.
	for _, capability := range []string{"events", "event-subscriptions"} {
		srv.SeedPlatformFlag(capabilityFlag(t, capability), platformflags.Definition{Enabled: true, Default: true})
	}
	if names := srv.TemplateNames(*e.Acct()); !slices.Contains(names, "event-worker") {
		t.Fatalf("templates with events on = %v, want event-worker", names)
	}
}
