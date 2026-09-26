package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAppLogDrainHTTPClientRefusesNodeLocalTargets pins the §11 egress
// guard on log-drain delivery. The drain URL is customer-supplied and apid
// only checks it when the drain is saved, so the delivering client must
// refuse loopback / link-local / private addresses at dial time, like the
// webhook and realtime-callback clients. The plain http.Client it used
// before posted customer logs to any address the host resolved to.
func TestAppLogDrainHTTPClientRefusesNodeLocalTargets(t *testing.T) {
	t.Setenv("FAAS_EGRESS_ALLOW_LOOPBACK", "")
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	manager := newAppLogDrainManager(nil, nil, nil, nil, nil)
	resp, err := manager.httpClient.Post(srv.URL+"/drain", "application/json", strings.NewReader("{}"))
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("log drain delivered to loopback %s (status %d); want an egress refusal", srv.URL, resp.StatusCode)
	}
	if hit {
		t.Fatal("loopback drain target received the request")
	}
	if !strings.Contains(err.Error(), "egress") {
		t.Fatalf("err = %v, want the egress guard's refusal", err)
	}
}

// A drain endpoint that answers with a redirect must not steer delivery —
// with the customer's auth header attached — to another host.
func TestAppLogDrainHTTPClientDoesNotFollowRedirects(t *testing.T) {
	t.Setenv("FAAS_EGRESS_ALLOW_LOOPBACK", "1")
	followed := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		followed = true
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/latest/meta-data", http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	resp, err := newAppLogDrainHTTPClient().Post(redirector.URL, "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	_ = resp.Body.Close()
	if followed || resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("redirect followed=%v status=%d; want the 307 returned as-is", followed, resp.StatusCode)
	}
}
