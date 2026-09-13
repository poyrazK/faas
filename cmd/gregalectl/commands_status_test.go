package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestStatusIncidentCreateCallsAdminAPIAndPrintsPermalink(t *testing.T) {
	var gotPath, gotIdempotency string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotIdempotency = r.URL.Path, r.Header.Get("Idempotency-Key")
		var request api.AdminStatusEventCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Kind != "incident" || request.Title != "API errors" || len(request.Components) != 1 || request.Components[0] != "api_console" {
			t.Fatalf("request = %#v", request)
		}
		_ = json.NewEncoder(w).Encode(api.PublicStatusEvent{ID: "11111111-1111-4111-8111-111111111111", Kind: "incident", Title: request.Title, State: "investigating", UpdatedAt: time.Now()})
	}))
	t.Cleanup(srv.Close)
	installTestOperatorSession(t, srv.URL, "session-cookie")
	oldOut := osStdout
	var out bytes.Buffer
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })

	code := cmdStatusDispatch([]string{"incident", "create", "--title", "API errors", "--impact", "degraded", "--components", "api_console", "--message", "Investigating."})
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if gotPath != "/v1/admin/status/incidents" || gotIdempotency == "" {
		t.Fatalf("path=%q idempotency=%q", gotPath, gotIdempotency)
	}
	if !strings.Contains(out.String(), srv.URL+"/status/incidents/11111111-1111-4111-8111-111111111111") {
		t.Fatalf("output missing public permalink: %q", out.String())
	}
}

func TestStatusMaintenanceLifecycleCommandsSendExpectedState(t *testing.T) {
	var states []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request api.AdminStatusEventUpdateRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		states = append(states, request.State)
		_ = json.NewEncoder(w).Encode(api.PublicStatusEvent{ID: "22222222-2222-4222-8222-222222222222", Kind: "maintenance", State: request.State, UpdatedAt: time.Now()})
	}))
	t.Cleanup(srv.Close)
	installTestOperatorSession(t, srv.URL, "session-cookie")
	oldOut := osStdout
	osStdout = &bytes.Buffer{}
	t.Cleanup(func() { osStdout = oldOut })
	for _, command := range []string{"start", "complete", "cancel"} {
		if code := cmdStatusDispatch([]string{"maintenance", command, "--id", "22222222-2222-4222-8222-222222222222", "--message", command}); code != 0 {
			t.Fatalf("%s exit=%d", command, code)
		}
	}
	want := []string{"in_progress", "completed", "cancelled"}
	if strings.Join(states, ",") != strings.Join(want, ",") {
		t.Fatalf("states=%v want=%v", states, want)
	}
}

func TestStatusMaintenanceUpdatePreservesCurrentLifecycle(t *testing.T) {
	const id = "22222222-2222-4222-8222-222222222222"
	var postedState string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(api.PublicStatusEvent{ID: id, Kind: "maintenance", State: "in_progress", UpdatedAt: time.Now()})
			return
		}
		var request api.AdminStatusEventUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		postedState = request.State
		_ = json.NewEncoder(w).Encode(api.PublicStatusEvent{ID: id, Kind: "maintenance", State: request.State, UpdatedAt: time.Now()})
	}))
	t.Cleanup(srv.Close)
	installTestOperatorSession(t, srv.URL, "session-cookie")
	oldOut := osStdout
	osStdout = &bytes.Buffer{}
	t.Cleanup(func() { osStdout = oldOut })

	if code := cmdStatusDispatch([]string{"maintenance", "update", "--id", id, "--message", "Work is proceeding."}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if postedState != "in_progress" {
		t.Fatalf("posted state=%q, want in_progress", postedState)
	}
}
