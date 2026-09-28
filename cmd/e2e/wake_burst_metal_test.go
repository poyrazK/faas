//go:build metal

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	wakeBurstApps   = 4
	wakeBurstRounds = 10
)

type wakeBurstApp struct {
	slug  string
	appID string
}

type wakeBurstRequestResult struct {
	app         wakeBurstApp
	firstByteMS int64
	wakeID      string
	wakeTier    string
	status      int
	body        string
	err         error
}

type wakeBurstTrace struct {
	WakeID                string           `json:"wake_id"`
	App                   string           `json:"app"`
	NodeID                string           `json:"node_id"`
	Method                string           `json:"method"`
	QueuedCount           int              `json:"queued_count"`
	AcceptedToFirstByteMS int64            `json:"accepted_to_first_byte_ms"`
	BootToCompleteMS      int64            `json:"boot_to_complete_ms"`
	BootToFirstByteMS     int64            `json:"boot_to_first_byte_ms"`
	CompleteToFirstByteMS int64            `json:"complete_to_first_byte_ms"`
	RestoreTotalMS        int64            `json:"restore_total_ms"`
	GatewayPhasesMS       map[string]int64 `json:"gateway_phases_ms"`
}

// TestColdWakeBurstPhaseAttributionMetal exercises ten waves of four
// simultaneous snapshot restores through the real gateway, scheduler, VMMD,
// and Firecracker path. It records each wake's placement, restore duration,
// boot/readiness/first-byte boundaries, and request-local gateway phases.
// The output is diagnostic: latency thresholds are enforced by a follow-up
// optimization once the correlated burst data identifies the bottleneck.
//
// Run on a metal runner with:
//
//	go test -tags metal ./cmd/e2e -run '^TestColdWakeBurstPhaseAttributionMetal$' -count=1 -timeout=20m
func TestColdWakeBurstPhaseAttributionMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal concurrent-wake test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}

	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	builderBaseRef := registry.AddImage("onebox-faas/builder-base", builderImg)
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", helloBody)
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideBuilderBase(t, builderBaseRef)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.DeployWake)
	img, _ := e2etest.HelloImageAboveBase("library/hello", helloBody)
	imageRef := registry.AddImage("library/hello", img)
	apps := make([]wakeBurstApp, 0, wakeBurstApps)
	for i := 0; i < wakeBurstApps; i++ {
		slug := fmt.Sprintf("wake-burst-%d", i)
		key := h.SeedAccount(context.Background(), api.PlanHobby, slug)
		falsy := false
		if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
			t.Fatalf("create app %s: status=%d", slug, got)
		}
		appID := mustGetAppID(t, h, key, slug)
		raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/deployments",
			api.CreateDeploymentRequest{Image: imageRef})
		if status != http.StatusAccepted {
			t.Fatalf("create deployment for %s: status=%d body=%s", slug, status, raw)
		}
		var deployment api.DeploymentResponse
		if err := json.Unmarshal(raw, &deployment); err != nil {
			t.Fatalf("decode deployment for %s: %v body=%s", slug, err, raw)
		}
		deployCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		if _, err := e2etest.WaitForDeploymentLive(deployCtx, t, pool, deployment.ID, 60*time.Second); err != nil {
			cancel()
			t.Fatalf("deployment for %s did not become live: %v", slug, err)
		}
		if _, err := e2etest.WaitForInstanceState(deployCtx, t, pool, appID, state.StateParked, 60*time.Second); err != nil {
			cancel()
			t.Fatalf("deployment instance for %s did not park: %v", slug, err)
		}
		cancel()
		apps = append(apps, wakeBurstApp{slug: slug, appID: appID})
	}

	client := h.HTTPClient()
	conn, err := grpc.NewClient("unix://"+h.ScheddSock, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial schedd: %v", err)
	}
	defer func() { _ = conn.Close() }()
	schedd := scheddpb.NewScheddClient(conn)

	// Prime the gateway's app routes sequentially, then return every app to a
	// parked snapshot. The measured waves therefore begin with routable apps
	// and zero running instances.
	for _, app := range apps {
		body, status := doGetWithHost(t, client, gatewayAppURL(h, app.slug), app.slug+".apps.test.example", 60*time.Second)
		if status != http.StatusOK || strings.TrimSpace(string(body)) != helloBody {
			t.Fatalf("prime %s: status=%d body=%q", app.slug, status, body)
		}
	}
	parkWakeBurstApps(t, pool, schedd, apps)

	traces := make([]wakeBurstTrace, 0, wakeBurstApps*wakeBurstRounds)
	for round := 0; round < wakeBurstRounds; round++ {
		parkedCtx, parkedCancel := context.WithTimeout(context.Background(), 30*time.Second)
		for _, app := range apps {
			if _, err := e2etest.WaitForInstanceState(parkedCtx, t, pool, app.appID, state.StateParked, 20*time.Second); err != nil {
				parkedCancel()
				t.Fatalf("round %d app %s is not parked: %v", round, app.slug, err)
			}
		}
		parkedCancel()

		results := runWakeBurstWave(t, client, h.GatewayURL, apps)
		wakeIDs := make(map[string]struct{}, len(results))
		for _, result := range results {
			if result.err != nil {
				t.Fatalf("round %d app %s request failed: %v", round, result.app.slug, result.err)
			}
			if result.status != http.StatusOK || strings.TrimSpace(result.body) != helloBody {
				t.Fatalf("round %d app %s: status=%d body=%q", round, result.app.slug, result.status, result.body)
			}
			if result.wakeTier != "restored" {
				t.Fatalf("round %d app %s wake tier=%q, want restored", round, result.app.slug, result.wakeTier)
			}
			if result.wakeID == "" {
				t.Fatalf("round %d app %s response has no x-faas-wake-id", round, result.app.slug)
			}
			if _, exists := wakeIDs[result.wakeID]; exists {
				t.Fatalf("round %d reused wake ID %s", round, result.wakeID)
			}
			wakeIDs[result.wakeID] = struct{}{}

			trace := collectWakeBurstTrace(t, pool, result, 15*time.Second)
			traces = append(traces, trace)
			encoded, err := json.Marshal(trace)
			if err != nil {
				t.Fatalf("marshal wake trace: %v", err)
			}
			t.Logf("wake_burst_sample=%s client_first_byte_ms=%d", encoded, result.firstByteMS)
		}
		parkWakeBurstApps(t, pool, schedd, apps)
	}

	firstByteSamples := make([]int64, 0, len(traces))
	restoreSamples := make([]int64, 0, len(traces))
	nodePlacement := make(map[string]int)
	for _, trace := range traces {
		firstByteSamples = append(firstByteSamples, trace.AcceptedToFirstByteMS)
		restoreSamples = append(restoreSamples, trace.RestoreTotalMS)
		nodePlacement[trace.NodeID]++
	}
	summary := map[string]any{
		"samples":                   len(traces),
		"first_byte_p50_ms":         wakeBurstPercentile(firstByteSamples, 0.50),
		"first_byte_p95_ms":         wakeBurstPercentile(firstByteSamples, 0.95),
		"boot_to_first_byte_p95_ms": wakeBurstPercentile(durationSamples(traces, func(trace wakeBurstTrace) int64 { return trace.BootToFirstByteMS }), 0.95),
		"restore_p95_ms":            wakeBurstPercentile(restoreSamples, 0.95),
		"node_placement":            nodePlacement,
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal wake burst summary: %v", err)
	}
	t.Logf("wake_burst_summary=%s", encoded)
}

