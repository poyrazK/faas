package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientRequestAuditAndDiscoveredRoutes(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/apps/acme/audit/requests":
			if r.URL.Query().Get("since") != now.Format(time.RFC3339Nano) || r.URL.Query().Get("limit") != "10" {
				t.Errorf("audit query=%s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"app_id":"app","records":[{"event_id":"event","route_template":"POST /payments","method":"POST","http_status":201,"latency_ms":381,"occurred_at":"2026-09-25T12:00:00Z"}],"since":"2026-09-25T12:00:00Z","until":"2026-09-25T13:00:00Z"}`))
		case "/v1/apps/acme/audit/routes":
			_, _ = w.Write([]byte(`{"app_id":"app","routes":["POST /payments"],"cap_hit":false,"source":"request_audit"}`))
		default:
			t.Errorf("unexpected SDK path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "token")
	records, err := c.GetAppsSlugAuditRequests(context.Background(), "acme", now, now.Add(time.Hour), 10)
	if err != nil || len(records.Records) != 1 || records.Records[0].HTTPStatus != 201 {
		t.Fatalf("audit records=%+v err=%v", records, err)
	}
	routes, err := c.GetAppsSlugAuditRoutes(context.Background(), "acme")
	if err != nil || len(routes.Routes) != 1 || routes.Routes[0] != "POST /payments" {
		t.Fatalf("audit routes=%+v err=%v", routes, err)
	}
}
