// spec: §14.A
// adr: 1527
package m9

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPendingDrainRecognizesRetainedPriorCompletion(t *testing.T) {
	previous := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	current := previous.Add(time.Hour)
	if !(drainStatus{Lifecycle: "draining", InitiatedAt: &current, CompletedAt: &previous}).pending(&previous) {
		t.Fatal("historical completion was mistaken for completion of this drain generation")
	}
	if (drainStatus{Lifecycle: "draining", InitiatedAt: &current, CompletedAt: &current}).pending(&previous) {
		t.Fatal("a new completion timestamp qualified as an unchanged pending drain")
	}
	if (drainStatus{Lifecycle: "maintenance", InitiatedAt: &current, CompletedAt: &previous}).pending(&previous) {
		t.Fatal("completed lifecycle qualified as pending")
	}
}

func TestDrainConfigRejectsUnsafeAPIOrigins(t *testing.T) {
	c := fixtureConfig()
	c.Drain = &DrainConfig{APIURL: "https://control.example.test", ScheddSHA256: strings.Repeat("b", 64)}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeConfig(strings.NewReader(string(raw))); err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"http://control.example.test", "https://secret@control.example.test",
		"https://control.example.test/path", "https://control.example.test?token=private",
		"https://control.example.test?", "https://control.example.test#private"} {
		c.Drain.APIURL = origin
		if err := c.Validate(); err == nil {
			t.Fatalf("unsafe drain API accepted: %s", origin)
		}
	}
}

func TestDrainAPIUsesRealRoutesWithoutRedirectingCredentials(t *testing.T) {
	var requests []string
	redirect := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer operator-token" {
			t.Error("operator authentication missing")
		}
		if redirect {
			http.Redirect(w, r, "/credential-trap", http.StatusTemporaryRedirect)
			return
		}
		_, _ = w.Write([]byte(`{"node_name":"source","lifecycle":"draining","drain_initiated_at":"2026-10-18T00:00:00Z"}`))
	}))
	defer server.Close()
	api, err := newDrainAPI(DrainConfig{APIURL: server.URL, ScheddSHA256: strings.Repeat("b", 64)}, "operator-token")
	if err != nil {
		t.Fatal(err)
	}
	api.client.Transport = server.Client().Transport // Trust only this test server's CA.
	status, err := api.status(t.Context(), http.MethodPost, "source")
	if err != nil || status.Lifecycle != "draining" {
		t.Fatalf("drain request: %+v %v", status, err)
	}
	if len(requests) != 1 || requests[0] != "POST /v1/compute-nodes/source/drain" {
		t.Fatalf("wrong operator route: %v", requests)
	}
	redirect = true
	if _, err := api.status(t.Context(), http.MethodGet, "source"); err == nil {
		t.Fatal("redirect qualified as drain progress")
	}
	if len(requests) != 2 {
		t.Fatalf("operator credentials followed redirect: %v", requests)
	}
}

func TestDrainAPIRejectsPrivateErrorsWrongNodeAndOversizedEvidence(t *testing.T) {
	for _, body := range []string{`{"node_name":"other","lifecycle":"draining"}`, "{partial", strings.Repeat("x", 65537)} {
		t.Run("invalid response", func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			api, err := newDrainAPI(DrainConfig{APIURL: server.URL, ScheddSHA256: strings.Repeat("b", 64)}, "private-token")
			if err != nil {
				t.Fatal(err)
			}
			api.client.Transport = server.Client().Transport
			if _, err := api.status(t.Context(), http.MethodGet, "source"); err == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
	api, err := newDrainAPI(DrainConfig{APIURL: "https://control.example.test", ScheddSHA256: strings.Repeat("b", 64)}, "private-token")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = api.status(ctx, http.MethodPost, "source")
	if err == nil || strings.Contains(err.Error(), "private-token") {
		t.Fatal("canceled call leaked authentication or succeeded")
	}
}

func TestDrainAPIPreflightChecksOperatorAccessAndDatabaseIdentity(t *testing.T) {
	denied := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if denied {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"admin_required","detail":"private-token"}`))
		} else if strings.HasSuffix(r.URL.Path, "/drain") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"not_found"}`))
		} else {
			_, _ = w.Write([]byte(`{"id":"source-id","name":"source"}`))
		}
	}))
	defer server.Close()
	api, err := newDrainAPI(DrainConfig{APIURL: server.URL, ScheddSHA256: strings.Repeat("b", 64)}, "private-token")
	if err != nil {
		t.Fatal(err)
	}
	api.client.Transport = server.Client().Transport
	if err := api.preflight(t.Context(), "source", "source-id"); err != nil {
		t.Fatal(err)
	}
	if err := api.preflight(t.Context(), "source", "different-database-id"); err == nil {
		t.Fatal("API from a different database passed preflight")
	}
	denied = true
	err = api.preflight(t.Context(), "source", "source-id")
	if err == nil || strings.Contains(err.Error(), "private-token") {
		t.Fatal("unauthorized preflight succeeded or exposed a private API error")
	}
}
