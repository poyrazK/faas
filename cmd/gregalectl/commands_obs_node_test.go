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

const obsNodeTestID = "11111111-2222-4333-8444-555555555555"

// obsNodeTestAPID serves the three reads `obs node` makes. A status in
// failing makes that path answer with it instead.
func obsNodeTestAPID(t *testing.T, failing map[string]int) *[]string {
	t.Helper()
	at := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	cpu, disk := 43.5, int64(18_200_000_000)
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		if code, ok := failing[r.URL.Path]; ok {
			w.WriteHeader(code)
			return
		}
		var body any
		switch r.URL.Path {
		case "/v1/admin/obs/nodes/fsn-3/detail":
			body = api.ObsNodeDetailResponse{
				Node: api.ObsNodeRow{ID: obsNodeTestID, Name: "fsn-3", Active: true, VPCPUs: 8, MemMB: 65536, MaxConcurrency: 32,
					AdmissionCeilingMB: 47600, OverlayIP: "10.20.0.5", LastHeartbeatAt: at.Add(-4 * time.Second), CreatedAt: at.Add(-48 * time.Hour),
					InstancesLive: 2, InstancesRunning: 1, InstancesWaking: 1, RAMUsedMB: 1056, AdmissionMarginMB: 46544, CPUPct60s: &cpu, DiskUsedBytes: &disk},
				Apps: []api.ObsNodeApp{
					{Slug: "small", RAMUsedMB: 264, InstancesLive: 1, InstancesWaking: 1},
					{Slug: "shop", RAMUsedMB: 792, InstancesLive: 1, InstancesRunning: 1, LastRequestAt: &at},
				},
				Instances: []api.ObsInstanceRow{
					{ID: "ins-1", AppSlug: "shop", State: "running", RAMMB: 512, StartedAt: at.Add(-time.Hour), LastRequestAt: at, WakeID: "0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"},
					{ID: "ins-2", AppSlug: "small", State: "parked"},
				},
				Drain: api.ObsNodeDrainStatus{RunningInstances: 1, WakingInstances: 1},
			}
		case "/v1/admin/obs/nodes/fsn-3/heartbeats":
			body = api.ObsHeartbeatListResponse{Name: "fsn-3", Heartbeats: []api.ObsHeartbeatRow{
				{GapToPreviousMs: 5000}, {GapToPreviousMs: 31000, Missed: true}, {GapToPreviousMs: 5000},
			}}
		case "/v1/admin/obs/nodes/wake-latency":
			body = api.ObsNodeWakeLatencyResponse{PrometheusAvailable: true, Window: "5m", FleetP95MS: 310, Quantiles: []api.ObsWakeLatencyQuantile{
				{NodeID: "other", NodeName: "fsn-4", P95MS: 900, SampleCount: 3},
				{NodeID: obsNodeTestID, NodeName: "fsn-3", P50MS: 120, P95MS: 290, P99MS: 410, SampleCount: 37},
			}}
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "admin")
	return &paths
}

func TestCmdObsNodePrintsTheSupportPage(t *testing.T) {
	out, stderr := obsBundleTestStreams(t)
	paths := obsNodeTestAPID(t, nil)
	if code := cmdObsDispatch([]string{subObsNode, "fsn-3", "--heartbeats", "3"}); code != 0 {
		t.Fatalf("code %d: %s", code, stderr)
	}
	if got := strings.Join(*paths, " "); got != "/v1/admin/obs/nodes/fsn-3/detail /v1/admin/obs/nodes/fsn-3/heartbeats?limit=3 /v1/admin/obs/nodes/wake-latency" {
		t.Fatalf("requests: %s", got)
	}
	page := out.String()
	for _, want := range []string{
		"node fsn-3  id=" + obsNodeTestID + "  active  overlay=10.20.0.5  created 2026-10-23 09:00:00\n",
		"capacity  8 vCPU  65536 MB  max_concurrency 32  admission ceiling 47600 MB\n",
		"now  RAM admitted 1056 of 47600 MB (2%, margin 46544 MB)  live 2 (1 running, 1 waking, 0 cold booting)  cpu 43.5% (60s)  snapshot disk 18.2 GB\n",
		"; last 3: max gap 31s, 1 missed, 0 stale\n",
		"wakes (5m)  p50 120 ms  p95 290 ms  p99 410 ms over 37 wakes  (fleet p95 310 ms)\n",
		"drain  not safe: 1 running, 1 waking, 0 cold booting\n",
		"apps  2\n  shop                        792 MB",
		"instances  1 live of 2\n",
		"wake 0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b\n",
		"  one wake's events and logs: gregalectl obs wake <wake_id>\n",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q\n%s", want, page)
		}
	}
}

func TestCmdObsNodeIsBestEffortForHeartbeatsAndWakes(t *testing.T) {
	out, stderr := obsBundleTestStreams(t)
	obsNodeTestAPID(t, map[string]int{"/v1/admin/obs/nodes/fsn-3/heartbeats": 503, "/v1/admin/obs/nodes/wake-latency": 503})
	if code := cmdObsNode([]string{"fsn-3"}); code != 0 {
		t.Fatalf("code %d: %s", code, stderr)
	}
	page := out.String()
	if !strings.Contains(page, "history unavailable: heartbeats: apid returned HTTP 503") || !strings.Contains(page, "wakes  unavailable: wake latency: apid returned HTTP 503") {
		t.Fatalf("page:\n%s", page)
	}

	out.Reset()
	if code := cmdObsNode([]string{"--json", "fsn-3"}); code != 0 {
		t.Fatalf("json code %d: %s", code, stderr)
	}
	var doc obsNodePage
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || doc.Detail.Node.Name != "fsn-3" || doc.Heartbeats != nil || doc.WakeNote == "" {
		t.Fatalf("json: %v %s", err, out)
	}
}

func TestCmdObsNodeErrors(t *testing.T) {
	_, stderr := obsBundleTestStreams(t)
	paths := obsNodeTestAPID(t, map[string]int{"/v1/admin/obs/nodes/fsn-3/detail": 404})
	if code := cmdObsNode([]string{"fsn-3"}); code != 1 || !strings.Contains(stderr.String(), "no compute node is named fsn-3") {
		t.Fatalf("404: code %d: %s", code, stderr)
	}
	calls := len(*paths)
	for _, args := range [][]string{{}, {"FSN_3"}, {"a..b"}, {"fsn-3", "extra"}, {"fsn-3", "--heartbeats", "0"}, {"fsn-3", "--instances", "101"}} {
		if code := cmdObsNode(args); code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
	}
	if len(*paths) != calls {
		t.Fatal("invalid input reached apid")
	}
}
