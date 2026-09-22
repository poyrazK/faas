// commands_workers_test.go — Tests for `gregale workers <list|status|logs|scale>` CLI surface.

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdWorkers_NoArgsEmptyList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `[]`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)

	stdout, restore := captureStdout(t)
	code := cmdWorkers([]string{})
	restore()

	if code != 0 {
		t.Fatalf("cmdWorkers(no args) = %d, want 0", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "No background workers found") {
		t.Fatalf("expected empty workers message, got: %s", out)
	}
}

func TestCmdWorkers_Help(t *testing.T) {
	stdout, restore := captureStdout(t)
	code := cmdWorkers([]string{"--help"})
	restore()

	if code != 0 {
		t.Fatalf("cmdWorkers(--help) = %d, want 0", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "usage: gregale workers") {
		t.Fatalf("expected usage message, got: %s", out)
	}
}

func TestCmdWorkersList_FiltersNonWorkersAndFormatsTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps":
			w.Header().Set("Content-Type", "application/json")
			apps := []api.AppResponse{
				{
					ID:     "app-worker-1",
					Slug:   "order-worker",
					Status: "running",
					Manifest: api.AppManifest{
						ExecutionMode: api.ExecutionModeWorker,
						WorkerReplicas: &api.WorkerScaling{
							Min:    0,
							Max:    20,
							Metric: "queue_lag",
							Target: 500,
						},
						StopGracePeriod: 90 * time.Second,
						StopSignal:      "SIGINT",
					},
				},
				{
					ID:     "app-web-1",
					Slug:   "web-api",
					Status: "running",
					Manifest: api.AppManifest{
						ExecutionMode: api.ExecutionModeRequest,
					},
				},
			}
			_ = json.NewEncoder(w).Encode(apps)
		case "/v1/apps/order-worker/instances":
			w.Header().Set("Content-Type", "application/json")
			instances := []api.InstanceResponse{
				{ID: "ins-1", State: "running", RAMMB: 512, StartedAt: "2026-09-21T20:00:00Z"},
				{ID: "ins-2", State: "running", RAMMB: 512, StartedAt: "2026-09-21T20:10:00Z"},
			}
			_ = json.NewEncoder(w).Encode(instances)
		case "/v1/triggers":
			w.Header().Set("Content-Type", "application/json")
			triggers := []api.Trigger{
				{
					ID:     "trig-1",
					AppID:  "app-worker-1",
					Kind:   api.TriggerKind("amqp"),
					Config: json.RawMessage(`{"queue":"orders"}`),
				},
			}
			_ = json.NewEncoder(w).Encode(triggers)
		case "/v1/triggers/trig-1/metrics":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"trigger_id":"trig-1","pending_count":42,"claimed_count":2,"succeeded_count":100,"dead_letter_count":0}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)

	stdout, restore := captureStdout(t)
	code := cmdWorkersList([]string{})
	restore()

	if code != 0 {
		t.Fatalf("cmdWorkersList = %d, want 0", code)
	}

	out := stdout.String()
	if !strings.Contains(out, "order-worker") {
		t.Errorf("missing worker pool slug; got: %s", out)
	}
	if strings.Contains(out, "web-api") {
		t.Errorf("list must filter out non-worker app web-api; got: %s", out)
	}
	if !strings.Contains(out, "2 running") {
		t.Errorf("expected '2 running' replicas; got: %s", out)
	}
	if !strings.Contains(out, "0..20") {
		t.Errorf("expected '0..20' scaling bounds; got: %s", out)
	}
	if !strings.Contains(out, "queue_lag") {
		t.Errorf("expected 'queue_lag' metric; got: %s", out)
	}
	if !strings.Contains(out, "500") {
		t.Errorf("expected '500' target; got: %s", out)
	}
	if !strings.Contains(out, "amqp:orders") {
		t.Errorf("expected 'amqp:orders' source; got: %s", out)
	}
}

func TestCmdWorkersList_JSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps":
			w.Header().Set("Content-Type", "application/json")
			apps := []api.AppResponse{
				{
					ID:     "app-w1",
					Slug:   "task-consumer",
					Status: "running",
					Manifest: api.AppManifest{
						ExecutionMode: api.ExecutionModeWorker,
						WorkerReplicas: &api.WorkerScaling{
							Min:    1,
							Max:    10,
							Metric: "queue_depth",
							Target: 50,
						},
						StopGracePeriod: 45 * time.Second,
						StopSignal:      "SIGQUIT",
					},
				},
			}
			_ = json.NewEncoder(w).Encode(apps)
		case "/v1/apps/task-consumer/instances":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `[]`)
		case "/v1/triggers":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `[]`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)
	jsonOutput = true

	stdout, restore := captureStdout(t)
	code := cmdWorkersList([]string{})
	restore()

	if code != 0 {
		t.Fatalf("cmdWorkersList (json) = %d, want 0", code)
	}

	var res []WorkerPoolSummary
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("invalid json output: %v, raw: %s", err, stdout.String())
	}
	if len(res) != 1 {
		t.Fatalf("len(res) = %d, want 1", len(res))
	}
	if res[0].Slug != "task-consumer" || res[0].MinReplicas != 1 || res[0].MaxReplicas != 10 || res[0].DrainTimeout != 45 || res[0].StopSignal != "SIGQUIT" {
		t.Fatalf("unexpected summary payload: %+v", res[0])
	}
}

