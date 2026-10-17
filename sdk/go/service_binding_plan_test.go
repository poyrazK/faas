package faas

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServiceBindingPlanPrecondition(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusConflict} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			fingerprint := strings.Repeat("a", 64)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodPatch || r.URL.Path != "/v1/apps/demo" {
					t.Errorf("wrong request %s %s", r.Method, r.URL.Path)
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if string(body["expected_service_binding_fingerprint"]) != `"`+fingerprint+`"` || string(body["service_binding_targets"]) != "[]" {
					t.Errorf("body=%s", body)
				}
				w.WriteHeader(status)
				if status == http.StatusConflict {
					_, _ = w.Write([]byte(`{"title":"Service binding plan is stale","status":409,"code":"conflict"}`))
					return
				}
				_ = json.NewEncoder(w).Encode(AppResponse{ID: "app1", ServiceBindingFingerprint: strings.Repeat("b", 64)})
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			empty := []string{}
			app, err := client.UpdateApp(context.Background(), "demo", UpdateAppRequest{ExpectedServiceBindingFingerprint: &fingerprint, ServiceBindingTargets: &empty})
			if status == http.StatusConflict {
				if err == nil {
					t.Fatal("conflict was accepted")
				}
			} else if err != nil || app.ServiceBindingFingerprint != strings.Repeat("b", 64) {
				t.Fatalf("response=%+v err=%v", app, err)
			}
			if calls != 1 {
				t.Fatalf("requests=%d; must not retry unguarded", calls)
			}
		})
	}
}
