package main

import (
	"context"
	"errors"
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
	content := "version: 1\nscenarios:\n  customer-export:\n    project: export-api\n    source: .\n    consumers: [{name: customer-a}, {name: customer-b, scopes: [read]}]\n    services:\n      worker:\n        source: ./worker\n        secrets: {NOTIFICATION_URL: '${service.notifications.url}/deliver'}\n      notifications: {source: ./notifications}\n    trigger: [node, test/submit.mjs]\n    command: [go, test, ./test]\n    postgres: true\n    buckets: [{name: exports, service: worker, prefix: EXPORT_STORAGE}]\n    wait_for:\n      queue_idle: true\n      objects: [{bucket: exports, prefix: 'reports/${GREGALE_TEST_RUN_ID}/', min_count: 1}]\n"
	content = strings.Replace(content, "    consumers:", "    consumer_auth_mode: required\n    consumers:", 1)
	content = strings.Replace(content, "notifications: {source: ./notifications}", "notifications: {fixture: delivery-sink, fail_first: 1}", 1)
	content = strings.Replace(content, "      queue_idle: true", "      queue_idle: true\n      deliveries: [{service: notifications, min_attempts: 2, last_status: 200}]", 1)
	content = strings.Replace(content, "    command:", "    simulation: [sh, -c, 'exit 0']\n    command:", 1)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	scenarios, sourceDir, err := readTestManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if sourceDir != dir || !scenarios["customer-export"].Postgres || len(scenarios["customer-export"].Command) != 3 || len(scenarios["customer-export"].Simulation) != 3 || scenarios["customer-export"].Services["worker"].Source != "./worker" || scenarios["customer-export"].Services["notifications"].Fixture != testDeliverySinkFixture || len(scenarios["customer-export"].Consumers) != 2 || scenarios["customer-export"].ConsumerAuthMode != api.ConsumerAuthModeRequired || len(scenarios["customer-export"].WaitFor.Deliveries) != 1 {
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
	if err := os.WriteFile(path, []byte(strings.Replace(content, "fail_first: 1", "fail_first: 21", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readTestManifest(path); err == nil {
		t.Fatal("excessive fixture failures were accepted")
	}
	if err := os.WriteFile(path, []byte(strings.Replace(content, "fixture: delivery-sink", "source: ./notifications, fixture: delivery-sink", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readTestManifest(path); err == nil {
		t.Fatal("ambiguous service source and fixture was accepted")
	}
	if err := os.WriteFile(path, []byte(strings.Replace(content, "fail_first: 1", "fail_first: 1, secrets: {GREGALE_TEST_SINK_TOKEN: override}", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readTestManifest(path); err == nil {
		t.Fatal("fixture token override was accepted")
	}
	if err := os.WriteFile(path, []byte(strings.Replace(content, "min_attempts: 2", "min_attempts: 0", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readTestManifest(path); err == nil {
		t.Fatal("zero delivery attempts were accepted")
	}
	if err := os.WriteFile(path, []byte(strings.Replace(content, "customer-b, scopes: [read]", "customer-a, scopes: [read]", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readTestManifest(path); err == nil {
		t.Fatal("duplicate consumer was accepted")
	}
	if err := os.WriteFile(path, []byte(strings.Replace(content, "consumer_auth_mode: required", "consumer_auth_mode: unsupported", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readTestManifest(path); err == nil {
		t.Fatal("invalid consumer auth mode was accepted")
	}
	if err := os.WriteFile(path, []byte(strings.Replace(content, "    postgres: true", "    timeout: 1ns", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readTestManifest(path); err == nil {
		t.Fatal("subsecond timeout was accepted")
	}
}

type testConsumerFakeClient struct {
	created []api.CreateAPIConsumerRequest
	keys    []api.CreateConsumerKeyRequest
	failKey bool
}

type testAccessFakeClient struct {
	slug string
	req  api.UpdateAppRequest
}

func (f *testAccessFakeClient) UpdateApp(_ context.Context, slug string, req api.UpdateAppRequest) (api.AppResponse, error) {
	f.slug, f.req = slug, req
	return api.AppResponse{}, nil
}

func TestConfigureTestWorkloadAccessClearsDefaultGatesAndKeepsConsumerPolicy(t *testing.T) {
	client := &testAccessFakeClient{}
	if err := configureTestWorkloadAccess(context.Background(), client, "isolated-api", api.ConsumerAuthModeRequired); err != nil {
		t.Fatal(err)
	}
	if client.slug != "isolated-api" || client.req.RequireAuthn == nil || *client.req.RequireAuthn ||
		client.req.PublicAuth == nil || client.req.PublicAuth.Mode != api.AppPublicAuthModeOpen ||
		client.req.ConsumerAuthMode == nil || *client.req.ConsumerAuthMode != api.ConsumerAuthModeRequired {
		t.Fatalf("test access update = (%q, %+v)", client.slug, client.req)
	}
	if err := configureTestWorkloadAccess(context.Background(), client, "isolated-worker", ""); err != nil {
		t.Fatal(err)
	}
	if client.req.ConsumerAuthMode != nil {
		t.Fatalf("worker unexpectedly inherited consumer auth: %+v", client.req)
	}
}

func (f *testConsumerFakeClient) CreateAPIConsumer(_ context.Context, _ string, req api.CreateAPIConsumerRequest) (api.APIConsumerResponse, error) {
	f.created = append(f.created, req)
	return api.APIConsumerResponse{ID: fmt.Sprintf("consumer-%d", len(f.created))}, nil
}

func (f *testConsumerFakeClient) CreateConsumerKey(_ context.Context, _, _ string, req api.CreateConsumerKeyRequest) (api.ConsumerKeyResponse, error) {
	f.keys = append(f.keys, req)
	if f.failKey {
		return api.ConsumerKeyResponse{}, errors.New("key creation failed")
	}
	return api.ConsumerKeyResponse{Key: fmt.Sprintf("secret-%d", len(f.keys))}, nil
}

func TestProvisionTestConsumersReturnsShortLivedCredentialsAndPartialCleanupIDs(t *testing.T) {
	client := &testConsumerFakeClient{}
	env, ids, err := provisionTestConsumers(context.Background(), client, "api-app", []testConsumer{{Name: "customer-a"}, {Name: "customer-b", Scopes: []string{"read"}}}, "run-123", 15*time.Minute)
	if err != nil || len(ids) != 2 || len(env) != 4 {
		t.Fatalf("provision = (%v, %v, %v)", env, ids, err)
	}
	if env[0] != "GREGALE_TEST_CONSUMER_CUSTOMER_A_ID=consumer-1" || env[1] != "GREGALE_TEST_CONSUMER_CUSTOMER_A_KEY=secret-1" ||
		env[2] != "GREGALE_TEST_CONSUMER_CUSTOMER_B_ID=consumer-2" || env[3] != "GREGALE_TEST_CONSUMER_CUSTOMER_B_KEY=secret-2" {
		t.Fatalf("consumer environment = %v", env)
	}
	if len(client.keys[0].Scopes) != 2 || client.keys[0].Scopes[0] != "read" || client.keys[0].Scopes[1] != "write" ||
		len(client.keys[1].Scopes) != 1 || client.keys[1].Scopes[0] != "read" || client.keys[0].ExpiresAt == nil ||
		client.keys[0].ExpiresAt.Before(time.Now().Add(time.Hour)) {
		t.Fatalf("key requests = %+v", client.keys)
	}
	client.failKey = true
	_, partialIDs, err := provisionTestConsumers(context.Background(), client, "api-app", []testConsumer{{Name: "failed-key"}}, "run-123", time.Minute)
	if err == nil || len(partialIDs) != 1 {
		t.Fatalf("failed key cleanup ids = (%v, %v)", partialIDs, err)
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

func TestMaterializeScenarioDeliverySinkIsDeployableSource(t *testing.T) {
	dir, cleanup, err := materializeScenarioFixture(testDeliverySinkFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	config, err := resolveDevSourceConfig(dir)
	if err != nil || config.shape != shapeApp {
		t.Fatalf("delivery sink source shape = (%+v, %v)", config, err)
	}
	for _, file := range []string{"package.json", "server.js"} {
		if _, err := os.Stat(filepath.Join(dir, file)); err != nil {
			t.Fatalf("fixture file %s: %v", file, err)
		}
	}
	archive := filepath.Join(t.TempDir(), "delivery-sink.tar.gz")
	count, err := packDirToTarGz(dir, archive, defaultZeroConfigSourceCapMB, nil)
	if err != nil || count != 2 {
		t.Fatalf("fixture archive = (%d files, %v)", count, err)
	}
}

func TestScenarioAcceptanceFixtureManifestAndSource(t *testing.T) {
	manifest := filepath.Join("..", "..", "tests", "scenario-acceptance", "gregale-test.yaml")
	scenarios, dir, err := readTestManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	scenario, ok := scenarios["delivery-smoke"]
	if !ok || scenario.Services["sink"].Fixture != testDeliverySinkFixture || len(scenario.WaitFor.Deliveries) != 1 {
		t.Fatalf("acceptance scenario = %+v", scenario)
	}
	source, err := resolveDeploySourceDir(dir, scenario.Source)
	if err != nil {
		t.Fatal(err)
	}
	config, err := resolveDevSourceConfig(source)
	if err != nil || config.shape != shapeApp {
		t.Fatalf("acceptance app shape = (%+v, %v)", config, err)
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

type testServiceHotFakeClient struct {
	wakes    []api.WakeTimelineJSONRow
	requests []api.DebugTelemetryRequestItem
}

func (f *testServiceHotFakeClient) GetAppWakeTimeline(context.Context, string, api.AppWakeTimelineOptions) (api.AppWakeTimelineResponse, error) {
	return api.AppWakeTimelineResponse{Rows: f.wakes}, nil
}

func (f *testServiceHotFakeClient) ListAppDebugRequestsWithOptions(context.Context, string, api.DebugTelemetryListOptions) (api.DebugTelemetryListResponse, error) {
	return api.DebugTelemetryListResponse{Requests: f.requests}, nil
}

func TestVerifyTestServiceHotRequiresHandledRequestWithoutNewWake(t *testing.T) {
	cutoff := time.Now().UTC().Add(-time.Minute)
	client := &testServiceHotFakeClient{
		wakes: []api.WakeTimelineJSONRow{{WakeID: "baseline"}},
		requests: []api.DebugTelemetryRequestItem{
			{ID: "new-request", RequestID: "public-request", InstanceID: "vm-1", Status: 202, ReceivedAt: cutoff.Add(time.Second).Format(time.RFC3339Nano)},
			{ID: "late-smoke", RequestID: "smoke", InstanceID: "vm-1", ColdBoot: true, ReceivedAt: cutoff.Add(-time.Second).Format(time.RFC3339Nano)},
			{ID: "baseline-request", InstanceID: "vm-1"},
		},
	}
	wakes := map[string]bool{"baseline": true}
	requests := map[string]bool{"baseline-request": true}
	evidence, err := verifyTestServiceHot(context.Background(), client, "worker-app", wakes, requests, cutoff)
	if err != nil || evidence.RequestID != "public-request" || evidence.InstanceID != "vm-1" || evidence.Status != 202 {
		t.Fatalf("hot service evidence = (%+v, %v)", evidence, err)
	}
	client.wakes = []api.WakeTimelineJSONRow{{WakeID: "unexpected-wake"}, {WakeID: "baseline"}}
	if _, err := verifyTestServiceHot(context.Background(), client, "worker-app", wakes, requests, cutoff); err == nil {
		t.Fatal("new service wake satisfied warm profile")
	}
	client.wakes = []api.WakeTimelineJSONRow{{WakeID: "baseline"}}
	client.requests[0].ColdBoot = true
	if _, err := verifyTestServiceHot(context.Background(), client, "worker-app", wakes, requests, cutoff); err == nil {
		t.Fatal("waking service request satisfied warm profile")
	}
}

func TestWaitForTestDeliveryRequiresWorkloadWakeBeforeInspection(t *testing.T) {
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		if r.URL.Path != "/__gregale_test__/attempts" || r.Header.Get("Authorization") != "Bearer sink-token" {
			t.Errorf("sink inspection request = %s auth %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		if reads == 1 {
			_, _ = w.Write([]byte(`{"attempts":[{"status":503}]}`))
		} else {
			_, _ = w.Write([]byte(`{"attempts":[{"status":503},{"status":200}]}`))
		}
	}))
	defer server.Close()
	condition := testDeliveryOutput{Service: "notifications", MinAttempts: 2, LastStatus: 200}
	client := &testServiceWakeFakeClient{rows: []api.WakeTimelineJSONRow{{WakeID: "baseline"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 650*time.Millisecond)
	defer cancel()
	if _, err := waitForTestDelivery(ctx, client, "sink-app", server.URL, "sink-token", "cold", map[string]bool{"baseline": true}, condition); err == nil {
		t.Fatal("delivery inspection proceeded without a post-trigger sink wake")
	}
	if reads != 0 {
		t.Fatalf("sink was inspected before delivery wake: %d reads", reads)
	}
	client.rows = []api.WakeTimelineJSONRow{{WakeID: "delivery-wake"}, {WakeID: "baseline"}}
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	evidence, err := waitForTestDelivery(ctx, client, "sink-app", server.URL, "sink-token", "cold", map[string]bool{"baseline": true}, condition)
	if err != nil || evidence.Attempts != 2 || len(evidence.Statuses) != 2 || evidence.Statuses[0] != 503 || evidence.Statuses[1] != 200 || reads != 2 {
		t.Fatalf("delivery evidence = (%+v, %v), reads=%d", evidence, err, reads)
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

func TestSimulatedScenarioRunsWithoutPlatformAndOmitsWakeEvidence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GREGALE_TEST_URL", "https://production.example")
	t.Setenv("FAAS_TOKEN", "operator-secret")
	manifest := filepath.Join(dir, "gregale-test.yaml")
	report := filepath.Join(dir, "report.json")
	contents := "version: 1\nscenarios:\n  local-test:\n    project: local-test\n    source: .\n    simulation: [sh, -c, 'test \"$GREGALE_TEST_ENGINE\" = simulated && test -z \"$GREGALE_TEST_URL\" && test -z \"$FAAS_TOKEN\"']\n    command: [sh, -c, 'exit 1']\n"
	if err := os.WriteFile(manifest, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := cmdTest([]string{"--scenario", "local-test", "--engine", "simulated", "--manifest", manifest, "--report", report}); code != 0 {
		t.Fatalf("simulated test exit = %d, want 0", code)
	}
	body, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"engine": "simulated"`) || strings.Contains(string(body), `"evidence"`) || strings.Contains(string(body), `"app_slug"`) {
		t.Fatalf("simulated report mislabels platform evidence: %s", body)
	}
	if code := cmdTest([]string{"--scenario", "local-test", "--engine", "simulated", "--profile", "cold", "--manifest", manifest}); code == 0 {
		t.Fatal("simulated run accepted a VM lifecycle profile")
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