func runWakeBurstWave(t *testing.T, client *http.Client, gatewayURL string, apps []wakeBurstApp) []wakeBurstRequestResult {
	t.Helper()
	ready := make(chan struct{}, len(apps))
	start := make(chan struct{})
	results := make([]wakeBurstRequestResult, len(apps))
	var wg sync.WaitGroup
	for i, app := range apps {
		wg.Add(1)
		go func(i int, app wakeBurstApp) {
			defer wg.Done()
			ready <- struct{}{}
			<-start
			startedAt := time.Now()
			requestCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, gatewayURL+"/", nil)
			if err != nil {
				results[i] = wakeBurstRequestResult{app: app, err: err}
				return
			}
			req.Host = app.slug + ".apps.test.example"
			resp, err := client.Do(req)
			if err != nil {
				results[i] = wakeBurstRequestResult{app: app, err: err}
				return
			}
			firstByteMS := time.Since(startedAt).Milliseconds()
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			results[i] = wakeBurstRequestResult{
				app: app, firstByteMS: firstByteMS,
				wakeID:   resp.Header.Get("x-faas-wake-id"),
				wakeTier: resp.Header.Get("x-faas-wake"), status: resp.StatusCode,
				body: string(body), err: readErr,
			}
		}(i, app)
	}
	for range apps {
		<-ready
	}
	close(start)
	wg.Wait()
	return results
}

