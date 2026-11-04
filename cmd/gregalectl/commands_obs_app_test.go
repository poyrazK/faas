package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func obsAppTestAPID(t *testing.T, status int, body api.ObsAppDetailResponse) (*httptest.Server, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r)
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "admin")
	return srv, &seen
}

func obsAppFixture() api.ObsAppDetailResponse {
	at := time.Date(2026, 10, 24, 8, 15, 0, 0, time.UTC)
	return api.ObsAppDetailResponse{
		App: api.ObsAppDetail{ID: "0193a1b2-0000-7e5f-8a9b-0c1d2e3f4a5b", AccountID: "acct-1", Slug: "shop", Type: "app", Runtime: "node22", Status: "active", RAMMB: 512, MaxConcurrency: 2, CreatedAt: at},
		Deployments: []api.ObsDeploymentRow{
			{ID: "dep-new", Status: "live", Kind: "git", CommitSHA: "abcdef1234567890", CreatedAt: at},
			{ID: "dep-old", Status: "failed", Kind: "git", ErrorCode: "build_failed", CreatedAt: at.Add(-time.Hour)},
		},
		Instances: []api.ObsInstanceRow{
			{ID: "ins-1", State: "running", NodeName: "fsn-3", StartedAt: at.Add(time.Hour), LastRequestAt: at.Add(time.Hour + time.Minute)},
			{ID: "ins-2", State: "parked"},
			{ID: "ins-3", State: "parked"},
		},
		Health: api.ObsAppHealth{
			Metrics:           api.AppMetricsResponse{Range: "1h", Source: "prometheus", RequestCount: 1204, ErrorRatePct: 0.4, LatencyP50MS: 12, LatencyP95MS: 80, LatencyP99MS: 210, ColdStartPct: 2.1},
			ErrorsUnavailable: true,
		},
		Snapshots: []api.ObsAppSnapshot{
			{DeploymentID: "dep-new", Tier: "warm"},
			{DeploymentID: "dep-new", Tier: "init", Present: true, FCVersion: "1.7.0", MemBytes: 128 << 20, CreatedAt: &at},
		},
		Wakes: &api.AppWakeTimelineResponse{WakeCount24h: 2, AtCapacityCount: 1, AtCapacityPct: 50, Rows: []api.WakeTimelineJSONRow{
			{WakeID: "0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", At: "2026-10-24T09:15:00Z", Method: "restore", Tier: "init", ReadyInMS: 290, Trigger: "http", State: "running", AtCapacity: true},
			{WakeID: "0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5c", At: "2026-10-24T07:00:00Z", Method: "cold", ReadyInMS: -1, Trigger: "cron", State: "parked"},
		}},
	}
}

func TestCmdObsAppPrintsTheSupportPage(t *testing.T) {
	out, stderr := obsBundleTestStreams(t)
	_, seen := obsAppTestAPID(t, http.StatusOK, obsAppFixture())

	// The slug may come before the flags.
	if code := cmdObsDispatch([]string{subObsApp, "shop", "--range", "1h", "--wakes", "1"}); code != 0 {
		t.Fatalf("code %d: %s", code, stderr)
	}
	r := (*seen)[0]
	if r.URL.Path != "/v1/admin/obs/apps/shop" || r.URL.Query().Get("range") != "1h" || r.Header.Get("Authorization") != "Bearer admin" {
		t.Fatalf("request %s %v", r.URL, r.Header)
	}
	page := out.String()
	for _, want := range []string{
		"app shop  id=0193a1b2-0000-7e5f-8a9b-0c1d2e3f4a5b  account=acct-1  app node22  status=active\n",
		"live  deployment dep-new (git, 2026-10-24 08:15:00 commit abcdef123456)\n",
		"      next wake: restore from the init snapshot (128 MB memory, fc 1.7.0, taken 2026-10-24 08:15:00)\n",
		"instances  3 (1 running, 2 parked)\n",
		"  running      fsn-3        started 2026-10-24 09:15:00  last request 2026-10-24 09:16:00  ins-1  wake -\n",
		"health (1h, prometheus)  1204 requests  5xx 0.40%  p50 12 ms  p95 80 ms  p99 210 ms  cold starts 2.1%\n",
		"errors (24h)  unavailable: apid could not read the error groups\n",
		"wakes (24h)  2, 1 at capacity (50%)\n",
		"  2026-10-24 09:15:00  0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b  restore  init    290 ms  http       running  at capacity\n",
		"  one wake's events and logs: gregalectl obs wake <wake_id>\n",
		"deployments  2\n",
		"  2026-10-24 07:15:00  failed     git      dep-old error build_failed\n",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q\n%s", want, page)
		}
	}
	if strings.Contains(page, "0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5c") {
		t.Errorf("--wakes 1 printed a second wake:\n%s", page)
	}
}

func TestCmdObsAppNextWakeAndJSON(t *testing.T) {
	out, stderr := obsBundleTestStreams(t)
	fixture := obsAppFixture()
	fixture.Snapshots = []api.ObsAppSnapshot{{DeploymentID: "dep-new", Tier: "warm"}, {DeploymentID: "dep-new", Tier: "init"}}
	obsAppTestAPID(t, http.StatusOK, fixture)
	if code := cmdObsApp([]string{"shop"}); code != 0 {
		t.Fatalf("code %d: %s", code, stderr)
	}
	if !strings.Contains(out.String(), "next wake: cold boot: no usable snapshot") {
		t.Fatalf("page:\n%s", out)
	}

	out.Reset()
	if code := cmdObsApp([]string{"--json", "shop"}); code != 0 {
		t.Fatalf("json code %d: %s", code, stderr)
	}
	var doc api.ObsAppDetailResponse
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || doc.App.Slug != "shop" || len(doc.Snapshots) != 2 {
		t.Fatalf("json: %v %s", err, out)
	}
}

func TestCmdObsAppErrors(t *testing.T) {
	_, stderr := obsBundleTestStreams(t)
	_, seen := obsAppTestAPID(t, http.StatusNotFound, api.ObsAppDetailResponse{})
	if code := cmdObsApp([]string{"no-such-app"}); code != 1 || !strings.Contains(stderr.String(), "no app has the id or slug no-such-app") {
		t.Fatalf("404: code %d: %s", code, stderr)
	}
	calls := len(*seen)
	for _, args := range [][]string{{}, {"Bad_Slug"}, {"shop", "extra"}, {"shop", "--range", "2y"}, {"shop", "--wakes", "101"}} {
		if code := cmdObsApp(args); code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
	}
	if len(*seen) != calls {
		t.Fatal("invalid input reached apid")
	}
}
