package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestScenarioRegistrationReadAndConditionalDelete(t *testing.T) {
	// adr: 984 — recovery sends a membership precondition rather than an unscoped delete.
	digest := ScenarioTestRegistrationSHA256(map[string]string{"worker": "two", "app": "one"})
	deletes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing auth")
		}
		if r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"schema_version":1,"members":{"app":"one","worker":"two"},"expires_at":"2026-10-01T00:00:00Z","expiry_state":"expired"}`))
			return
		}
		if r.Method != "DELETE" || r.URL.Path != "/v1/dev/test-runs/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/registration" || r.URL.Query().Get("members_sha256") != digest {
			t.Error("missing conditional membership")
		}
		deletes++
		w.WriteHeader(204)
	}))
	defer server.Close()
	client := NewClient(server.URL, "test-token")
	registration, err := client.GetScenarioTestRegistration(context.Background(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil || registration.SchemaVersion != 1 || ScenarioTestRegistrationSHA256(registration.Members) != digest || registration.ExpiryState != "expired" || registration.ExpiresAt == nil || !registration.ExpiresAt.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("registration response lost", registration, err)
	}
	if err := client.DeleteScenarioTestRegistration(context.Background(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", digest); err != nil || deletes != 1 {
		t.Fatal(err)
	}
}

func TestScenarioRegistrationDeleteFailsClosedOnUnsupportedOrChangedServer(t *testing.T) {
	// adr: 984 — unsupported routes and changed registrations grant no deletion authority.
	for _, tc := range []struct {
		name, body string
		status     int
		wantError  bool
	}{
		{"old-server", "", 404, true},
		{"unversioned", `{"members":{}}`, 200, true},
		{"changed", `{"schema_version":1,"members":{"app":"replacement"}}`, 200, true},
		{"already-absent", `{"schema_version":1,"members":{}}`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deletes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "DELETE" {
					deletes++
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			err := NewClient(server.URL, "test-token").DeleteScenarioTestRegistration(context.Background(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ScenarioTestRegistrationSHA256(map[string]string{"app": "owned"}))
			if (err != nil) != tc.wantError || deletes != 0 {
				t.Fatalf("unsafe conditional delete: deletes=%d err=%v", deletes, err)
			}
		})
	}
}
