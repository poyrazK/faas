package e2e_test

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestE2E_ServiceRetryBudget_TwoGatewayCanary is the fleet canary for the
// shared retry budget. Two real gatewayd-internal processes use independent
// local state but the same Redis backend. A pair of failing originals must
// admit only one replay across the fleet; when Redis becomes unavailable,
// each gateway must keep serving originals while denying its replay.
func TestE2E_ServiceRetryBudget_TwoGatewayCanary(t *testing.T) {
	redis := miniredis.RunT(t)
	runServiceRetryBudgetCanary(t, "redis://"+redis.Addr(), func(*normalPathFixture) { redis.Close() })
}

// ADR-570: a missing Redis setting uses the authoritative Postgres budget.
func TestE2E_ServiceRetryBudget_PostgresDefaultTwoGatewayCanary(t *testing.T) {
	runServiceRetryBudgetCanary(t, "", func(f *normalPathFixture) {
		if _, err := f.h.Pool.Exec(f.ctx, "ALTER TABLE traffic_retry_counters RENAME TO traffic_retry_counters_offline"); err != nil {
			t.Fatal(err)
		}
	})
}

func runServiceRetryBudgetCanary(t *testing.T, redisURL string, failBackend func(*normalPathFixture)) {
	t.Helper()
	t.Setenv("FAAS_GATEWAY_RETRY_BUDGET_REDIS_URL", "")
	t.Setenv("FAAS_GATEWAY_RETRY_BUDGET_REDIS_URL_FILE", "")
	const (
		callerSlug    = "retry-budget-canary-caller"
		sharedTarget  = "retry-budget-shared"
		outageTarget  = "retry-budget-outage"
		secondaryName = "retry-budget-gateway-b"
	)
	targets := []string{sharedTarget, outageTarget}
	reliability := api.ServiceReliabilityPolicy{MaxAttempts: 2, RetryBudgetPercent: 10}
	request := api.CreateAppRequest{
		Slug:                  callerSlug,
		Type:                  string(state.AppTypeApp),
		RequireAuthn:          boolPtr(false),
		ServiceBindingTargets: &targets,
		ServiceReliability: map[string]api.ServiceReliabilityPolicy{
			sharedTarget: reliability,
			outageTarget: reliability,
		},
	}
	f := newNormalPathFixtureWithRequest(t, callerSlug, api.PlanPro, 0, request,
		"FAAS_E2E_GATEWAY_NODE_NAME="+state.DefaultLocalNodeName,
		"FAAS_GATEWAY_RETRY_BUDGET_REDIS_URL="+redisURL)
	if f == nil {
		return // pgtest skips when PostgreSQL is unavailable.
	}
	secondaryControlURL := startRetryBudgetCanarySecondary(t, f, secondaryName, redisURL)

	sharedApp := createServiceApp(t, f, sharedTarget, nil)
	sharedDeployment, sharedFirst := createNormalPathLiveDeployment(t, f, sharedApp.ID, "shared-first")
	sharedSecond := createRetryBudgetCanarySibling(t, f, sharedApp.ID, sharedDeployment.ID, "shared-second")
	f.vmmd.FailAll(sharedFirst.ID, status.Error(codes.Unavailable, "canary stale target"))
	f.vmmd.FailAll(sharedSecond.ID, status.Error(codes.Unavailable, "canary stale target"))

	before := f.vmmd.ForwardCount()
	statuses := runRetryBudgetCanaryCalls(t, f.app.ID, sharedTarget,
		[]string{f.h.GatewayControlURL, secondaryControlURL}, "/shared")
	for i, code := range statuses {
		if code != http.StatusServiceUnavailable {
			t.Errorf("shared-budget request %d status=%d, want 503 from the injected target failures", i, code)
		}
	}
	if got := f.vmmd.ForwardCount() - before; got != 3 {
		t.Fatalf("shared-budget VMMD forwards=%d, want 3 (two originals plus exactly one fleet-wide replay)", got)
	}

	primaryMetrics := readRetryBudgetCanaryMetrics(t, f.h.GatewayControlURL)
	secondaryMetrics := readRetryBudgetCanaryMetrics(t, secondaryControlURL)
	assertSharedRetryBudgetMetrics(t, primaryMetrics, secondaryMetrics)
	if got := retryBudgetCanaryCounter(primaryMetrics, "admit", "allowed") + retryBudgetCanaryCounter(secondaryMetrics, "admit", "allowed"); got != 1 {
		t.Errorf("fleet admitted replay counter=%v, want 1", got)
	}

	outageApp := createServiceApp(t, f, outageTarget, nil)
	outageDeployment, outageFirst := createNormalPathLiveDeployment(t, f, outageApp.ID, "outage-first")
	outageSecond := createRetryBudgetCanarySibling(t, f, outageApp.ID, outageDeployment.ID, "outage-second")
	f.vmmd.FailAll(outageFirst.ID, status.Error(codes.Unavailable, "canary stale target"))
	f.vmmd.FailAll(outageSecond.ID, status.Error(codes.Unavailable, "canary stale target"))
	failBackend(f)

	before = f.vmmd.ForwardCount()
	statuses = runRetryBudgetCanaryCalls(t, f.app.ID, outageTarget,
		[]string{f.h.GatewayControlURL, secondaryControlURL}, "/outage")
	for i, code := range statuses {
		if code != http.StatusServiceUnavailable {
			t.Errorf("Shared-store-outage request %d status=%d, want original 503", i, code)
		}
	}
	if got := f.vmmd.ForwardCount() - before; got != 2 {
		t.Fatalf("Shared-store-outage VMMD forwards=%d, want 2 originals and no replay", got)
	}
	primaryMetrics = readRetryBudgetCanaryMetrics(t, f.h.GatewayControlURL)
	secondaryMetrics = readRetryBudgetCanaryMetrics(t, secondaryControlURL)
	if retryBudgetCanaryCounter(primaryMetrics, "observe", "error") < 1 || retryBudgetCanaryCounter(secondaryMetrics, "observe", "error") < 1 {
		t.Errorf("each gateway should record a shared original-observation error; primary=%v secondary=%v",
			retryBudgetCanaryCounter(primaryMetrics, "observe", "error"),
			retryBudgetCanaryCounter(secondaryMetrics, "observe", "error"))
	}
}

