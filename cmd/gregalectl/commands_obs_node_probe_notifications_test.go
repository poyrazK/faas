package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func nodeNotificationCLIResponse() api.ObsNodeProbeNotifications {
	fleet := servingNotificationCLIResponse()
	expected, age := true, fleet.Probes[0].MaxResultAgeSeconds
	out := api.ObsNodeProbeNotifications{GeneratedAt: fleet.GeneratedAt, Scope: operatorobs.NodeProbeNotificationsScope, Node: "fsn-3", State: "available", MaxAgeSeconds: 120, Inventory: fleet.Inventory, Expected: &expected, MaxResultAgeSeconds: &age, Channels: fleet.ProbeNotifications.Channels}
	for i := range out.Channels {
		out.Channels[i].Node = out.Node
	}
	return out
}

func TestCmdObsNodeProbeNotificationsHumanAndJSON(t *testing.T) {
	stdout, stderr := obsBundleTestStreams(t)
	response := nodeNotificationCLIResponse()
	paths := obsNodeTestAPIDWithNotifications(t, nil, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer admin" || r.URL.RawQuery != "" {
			t.Error("node lookup changed authenticated scoped read")
		}
		_ = json.NewEncoder(w).Encode(response)
	})
	for _, args := range [][]string{{"fsn-3"}, {"fsn-3", "--json"}} {
		stdout.Reset()
		stderr.Reset()
		if code := cmdObsNode(args); code != 0 {
			t.Fatalf("node read failed: %d %s", code, stderr)
		}
		if !strings.Contains(stdout.String(), "last_submission_failed") || !strings.Contains(stdout.String(), "timeout") {
			t.Fatal("node support page omitted direct channel evidence")
		}
	}
	for _, path := range *paths {
		if path == operatorobs.ServingHealthPath {
			t.Fatal("node inspection tried to filter a fleet preview")
		}
	}
	var page obsNodePage
	if json.Unmarshal(stdout.Bytes(), &page) != nil || page.ProbeNotifications == nil || len(page.ProbeNotifications.Channels) != 2 {
		t.Fatal("JSON node page lost complete two-channel evidence")
	}
}

func TestCmdObsNodeProbeNotificationsBestEffortAndRedaction(t *testing.T) {
	stdout, stderr := obsBundleTestStreams(t)
	response := nodeNotificationCLIResponse()
	mode := "valid"
	var redirected atomic.Int64
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { redirected.Add(1); w.WriteHeader(200) }))
	t.Cleanup(sink.Close)
	obsNodeTestAPIDWithNotifications(t, nil, func(w http.ResponseWriter, r *http.Request) {
		switch mode {
		case "old":
			w.WriteHeader(http.StatusNotFound)
		case "failed":
			http.Error(w, "https://private-provider/token", 503)
		case "redirect":
			http.Redirect(w, r, sink.URL+"/private-receipt", http.StatusTemporaryRedirect)
		default:
			_ = json.NewEncoder(w).Encode(response)
		}
	})
	for _, tc := range []struct {
		mode, reason string
		mutate       func(*api.ObsNodeProbeNotifications)
	}{
		{"old", "server_does_not_report_probe_notifications", func(*api.ObsNodeProbeNotifications) {}},
		{"failed", "probe_notifications_unavailable", func(*api.ObsNodeProbeNotifications) {}},
		{"redirect", "probe_notifications_unavailable", func(*api.ObsNodeProbeNotifications) {}},
		{"invalid", "invalid_probe_notifications", func(out *api.ObsNodeProbeNotifications) {
			out.Channels[0].FailureCode = "https://private-provider/token"
		}},
		{"wrong node", "invalid_probe_notifications", func(out *api.ObsNodeProbeNotifications) {
			out.Node = "fsn-99"
			for i := range out.Channels {
				out.Channels[i].Node = out.Node
			}
		}},
		{"stale", "invalid_probe_notifications", func(out *api.ObsNodeProbeNotifications) {
			at := out.GeneratedAt.Add(-121 * time.Second)
			out.Channels[0].EvaluatedAt = &at
		}},
		{"missing channel", "invalid_probe_notifications", func(out *api.ObsNodeProbeNotifications) { out.Channels = out.Channels[:1] }},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			mode, response = tc.mode, nodeNotificationCLIResponse()
			tc.mutate(&response)
			for _, args := range [][]string{{"fsn-3"}, {"--json", "fsn-3"}} {
				stdout.Reset()
				stderr.Reset()
				if code := cmdObsNode(args); code != 0 || !strings.Contains(stdout.String(), tc.reason) || strings.Contains(stdout.String()+stderr.String(), "private-provider") {
					t.Fatalf("best-effort lookup leaked or blocked node detail: %d %s %s", code, stdout, stderr)
				}
			}
		})
	}
	if redirected.Load() != 0 {
		t.Fatal("scoped notification read followed a redirect")
	}
}

func TestObsNodeProbeNotificationTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	t.Cleanup(srv.Close)
	client := &http.Client{Timeout: 40 * time.Millisecond}
	out, reason := fetchObsNodeProbeNotifications(context.Background(), client, srv.URL+"/", "fsn-3", obsCredential{})
	if out != nil || reason != "probe_notifications_unavailable" || client.CheckRedirect != nil {
		t.Fatal("lookup timeout became known evidence or mutated other node reads")
	}
}

func TestObsNodeProbeNotificationValidation(t *testing.T) {
	for _, mutate := range []func(*api.ObsNodeProbeNotifications){
		func(out *api.ObsNodeProbeNotifications) { out.Scope = "private-scope" },
		func(out *api.ObsNodeProbeNotifications) { out.Expected = nil },
		func(out *api.ObsNodeProbeNotifications) { out.MaxResultAgeSeconds = nil },
		func(out *api.ObsNodeProbeNotifications) { out.Channels[1] = out.Channels[0] },
		func(out *api.ObsNodeProbeNotifications) { out.Channels[0].Channel = "https://private/token" },
		func(out *api.ObsNodeProbeNotifications) { out.Channels[0].LastAttemptAt = nil },
		func(out *api.ObsNodeProbeNotifications) { out.Channels[0].Reason = "private-provider-text" },
		func(out *api.ObsNodeProbeNotifications) { out.Inventory.ObservedAt = nil },
	} {
		out := nodeNotificationCLIResponse()
		if !operatorobs.NodeProbeNotificationsValid(out) {
			t.Fatal("valid scoped response rejected")
		}
		mutate(&out)
		if operatorobs.NodeProbeNotificationsValid(out) {
			t.Fatal("invalid scoped response accepted")
		}
	}
}
