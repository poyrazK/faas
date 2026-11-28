package apidserver_test

import (
	"encoding/json"
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

// Interactive sessions (exec -it, cp, port-forward, postgres connect) need
// FAAS_INTERACTIVE_APP_TASKS on top of the app-task API; app-tasks alone used
// to advertise them while every session answered 501.
func TestInteractiveAppSessionsCapabilityFollowsItsGate(t *testing.T) {
	read := func(appTasks, interactive bool) (tasks, sessions api.CapabilityStatus) {
		t.Helper()
		recorder := apidserver.CapabilitiesWithAppTaskGates(appTasks, interactive)
		var response api.CapabilitiesResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("status %d: %v", recorder.Code, err)
		}
		for _, capability := range response.Capabilities {
			switch capability.Key {
			case "app-tasks":
				tasks = capability
			case "interactive-app-sessions":
				sessions = capability
			}
		}
		if tasks.Key == "" || sessions.Key == "" {
			t.Fatalf("app-tasks or interactive-app-sessions missing from %d capabilities", len(response.Capabilities))
		}
		return tasks, sessions
	}
	if tasks, sessions := read(false, false); tasks.Enabled || sessions.Enabled {
		t.Fatalf("app task API off: app-tasks=%v sessions=%v, want both off", tasks.Enabled, sessions.Enabled)
	}
	if _, sessions := read(false, true); sessions.Enabled {
		t.Fatal("interactive sessions advertised while the app-task API is off")
	}
	tasks, sessions := read(true, false)
	if sessions.Enabled {
		t.Fatal("interactive sessions advertised while FAAS_INTERACTIVE_APP_TASKS is off")
	}
	tasksOnly := tasks.Enabled
	tasks, sessions = read(true, true)
	if sessions.Enabled != tasks.Enabled || tasks.Enabled != tasksOnly {
		t.Fatalf("both gates on: app-tasks=%v sessions=%v (app-tasks alone %v); want sessions to follow app-tasks", tasks.Enabled, sessions.Enabled, tasksOnly)
	}
}
