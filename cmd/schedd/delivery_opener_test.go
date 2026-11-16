package main

import (
	"strings"
	"testing"
)

// adr: 20261110210000 slice 3g — FAAS_SCHEDD_DELIVERY_OPENER=secretd needs
// secretd and refuses the features that would still open delivery values on
// the node.
func TestDeliveryOpenerFromEnv(t *testing.T) {
	for _, tc := range []struct {
		name      string
		env       map[string]string
		secretd   bool
		execution bool
		want      bool
		err       string
	}{
		{name: "unset", secretd: true},
		{name: "local", env: map[string]string{"FAAS_SCHEDD_DELIVERY_OPENER": "local"}, secretd: true},
		{name: "secretd", env: map[string]string{"FAAS_SCHEDD_DELIVERY_OPENER": "secretd"}, secretd: true, want: true},
		{name: "unknown", env: map[string]string{"FAAS_SCHEDD_DELIVERY_OPENER": "maybe"}, secretd: true, err: "want local or secretd"},
		{name: "no secretd", env: map[string]string{"FAAS_SCHEDD_DELIVERY_OPENER": "secretd"}, err: "FAAS_SCHEDD_SECRETD_TARGET"},
		{name: "execution", env: map[string]string{"FAAS_SCHEDD_DELIVERY_OPENER": "secretd"}, secretd: true, execution: true, err: "execution dispatch"},
		{name: "commit relay", env: map[string]string{"FAAS_SCHEDD_DELIVERY_OPENER": "secretd", "FAAS_COMMIT_RELAY_ENABLED": "true"}, secretd: true, err: "commit relay"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := deliveryOpenerFromEnv(func(k string) string { return tc.env[k] }, tc.secretd, tc.execution)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	if triggerConfigOpenerFor(secretdGrants{}, false) != nil {
		t.Fatal("a local schedd got a remote trigger opener")
	}
	if triggerConfigOpenerFor(secretdGrants{}, true) == nil {
		t.Fatal("a secretd schedd got no remote trigger opener")
	}
}
