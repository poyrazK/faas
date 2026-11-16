package apidserver_test

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// Hunt 34 H34-16/16b: `feature list` showed crash snapshots, production forks
// and on-demand profiling as available on an installation that refused every
// request for them. Each follows its installation gate.
func TestCapabilitiesFollowInstallationGates(t *testing.T) {
	for _, on := range []bool{true, false} {
		recorder := apidserver.CapabilitiesWithInstallationGates(on, on, on)
		var response api.CapabilitiesResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("status %d: %v", recorder.Code, err)
		}
		for _, key := range []string{"crash-snapshots", "production-forks", "profile-captures"} {
			found := false
			for _, capability := range response.Capabilities {
				if capability.Key != key {
					continue
				}
				found = true
				if capability.Enabled != on {
					t.Errorf("gates %t: %s enabled = %t", on, key, capability.Enabled)
				}
				if !on && capability.UnavailableReason != api.CapabilityUnavailableRuntime {
					t.Errorf("%s reason = %q, want runtime", key, capability.UnavailableReason)
				}
			}
			if !found {
				t.Fatalf("capability %q missing", key)
			}
		}
	}
}
