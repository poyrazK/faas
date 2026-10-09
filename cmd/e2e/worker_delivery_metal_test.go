//go:build metal

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// TestWorkerPoolPushDeliveryAcrossScaleInMetal is the delivery half of the
// worker pool acceptance. The KVM-free e2e proves the scaling loop; this one
// runs real guests behind the real gateway so a push delivery that lands on
// a worker being stopped shows up as a retried attempt or a dead letter.
//
// Every message must complete on its first attempt while the pool scales out
// and back to zero, and every worker must leave through a clean stop.
func TestWorkerPoolPushDeliveryAcrossScaleInMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping worker delivery acceptance")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping worker delivery acceptance")
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
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "full-rootfs")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.DeployWake)
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	const slug = "worker-delivery"
	key := h.SeedAccount(context.Background(), api.PlanPro)
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, slug)
	poolMax := api.MustLimitsFor(api.PlanPro).WorkerReplicasMax

	workerMode := api.ExecutionModeWorker
	if body, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/"+slug, api.UpdateAppRequest{
		ExecutionMode:  &workerMode,
		WorkerReplicas: &api.WorkerScaling{Min: 0, Max: poolMax},
		ScalingPolicy: &api.ScalingPolicy{
			Targets:           []api.ScalingTarget{{Metric: api.ScalingMetricQueueDepth, Value: 2}},
			ScaleOutCooldownS: api.MinScaleOutCooldownS,
			ScaleInCooldownS:  api.MinScaleInCooldownS,
		},
	}); status != http.StatusOK {
		t.Fatalf("PATCH worker app: status=%d body=%s", status, body)
	}
	enabled := true
	if body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/queue-bindings", api.CreateQueueBindingRequest{
		Name: "jobs", QueueName: "jobs", Mode: "push", WorkloadClass: "worker", Enabled: &enabled, MaxConcurrency: poolMax,
	}); status != http.StatusCreated {
		t.Fatalf("create push queue binding: status=%d body=%s", status, body)
	}

	// Each delivery holds its worker for two seconds, so a burst builds a
	// backlog the pool must scale out for, and the last deliveries are still
	// in flight when demand starts falling.
	image, _ := e2etest.HelloImageWithDelay("library/slow-worker", "worker done", 2*time.Second)
	ref := registry.AddImage("library/slow-worker", image)
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/deployments", api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, body)
	}
	deployCtx, deployCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer deployCancel()
	if _, err := e2etest.WaitForDeploymentLive(deployCtx, t, pool, parseImageDeployment(t, body), 75*time.Second); err != nil {
		t.Fatalf("worker deployment did not reach live: %v", err)
	}

	const messages = 24
	sent := make(map[string]bool, messages)
	for i := range messages {
		payload := json.RawMessage(fmt.Sprintf(`{"n":%d}`, i))
		body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/queues/send",
			api.QueueSendRequest{QueueName: "jobs", Payload: payload})
		if status != http.StatusCreated {
			t.Fatalf("send %d: status=%d body=%s", i, status, body)
		}
		var resp api.QueueSendResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("decode send %d: %v", i, err)
		}
		sent[resp.ID] = true
	}

	// The pool scales out for the backlog.
	peak := 0
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		n := liveWorkerCount(t, h, appID)
		if n > peak {
			peak = n
		}
		if n > poolMax {
			t.Fatalf("pool reached %d workers, above the plan cap %d", n, poolMax)
		}
		if done, _ := queueOutcome(t, h, appID); done == messages {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if peak < 2 {
		t.Fatalf("pool never scaled out for a %d-message backlog: peak=%d", messages, peak)
	}

	// The pool shrinks back to zero once the queue stays empty.
	window := time.Duration(api.WorkerScaleInStabilizationSeconds) * time.Second
	deadline = time.Now().Add(window + 2*time.Minute)
	for time.Now().Before(deadline) && liveWorkerCount(t, h, appID) > 0 {
		time.Sleep(time.Second)
	}
	if n := liveWorkerCount(t, h, appID); n != 0 {
		t.Fatalf("pool still has %d workers after the queue drained", n)
	}

	// Every message completed exactly once: a delivery killed by a stopping
	// worker would show a second attempt or a dead letter.
	done, rows := queueOutcome(t, h, appID)
	if done != messages || len(rows) != messages {
		t.Fatalf("completed %d of %d messages: %+v", done, messages, rows)
	}
	for _, row := range rows {
		if !sent[row.id] {
			t.Fatalf("unexpected queue row %s", row.id)
		}
		if row.state != "completed" || row.attempts != 1 {
			t.Fatalf("message %s finished %s after %d attempts; scale-in must not cost a delivery attempt", row.id, row.state, row.attempts)
		}
	}
	var failed int
	if err := h.Pool.QueryRow(context.Background(),
		`select count(*) from instances where app_id = $1 and mode = 'worker' and state = 'failed'`, appID).Scan(&failed); err != nil {
		t.Fatalf("count failed workers: %v", err)
	}
	if failed != 0 {
		t.Fatalf("%d workers ended FAILED; scale-in must stop them cleanly", failed)
	}
}

type queueRowOutcome struct {
	id       string
	state    string
	attempts int
}

// queueOutcome returns the number of completed queue messages and every row.
func queueOutcome(t *testing.T, h *e2etest.Harness, appID string) (int, []queueRowOutcome) {
	t.Helper()
	rows, err := h.Pool.Query(context.Background(),
		`select id::text, state, attempts from invocations where app_id = $1 and source = 'queue' order by created_at`, appID)
	if err != nil {
		t.Fatalf("list queue rows: %v", err)
	}
	defer rows.Close()
	var out []queueRowOutcome
	completed := 0
	for rows.Next() {
		var row queueRowOutcome
		if err := rows.Scan(&row.id, &row.state, &row.attempts); err != nil {
			t.Fatalf("scan queue row: %v", err)
		}
		if row.state == "completed" {
			completed++
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list queue rows: %v", err)
	}
	return completed, out
}

func liveWorkerCount(t *testing.T, h *e2etest.Harness, appID string) int {
	t.Helper()
	var n int
	if err := h.Pool.QueryRow(context.Background(),
		`select count(*) from instances where app_id = $1 and mode = 'worker'
		    and state in ('waking','cold_booting','running','warm')`, appID).Scan(&n); err != nil {
		t.Fatalf("count workers: %v", err)
	}
	return n
}
