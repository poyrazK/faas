//go:build metal

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// TestQueuePushParallelDispatchMetal is the ADR-933 acceptance: a slow push
// handler behind a binding with max_concurrency > 1 receives deliveries in
// parallel through the real schedd → gatewayd → guest path, every message
// completes on its first attempt, and nothing is dead-lettered.
//
// Push consumers are function apps (ADR-231); worker-mode apps never join the
// gateway target set (ADR-051) and consume through pull bindings instead.
func TestQueuePushParallelDispatchMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping parallel queue push acceptance")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping parallel queue push acceptance")
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
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builderImg))
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "push-parallel")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	// Function deploys build from source, so builderd is required.
	h := e2etest.Start(t, pool, e2etest.All)
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	const slug = "push-parallel"
	key := h.SeedAccount(context.Background(), api.PlanPro)
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{
		Slug: slug, Type: "function", Runtime: "node22", RequireAuthn: &falsy,
	}); got != http.StatusCreated {
		t.Fatalf("create function app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, slug)

	depID := postFunctionDeployment(t, h, key, slug, slowFunctionFixture(t))
	deployCtx, deployCancel := context.WithTimeout(context.Background(), 16*time.Minute)
	defer deployCancel()
	if _, err := e2etest.WaitForDeploymentLive(deployCtx, t, pool, depID, 15*time.Minute); err != nil {
		t.Fatalf("function deployment did not reach live: %v", err)
	}

	const maxConcurrency = 4
	enabled := true
	if body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/queue-bindings", api.CreateQueueBindingRequest{
		Name: "jobs", QueueName: "jobs", Mode: "push", WorkloadClass: "http", Enabled: &enabled, MaxConcurrency: maxConcurrency,
	}); status != http.StatusCreated {
		t.Fatalf("create push binding: status=%d body=%s", status, body)
	}

	const messages = 12
	started := time.Now()
	for i := range messages {
		body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/queues/send",
			api.QueueSendRequest{QueueName: "jobs", Payload: json.RawMessage(fmt.Sprintf(`{"n":%d}`, i))})
		if status != http.StatusCreated {
			t.Fatalf("send %d: status=%d body=%s", i, status, body)
		}
	}

	// Serial delivery of 12 two-second messages takes at least 24 s. Watch
	// in-flight deliveries and live instances until every message settles.
	peakInFlight, peakInstances := 0, 0
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		var inFlight, settled, instances int
		if err := h.Pool.QueryRow(context.Background(), `select
			count(*) filter (where state = 'dispatching'),
			count(*) filter (where state in ('completed','failed','dead_letter'))
			from invocations where app_id = $1 and source = 'queue'`, appID).Scan(&inFlight, &settled); err != nil {
			t.Fatalf("queue state: %v", err)
		}
		if err := h.Pool.QueryRow(context.Background(), `select count(*) from instances
			where app_id = $1 and state in ('waking','cold_booting','running')`, appID).Scan(&instances); err != nil {
			t.Fatalf("instance count: %v", err)
		}
		peakInFlight, peakInstances = max(peakInFlight, inFlight), max(peakInstances, instances)
		if inFlight > maxConcurrency {
			t.Fatalf("%d deliveries in flight, above the binding's max_concurrency %d", inFlight, maxConcurrency)
		}
		if settled == messages {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	elapsed := time.Since(started)
	t.Logf("ADR-933 evidence: %d messages settled in %s; peak in-flight %d; peak live instances %d",
		messages, elapsed.Round(100*time.Millisecond), peakInFlight, peakInstances)

	rows, err := h.Pool.Query(context.Background(),
		`select id::text, state, attempts from invocations where app_id = $1 and source = 'queue'`, appID)
	if err != nil {
		t.Fatalf("list queue rows: %v", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id, state string
		var attempts int
		if err := rows.Scan(&id, &state, &attempts); err != nil {
			t.Fatalf("scan queue row: %v", err)
		}
		count++
		if state != "completed" || attempts != 1 {
			t.Fatalf("message %s finished %s after %d attempts; parallel lanes must not cost delivery attempts", id, state, attempts)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list queue rows: %v", err)
	}
	if count != messages {
		t.Fatalf("found %d queue rows, sent %d", count, messages)
	}
	if peakInFlight < 2 {
		t.Fatalf("deliveries never overlapped (peak in-flight %d); ADR-933 lanes did not run in parallel", peakInFlight)
	}
}

// slowFunctionFixture is a node22 function handler that holds each
// invocation for two seconds, so overlapping deliveries are observable.
func slowFunctionFixture(t *testing.T) []byte {
	t.Helper()
	return buildTarGz(t, map[string]string{"handler.js": `// ADR-933 slow push consumer.
exports.handler = async () => {
  await new Promise((resolve) => setTimeout(resolve, 2000));
  return { statusCode: 200, body: 'done' };
};
`})
}

// postFunctionDeployment uploads a function source tarball with the CLI's
// runtime/handler form fields and returns the deployment id.
func postFunctionDeployment(t *testing.T, h *e2etest.Harness, key, slug string, source []byte) string {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("source", "src.tar.gz")
	if err != nil {
		t.Fatalf("multipart source: %v", err)
	}
	if _, err := part.Write(source); err != nil {
		t.Fatalf("multipart write: %v", err)
	}
	for field, value := range map[string]string{"runtime": "node22", "handler": "handler.handler"} {
		if err := mw.WriteField(field, value); err != nil {
			t.Fatalf("multipart %s: %v", field, err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("multipart close: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/v1/apps/%s/deployments", h.APIDURL, slug), &body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("function deploy: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("function deploy status=%d body=%s", resp.StatusCode, raw)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.ID == "" {
		t.Fatalf("decode function deploy: %v body=%s", err, raw)
	}
	return created.ID
}
