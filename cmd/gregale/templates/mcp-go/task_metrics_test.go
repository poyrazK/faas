package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTaskMetricsPublishEveryQueueHealthGauge(t *testing.T) {
	posted := map[string]float64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Value float64 `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		posted[name] = body.Value
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	metrics := mcpTaskQueueMetrics{outstandingTasks: 9, oldestAgeSeconds: 10, runningTasks: 1, capacityWaitingTasks: 2,
		failedTasks: 3, retryWaitingTasks: 4, activeWorkers: 5, drainingWorkers: 6, unsupportedHandlerTasks: 7, withoutCompatibleWorkerTasks: 8}
	if err := publishMCPTaskGauges(t.Context(), server.Client(), mcpTaskMetricsSettings{appSlug: "test", token: "test-only", apiURL: server.URL}, metrics); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]float64{"outstanding": 9, "oldest_age_seconds": 10, "running": 1, "capacity_waiting": 2,
		"failed": 3, "retry_waiting": 4, "active_workers": 5, "draining_workers": 6, "unsupported_handler_tasks": 7, "without_compatible_worker": 8} {
		if got, present := posted["mcp_tasks_"+name]; !present || got != want {
			t.Errorf("metric %s = %v (present=%v), want %v", name, got, present, want)
		}
	}
	if len(posted) != 10 {
		t.Fatalf("published %d metrics, want 10", len(posted))
	}
}

func TestTaskQueueMetricsPostgresStatesAndWorkerDrain(t *testing.T) {
	runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{MaxRunning: 2, MaxRunningPerOwner: 1})
	var err error
	runtime.workerID, err = newTaskUUID()
	if err != nil {
		t.Fatal(err)
	}
	runtime.deploymentID = "deployment-test"
	runtime.drainTimeout = 10 * time.Second
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := runtime.pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	failed := insertExecutionTestTask(t, runtime, ctx, "alice")
	exec(`UPDATE gregale_mcp_tasks SET status='failed' WHERE task_id=$1`, failed)
	metrics, err := runtime.queueMetrics(ctx)
	if err != nil || metrics.outstandingTasks != 0 || metrics.failedTasks != 1 || metrics.oldestAgeSeconds != 0 {
		t.Fatalf("failed-only queue: metrics=%+v err=%v", metrics, err)
	}
	ids := make([]string, 0, 6)
	for _, owner := range []string{"alice", "alias", "bob", "carol", "dave", "expired"} {
		ids = append(ids, insertExecutionTestTask(t, runtime, ctx, owner))
	}
	if err := runtime.associateOwners(ctx, [][]byte{[]byte("alice"), []byte("alias")}); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE gregale_mcp_tasks SET status='running',lease_token=gen_random_uuid(),lease_expires_at=clock_timestamp()+interval '1 minute',attempt_count=1 WHERE task_id=$1`, ids[0])
	exec(`UPDATE gregale_mcp_tasks SET next_attempt_at=clock_timestamp()+interval '1 hour' WHERE task_id=$1`, ids[2])
	exec(`UPDATE gregale_mcp_tasks SET handler_version='unsupported' WHERE task_id=$1`, ids[3])
	exec(`UPDATE gregale_mcp_tasks SET status='running',lease_token=gen_random_uuid(),lease_expires_at=clock_timestamp()-interval '1 second',attempt_count=1 WHERE task_id=$1`, ids[4])
	exec(`UPDATE gregale_mcp_tasks SET created_at=clock_timestamp()-interval '2 hours',expires_at=clock_timestamp()-interval '1 hour' WHERE task_id=$1`, ids[5])
	exec(`INSERT INTO gregale_mcp_task_workers(namespace,worker_id,handlers,draining,heartbeat_at,expires_at) VALUES($1,gen_random_uuid(),'[]',true,clock_timestamp(),clock_timestamp()+interval '1 minute')`, runtime.namespace)
	if err := runtime.heartbeatWorkerCapabilities(ctx); err != nil {
		t.Fatal(err)
	}
	metrics, err = runtime.queueMetrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.outstandingTasks != 5 || metrics.runningTasks != 1 || metrics.capacityWaitingTasks != 1 || metrics.failedTasks != 1 ||
		metrics.retryWaitingTasks != 1 || metrics.activeWorkers != 1 || metrics.drainingWorkers != 1 || metrics.unsupportedHandlerTasks != 1 || metrics.withoutCompatibleWorkerTasks != 1 {
		t.Fatalf("queue-state metrics = %+v", metrics)
	}
	runtime.execution.maxRunning = 1
	metrics, err = runtime.queueMetrics(ctx)
	if err != nil || metrics.capacityWaitingTasks != 3 {
		t.Fatalf("namespace capacity: metrics=%+v err=%v", metrics, err)
	}
	runtime.drainMu.Lock()
	runtime.drainStarted = time.Now()
	runtime.drainMu.Unlock()
	if err := runtime.markWorkerDraining(ctx); err != nil {
		t.Fatal(err)
	}
	if err := runtime.heartbeatWorkerCapabilities(ctx); err != nil {
		t.Fatal(err)
	}
	metrics, err = runtime.queueMetrics(ctx)
	if err != nil || metrics.activeWorkers != 0 || metrics.drainingWorkers != 2 || metrics.unsupportedHandlerTasks != 0 || metrics.withoutCompatibleWorkerTasks != 3 {
		t.Fatalf("draining worker reactivated: metrics=%+v err=%v", metrics, err)
	}
}
