package mcpmanagement

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// App logs are served by the compute gateway behind the public edge, not by
// apid. The hosted bridge talks to apid's loopback listener, so get_logs
// answered "no such API route" for every hosted connection; it now streams
// logs from LogsAPIBase with the connection's credential.
func TestGetLogsUsesLogsAPIBase(t *testing.T) {
	apid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such API route"))
	}))
	defer apid.Close()
	var auth string
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/apps/alpha/logs" {
			http.NotFound(w, r)
			return
		}
		data, _ := json.Marshal(api.LogEvent{Seq: 1, Line: "served by the edge"})
		_, _ = w.Write([]byte("event: log\ndata: " + string(data) + "\n\n"))
	}))
	defer edge.Close()
	_, m, err := newManagementServer(api.NewClient(apid.URL, testToken), Options{APIBase: apid.URL, LogsAPIBase: edge.URL, Token: testToken})
	if err != nil {
		t.Fatal(err)
	}
	out, err := m.getLogs(t.Context(), logsArgs{App: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	snap := out.(logSnapshot)
	if len(snap.Items) != 1 || snap.Items[0].Line != "served by the edge" {
		t.Fatalf("snapshot = %+v", snap)
	}
	if auth != "Bearer "+testToken {
		t.Fatalf("log request carried %q, want the connection's credential", auth)
	}
}

func TestHostedBridgeDefaultsLogsToItsOrigin(t *testing.T) {
	svc, err := NewHTTPService(HTTPOptions{APIBase: "http://127.0.0.1:8081", ResourceURL: "https://api.example.test/mcp", RedirectURIs: []string{"http://127.0.0.1/callback"}, Hosted: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := svc.remote.opts.LogsAPIBase; got != "https://api.example.test" {
		t.Fatalf("LogsAPIBase = %q", got)
	}
	g := &oauthGrant{client: api.NewClient("http://127.0.0.1:8081", testToken)}
	if got := svc.remote.grantOptions(g, nil, nil).LogsAPIBase; got != "https://api.example.test" {
		t.Fatalf("grant LogsAPIBase = %q", got)
	}
}

// parkedLogsBackend answers the live stream as the gateway does for an app
// with no running instance, and serves the newest instance's archive.
func parkedLogsBackend(t *testing.T, instances []api.InstanceResponse, live string, availability api.AppDeploymentAvailability) (*httptest.Server, *string) {
	t.Helper()
	var archived string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps/alpha/instances":
			_ = json.NewEncoder(w).Encode(instances)
		case "/v1/apps/alpha":
			_ = json.NewEncoder(w).Encode(api.AppResponse{Slug: "alpha", DeploymentAvailability: availability})
		case "/v1/apps/alpha/logs":
			if r.URL.Query().Get("archive") != "1" {
				_, _ = w.Write([]byte(live))
				return
			}
			archived = r.URL.Query().Get("instance") + "/" + r.URL.Query().Get("date")
			for i, line := range []string{"booting", "ERROR payment failed", "INFO served /checkout"} {
				data, _ := json.Marshal(api.LogEvent{Seq: int64(i + 1), Line: line, WrittenAt: "2026-11-20T10:00:0" + string(rune('0'+i)) + "Z"})
				_, _ = w.Write([]byte("event: log\ndata: " + string(data) + "\n\n"))
			}
			_, _ = w.Write([]byte("event: end\ndata: {\"reason\":\"archive_complete\"}\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &archived
}

const noRunningInstance = "event: degraded\ndata: {\"code\":\"not_found\"}\n\n"

// A parked app's get_logs answered "Log backend is degraded; retry later",
// which no retry fixed. It now reads the newest instance's archive, applying
// the filters the live stream applies server-side.
func TestGetLogsReadsParkedAppArchive(t *testing.T) {
	instances := []api.InstanceResponse{
		{ID: "old", DeploymentID: "d1", StartedAt: "2026-11-19T09:00:00Z", ParkedAt: "2026-11-19T09:05:00Z"},
		{ID: "newest", DeploymentID: "d1", StartedAt: "2026-11-20T09:59:00Z", ParkedAt: "2026-11-20T10:05:00Z"},
	}
	srv, archived := parkedLogsBackend(t, instances, noRunningInstance, api.AppDeploymentAvailabilityLive)
	_, m, err := newManagementServer(api.NewClient(srv.URL, testToken), Options{APIBase: srv.URL, Token: testToken})
	if err != nil {
		t.Fatal(err)
	}
	out, err := m.getLogs(t.Context(), logsArgs{App: "alpha", Level: "error", Grep: "PAYMENT"})
	if err != nil {
		t.Fatal(err)
	}
	snap := out.(logSnapshot)
	if *archived != "newest/2026-11-20" || snap.Source != "archive" || snap.InstanceID != "newest" {
		t.Fatalf("archived %q, snapshot %+v", *archived, snap)
	}
	if len(snap.Items) != 1 || snap.Items[0].Line != "ERROR payment failed" {
		t.Fatalf("items = %+v, want the one error line matching the grep", snap.Items)
	}
	out, err = m.getLogs(t.Context(), logsArgs{App: "alpha", Since: "2026-11-20T10:00:01Z"})
	if err != nil || len(out.(logSnapshot).Items) != 2 {
		t.Fatalf("since filter: %+v %v", out, err)
	}
}

func TestGetLogsExplainsMissingParkedLogs(t *testing.T) {
	for _, tc := range []struct {
		name, live, code string
		availability     api.AppDeploymentAvailability
		instances        []api.InstanceResponse
	}{
		{"no instance in window", noRunningInstance, "app_logs_unavailable", api.AppDeploymentAvailabilityLive, nil},
		{"no live deployment", noRunningInstance, "no_live_deployment", api.AppDeploymentAvailabilityMissing, nil},
		{"scheduler down", "event: degraded\ndata: {}\n\n", "logs_unavailable", api.AppDeploymentAvailabilityLive, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := parkedLogsBackend(t, tc.instances, tc.live, tc.availability)
			_, m, err := newManagementServer(api.NewClient(srv.URL, testToken), Options{APIBase: srv.URL, Token: testToken})
			if err != nil {
				t.Fatal(err)
			}
			_, err = m.getLogs(t.Context(), logsArgs{App: "alpha"})
			var te *inputError
			if !errors.As(err, &te) || te.code != tc.code {
				t.Fatalf("error = %v, want code %s", err, tc.code)
			}
			if strings.Contains(err.Error(), "degraded") && tc.code != "logs_unavailable" {
				t.Fatalf("parked app still reported a degraded backend: %v", err)
			}
		})
	}
}

// gregale_context returned the full capability registry, 95 KB (about 49k
// tokens with the text copy) on production. It now lists keys, and a hosted
// connection reports the public API origin instead of apid's loopback.
func TestContextSummarizesCapabilities(t *testing.T) {
	var registry api.CapabilitiesResponse
	registry.Plan, registry.RegistryVersion, registry.HiddenCapabilities = "scale", 3, []string{"managed-redis"}
	for i := range 237 {
		registry.Capabilities = append(registry.Capabilities, api.CapabilityStatus{Key: fmt.Sprintf("cap-%03d", i), Name: strings.Repeat("n", 40), Description: strings.Repeat("d", 300), Enabled: i%10 != 0, Maturity: api.CapabilityMaturityGA})
	}
	registry.Capabilities[1].Maturity = api.CapabilityMaturityPreview
	sum := summarizeCapabilities(registry)
	raw, _ := json.Marshal(sum)
	if len(raw) > 8<<10 {
		t.Fatalf("summary is %d bytes", len(raw))
	}
	if len(sum.Enabled) != 213 || len(sum.Disabled) != 24 || len(sum.NotGA) != 1 || sum.NotGA[0] != "cap-001" || sum.Hidden[0] != "managed-redis" {
		t.Fatalf("summary = %+v", sum)
	}
	hosted := &management{opts: Options{APIBase: "http://127.0.0.1:8081", LogsAPIBase: "https://api.example.test"}}
	if hosted.publicAPIBase() != "https://api.example.test" {
		t.Fatalf("hosted api_base = %q", hosted.publicAPIBase())
	}
	local := &management{opts: Options{APIBase: "https://api.example.test"}}
	if local.publicAPIBase() != "https://api.example.test" {
		t.Fatalf("local api_base = %q", local.publicAPIBase())
	}
}

// Restarting a parked app was planned, then refused at apply with a recovery
// that said to plan the same restart again. It is now refused at planning
// with the reason: the next request boots the current configuration.
func TestPlanRestartRefusesAParkedApp(t *testing.T) {
	parked := []api.InstanceResponse{{ID: "old", State: "parked", StartedAt: "2026-11-20T09:00:00Z"}}
	srv, _ := parkedLogsBackend(t, parked, noRunningInstance, api.AppDeploymentAvailabilityLive)
	_, m, err := newManagementServer(api.NewClient(srv.URL, testToken), Options{APIBase: srv.URL, Token: testToken, AllowWrites: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.planRestart(t.Context(), operationArgs{App: "alpha", Reason: "reload config"})
	var te *inputError
	if !errors.As(err, &te) || te.code != "nothing_to_restart" {
		t.Fatalf("error = %v", err)
	}
	if anyInstanceRunning(parked) || !anyInstanceRunning([]api.InstanceResponse{{State: "RUNNING"}}) || !anyInstanceRunning([]api.InstanceResponse{{State: "waking"}}) {
		t.Fatal("anyInstanceRunning misreads instance states")
	}
}
