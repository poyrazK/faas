package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Compute cap enforcement suspends databases at their ceiling, and the cap
// notice email is the customer's only warning, so apid refuses enforcement
// while that email cannot be sent (bug hunt H88).
func TestManagedPostgresCapEnforcementNeedsTheNoticeEmail(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		ok   bool
	}{
		{"internal mail off", map[string]string{}, false},
		{"cap notices off", map[string]string{internalmail.EnableEnv: "1", internalmail.ProducerOutboxEnableEnv: "1"}, false},
		{"no producer outbox", map[string]string{internalmail.EnableEnv: "1", internalmail.ManagedPostgresCapOutboxEnableEnv: "1"}, false},
		{"notice path on", map[string]string{internalmail.EnableEnv: "1", internalmail.ProducerOutboxEnableEnv: "1",
			internalmail.ManagedPostgresCapOutboxEnableEnv: "1"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := apidserver.ManagedPostgresCapNoticeReady(func(k string) string { return tc.env[k] })
			if (err == nil) != tc.ok {
				t.Fatalf("ready = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

// Deleting databases past their plan needs the suspension email: it is the
// only notice of the deletion date (ADR-20261110184700).
func TestManagedPostgresPlanExcessDeleteNeedsTheSuspensionEmail(t *testing.T) {
	mailOn := map[string]string{internalmail.EnableEnv: "1", internalmail.ProducerOutboxEnableEnv: "1",
		internalmail.ManagedPostgresPlanExcessOutboxEnableEnv: "1"}
	cases := []struct {
		name string
		mode managedpostgres.CapEnforcementMode
		env  map[string]string
		ok   bool
	}{
		{"on without the email", managedpostgres.CapEnforcementOn, map[string]string{}, false},
		{"on without the producer outbox", managedpostgres.CapEnforcementOn,
			map[string]string{internalmail.EnableEnv: "1", internalmail.ManagedPostgresPlanExcessOutboxEnableEnv: "1"}, false},
		{"on with the email", managedpostgres.CapEnforcementOn, mailOn, true},
		{"shadow without the email", managedpostgres.CapEnforcementShadow, map[string]string{}, true},
		{"off", managedpostgres.CapEnforcementOff, map[string]string{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := apidserver.ManagedPostgresPlanExcessDeleteReady(tc.mode, func(k string) string { return tc.env[k] })
			if (err == nil) != tc.ok {
				t.Fatalf("ready = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}
