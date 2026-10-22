package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// adr: 20261016151947 — the Neon activity probe reads the endpoint's compute
// state: running (or starting) resumes a released hub, idle or stopping does
// not, and anything else is an error that changes nothing.
func TestNeonAwake(t *testing.T) {
	state := "active"
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/projects/proj-1/endpoints/ep-1" || r.Header.Get("Authorization") != "Bearer key-1" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"endpoint":{"id":"ep-1","current_state":"` + state + `"}}`))
	}))
	defer srv.Close()
	probe := neonAwake(srv.Client(), srv.URL+"/api/v2/", "key-1", "proj-1", "ep-1")
	for s, want := range map[string]bool{"active": true, "starting": true, "init": true, "idle": false, "stopping": false} {
		state = s
		if got, err := probe(context.Background()); err != nil || got != want {
			t.Errorf("state %s: awake %v, %v; want %v", s, got, err, want)
		}
	}
	state = "mystery"
	if _, err := probe(context.Background()); err == nil {
		t.Error("an unknown state was not an error")
	}
	state, status = "active", http.StatusUnauthorized
	if _, err := probe(context.Background()); err == nil {
		t.Error("an HTTP error was not an error")
	}
}

// adr: 20261016151947 — releasing needs a way to notice the database waking.
func TestAwakeProbeConfiguration(t *testing.T) {
	release, source := *releaseAfter, *awakeSource
	defer func() { *releaseAfter, *awakeSource = release, source }()

	*releaseAfter, *awakeSource = 0, ""
	if p, err := awakeProbe(); p != nil || err != nil {
		t.Fatalf("never releasing: %v, %v", p != nil, err)
	}
	*awakeSource = "neon"
	if _, err := awakeProbe(); err == nil {
		t.Error("-awake without -release-after was accepted")
	}
	*releaseAfter, *awakeSource = 60e9, ""
	if _, err := awakeProbe(); err == nil {
		t.Error("-release-after without -awake was accepted")
	}
	*awakeSource = "neon"
	if _, err := awakeProbe(); err == nil {
		t.Error("-awake neon without its Neon flags was accepted")
	}
	*awakeSource = "schedd"
	if _, err := awakeProbe(); err == nil {
		t.Error("an unknown -awake was accepted")
	}
}