func collectWakeBurstTrace(t *testing.T, pool *pgxpool.Pool, result wakeBurstRequestResult, deadline time.Duration) wakeBurstTrace {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	store := state.NewPgStore(pool)
	poll := time.NewTicker(100 * time.Millisecond)
	defer poll.Stop()
	end := time.Now().Add(deadline)
	var rows []state.Event
	for {
		var err error
		rows, err = store.ListEventsByWakeID(ctx, result.wakeID, time.Time{}, 100)
		if err != nil {
			t.Fatalf("list wake events for %s: %v", result.wakeID, err)
		}
		byKind := make(map[string]state.Event, len(rows))
		for _, row := range rows {
			byKind[row.Kind] = row
		}
		if _, ok := byKind[events.WakeQueueAccepted]; ok {
			if _, ok := byKind[events.WakeBootStarted]; ok {
				if _, ok := byKind[events.WakeRestoreBreakdown]; ok {
					if _, ok := byKind[events.WakeReadiness200]; ok {
						if _, ok := byKind[events.WakeBootCompleted]; ok {
							if _, ok := byKind[events.WakeProxyFirstByte]; ok {
								return decodeWakeBurstTrace(t, result, byKind)
							}
						}
					}
				}
			}
		}
		if time.Now().After(end) {
			t.Fatalf("wake %s missing correlated timeline rows (got %v)", result.wakeID, wakeBurstKinds(rows))
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for wake %s timeline: %v (got %v)", result.wakeID, ctx.Err(), wakeBurstKinds(rows))
		case <-poll.C:
		}
	}
}

