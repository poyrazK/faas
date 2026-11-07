package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/internalmail"
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
