package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
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

// Prod findings 2026-11-15 #25: app tasks, TCP/UDP listeners and the Data
// API were listed as available while their runtime was off.
func TestCapabilitiesFollowRuntimeGates(t *testing.T) {
	for _, tc := range []struct {
		appTasks, tcp, udp bool
	}{{}, {appTasks: true, tcp: true}, {udp: true}} {
		var response api.CapabilitiesResponse
		if err := json.Unmarshal(apidserver.CapabilitiesWithRuntimeGates(tc.appTasks, tc.tcp, tc.udp).Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		// No managed PostgreSQL bindings are configured, so the Data API
		// is never available here.
		want := map[string]bool{"app-tasks": tc.appTasks, "tcp-listeners": tc.tcp, "udp-listeners": tc.udp, "data-api": false}
		for _, capability := range response.Capabilities {
			on, ok := want[capability.Key]
			if !ok {
				continue
			}
			delete(want, capability.Key)
			if capability.Enabled != on {
				t.Errorf("%+v: %s enabled = %t, want %t", tc, capability.Key, capability.Enabled, on)
			}
			if !on && capability.UnavailableReason != api.CapabilityUnavailableRuntime {
				t.Errorf("%s reason = %q, want runtime", capability.Key, capability.UnavailableReason)
			}
			// Agent eval 2026-11-24: "tcp-listeners unavailable" was read
			// as "private TCP is off". The detail names public ports and
			// says private TCP between apps does not depend on them.
			if !on && (capability.Key == "tcp-listeners" || capability.Key == "udp-listeners") &&
				(!strings.Contains(capability.UnavailableDetail, "Public ") || !strings.Contains(capability.UnavailableDetail, "is not about")) {
				t.Errorf("%s detail = %q, want public ports named and private TCP ruled out", capability.Key, capability.UnavailableDetail)
			}
		}
		if len(want) != 0 {
			t.Fatalf("capabilities missing: %v", want)
		}
	}
}

// Prod hunt 2026-11-28: AI access tokens were listed as available and could
// be created, while their documented host answered "No such app".
func TestAIAccessTokensFollowThePublicHost(t *testing.T) {
	for _, on := range []bool{true, false} {
		var response api.CapabilitiesResponse
		if err := json.Unmarshal(apidserver.CapabilitiesWithAIAccessTokenHost(on, api.PlanScale).Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, capability := range response.Capabilities {
			if capability.Key != "ai-access-tokens" {
				continue
			}
			found = true
			// With the host on, the preview's platform flag (off on a bare
			// server) still decides; it is never blamed on the host.
			blamesHost := strings.Contains(capability.UnavailableDetail, "public host")
			if on && blamesHost {
				t.Errorf("host on: detail %q still blames the host", capability.UnavailableDetail)
			}
			if !on && (capability.Enabled || capability.UnavailableReason != api.CapabilityUnavailableRuntime || !blamesHost) {
				t.Errorf("host off: enabled %t reason %q detail %q", capability.Enabled, capability.UnavailableReason, capability.UnavailableDetail)
			}
		}
		if !found {
			t.Fatal("ai-access-tokens missing from the capability list")
		}
	}

	// A plan that excludes the gateway is told so, not that the host is off.
	var free api.CapabilitiesResponse
	if err := json.Unmarshal(apidserver.CapabilitiesWithAIAccessTokenHost(false, api.PlanFree).Body.Bytes(), &free); err != nil {
		t.Fatal(err)
	}
	for _, capability := range free.Capabilities {
		if capability.Key == "ai-access-tokens" && capability.UnavailableReason != api.CapabilityUnavailableRuntime &&
			strings.Contains(capability.UnavailableDetail, "public host") {
			t.Errorf("free plan: reason %q but detail %q blames the host", capability.UnavailableReason, capability.UnavailableDetail)
		}
	}

	// Creating a token is refused while the host is off: it would have
	// nowhere to be used.
	if recorder, reached := apidserver.CreateAIAccessTokenWithHost(false); reached || recorder.Code != http.StatusForbidden ||
		!strings.Contains(recorder.Body.String(), "public host") {
		t.Errorf("host off: create reached %t, status %d, body %s", reached, recorder.Code, recorder.Body.String())
	}
	if _, reached := apidserver.CreateAIAccessTokenWithHost(true); !reached {
		t.Error("host on: create did not reach the handler")
	}
}
