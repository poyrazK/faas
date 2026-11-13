package main

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func obsReceiverCLIResponse(now time.Time) api.ObsNotificationHealth {
	known := func(value float64, window time.Duration) api.ObsNotificationSignal {
		return api.ObsNotificationSignal{State: "healthy", Value: &value, ObservedAt: &now, WindowSeconds: int(window.Seconds()), MaxAgeSeconds: int(api.ObsHealthFreshnessMaxAge.Seconds())}
	}
	expected := true
	group := api.ObsNotificationReceiver{Receiver: "faas-page", Integration: "slack", Expected: &expected, State: "healthy",
		Requests: known(0, api.ObsNotificationWindow), Failures: known(0, api.ObsNotificationWindow), LastSuccessObservation: known(float64(now.Add(-time.Minute).Unix()), 0)}
	return api.ObsNotificationHealth{GeneratedAt: now, State: "healthy", WindowSeconds: 600,
		AlertmanagerAPI: api.ObsNotificationAPIStatus{State: "healthy"}, Signals: map[string]api.ObsNotificationSignal{}, Integrations: []api.ObsNotificationIntegration{}, Limitations: []string{},
		ReceiverHealth: &api.ObsNotificationReceiverHealth{State: "healthy", Collector: known(1, 0), Inventory: known(1, 0), Groups: []api.ObsNotificationReceiver{group}, Limitations: operatorobs.NotificationReceiverLimitations()}}
}

func TestCmdObsNotificationReceiverEvidence(t *testing.T) {
	stdout, stderr := obsBundleTestStreams(t)
	now := time.Now().UTC().Truncate(time.Second)
	response := obsReceiverCLIResponse(now)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != operatorobs.NotificationHealthPath || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer token" {
			t.Error("receiver read changed the protected endpoint")
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "token")
	if code := cmdObsNotificationHealth(nil); code != 0 || !strings.Contains(stdout.String(), "receiver_health=healthy") || !strings.Contains(stdout.String(), "receiver=faas-page integration=slack expected=true state=healthy") || !strings.Contains(stdout.String(), "requests=0 requests_state=healthy failures=0") || !strings.Contains(stdout.String(), "success_time=collector_confirmation") {
		t.Fatalf("human receiver health: %d %s %s", code, stdout, stderr)
	}
	stdout.Reset()
	response.ReceiverHealth.State, response.ReceiverHealth.Reason = "partial", "incomplete_evidence"
	group := &response.ReceiverHealth.Groups[0]
	group.State, group.Reason = "partial", "incomplete_evidence"
	group.LastSuccessObservation.State, group.LastSuccessObservation.Reason, group.LastSuccessObservation.Value = "no_samples", "success_not_observed", nil
	if code := cmdObsNotificationHealth(nil); code != 0 || !strings.Contains(stdout.String(), "last_success_observed_at=unknown") || strings.Contains(stdout.String(), "1970-") {
		t.Fatalf("cold baseline: %d %s %s", code, stdout, stderr)
	}
	stdout.Reset()
	if code := cmdObsNotificationHealth([]string{"--json"}); code != 0 || !strings.Contains(stdout.String(), `"receiver_health"`) || !strings.Contains(stdout.String(), `"value": null`) || !strings.Contains(stdout.String(), `"expected": true`) {
		t.Fatalf("receiver JSON: %d %s %s", code, stdout, stderr)
	}
	stdout.Reset()
	failures := 1.5
	group.Failures.Value, group.State, group.Reason = &failures, "degraded", "requests_failing"
	response.ReceiverHealth.State, response.ReceiverHealth.Reason = "degraded", "receiver_fault"
	if code := cmdObsNotificationHealth(nil); code != 0 || !strings.Contains(stdout.String(), "notification_health=healthy") || !strings.Contains(stdout.String(), "receiver_health=degraded") || !strings.Contains(stdout.String(), "failures=1.5") {
		t.Fatalf("independent assessments: %d %s %s", code, stdout, stderr)
	}
	for _, mode := range []string{"human", "json"} {
		stdout.Reset()
		stderr.Reset()
		group.Receiver = "private-receiver-address"
		var args []string
		if mode == "json" {
			args = []string{"--json"}
		}
		if code := cmdObsNotificationHealth(args); code != 1 || stdout.Len() != 0 || strings.Contains(stderr.String(), "private") {
			t.Fatalf("unsafe metadata accepted in %s: %d %s %s", mode, code, stdout, stderr)
		}
	}
}

func TestObsNotificationReceiverContractRejectsInvalidEvidence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name   string
		mutate func(*api.ObsNotificationReceiverHealth)
	}{
		{"missing groups", func(out *api.ObsNotificationReceiverHealth) { out.Groups = nil }},
		{"unknown receiver", func(out *api.ObsNotificationReceiverHealth) { out.Groups[0].Receiver = "private-role" }},
		{"unknown integration", func(out *api.ObsNotificationReceiverHealth) { out.Groups[0].Integration = "private-type" }},
		{"provider text in reason", func(out *api.ObsNotificationReceiverHealth) { out.Groups[0].Reason = "private-provider-error" }},
		{"duplicate group", func(out *api.ObsNotificationReceiverHealth) { out.Groups = append(out.Groups, out.Groups[0]) }},
		{"missing expected flag", func(out *api.ObsNotificationReceiverHealth) { out.Groups[0].Expected = nil }},
		{"inventory count mismatch", func(out *api.ObsNotificationReceiverHealth) { *out.Inventory.Value = 2 }},
		{"stale usable counter", func(out *api.ObsNotificationReceiverHealth) {
			old := now.Add(-3 * time.Minute)
			out.Groups[0].Requests.ObservedAt = &old
		}},
		{"future success clock", func(out *api.ObsNotificationReceiverHealth) {
			*out.Groups[0].LastSuccessObservation.Value = float64(now.Add(time.Minute).Unix())
		}},
		{"epoch-zero receipt", func(out *api.ObsNotificationReceiverHealth) { *out.Groups[0].LastSuccessObservation.Value = 0 }},
		{"nonfinite counter", func(out *api.ObsNotificationReceiverHealth) { *out.Groups[0].Requests.Value = math.Inf(1) }},
		{"missing limitations", func(out *api.ObsNotificationReceiverHealth) { out.Limitations = nil }},
		{"failed requests labelled healthy", func(out *api.ObsNotificationReceiverHealth) { *out.Groups[0].Failures.Value = 1 }},
		{"healthy aggregate receiver assessment with unknown source", func(out *api.ObsNotificationReceiverHealth) {
			out.Collector.State, out.Collector.Reason, out.Collector.Value = "unavailable", "query_failed", nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := obsReceiverCLIResponse(now).ReceiverHealth
			tc.mutate(out)
			if validObsNotificationReceivers(*out, now) {
				t.Fatal("invalid receiver evidence accepted")
			}
		})
	}
}