func startRetryBudgetCanarySecondary(t *testing.T, f *normalPathFixture, nodeName, redisURL string) string {
	t.Helper()
	primary, err := f.store.ComputeNodeByName(f.ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("load primary compute node: %v", err)
	}
	role := "compute-only"
	primary.Role = &role
	primaryGatewayTarget := "tcp://" + strings.TrimPrefix(f.h.GatewayURL, "http://")
	primary.GatewayTargetURL = &primaryGatewayTarget
	primary.TargetURL = "unix://" + f.h.VMMDSock
	if _, err := f.store.UpsertComputeNodeFromOperator(f.ctx, primary); err != nil {
		t.Fatalf("register primary gateway: %v", err)
	}

	secondary := primary
	secondary.ID = ""
	secondary.Name = nodeName
	missingURL := "tcp://127.0.0.1:1"
	secondary.GatewayTargetURL = &missingURL
	if _, err := f.store.UpsertComputeNodeFromOperator(f.ctx, secondary); err != nil {
		t.Fatalf("register secondary gateway: %v", err)
	}
	publicURL, controlURL := f.h.StartAdditionalGatewayWithControl(nodeName,
		"FAAS_GATEWAY_RETRY_BUDGET_REDIS_URL="+redisURL)
	secondaryGatewayTarget := "tcp://" + strings.TrimPrefix(publicURL, "http://")
	secondary.GatewayTargetURL = &secondaryGatewayTarget
	if _, err := f.store.UpsertComputeNodeFromOperator(f.ctx, secondary); err != nil {
		t.Fatalf("publish secondary gateway endpoint: %v", err)
	}
	return controlURL
}

