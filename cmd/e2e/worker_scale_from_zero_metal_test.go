//go:build metal

// worker_scale_from_zero_metal_test.go — a queue-driven worker pool at zero
// replicas boots a real Firecracker worker when a message arrives, and the
// test times each step of that path:
//
//	send → admit      schedd's targets tick reads the queue depth and inserts
//	                  the worker row (pkg/sched/worker_scope.go)
//	send → token      the worker process holds a gregale:queues token
//	                  (vmmd's mint counter): the first moment it could lease
//	token → lease     the first lease of the message, taken by the test on the
//	                  worker's behalf because a guest cannot reach apid here
//	send → running    a worker is RUNNING only after guest-init's
//	                  characterization window (api.CharacterizationDeadline)
//
// vmmd mints tokens for booting instances (ADR-1141 amendment 2026-10-17),
// so every worker must hold its token before it is RUNNING; before that
// change a consumer waited out the whole characterization window.
//
// Workers and jobs never restore from a snapshot (ADR-138), so every scale
// from zero is a cold boot. The deployment's first boot passes the full
// characterization window; later boots of it on the node only show that the
// process started (ADR-20261018101500), so each is RUNNING well inside the window. A
// burst of one message per pool slot shows the pool admitting one worker
// after another.
// FAAS_WORKER_SCALE_FROM_ZERO_CYCLES repeats the single-message cycle; each
// cycle first waits for the pool to drain back to zero, which takes
// api.WorkerScaleInStabilizationSeconds.
//
// Requires /dev/kvm, root, Firecracker on PATH and FAAS_TEST_KERNEL.
package e2e_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/events"
)

const scaleFromZeroQueue = "jobs"

// workerColdStart is one worker's path from the send that demanded it.
// Durations are measured from the send; zero means not observed.
type workerColdStart struct {
	instanceID    string
	admit         time.Duration // instance row first seen (polled every 20 ms)
	bootStarted   time.Duration // wake.boot_started (database clock)
	token         time.Duration // nth gregale:queues token minted by vmmd
	lease         time.Duration // nth message leased
	leaseCall     time.Duration // duration of that lease request
	running       time.Duration // instance observed RUNNING
	bootCompleted time.Duration // wake.boot_completed (database clock)
	method        string
	breakdown     map[string]any // wake.cold_boot_breakdown payload
}

// workerPoolRig is a Pro worker pool on real Firecracker: the app consumes a
// pull binding, scales on queue depth (target 1) from zero to the plan's
// worker cap, and its deployment is live.
type workerPoolRig struct {
	h       *e2etest.Harness
	pool    *pgxpool.Pool
	key     string
	slug    string
	appID   string
	poolMax int
}

// startWorkerPoolRig deploys the queue-consumer fixture as a worker pool.
// extraEnv reaches every daemon. The caller owns teardown of its workers.
func startWorkerPoolRig(t *testing.T, extraEnv ...string) *workerPoolRig {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		t.Skip("no Postgres for the metal worker pool")
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	_, identityEnv := workloadIdentityTrust(t)
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builderImg))
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "queue consumer")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.DeployWake|e2etest.VMMDMetrics, append(identityEnv, extraEnv...)...)
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	key := h.SeedAccount(context.Background(), api.PlanPro)
	poolMax := api.MustLimitsFor(api.PlanPro).WorkerReplicasMax
	const slug = "consumer"
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app"}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, slug)
	workerMode := api.ExecutionModeWorker
	if body, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/"+slug, api.UpdateAppRequest{
		ExecutionMode:  &workerMode,
		MaxConcurrency: &poolMax,
		WorkerReplicas: &api.WorkerScaling{Min: 0, Max: poolMax},
		ScalingPolicy: &api.ScalingPolicy{
			Targets:           []api.ScalingTarget{{Metric: api.ScalingMetricQueueDepth, Value: 1}},
			ScaleOutCooldownS: api.MinScaleOutCooldownS,
			ScaleInCooldownS:  api.MinScaleInCooldownS,
		},
	}); status != http.StatusOK {
		t.Fatalf("make the app a queue-driven worker pool: status=%d body=%s", status, body)
	}
	enabled := true
	if body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/queue-bindings", api.CreateQueueBindingRequest{
		Name: scaleFromZeroQueue, QueueName: scaleFromZeroQueue, Mode: "pull", WorkloadClass: "worker",
		Enabled: &enabled, MaxConcurrency: poolMax,
	}); status != http.StatusCreated {
		t.Fatalf("create pull binding: status=%d body=%s", status, body)
	}
	img, _ := e2etest.QueueConsumerImageAboveBase("library/consumer")
	ref := registry.AddImage("library/consumer", img)
	raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/deployments", api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, raw)
	}
	var dep api.DeploymentResponse
	if err := json.Unmarshal(raw, &dep); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, dep.ID, 12*time.Minute); err != nil {
		t.Fatalf("deployment not live: %v", err)
	}
	return &workerPoolRig{h: h, pool: pool, key: key, slug: slug, appID: appID, poolMax: poolMax}
}

func TestWorkerScaleFromZeroMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal worker scale-from-zero test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	cycles := 1
	if raw := os.Getenv("FAAS_WORKER_SCALE_FROM_ZERO_CYCLES"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			t.Fatalf("FAAS_WORKER_SCALE_FROM_ZERO_CYCLES=%q: want a positive integer", raw)
		}
		cycles = n
	}
	rig := startWorkerPoolRig(t)
	h, key, slug, appID, poolMax := rig.h, rig.key, rig.slug, rig.appID, rig.poolMax
	// Every message is acknowledged, so the pool drains to zero after the
	// stabilization window. Wait for it, or the worker microVMs outlive the
	// harness (vmmd keeps VMs across a restart).
	t.Cleanup(func() {
		if err := waitForIdleWorkerPool(t, h, appID, 4*time.Minute); err != nil {
			t.Errorf("teardown: %v; its microVMs will leak", err)
		}
	})

	var singles []workerColdStart
	for cycle := 1; cycle <= cycles; cycle++ {
		if err := waitForIdleWorkerPool(t, h, appID, 4*time.Minute); err != nil {
			t.Fatalf("cycle %d: %v", cycle, err)
		}
		got := measureWorkerScaleFromZero(t, h, key, slug, appID, 1)
		logWorkerColdStarts(t, fmt.Sprintf("cycle %d", cycle), got)
		singles = append(singles, got...)
	}
	if err := waitForIdleWorkerPool(t, h, appID, 4*time.Minute); err != nil {
		t.Fatalf("burst: %v", err)
	}
	burst := measureWorkerScaleFromZero(t, h, key, slug, appID, poolMax)
	logWorkerColdStarts(t, fmt.Sprintf("burst of %d", poolMax), burst)
	logWorkerScaleFromZeroSummary(t, singles, burst)
	for _, w := range append(singles, burst...) {
		if w.token == 0 || w.running == 0 || w.token >= w.running {
			t.Errorf("worker %s: token at %s, RUNNING at %s; a booting worker must get its queue token before readiness",
				w.instanceID, msOrDash(w.token), msOrDash(w.running))
		}
	}
	// ADR-20261018101500: the deployment's first boot passed the full characterization
	// window, so every pool boot after it only has to show its process
	// started. vmmd's wait_ready phase covers the guest boot and that window;
	// it was about 11 s per worker before. Disk staging (provision) is timed
	// separately and is not part of this check.
	for _, w := range append(singles, burst...) {
		waitReady, _ := w.breakdown["wait_ready_ms"].(float64)
		if wait := time.Duration(waitReady) * time.Millisecond; wait <= 0 || wait >= api.CharacterizationDeadline {
			t.Errorf("worker %s: waited %s for readiness; a proven worker must not wait the %s window",
				w.instanceID, msOrDash(wait), api.CharacterizationDeadline)
		}
	}
}