func decodeWakeBurstTrace(t *testing.T, result wakeBurstRequestResult, byKind map[string]state.Event) wakeBurstTrace {
	t.Helper()
	var started struct {
		AppID       string `json:"app_id"`
		InstanceID  string `json:"instance_id"`
		NodeID      string `json:"node_id"`
		Method      string `json:"method"`
		QueuedCount int    `json:"queued_count"`
	}
	var completed struct {
		Method string `json:"method"`
	}
	var restore struct {
		TotalMS int64 `json:"total_ms"`
	}
	var firstByte struct {
		AppID           string           `json:"app_id"`
		WakeID          string           `json:"wake_id"`
		LatencyMS       int64            `json:"latency_ms"`
		GatewayPhasesMS map[string]int64 `json:"gateway_phases_ms"`
	}
	for kind, target := range map[string]any{
		events.WakeBootStarted:      &started,
		events.WakeBootCompleted:    &completed,
		events.WakeRestoreBreakdown: &restore,
		events.WakeProxyFirstByte:   &firstByte,
	} {
		if err := json.Unmarshal(byKind[kind].Data, target); err != nil {
			t.Fatalf("decode %s for wake %s: %v (data=%s)", kind, result.wakeID, err, byKind[kind].Data)
		}
	}
	if firstByte.WakeID != result.wakeID || firstByte.AppID != result.app.appID || started.AppID != result.app.appID {
		t.Fatalf("wake/event identity mismatch: response wake=%s app=%s, started app=%s, first-byte=%s/%s",
			result.wakeID, result.app.appID, started.AppID, firstByte.WakeID, firstByte.AppID)
	}
	if completed.Method != "restore" || started.Method != "restore" {
		t.Fatalf("wake %s method planned=%q completed=%q, want restore", result.wakeID, started.Method, completed.Method)
	}
	if started.QueuedCount != 0 {
		t.Fatalf("wake %s queued_count=%d, want zero for independent cold apps", result.wakeID, started.QueuedCount)
	}
	for _, phase := range []string{"pre_admission", "scheduler_wake", "target_publication", "post_publication", "internal_proxy"} {
		if _, ok := firstByte.GatewayPhasesMS[phase]; !ok {
			t.Fatalf("wake %s has no gateway phase %q: %#v", result.wakeID, phase, firstByte.GatewayPhasesMS)
		}
	}
	startedAt := byKind[events.WakeBootStarted].At
	completedAt := byKind[events.WakeBootCompleted].At
	firstByteAt := byKind[events.WakeProxyFirstByte].At
	return wakeBurstTrace{
		WakeID: result.wakeID, App: result.app.slug, NodeID: started.NodeID, Method: completed.Method,
		QueuedCount: started.QueuedCount, AcceptedToFirstByteMS: firstByte.LatencyMS,
		BootToCompleteMS:      completedAt.Sub(startedAt).Milliseconds(),
		BootToFirstByteMS:     firstByteAt.Sub(startedAt).Milliseconds(),
		CompleteToFirstByteMS: firstByteAt.Sub(completedAt).Milliseconds(),
		RestoreTotalMS:        restore.TotalMS, GatewayPhasesMS: firstByte.GatewayPhasesMS,
	}
}

func parkWakeBurstApps(t *testing.T, pool *pgxpool.Pool, schedd scheddpb.ScheddClient, apps []wakeBurstApp) {
	t.Helper()
	for _, app := range apps {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		instances, err := e2etest.WaitForInstanceState(ctx, t, pool, app.appID, state.StateRunning, 15*time.Second)
		if err != nil {
			cancel()
			t.Fatalf("app %s has no running instance to park: %v", app.slug, err)
		}
		if len(instances) == 0 {
			cancel()
			t.Fatalf("app %s has no running instance to park", app.slug)
		}
		for _, instance := range instances {
			if _, err := schedd.ParkInstance(ctx, &scheddpb.ParkInstanceRequest{InstanceId: instance.ID, Reason: "e2e_wake_burst"}); err != nil {
				cancel()
				t.Fatalf("park app %s instance %s: %v", app.slug, instance.ID, err)
			}
		}
		if _, err := e2etest.WaitForInstanceState(ctx, t, pool, app.appID, state.StateParked, 15*time.Second); err != nil {
			cancel()
			t.Fatalf("app %s did not park: %v", app.slug, err)
		}
		cancel()
	}
}

func wakeBurstKinds(rows []state.Event) []string {
	kinds := make([]string, 0, len(rows))
	for _, row := range rows {
		kinds = append(kinds, row.Kind)
	}
	return kinds
}

func durationSamples(traces []wakeBurstTrace, value func(wakeBurstTrace) int64) []int64 {
	samples := make([]int64, 0, len(traces))
	for _, trace := range traces {
		samples = append(samples, value(trace))
	}
	return samples
}

func wakeBurstPercentile(samples []int64, quantile float64) int64 {
	if len(samples) == 0 {
		return 0
	}
	sorted := append([]int64(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rank := int(math.Ceil(quantile*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}