func createRetryBudgetCanarySibling(t *testing.T, f *normalPathFixture, appID, deploymentID, version string) state.Instance {
	t.Helper()
	instance, err := f.store.CreateInstance(f.ctx, appID, deploymentID,
		string(state.StateRunning), e2etest.FakeSnapshotRAMMB, f.nodeID, "")
	if err != nil {
		t.Fatalf("create %s canary sibling: %v", version, err)
	}
	f.vmmd.SetVersion(instance.ID, version)
	return instance
}

func runRetryBudgetCanaryCalls(t *testing.T, callerAppID, target string, controlURLs []string, path string) []int {
	t.Helper()
	type result struct {
		status int
		err    error
	}
	start := make(chan struct{})
	results := make(chan result, len(controlURLs))
	for _, controlURL := range controlURLs {
		go func(controlURL string) {
			<-start
			status, err := retryBudgetCanaryServiceCall(controlURL, callerAppID, target, path)
			results <- result{status: status, err: err}
		}(controlURL)
	}
	close(start)
	statuses := make([]int, 0, len(controlURLs))
	for range controlURLs {
		result := <-results
		if result.err != nil {
			t.Errorf("canary service call: %v", result.err)
		}
		statuses = append(statuses, result.status)
	}
	return statuses
}

func retryBudgetCanaryServiceCall(controlURL, callerAppID, target, path string) (int, error) {
	req, err := http.NewRequest(http.MethodGet, controlURL+"/v1/internal/services/"+target+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-Faas-Caller-App", callerAppID)
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

func readRetryBudgetCanaryMetrics(t *testing.T, controlURL string) string {
	t.Helper()
	resp, err := http.Get(controlURL + "/metrics")
	if err != nil {
		t.Fatalf("scrape canary metrics from %s: %v", controlURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read canary metrics: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scrape canary metrics status=%d, body=%s", resp.StatusCode, body)
	}
	return string(body)
}

func assertSharedRetryBudgetMetrics(t *testing.T, primary, secondary string) {
	t.Helper()
	for name, metrics := range map[string]string{"primary": primary, "secondary": secondary} {
		shared, ok := retryBudgetCanaryGauge(metrics, "gateway_retry_budget_shared")
		if !ok || shared != 1 {
			t.Errorf("%s gateway shared-mode gauge=%v present=%v, want 1", name, shared, ok)
		}
	}
	primaryID := retryBudgetCanaryBackendID(primary)
	secondaryID := retryBudgetCanaryBackendID(secondary)
	if primaryID == "" || primaryID != secondaryID {
		t.Errorf("gateway retry-budget backend IDs differ: primary=%q secondary=%q", primaryID, secondaryID)
	}
}

func retryBudgetCanaryGauge(metrics, name string) (float64, bool) {
	for _, line := range strings.Split(metrics, "\n") {
		if !strings.HasPrefix(line, name+" ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return 0, false
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		return value, err == nil
	}
	return 0, false
}

func retryBudgetCanaryBackendID(metrics string) string {
	const prefix = `gateway_retry_budget_backend_info{backend_id="`
	for _, line := range strings.Split(metrics, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		labelValue := strings.TrimPrefix(line, prefix)
		if end := strings.Index(labelValue, `"}`); end >= 0 {
			return labelValue[:end]
		}
	}
	return ""
}

func retryBudgetCanaryCounter(metrics, operation, result string) float64 {
	const prefix = "gateway_retry_budget_backend_operations_total{"
	for _, line := range strings.Split(metrics, "\n") {
		if !strings.HasPrefix(line, prefix) ||
			!strings.Contains(line, `operation="`+operation+`"`) ||
			!strings.Contains(line, `result="`+result+`"`) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return 0
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return 0
		}
		return value
	}
	return 0
}