func TestCmdWorkersStatus_DetailAndDLQWarning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps/billing-worker":
			w.Header().Set("Content-Type", "application/json")
			app := api.AppResponse{
				ID:     "app-billing",
				Slug:   "billing-worker",
				Status: "running",
				RAMMB:  512,
				VCPU:   1,
				Manifest: api.AppManifest{
					ExecutionMode: api.ExecutionModeWorker,
					RestartPolicy: "always",
					WorkerReplicas: &api.WorkerScaling{
						Min:    0,
						Max:    30,
						Metric: "queue_lag",
						Target: 200,
					},
					StopGracePeriod: 120 * time.Second,
					StopSignal:      "SIGINT",
				},
			}
			_ = json.NewEncoder(w).Encode(app)
		case "/v1/apps/billing-worker/instances":
			w.Header().Set("Content-Type", "application/json")
			instances := []api.InstanceResponse{
				{
					ID:        "0192134a-0001-7000-8000-000000000001",
					State:     "running",
					RAMMB:     512,
					StartedAt: time.Now().Add(-15 * time.Minute).Format(time.RFC3339),
				},
			}
			_ = json.NewEncoder(w).Encode(instances)
		case "/v1/triggers":
			w.Header().Set("Content-Type", "application/json")
			triggers := []api.Trigger{
				{
					ID:     "trig-billing",
					AppID:  "app-billing",
					Kind:   api.TriggerKindKafka,
					Config: json.RawMessage(`{"topic":"invoices"}`),
				},
			}
			_ = json.NewEncoder(w).Encode(triggers)
		case "/v1/triggers/trig-billing/metrics":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"trigger_id":"trig-billing","pending_count":850,"claimed_count":1,"succeeded_count":500,"dead_letter_count":12}`)
		case "/v1/triggers/trig-billing/dlq":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"records":[{"record_id":"rec-1","reason":"poison_pill"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)

	stdout, restore := captureStdout(t)
	code, stderr := runWithStderr(t, func() int {
		return cmdWorkersStatus([]string{"billing-worker"})
	})
	restore()

	if code != 0 {
		t.Fatalf("cmdWorkersStatus = %d, want 0", code)
	}

	out := stdout.String()
	if !strings.Contains(out, "Worker Pool:  billing-worker") {
		t.Errorf("missing worker pool header; got: %s", out)
	}
	if !strings.Contains(out, "Replicas:   1 running (min: 0, max: 30)") {
		t.Errorf("missing replicas line; got: %s", out)
	}
	if !strings.Contains(out, "Backlog:    850 messages pending") {
		t.Errorf("missing backlog line; got: %s", out)
	}
	if !strings.Contains(out, "drain timeout: 120s, stop signal: SIGINT") {
		t.Errorf("missing lifecycle line; got: %s", out)
	}
	if !strings.Contains(out, "Broker:     kafka") {
		t.Errorf("missing broker line; got: %s", out)
	}
	if !strings.Contains(out, "DLQ:        12 dead-letter messages ⚠️") {
		t.Errorf("missing DLQ warning in status; got: %s", out)
	}
	if !strings.Contains(out, "0192134a-0001-7000-8000-000000000001") {
		t.Errorf("missing instance row; got: %s", out)
	}
	if !strings.Contains(stderr, "12 dead-letter messages detected") {
		t.Errorf("stderr must warn about dead-letter messages; got: %s", stderr)
	}
}

func TestCmdWorkersStatus_RejectsNonWorker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/web-app" {
			w.Header().Set("Content-Type", "application/json")
			app := api.AppResponse{
				ID:   "app-web",
				Slug: "web-app",
				Manifest: api.AppManifest{
					ExecutionMode: api.ExecutionModeRequest,
				},
			}
			_ = json.NewEncoder(w).Encode(app)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)

	code, stderr := runWithStderr(t, func() int {
		return cmdWorkersStatus([]string{"web-app"})
	})

	if code != 1 {
		t.Fatalf("cmdWorkersStatus(non-worker) = %d, want 1", code)
	}
	if !strings.Contains(stderr, "not a worker") {
		t.Fatalf("expected 'not a worker' error message; got: %s", stderr)
	}
}

func TestCmdWorkersScale_UpdatesParameters(t *testing.T) {
	var gotReq api.UpdateAppRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/my-worker":
			w.Header().Set("Content-Type", "application/json")
			app := api.AppResponse{
				ID:   "app-w",
				Slug: "my-worker",
				Manifest: api.AppManifest{
					ExecutionMode: api.ExecutionModeWorker,
					WorkerReplicas: &api.WorkerScaling{
						Min:    0,
						Max:    5,
						Metric: "queue_lag",
						Target: 100,
					},
					StopGracePeriod: 30 * time.Second,
					StopSignal:      "SIGTERM",
				},
			}
			_ = json.NewEncoder(w).Encode(app)
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/apps/my-worker":
			_ = json.NewDecoder(r.Body).Decode(&gotReq)
			w.Header().Set("Content-Type", "application/json")
			app := api.AppResponse{
				ID:   "app-w",
				Slug: "my-worker",
				Manifest: api.AppManifest{
					ExecutionMode: api.ExecutionModeWorker,
					WorkerReplicas: &api.WorkerScaling{
						Min:    2,
						Max:    50,
						Metric: "queue_depth",
						Target: 250,
					},
					StopGracePeriod: 90 * time.Second,
					StopSignal:      "SIGQUIT",
				},
			}
			_ = json.NewEncoder(w).Encode(app)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)

	stdout, restore := captureStdout(t)
	code := cmdWorkersScale([]string{
		"my-worker",
		"--min", "2",
		"--max", "50",
		"--target", "250",
		"--metric", "queue_depth",
		"--drain-timeout", "90s",
		"--stop-signal", "SIGQUIT",
	})
	restore()

	if code != 0 {
		t.Fatalf("cmdWorkersScale = %d, want 0", code)
	}

	if gotReq.WorkerReplicas == nil {
		t.Fatal("WorkerReplicas was not sent in PATCH")
	}
	if gotReq.WorkerReplicas.Min != 2 || gotReq.WorkerReplicas.Max != 50 || gotReq.WorkerReplicas.Target != 250 || gotReq.WorkerReplicas.Metric != "queue_depth" {
		t.Errorf("unexpected WorkerReplicas: %+v", gotReq.WorkerReplicas)
	}
	if gotReq.StopGracePeriodS == nil || *gotReq.StopGracePeriodS != 90 {
		t.Errorf("unexpected StopGracePeriodS: %v", gotReq.StopGracePeriodS)
	}
	if gotReq.StopSignal == nil || *gotReq.StopSignal != "SIGQUIT" {
		t.Errorf("unexpected StopSignal: %v", gotReq.StopSignal)
	}

	out := stdout.String()
	if !strings.Contains(out, "Worker scaling updated for my-worker") {
		t.Errorf("missing success banner in output; got: %s", out)
	}
}

func TestCmdWorkersScale_RejectsInvalidBounds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/my-worker" {
			w.Header().Set("Content-Type", "application/json")
			app := api.AppResponse{
				ID:   "app-w",
				Slug: "my-worker",
				Manifest: api.AppManifest{
					ExecutionMode: api.ExecutionModeWorker,
					WorkerReplicas: &api.WorkerScaling{
						Min: 0, Max: 5, Target: 100,
					},
				},
			}
			_ = json.NewEncoder(w).Encode(app)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)

	// max < min
	code, stderr := runWithStderr(t, func() int {
		return cmdWorkersScale([]string{"my-worker", "--min", "10", "--max", "2"})
	})
	if code != 1 {
		t.Fatalf("cmdWorkersScale(max < min) = %d, want 1", code)
	}
	if !strings.Contains(stderr, "cannot be less than min replicas") {
		t.Errorf("unexpected stderr: %s", stderr)
	}

	// invalid metric
	code, stderr = runWithStderr(t, func() int {
		return cmdWorkersScale([]string{"my-worker", "--metric", "invalid_metric"})
	})
	if code != 1 {
		t.Fatalf("cmdWorkersScale(invalid metric) = %d, want 1", code)
	}
	if !strings.Contains(stderr, "must be one of {queue_lag, queue_depth}") {
		t.Errorf("unexpected stderr: %s", stderr)
	}

	// invalid stop signal
	code, stderr = runWithStderr(t, func() int {
		return cmdWorkersScale([]string{"my-worker", "--stop-signal", "SIGKILL"})
	})
	if code != 1 {
		t.Fatalf("cmdWorkersScale(invalid signal) = %d, want 1", code)
	}
	if !strings.Contains(stderr, "unsupported stop signal") {
		t.Errorf("unexpected stderr: %s", stderr)
	}
}

func TestCmdWorkersLogs_RejectsNonWorker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/web-app" {
			w.Header().Set("Content-Type", "application/json")
			app := api.AppResponse{
				ID:   "app-web",
				Slug: "web-app",
				Manifest: api.AppManifest{
					ExecutionMode: api.ExecutionModeRequest,
				},
			}
			_ = json.NewEncoder(w).Encode(app)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)

	code, stderr := runWithStderr(t, func() int {
		return cmdWorkersLogs([]string{"web-app"})
	})
	if code != 1 {
		t.Fatalf("cmdWorkersLogs(non-worker) = %d, want 1", code)
	}
	if !strings.Contains(stderr, "not a worker") {
		t.Fatalf("expected 'not a worker' error; got: %s", stderr)
	}
}
