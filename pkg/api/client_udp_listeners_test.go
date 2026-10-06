package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUDPListenerClientHTTPContract(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != method || r.Header.Get("Authorization") != "Bearer fp_test" {
					t.Errorf("request=%s auth=%q", r.Method, r.Header.Get("Authorization"))
				}
				path := "/v1/apps/app%2Fname/udp-listeners"
				if method == http.MethodPatch || method == http.MethodDelete {
					path += "/dns%2Fname"
				}
				if r.URL.EscapedPath() != path {
					t.Errorf("path=%s want=%s", r.URL.EscapedPath(), path)
				}
				if method == http.MethodPost || method == http.MethodPatch {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if method == http.MethodPost && (body["name"] != "dns" || body["guest_port"] != float64(5353)) {
						t.Errorf("create=%v", body)
					}
					if method == http.MethodPatch {
						if enabled, ok := body["enabled"].(bool); !ok || enabled {
							t.Errorf("disable=%v", body)
						}
					}
				}
				w.Header().Set("Content-Type", "application/json")
				if method == http.MethodDelete {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				value := `{"id":"listener","name":"dns","guest_port":5353,"public_port":40100,"protocol":"udp","enabled":false}`
				if method == http.MethodGet {
					value = "[" + value + "]"
				}
				_, _ = w.Write([]byte(value))
			}))
			defer srv.Close()
			client := NewClient(srv.URL, "fp_test")
			ctx := context.Background()
			var err error
			switch method {
			case http.MethodGet:
				var rows []UDPListenerResponse
				rows, err = client.ListAppUDPListeners(ctx, "app/name")
				if err == nil && (len(rows) != 1 || rows[0].ID != "listener") {
					t.Fatalf("rows=%+v", rows)
				}
			case http.MethodPost:
				var row UDPListenerResponse
				row, err = client.CreateAppUDPListener(ctx, "app/name", CreateUDPListenerRequest{Name: "dns", GuestPort: 5353})
				if err == nil && row.PublicPort != 40100 {
					t.Fatalf("row=%+v", row)
				}
			case http.MethodPatch:
				disabled := false
				var row UDPListenerResponse
				row, err = client.UpdateAppUDPListener(ctx, "app/name", "dns/name", UpdateUDPListenerRequest{Enabled: &disabled})
				if err == nil && row.Enabled {
					t.Fatal("disable response lost")
				}
			case http.MethodDelete:
				err = client.DeleteAppUDPListener(ctx, "app/name", "dns/name")
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUDPListenerClientPreservesQuotaProblem(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { WriteProblem(w, ErrUDPListenerLimit(16, 17)) }))
	defer srv.Close()
	_, err := NewClient(srv.URL, "fp_test").CreateAppUDPListener(context.Background(), "app", CreateUDPListenerRequest{Name: "dns", GuestPort: 5353})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Problem.Status != http.StatusConflict || !strings.Contains(apiErr.Problem.Code, "udp_listener_limit") {
		t.Fatalf("error=%v", err)
	}
}
