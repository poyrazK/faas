package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func TestCmdObsSummaryBoundsAndUnknown(t *testing.T) {
	out, stderr := obsBundleTestStreams(t)
	now := time.Now().UTC()
	response := api.ObsAlertWorkloadSummary{AsOf: now, Scope: "retained_platform_alert_episodes", AgeBasis: "episode_started_at", Collection: api.ObsAlertCollectionHealth{State: "unknown", CollectionState: "never_collected"}, Groups: []api.ObsAlertWorkloadGroup{}}
	calls := 0
	mode := "ok"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != operatorobs.AlertWorkloadSummaryPath || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("request: %s %s", r.Method, r.URL)
		}
		switch mode {
		case "error":
			w.WriteHeader(503)
			_, _ = w.Write([]byte("private upstream body"))
		case "invalid":
			_, _ = w.Write([]byte(`{}`))
		default:
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "token")
	if code := cmdObsDispatch([]string{subObsSummary}); code != 0 || !strings.Contains(out.String(), "counts=unknown") || strings.Contains(out.String(), "total=0") {
		t.Fatalf("unknown: %d %s %s", code, out, stderr)
	}
	out.Reset()
	response.Counts = &api.ObsAlertWorkloadCounts{Total: 2, Unknown: 2, NeedsAttention: 2}
	response.Collection.CollectionState = "stale"
	response.OldestUnowned = &api.ObsAlertWorkloadOldest{IncidentID: "platform_alert/" + strings.Repeat("a", 64), Status: "unknown", AgeSeconds: 300, EvidencePath: "/evidence", StartedAt: now.Add(-5 * time.Minute)}
	if code := cmdObsDispatch([]string{subObsSummary, "--json"}); code != 0 {
		t.Fatalf("json: %d %s", code, stderr)
	}
	var parsed api.ObsAlertWorkloadSummary
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil || parsed.Counts.Unknown != 2 || parsed.OldestUnowned.AgeSeconds != 300 {
		t.Fatalf("json: %s %v", out, err)
	}
	out.Reset()
	if code := cmdObsSummary(nil); code != 0 || !strings.Contains(out.String(), "collection=stale") || !strings.Contains(out.String(), "needs_attention=2") || !strings.Contains(out.String(), "age_seconds=300") {
		t.Fatalf("human: %d %s", code, out)
	}
	out.Reset()
	response.Counts = &api.ObsAlertWorkloadCounts{}
	response.Collection.CollectionState = "current"
	response.OldestUnowned = nil
	if code := cmdObsSummary(nil); code != 0 || !strings.Contains(out.String(), "total=0") || strings.Contains(out.String(), "counts=unknown") {
		t.Fatalf("quiet: %d %s", code, out)
	}
	before := calls
	for _, args := range [][]string{{"--timeout", "0s"}, {"extra"}, {"--limit", "1"}} {
		if code := cmdObsSummary(args); code != 2 {
			t.Fatalf("invalid: %d", code)
		}
	}
	if calls != before {
		t.Fatal("invalid arguments sent requests")
	}
	for _, m := range []string{"invalid", "error"} {
		mode = m
		out.Reset()
		stderr.Reset()
		if code := cmdObsSummary(nil); code != 1 || out.Len() != 0 || strings.Contains(stderr.String(), "private upstream body") {
			t.Fatalf("failure: %d %s %s", code, out, stderr)
		}
	}
}
