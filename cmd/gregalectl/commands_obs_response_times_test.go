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

func TestCmdObsResponseTimes(t *testing.T) {
	out, stderr := obsBundleTestStreams(t)
	now := time.Now().UTC()
	since := now.Add(-time.Hour).Format(time.RFC3339Nano)
	response := api.ObsAlertResponseTimes{AsOf: now, Since: now.Add(-time.Hour), Scope: "retained_activation_cohort", DurationBasis: "episode_activation", PercentileMethod: "nearest_rank_whole_seconds", Collection: api.ObsAlertCollectionHealth{State: "unknown", CollectionState: "never_collected"}, Groups: []api.ObsAlertResponseGroup{}}
	calls := 0
	mode := "ok"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != operatorobs.AlertResponseTimesPath || r.URL.Query().Get("since") != since || r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("request: %s %s", r.Method, r.URL)
		}
		if mode == "error" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte("private upstream body"))
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "token")
	args := []string{"--since", since}
	if code := cmdObsDispatch(append([]string{subObsResponseTimes}, args...)); code != 0 || !strings.Contains(out.String(), "totals=unknown") || strings.Contains(out.String(), "p50_seconds=0") {
		t.Fatalf("unknown: %d %s %s", code, out, stderr)
	}
	zero := int64(0)
	response.Totals = &api.ObsAlertResponseAggregate{Episodes: 2, Acknowledgment: api.ObsAlertResponseMetric{Samples: 1, Unknown: 1, P50Seconds: &zero, P95Seconds: &zero}}
	out.Reset()
	if code := cmdObsResponseTimes(append(append([]string{}, args...), "--json")); code != 0 {
		t.Fatalf("json: %d %s", code, stderr)
	}
	var got api.ObsAlertResponseTimes
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Totals.Acknowledgment.P50Seconds == nil || *got.Totals.Acknowledgment.P50Seconds != 0 {
		t.Fatalf("zero sample: %s %v", out, err)
	}
	out.Reset()
	if code := cmdObsResponseTimes(args); code != 0 || !strings.Contains(out.String(), "unknown=1") || !strings.Contains(out.String(), "p50_seconds=0") || !strings.Contains(out.String(), "p50_seconds=unavailable") {
		t.Fatalf("coverage: %d %s", code, out)
	}
	before := calls
	for _, bad := range [][]string{{"--since", "bad"}, {"--timeout", "0s"}, {"extra"}, {"--limit", "5"}} {
		if code := cmdObsResponseTimes(bad); code != 2 {
			t.Fatalf("invalid: %d", code)
		}
	}
	if calls != before {
		t.Fatal("invalid args sent requests")
	}
	mode = "error"
	out.Reset()
	stderr.Reset()
	if code := cmdObsResponseTimes(args); code != 1 || out.Len() != 0 || strings.Contains(stderr.String(), "private upstream body") {
		t.Fatalf("failure: %d %s %s", code, out, stderr)
	}
}
