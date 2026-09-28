package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestReadTestManifestValidatesAndResolvesSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gregale-test.yaml")
	content := "version: 1\nscenarios:\n  customer-export:\n    project: export-api\n    source: .\n    services:\n      worker:\n        source: ./worker\n        secrets: {NOTIFICATION_URL: '${service.notifications.url}/deliver'}\n      notifications: {source: ./notifications}\n    trigger: [node, test/submit.mjs]\n    command: [go, test, ./test]\n    postgres: true\n    buckets: [{name: exports, service: worker, prefix: EXPORT_STORAGE}]\n    wait_for:\n      queue_idle: true\n      objects: [{bucket: exports, prefix: 'reports/${GREGALE_TEST_RUN_ID}/', min_count: 1}]\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	scenarios, sourceDir, err := readTestManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if sourceDir != dir || !scenarios["customer-export"].Postgres || len(scenarios["customer-export"].Command) != 3 || scenarios["customer-export"].Services["worker"].Source != "./worker" {
		t.Fatalf("manifest = %+v, source = %q", scenarios, sourceDir)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(content, "command:", "unknown_field: x\n    command:", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readTestManifest(path); err == nil {
		t.Fatal("unknown scenario field was accepted")
	}
	if err := os.WriteFile(path, []byte(strings.Replace(content, "EXPORT_STORAGE", "bad-prefix", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readTestManifest(path); err == nil {
		t.Fatal("invalid bucket binding prefix was accepted")
	}
	if err := os.WriteFile(path, []byte(strings.Replace(content, "service: worker", "service: missing", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readTestManifest(path); err == nil {
		t.Fatal("unknown bucket owner was accepted")
	}
	if err := os.WriteFile(path, []byte(strings.Replace(content, "service.notifications.url", "service.missing.url", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readTestManifest(path); err == nil {
		t.Fatal("unknown secret service reference was accepted")
	}
	if err := os.WriteFile(path, []byte(strings.Replace(content, "    postgres: true", "    timeout: 1ns", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readTestManifest(path); err == nil {
		t.Fatal("subsecond timeout was accepted")
	}
}

func TestExpandTestSecretValue(t *testing.T) {
	got, err := expandTestSecretValue("${service.notifications.url}/deliver?run=${run.id}&bucket=${bucket.exports.name}",
		map[string]string{"notifications": "https://sink.example"}, map[string]string{"notifications": "sink-app"},
		map[string]testBucketRef{"exports": {Name: "exports-123"}}, "run-123")
	if err != nil || got != "https://sink.example/deliver?run=run-123&bucket=exports-123" {
		t.Fatalf("expanded = (%q, %v)", got, err)
	}
	for _, value := range []string{"${service.unknown.url}", "${bucket.unknown.name}", "${run.id", "${service.notifications.secret}"} {
		if _, err := expandTestSecretValue(value, map[string]string{"notifications": "https://sink.example"}, nil, nil, "run-123"); err == nil {
			t.Errorf("reference %q was accepted", value)
		}
	}
}

type testServiceWakeFakeClient struct {
	rows     []api.WakeTimelineJSONRow
	selected string
	method   string
}

func (f *testServiceWakeFakeClient) GetAppWakeTimeline(context.Context, string, api.AppWakeTimelineOptions) (api.AppWakeTimelineResponse, error) {
	return api.AppWakeTimelineResponse{Rows: f.rows}, nil
}

func (f *testServiceWakeFakeClient) ListWakeTimeline(_ context.Context, _, wakeID, _ string, _ int) (api.WakeTimelineResponse, error) {
	f.selected = wakeID
	return api.WakeTimelineResponse{Events: []api.WakeTimelineEvent{{Kind: "wake.boot_completed", Data: map[string]any{"method": f.method}}}}, nil
}

func TestVerifyTestServiceWakeChecksFirstNewCompletedBoot(t *testing.T) {
	client := &testServiceWakeFakeClient{
		rows:   []api.WakeTimelineJSONRow{{WakeID: "later"}, {WakeID: "first"}, {WakeID: "baseline"}},
		method: "cold_boot",
	}
	evidence, err := verifyTestServiceWake(context.Background(), client, "worker-app", "cold", map[string]bool{"baseline": true})
	if err != nil || evidence.WakeID != "first" || evidence.Method != "cold_boot" || client.selected != "first" {
		t.Fatalf("service evidence = (%+v, %v), selected %q", evidence, err, client.selected)
	}
}

type testOutputFakeClient struct {
	polls int
	slugs []string
}

func (f *testOutputFakeClient) QueueState(_ context.Context, slug string) (api.QueueStateResponse, error) {
	f.polls++
	f.slugs = append(f.slugs, slug)
	if f.polls < 2 {
		return api.QueueStateResponse{Depth: 1}, nil
	}
	return api.QueueStateResponse{}, nil
}

func TestWaitForTestOutputsChecksEveryWorkloadQueue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client := &testOutputFakeClient{}
	if _, err := waitForTestOutputs(ctx, client, []string{"gateway", "worker"}, testWaitFor{QueueIdle: true}, nil, "run-123"); err != nil {
		t.Fatal(err)
	}
	if client.polls != 6 {
		t.Fatalf("queue reads = %d, want three samples per workload", client.polls)
	}
	for i, slug := range client.slugs {
		want := "gateway"
		if i%2 == 1 {
			want = "worker"
		}
		if slug != want {
			t.Fatalf("queue read %d = %q, want %q", i, slug, want)
		}
	}
}

func (f *testOutputFakeClient) ListBucketObjects(_ context.Context, _, bucket, prefix, cursor string, _ int) (api.BucketObjectPage, error) {
	if bucket != "exports-123" || prefix != "reports/run-123/" || cursor != "" {
		return api.BucketObjectPage{}, fmt.Errorf("unexpected object lookup %q %q %q", bucket, prefix, cursor)
	}
	if f.polls < 2 {
		return api.BucketObjectPage{}, nil
	}
	return api.BucketObjectPage{Items: []api.BucketObject{{Key: "reports/run-123/result.csv", SizeBytes: 25}}}, nil
}

func TestWaitForTestOutputsPollsQueueAndObjects(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client := &testOutputFakeClient{}
	outputs, err := waitForTestOutputs(ctx, client, []string{"test-app"}, testWaitFor{
		QueueIdle: true,
		Objects:   []testObjectOutput{{Bucket: "exports", Prefix: "reports/${GREGALE_TEST_RUN_ID}/", MinCount: 1, MinTotalBytes: 1}},
	}, map[string]testBucketRef{"exports": {Name: "exports-123", AppSlug: "test-app"}}, "run-123")
	if err != nil {
		t.Fatal(err)
	}
	if client.polls != 3 || len(outputs) != 1 || outputs[0].Count != 1 || outputs[0].TotalBytes != 25 {
		t.Fatalf("polls=%d outputs=%+v", client.polls, outputs)
	}
}

func TestTestProxyPreservesRequestAndCapturesWakeEvidence(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "" || r.URL.Path != "/exports" || r.Header.Get("X-Customer") != "a" {
			t.Errorf("forwarded request: host=%q path=%q customer=%q", r.Host, r.URL.Path, r.Header.Get("X-Customer"))
		}
		w.Header().Set("X-Faas-Wake", "restored")
		w.Header().Set("X-Faas-Wake-ID", "wake-123")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy, recorder := newTestProxy(target)
	defer proxy.Close()
	request, err := http.NewRequest(http.MethodPost, proxy.URL+"/exports", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Customer", "a")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	got := recorder.snapshot()
	if got.Requests != 1 || got.Header != "restored" || got.WakeID != "wake-123" || got.Status != http.StatusAccepted {
		t.Fatalf("evidence = %+v", got)
	}
	if err := verifyTestProfile(context.Background(), api.NewClient(upstream.URL, "token"), "api", "warm", &got); err == nil {
		t.Fatal("a restored request satisfied the warm profile")
	}
}

func TestSelectedTestProfiles(t *testing.T) {
	profiles, err := selectedTestProfiles("all")
	if err != nil || strings.Join(profiles, ",") != "warm,cold,restored" {
		t.Fatalf("all profiles = %v, %v", profiles, err)
	}
	if _, err := selectedTestProfiles("coldish"); err == nil {
		t.Fatal("invalid profile accepted")
	}
}

func TestTestProxyRecordsFirstRequestEvenWhenItFinishesSecond(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/first" {
			close(firstStarted)
			<-releaseFirst
			w.Header().Set("X-Faas-Wake", "cold")
			w.Header().Set("X-Faas-Wake-ID", "wake-first")
		} else {
			w.Header().Set("X-Faas-Wake", "hot")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	proxy, recorder := newTestProxy(target)
	defer proxy.Close()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		response, err := http.Get(proxy.URL + "/first")
		if err != nil {
			t.Errorf("first request: %v", err)
			return
		}
		_ = response.Body.Close()
	}()
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not start")
	}
	response, err := http.Get(proxy.URL + "/second")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	close(releaseFirst)
	wg.Wait()
	got := recorder.snapshot()
	if got.Requests != 2 || got.Header != "cold" || got.WakeID != "wake-first" {
		t.Fatalf("evidence = %+v", got)
	}
}

func TestVerifyTestProfileUsesCompletedWakeMethod(t *testing.T) {
	for _, tc := range []struct {
		profile, header, method string
	}{
		{profile: "cold", header: "cold", method: "cold_boot"},
		{profile: "restored", header: "restored", method: "restore"},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/apps/api/wakes/wake-123/timeline" {
					t.Errorf("timeline path = %q", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"wake_id":"wake-123","app_id":"api","events":[{"at":"2026-09-28T00:00:00Z","kind":"wake.boot_completed","actor":"schedd","data":{"method":"` + tc.method + `"}}]}`))
			}))
			defer server.Close()
			evidence := testWakeEvidence{Requests: 1, Header: tc.header, WakeID: "wake-123"}
			if err := verifyTestProfile(context.Background(), api.NewClient(server.URL, "token"), "api", tc.profile, &evidence); err != nil {
				t.Fatal(err)
			}
			if evidence.Method != tc.method {
				t.Fatalf("method = %q, want %q", evidence.Method, tc.method)
			}
		})
	}
}
