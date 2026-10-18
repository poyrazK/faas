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

func appChangesServer(t *testing.T, payload api.AppChangeTimelineResponse) (*string, *string) {
	t.Helper()
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	return &gotPath, &gotQuery
}

func captureAppChangesStdout(t *testing.T) *bytes.Buffer {
	t.Helper()
	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	t.Cleanup(func() { osStdout = old })
	return &stdout
}

func TestCmdAppChanges_RendersTimeline(t *testing.T) {
	at := time.Date(2026, 10, 9, 14, 2, 0, 0, time.UTC)
	gotPath, gotQuery := appChangesServer(t, api.AppChangeTimelineResponse{
		AppSlug: "shop", Since: at.Add(-24 * time.Hour), Until: at.Add(time.Hour), Truncated: true,
		UnavailableSources: []string{api.ChangeSourceIncident},
		Events: []api.AppChangeEvent{
			{At: at.Add(5 * time.Minute), Source: api.ChangeSourceHealth, Summary: "Health changed from healthy to degraded"},
			{At: at, Source: api.ChangeSourceDeployment, Summary: "Deployment a1b2c3d4 created"},
		},
	})
	stdout := captureAppChangesStdout(t)

	if code := cmdAppDispatch([]string{"shop", "changes"}); code != 0 {
		t.Fatalf("app shop changes = %d, want 0", code)
	}
	if *gotPath != "/v1/apps/shop/changes" || *gotQuery != "" {
		t.Fatalf("request = %s?%s, want /v1/apps/shop/changes with server defaults", *gotPath, *gotQuery)
	}
	out := stdout.String()
	for _, want := range []string{
		"Warning: incident changes could not be read",
		"2026-10-09 14:07:00Z  health         Health changed from healthy to degraded",
		"2026-10-09 14:02:00Z  deployment     Deployment a1b2c3d4 created",
		"showing the newest 2 changes",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull:\n%s", want, out)
		}
	}
	if strings.Index(out, "health ") > strings.Index(out, "deployment ") {
		t.Errorf("events must keep the server's newest-first order:\n%s", out)
	}
}

func TestCmdAppChanges_PassesWindow(t *testing.T) {
	_, gotQuery := appChangesServer(t, api.AppChangeTimelineResponse{AppSlug: "shop"})
	stdout := captureAppChangesStdout(t)

	if code := cmdAppChanges("shop", []string{"--since", "2026-10-08T12:00:00Z", "--until", "2026-10-09T12:00:00Z"}); code != 0 {
		t.Fatalf("app changes with window = %d, want 0", code)
	}
	if *gotQuery != "since=2026-10-08T12%3A00%3A00Z&until=2026-10-09T12%3A00%3A00Z" {
		t.Fatalf("query = %q", *gotQuery)
	}
	if !strings.Contains(stdout.String(), "(no recorded changes in this window)") {
		t.Fatalf("empty timeline should say so:\n%s", stdout.String())
	}
}

func TestCmdAppChanges_RejectsBadWindowBeforeCallingAPI(t *testing.T) {
	gotPath, _ := appChangesServer(t, api.AppChangeTimelineResponse{})
	_, _, restore := swapIO(t)
	defer restore()
	if code := cmdAppChanges("shop", []string{"--since", "yesterday"}); code == 0 {
		t.Fatal("non-RFC3339 --since must fail")
	}
	if *gotPath != "" {
		t.Fatalf("API was called with an invalid window: %s", *gotPath)
	}
}
