package operatorobs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func notificationTestAlert(now time.Time) alertmanagerAlert {
	row := alertmanagerAlert{Labels: map[string]string{"alertname": "FaasApiDown", "severity": "page", "component": "apid"}, StartsAt: now.Add(-30 * time.Second), UpdatedAt: now, EndsAt: now.Add(time.Minute)}
	row.Status.State = "active"
	row.Status.SilencedBy = []string{}
	row.Status.InhibitedBy = []string{}
	return row
}

func TestAlertmanagerClientReadBoundary(t *testing.T) {
	now := time.Now().UTC()
	for _, base := range []string{"https://remote.example", "http://localhost:9094", "http://127.0.0.1:9094/path", "http://user:secret@127.0.0.1", "http://127.0.0.1?secret=value", "http://127.0.0.1#fragment", "file:///etc/secret", "://"} {
		_, status := NewAlertmanagerClient(base, nil).alerts(t.Context(), now)
		if status.State != "unavailable" || status.Reason != "invalid_configuration" {
			t.Fatalf("configuration %s: %+v", base, status)
		}
	}
	for _, client := range []*AlertmanagerClient{nil, NewAlertmanagerClient("", nil), NewAlertmanagerClient("disabled", nil)} {
		_, status := client.alerts(t.Context(), now)
		if status.State != "disabled" {
			t.Fatalf("disabled: %+v", status)
		}
	}
	mode := "ok"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v2/alerts" || r.URL.Query().Get("silenced") != "true" || r.URL.Query().Get("inhibited") != "true" {
			t.Errorf("request: %s %s", r.Method, r.URL)
		}
		switch mode {
		case "error":
			http.Error(w, "private provider payload", 503)
		case "redirect":
			http.Redirect(w, r, "/configuration", 302)
		default:
			_ = json.NewEncoder(w).Encode([]alertmanagerAlert{notificationTestAlert(now)})
		}
	}))
	defer srv.Close()
	client := NewAlertmanagerClient(srv.URL, nil)
	rows, status := client.alerts(t.Context(), now)
	if len(rows) != 1 || status.State != "healthy" || status.ObservedAt == nil {
		t.Fatalf("read: %v %+v", rows, status)
	}
	for _, modeValue := range []string{"error", "redirect"} {
		mode = modeValue
		_, status = client.alerts(t.Context(), now)
		if status.State != "unavailable" || status.Reason != "http_error" {
			t.Fatalf("failure: %+v", status)
		}
	}
}

func TestAlertmanagerClientRejectsIncompleteAndUnboundedSnapshots(t *testing.T) {
	for _, body := range []string{"null", "{}", "[] {}", "[{}]", strings.Repeat(" ", api.ObsAlertmanagerResponseMaxBytes+1), "[" + strings.Repeat("null,", api.ObsAlertmanagerAlertsMax) + "null]"} {
		if _, err := decodeAlertmanagerAlerts(strings.NewReader(body)); err == nil {
			t.Fatal("invalid snapshot accepted")
		}
	}
	if rows, err := decodeAlertmanagerAlerts(strings.NewReader("[]")); err != nil || rows == nil {
		t.Fatalf("empty snapshot: %v %v", rows, err)
	}
	row := notificationTestAlert(time.Now())
	row.Status.State = "future_state"
	body, _ := json.Marshal([]alertmanagerAlert{row})
	if _, err := decodeAlertmanagerAlerts(strings.NewReader(string(body))); err == nil {
		t.Fatal("unknown state accepted")
	}
}
