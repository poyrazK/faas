//go:build metal

package e2e_test

// adr: 638

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestDurableEntityNativeParkRestoreMetal uses real daemon processes and a
// Firecracker guest. The separate S3 wire fixture survives apid replacement;
// it does not qualify the durability, latency or cost of a live provider.
func TestDurableEntityNativeParkRestoreMetal(t *testing.T) {
	if !metalAvailable(t) {
		return
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatal(err)
	}
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(registry.Close)
	base, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", base))
	deployBase, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "durable-counter")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBase)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")
	fixture := e2etest.NewDurableEntityS3Fixture(t)
	h := e2etest.Start(t, pool, e2etest.DeployWake)
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	counter := deployNativeDurableCounter(t, ctx, h, registry)
	for _, entry := range fixture.APIDEnv(t, counter.appID) {
		name, value, _ := strings.Cut(entry, "=")
		if err := h.SetAPIDEnv(name, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.RestartAPID(); err != nil {
		t.Fatal(err)
	}

	assertNativeDurableCounter(t, ctx, h, counter, "one", `{"delta":1}`, 1, 1, false)
	firstWake := nativeDurableCounterWake(t, ctx, h, counter, "one")
	parkNativeDurableCounter(t, ctx, h, counter)
	assertNativeDurableCounter(t, ctx, h, counter, "one", `{"delta":1}`, 1, 1, true)
	assertNativeDurableHandlers(t, ctx, h, counter, 1)
	assertNativeDurableParked(t, ctx, h, counter)
	assertNativeDurableCounter(t, ctx, h, counter, "two", `{"delta":1}`, 2, 2, false)
	secondWake := assertNativeDurableRestore(t, ctx, h, counter, "two", firstWake)
	parkNativeDurableCounter(t, ctx, h, counter)

	// Kill and reap only the harness-owned apid. The bucket, SQL invocation
	// ledger and other daemons stay alive. No request or lease is in flight.
	if err := h.KillAPID(); err != nil {
		t.Fatal(err)
	}
	if err := h.RestartAPID(); err != nil {
		t.Fatal(err)
	}
	assertNativeDurableCounter(t, ctx, h, counter, "one", `{"delta":1}`, 1, 1, true)
	assertNativeDurableCounter(t, ctx, h, counter, "two", `{"delta":1}`, 2, 2, true)
	assertNativeDurableHandlers(t, ctx, h, counter, 2)
	assertNativeDurableParked(t, ctx, h, counter)
	assertNativeDurableCounter(t, ctx, h, counter, "three", `{"delta":1}`, 3, 3, false)
	thirdWake := assertNativeDurableRestore(t, ctx, h, counter, "three", secondWake)
	assertNativeDurableProblem(t, ctx, h, counter, "one", `{"delta":10}`, http.StatusConflict, "durable_entity_request_conflict")
	assertNativeDurableHandlers(t, ctx, h, counter, 3)
	assertNativeDurableProblem(t, ctx, h, counter, "bad", `{"delta":"invalid"}`, http.StatusBadGateway, "durable_entity_handler_failed")
	assertNativeDurableCounter(t, ctx, h, counter, "four", `{"delta":1}`, 4, 4, false)
	assertNativeDurableCounter(t, ctx, h, counter, "one", `{"delta":1}`, 1, 1, true)
	assertNativeDurableHandlers(t, ctx, h, counter, 5)
	evidence, err := json.Marshal(map[string]any{
		"schema_version": 1, "runtime": "native_kvm", "provider": "s3_wire_fixture", "live_provider_qualified": false,
		"apid_replaced": true, "inflight_owner_takeover_tested": false, "restore_wake_ids": []string{secondWake, thirdWake},
		"successful_transitions": 4, "receipt_replays": 4, "request_conflicts": 1, "failed_handlers": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("durable entity native acceptance: %s", evidence)
}

type nativeDurableCounter struct{ key, slug, appID string }

func deployNativeDurableCounter(t *testing.T, ctx context.Context, h *e2etest.Harness, registry *e2etest.FakeRegistry) nativeDurableCounter {
	t.Helper()
	c := nativeDurableCounter{key: h.SeedAccount(ctx, api.PlanPro, "durable-native-"+randHexSuffix()), slug: "durable-native-" + randHexSuffix()}
	falsy := false
	if body, status := nativeDurableAPIRequest(t, ctx, h, c.key, "/v1/apps", api.CreateAppRequest{
		Slug: c.slug, Type: "app", RequireAuthn: &falsy, RevisionPinTTLSeconds: 3600,
	}); status != http.StatusCreated {
		t.Fatalf("create counter: status=%d body=%s", status, body)
	}
	app, err := state.NewPgStore(h.Pool).AppBySlug(ctx, c.slug)
	if err != nil {
		t.Fatal(err)
	}
	c.appID = app.ID
	image, _ := e2etest.DurableCounterImageAboveBase("library/durable-counter")
	ref := registry.AddImage("library/durable-counter", image)
	body, status := nativeDurableAPIRequest(t, ctx, h, c.key, "/v1/apps/"+c.slug+"/deployments", api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("deploy counter: status=%d body=%s", status, body)
	}
	var deployment api.DeploymentResponse
	if err := json.Unmarshal(body, &deployment); err != nil {
		t.Fatal(err)
	}
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, h.Pool, deployment.ID, 90*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, h.Pool, c.appID, state.StateParked, 60*time.Second); err != nil {
		t.Fatal(err)
	}
	return c
}

func callNativeDurableCounter(t *testing.T, ctx context.Context, h *e2etest.Harness, c nativeDurableCounter, requestID, payload string) ([]byte, int) {
	t.Helper()
	return nativeDurableAPIRequest(t, ctx, h, c.key, "/v1/apps/"+c.slug+"/entities/invoke", api.DurableEntityInvokeRequest{
		Namespace: "counters", Key: "native-acceptance", RequestID: requestID, Payload: json.RawMessage(payload),
	})
}

func nativeDurableAPIRequest(t *testing.T, ctx context.Context, h *e2etest.Harness, key, path string, payload any) ([]byte, int) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, h.APIDURL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	response, err := h.HTTPClient().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	result, err := io.ReadAll(io.LimitReader(response.Body, api.MaxDurableEntityInvocationBytes))
	if err != nil {
		t.Fatal(err)
	}
	return result, response.StatusCode
}

func assertNativeDurableCounter(t *testing.T, ctx context.Context, h *e2etest.Harness, c nativeDurableCounter, requestID, payload string, version uint64, count int64, replayed bool) {
	t.Helper()
	body, status := callNativeDurableCounter(t, ctx, h, c, requestID, payload)
	if status != http.StatusOK {
		t.Fatalf("counter %s: status=%d body=%s", requestID, status, body)
	}
	var result api.DurableEntityInvokeResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	var value struct {
		Count int64 `json:"count"`
	}
	if err := json.Unmarshal(result.Value, &value); err != nil {
		t.Fatal(err)
	}
	if result.Version != version || result.Replayed != replayed || value.Count != count {
		t.Fatalf("counter %s: version=%d count=%d replayed=%t; want %d/%d/%t", requestID, result.Version, value.Count, result.Replayed, version, count, replayed)
	}
}

func assertNativeDurableProblem(t *testing.T, ctx context.Context, h *e2etest.Harness, c nativeDurableCounter, requestID, payload string, status int, code string) {
	t.Helper()
	body, got := callNativeDurableCounter(t, ctx, h, c, requestID, payload)
	var problem api.Problem
	if err := json.Unmarshal(body, &problem); err != nil {
		t.Fatal(err)
	}
	if got != status || problem.Code != code {
		t.Fatalf("counter problem: status=%d body=%s; want %d/%s", got, body, status, code)
	}
}

func assertNativeDurableHandlers(t *testing.T, ctx context.Context, h *e2etest.Harness, c nativeDurableCounter, want int) {
	t.Helper()
	var count int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM invocations WHERE app_id=$1::uuid AND path=$2`, c.appID, api.DurableEntityHandlerPath).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("entity handler invocations=%d, want %d; receipt replay/conflict must not dispatch", count, want)
	}
}

func nativeDurableCounterWake(t *testing.T, ctx context.Context, h *e2etest.Harness, c nativeDurableCounter, requestID string) string {
	t.Helper()
	var instanceID string
	if err := h.Pool.QueryRow(ctx, `SELECT instance_id FROM invocations WHERE app_id=$1::uuid AND path=$2 AND payload->>'request_id'=$3 AND state='completed'`, c.appID, api.DurableEntityHandlerPath, requestID).Scan(&instanceID); err != nil {
		t.Fatal(err)
	}
	instance, err := state.NewPgStore(h.Pool).InstanceByID(ctx, instanceID)
	if err != nil {
		t.Fatal(err)
	}
	if instance.WakeID == "" {
		t.Fatal("completed entity invocation has no native wake identity")
	}
	return instance.WakeID
}

func assertNativeDurableRestore(t *testing.T, ctx context.Context, h *e2etest.Harness, c nativeDurableCounter, requestID, priorWake string) string {
	t.Helper()
	wakeID := nativeDurableCounterWake(t, ctx, h, c, requestID)
	if wakeID == priorWake {
		t.Fatal("new transition reused the pre-park wake identity")
	}
	if _, err := e2etest.WaitForWakeMethod(ctx, t, h.Pool, wakeID, "restore", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	return wakeID
}

func parkNativeDurableCounter(t *testing.T, ctx context.Context, h *e2etest.Harness, c nativeDurableCounter) {
	t.Helper()
	instance, err := state.NewPgStore(h.Pool).RunningInstanceForApp(ctx, c.appID)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := grpc.NewClient("unix://"+h.ScheddSock, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	parkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := scheddpb.NewScheddClient(conn).ParkInstance(parkCtx, &scheddpb.ParkInstanceRequest{InstanceId: instance.ID, Reason: "e2e_durable_entity"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, h.Pool, c.appID, state.StateParked, 60*time.Second); err != nil {
		t.Fatal(err)
	}
}

func assertNativeDurableParked(t *testing.T, ctx context.Context, h *e2etest.Harness, c nativeDurableCounter) {
	t.Helper()
	instances, err := state.NewPgStore(h.Pool).ListInstancesForApp(ctx, c.appID)
	if err != nil {
		t.Fatal(err)
	}
	parked := false
	for _, instance := range instances {
		if instance.State == string(state.StateParked) {
			parked = true
		}
		if instance.State == string(state.StateRunning) || instance.State == string(state.StateWaking) || instance.State == string(state.StateColdBooting) {
			t.Fatalf("receipt replay woke a guest: %s/%s", instance.ID, instance.State)
		}
	}
	if !parked {
		t.Fatal("no parked counter instance remains")
	}
}