// measureWorkerScaleFromZero sends messages to an idle pool, leases each one
// as soon as a worker holds a queue token, and returns one record per worker
// in admission order.
func measureWorkerScaleFromZero(t *testing.T, h *e2etest.Harness, key, slug, appID string, messages int) []workerColdStart {
	t.Helper()
	ctx := context.Background()
	minted0 := vmmdMintedQueueTokens(t, h)
	// instances has no creation timestamp; workers that already exist (the
	// deployment's prime and earlier cycles) are excluded by id.
	earlier := map[string]bool{}
	rows, err := h.Pool.Query(ctx, `select id::text from instances where app_id = $1 and mode = 'worker'`, appID)
	if err != nil {
		t.Fatalf("list earlier workers: %v", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan earlier worker: %v", err)
		}
		earlier[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list earlier workers: %v", err)
	}
	var sentDB time.Time
	if err := h.Pool.QueryRow(ctx, `select clock_timestamp()`).Scan(&sentDB); err != nil {
		t.Fatalf("read database clock: %v", err)
	}
	sent := time.Now()
	for i := range messages {
		body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/queues/send",
			api.QueueSendRequest{QueueName: scaleFromZeroQueue, Payload: json.RawMessage(fmt.Sprintf(`{"n":%d}`, i))})
		if status != http.StatusCreated {
			t.Fatalf("send %d: status=%d body=%s", i, status, body)
		}
	}
	var workers []*workerColdStart
	byID := map[string]*workerColdStart{}
	var tokens, leases, leaseCalls []time.Duration
	deadline := sent.Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		now := time.Since(sent)
		rows, err := h.Pool.Query(ctx, `select id::text, state from instances
			where app_id = $1 and mode = 'worker' order by id`, appID)
		if err != nil {
			t.Fatalf("list workers: %v", err)
		}
		running := 0
		for rows.Next() {
			var id, st string
			if err := rows.Scan(&id, &st); err != nil {
				t.Fatalf("scan worker: %v", err)
			}
			if earlier[id] {
				continue
			}
			w := byID[id]
			if w == nil {
				w = &workerColdStart{instanceID: id, admit: now}
				byID[id] = w
				workers = append(workers, w)
			}
			if st == "running" && w.running == 0 {
				w.running = now
			}
			if w.running != 0 {
				running++
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("list workers: %v", err)
		}
		for minted := vmmdMintedQueueTokens(t, h) - minted0; len(tokens) < minted; {
			tokens = append(tokens, now)
		}
		for len(leases) < len(tokens) && len(leases) < messages {
			started := time.Now()
			if !leaseAndAckOne(t, h, key, slug) {
				break
			}
			leaseCalls = append(leaseCalls, time.Since(started))
			leases = append(leases, time.Since(sent))
		}
		if len(leases) == messages && running >= messages {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(leases) != messages {
		t.Fatalf("leased %d of %d messages within %s; workers=%d tokens=%d",
			len(leases), messages, deadline.Sub(sent).Round(time.Second), len(workers), len(tokens))
	}
	if len(tokens) != len(workers) {
		// Mints are attributed to workers in admission order, which holds
		// only while each worker asks once.
		t.Logf("vmmd minted %d queue tokens for %d workers; token times may be misattributed", len(tokens), len(workers))
	}
	out := make([]workerColdStart, 0, len(workers))
	for i, w := range workers {
		if i < len(tokens) {
			w.token = tokens[i]
		}
		if i < len(leases) {
			w.lease, w.leaseCall = leases[i], leaseCalls[i]
		}
		readWorkerBootEvents(t, h, sentDB, w)
		out = append(out, *w)
	}
	if len(out) < messages {
		t.Fatalf("%d messages started %d workers; queue_depth target 1 demands one worker per message", messages, len(out))
	}
	return out
}

// leaseAndAckOne leases at most one message and acknowledges it at once, as
// a consumer whose handler is instant would.
func leaseAndAckOne(t *testing.T, h *e2etest.Harness, key, slug string) bool {
	t.Helper()
	path := "/v1/apps/" + slug + "/queues/" + scaleFromZeroQueue + "/leases"
	raw, status := doReq(t, h, key, http.MethodPost, path, api.QueueLeaseRequest{MaxMessages: 1})
	if status != http.StatusOK {
		t.Fatalf("lease: status=%d body=%s", status, raw)
	}
	var lease api.QueueLeaseResponse
	if err := json.Unmarshal(raw, &lease); err != nil {
		t.Fatalf("lease response %s: %v", raw, err)
	}
	if len(lease.Messages) == 0 {
		return false
	}
	acked, status := doReq(t, h, key, http.MethodPost, path+":ack", api.QueueLeaseReceiptsRequest{
		Receipts: []string{lease.Messages[0].Receipt},
	})
	var outcome api.QueueLeaseReceiptsResponse
	if status != http.StatusOK || json.Unmarshal(acked, &outcome) != nil || len(outcome.Succeeded) != 1 {
		t.Fatalf("ack: status=%d body=%s", status, acked)
	}
	return true
}

// readWorkerBootEvents fills in the wake events schedd and vmmd recorded for
// the worker's boot.
func readWorkerBootEvents(t *testing.T, h *e2etest.Harness, sentDB time.Time, w *workerColdStart) {
	t.Helper()
	rows, err := h.Pool.Query(context.Background(), `select kind, at, data from events
		where kind = any($2) and data->>'instance_id' = $1 order by at, id`, w.instanceID,
		[]string{events.WakeBootStarted, events.WakeColdBootBreakdown, events.WakeBootCompleted})
	if err != nil {
		t.Fatalf("read boot events: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var at time.Time
		var data []byte
		if err := rows.Scan(&kind, &at, &data); err != nil {
			t.Fatalf("scan boot event: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatalf("decode %s: %v", kind, err)
		}
		switch kind {
		case events.WakeBootStarted:
			w.bootStarted = at.Sub(sentDB)
		case events.WakeColdBootBreakdown:
			w.breakdown = payload
		case events.WakeBootCompleted:
			w.bootCompleted = at.Sub(sentDB)
			w.method, _ = payload["method"].(string)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read boot events: %v", err)
	}
}

// vmmdMintedQueueTokens reads vmmd's count of minted workload tokens. Only
// this test's workers ask for tokens.
func vmmdMintedQueueTokens(t *testing.T, h *e2etest.Harness) int {
	t.Helper()
	if h.VMMDMetricsURL == "" {
		t.Fatal("harness started without VMMDMetrics")
	}
	resp, err := http.Get(h.VMMDMetricsURL)
	if err != nil {
		t.Fatalf("scrape vmmd metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	const series = `vmmd_workload_identity_tokens_total{result="minted"} `
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		if value, ok := strings.CutPrefix(scanner.Text(), series); ok {
			n, err := strconv.ParseFloat(value, 64)
			if err != nil {
				t.Fatalf("parse %s%s: %v", series, value, err)
			}
			return int(n)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read vmmd metrics: %v", err)
	}
	t.Fatalf("vmmd metrics have no %q series", strings.TrimSpace(series))
	return 0
}

// waitForIdleWorkerPool waits until the app has no worker that holds memory.
func waitForIdleWorkerPool(t *testing.T, h *e2etest.Harness, appID string, timeout time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var live int
		if err := h.Pool.QueryRow(context.Background(), `select count(*) from instances
			where app_id = $1 and mode = 'worker'
			  and state in ('waking','cold_booting','running','draining','warm')`, appID).Scan(&live); err != nil {
			return fmt.Errorf("count workers: %w", err)
		}
		if live == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("worker pool still has %d live workers after %s", live, timeout)
		}
		time.Sleep(time.Second)
	}
}

func logWorkerColdStarts(t *testing.T, label string, workers []workerColdStart) {
	t.Helper()
	for i, w := range workers {
		t.Logf("worker scale-from-zero %s, worker %d (%s): admit=%s boot_started=%s token=%s lease=%s (call %s) running=%s boot_completed=%s method=%s breakdown=%s",
			label, i+1, w.instanceID, msOrDash(w.admit), msOrDash(w.bootStarted), msOrDash(w.token), msOrDash(w.lease), msOrDash(w.leaseCall),
			msOrDash(w.running), msOrDash(w.bootCompleted), w.method, coldBootPhases(w.breakdown))
	}
}

func logWorkerScaleFromZeroSummary(t *testing.T, singles, burst []workerColdStart) {
	t.Helper()
	pick := func(ws []workerColdStart, f func(workerColdStart) time.Duration) []time.Duration {
		out := make([]time.Duration, 0, len(ws))
		for _, w := range ws {
			out = append(out, f(w))
		}
		return out
	}
	for _, metric := range []struct {
		name string
		f    func(workerColdStart) time.Duration
	}{
		{"send→admit", func(w workerColdStart) time.Duration { return w.admit }},
		{"send→token", func(w workerColdStart) time.Duration { return w.token }},
		{"send→lease", func(w workerColdStart) time.Duration { return w.lease }},
		{"send→running", func(w workerColdStart) time.Duration { return w.running }},
	} {
		t.Logf("worker scale-from-zero summary %s over %d single-message cycles: %s; burst of %d: %s",
			metric.name, len(singles), durationSpread(pick(singles, metric.f)), len(burst), durationList(pick(burst, metric.f)))
	}
}

func durationSpread(values []time.Duration) string {
	if len(values) == 0 {
		return "none"
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	return fmt.Sprintf("min %s median %s max %s", msOrDash(sorted[0]), msOrDash(sorted[len(sorted)/2]), msOrDash(sorted[len(sorted)-1]))
}

func durationList(values []time.Duration) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = msOrDash(v)
	}
	return strings.Join(parts, ", ")
}

func msOrDash(d time.Duration) string {
	if d == 0 {
		return "-"
	}
	return fmt.Sprintf("%dms", d.Milliseconds())
}

// coldBootPhases renders vmmd's non-zero cold-boot phases in payload order.
func coldBootPhases(breakdown map[string]any) string {
	if breakdown == nil {
		return "-"
	}
	var parts []string
	for _, name := range []string{"resolve_images_ms", "chroot_ms", "provision_ms", "stage_runtime_ms", "prepare_config_ms",
		"helper_ms", "start_jailer_ms", "bind_tun_ms", "cgroup_ms", "write_config_ms", "wait_ready_ms", "quota_restore_ms", "total_ms"} {
		if v, ok := breakdown[name].(float64); ok && v > 0 {
			parts = append(parts, fmt.Sprintf("%s=%.0f", strings.TrimSuffix(name, "_ms"), v))
		}
	}
	return strings.Join(parts, " ")
}
