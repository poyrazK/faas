package githubd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func hookDeliveryServer(t *testing.T, status int, deliveries *[]HookDelivery) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/hook/deliveries" || r.URL.Query().Get("per_page") != "100" {
			t.Errorf("request = %s, want /app/hook/deliveries?per_page=100", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer app-jwt" {
			t.Errorf("Authorization = %q, want the App JWT", r.Header.Get("Authorization"))
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(*deliveries)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The prod outage this guards against: the App's webhook URL pointed at the
// marketing site (404), then the secret drifted (401). githubd saw nothing;
// only GitHub's delivery log did.
func TestHookDeliveryMonitor_ReportsFailuresGitHubRecorded(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	deliveries := []HookDelivery{
		{ID: 30, GUID: "guid-30", DeliveredAt: now.Add(-5 * time.Minute), StatusCode: 401, Status: "Invalid HTTP Response: 401", Event: "pull_request", Action: "opened", InstallationID: 7},
		{ID: 29, GUID: "guid-29", DeliveredAt: now.Add(-10 * time.Minute), StatusCode: 202, Status: "OK", Event: "push"},
		{ID: 28, GUID: "guid-28", DeliveredAt: now.Add(-20 * time.Minute), StatusCode: 0, Status: "timed out", Event: "push"},
		{ID: 27, GUID: "guid-27", DeliveredAt: now.Add(-3 * time.Hour), StatusCode: 404, Status: "Invalid HTTP Response: 404", Event: "push"},
	}
	srv := hookDeliveryServer(t, http.StatusOK, &deliveries)
	var logs bytes.Buffer
	metrics := NewHookDeliveryMetrics()
	m := &HookDeliveryMonitor{
		MintJWT: func() (string, error) { return "app-jwt", nil },
		Client:  srv.Client(),
		BaseURL: srv.URL,
		Metrics: metrics,
		Log:     slog.New(slog.NewJSONHandler(&logs, nil)),
		Now:     func() time.Time { return now },
	}

	summary, err := m.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	want := HookDeliverySummary{RecentDelivered: 1, RecentFailed: 2, HasLatest: true, LatestFailed: true, LatestStatusCode: 401}
	if summary != want {
		t.Fatalf("summary = %+v, want %+v", summary, want)
	}
	if got := testutil.CollectAndCount(metrics, "githubd_github_hook_latest_delivery_failed"); got != 1 {
		t.Fatalf("latest_delivery_failed series = %d, want 1", got)
	}
	expected := `
# HELP githubd_github_hook_deliveries_recent App webhook deliveries GitHub recorded in the last hour, by outcome.
# TYPE githubd_github_hook_deliveries_recent gauge
githubd_github_hook_deliveries_recent{outcome="delivered"} 1
githubd_github_hook_deliveries_recent{outcome="failed"} 2
# HELP githubd_github_hook_latest_delivery_failed 1 when GitHub's newest App webhook delivery got no 2xx response.
# TYPE githubd_github_hook_latest_delivery_failed gauge
githubd_github_hook_latest_delivery_failed 1
# HELP githubd_github_hook_latest_delivery_status_code HTTP status GitHub recorded for its newest App webhook delivery (0 = timeout or no connection).
# TYPE githubd_github_hook_latest_delivery_status_code gauge
githubd_github_hook_latest_delivery_status_code 401
`
	if err := testutil.CollectAndCompare(metrics, strings.NewReader(expected),
		"githubd_github_hook_deliveries_recent", "githubd_github_hook_latest_delivery_failed",
		"githubd_github_hook_latest_delivery_status_code"); err != nil {
		t.Fatal(err)
	}

	// The first poll logs failures inside the window only (not guid-27), and
	// a later poll logs only deliveries it has not seen.
	if !strings.Contains(logs.String(), "guid-30") || !strings.Contains(logs.String(), "guid-28") || strings.Contains(logs.String(), "guid-27") {
		t.Fatalf("first poll logs = %s", logs.String())
	}
	logs.Reset()
	deliveries = append([]HookDelivery{
		{ID: 31, GUID: "guid-31", DeliveredAt: now.Add(-time.Minute), StatusCode: 202, Status: "OK", Event: "pull_request", Redelivery: true},
	}, deliveries...)
	summary, err = m.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("second PollOnce: %v", err)
	}
	if summary.LatestFailed || summary.LatestStatusCode != 202 {
		t.Fatalf("after a successful redelivery summary = %+v, want latest ok", summary)
	}
	if logs.Len() != 0 {
		t.Fatalf("second poll relogged deliveries: %s", logs.String())
	}
}

func TestHookDeliveryMonitor_PollFailureKeepsDeliveryGaugesAbsent(t *testing.T) {
	deliveries := []HookDelivery{}
	srv := hookDeliveryServer(t, http.StatusUnauthorized, &deliveries)
	metrics := NewHookDeliveryMetrics()
	m := &HookDeliveryMonitor{
		MintJWT: func() (string, error) { return "app-jwt", nil },
		Client:  srv.Client(),
		BaseURL: srv.URL,
		Metrics: metrics,
	}
	if _, err := m.PollOnce(context.Background()); !errors.Is(err, errHookDeliveriesUnavailable) {
		t.Fatalf("err = %v, want unavailable", err)
	}
	polls := `
# HELP githubd_github_hook_delivery_polls_total Polls of GitHub's App webhook delivery log, by result.
# TYPE githubd_github_hook_delivery_polls_total counter
githubd_github_hook_delivery_polls_total{result="error"} 1
githubd_github_hook_delivery_polls_total{result="ok"} 0
`
	if err := testutil.CollectAndCompare(metrics, strings.NewReader(polls), "githubd_github_hook_delivery_polls_total"); err != nil {
		t.Fatal(err)
	}
	if got := testutil.CollectAndCount(metrics, "githubd_github_hook_latest_delivery_failed"); got != 0 {
		t.Fatalf("latest_delivery_failed series = %d before any successful poll, want 0", got)
	}

	m.MintJWT = func() (string, error) { return "", errors.New("no key") }
	if _, err := m.PollOnce(context.Background()); !errors.Is(err, errHookDeliveriesUnavailable) {
		t.Fatalf("mint failure err = %v, want unavailable", err)
	}
}
