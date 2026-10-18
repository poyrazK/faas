package ciruntime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/guest/cirunner"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/ciruntime"
	"github.com/onebox-faas/faas/pkg/ci"
)

type progressExecutor struct {
	ready, release chan struct{}
	prefix         []byte
}

func (e *progressExecutor) Run(ctx context.Context, launch cirunner.StepExecution, logs io.Writer) (int, error) {
	step := launch.Step
	if step.Name == "prepare" {
		_, err := logs.Write([]byte("prepare\n"))
		return 0, err
	}
	if _, err := logs.Write(e.prefix); err != nil {
		return 125, err
	}
	close(e.ready)
	select {
	case <-ctx.Done():
		return 143, ctx.Err()
	case <-e.release:
		_, err := logs.Write([]byte("tail\n"))
		return 0, err
	}
}

func TestCILiveProgressTLSRunnerRestartAndLostAcknowledgement(t *testing.T) {
	h := fixture(t)
	// Swap the stateless handler's store as an apid restart, without replacing
	// the TLS origin or any durable runtime/attempt identity.
	h.server.Close()
	var current atomic.Pointer[ci.PostgresStore]
	current.Store(h.store)
	h.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		(ciruntime.Handler{Config: h.cfg, Store: current.Load(), Blobs: h.objects}).Routes().ServeHTTP(w, r)
	}))
	t.Cleanup(h.server.Close)
	client := *h.server.Client()
	fault := &lostAck{base: client.Transport, suffix: "/progress"}
	client.Transport = fault
	transport, err := cirunner.NewTransport(h.server.URL, h.proof, &client)
	if err != nil {
		t.Fatal(err)
	}
	executor := &progressExecutor{ready: make(chan struct{}), release: make(chan struct{}), prefix: append([]byte{0, 255}, bytes.Repeat([]byte("x"), api.CILogPageMaxBytes+5)...)}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	work := t.TempDir()
	type completed struct {
		result ci.JobsResultManifest
		err    error
	}
	done := make(chan completed, 1)
	go func() {
		result, err := (cirunner.Runner{Manifest: h.view.Manifest, Transport: transport, Executor: executor, WorkParent: work}).Run(ctx)
		done <- completed{result, err}
	}()
	select {
	case <-executor.ready:
	case <-time.After(6 * time.Second):
		t.Fatal("runner did not reach live step")
	}
	wanted := append([]byte("prepare\n"), executor.prefix...)
	until := time.Now().Add(6 * time.Second)
	for {
		h.tick(t)
		a := h.row.Jobs[0].Attempt
		if len(a.Logs) == len(wanted) && a.Steps[1].Status == ci.StatusRunning {
			break
		}
		if time.Now().After(until) {
			t.Fatalf("live output missing: status=%s bytes=%d", a.Steps[1].Status, len(a.Logs))
		}
		time.Sleep(20 * time.Millisecond)
	}
	a := h.row.Jobs[0].Attempt
	if !bytes.Equal(a.Logs, wanted) || a.Steps[0].StartedAt == nil || a.Steps[0].FinishedAt == nil || a.Steps[1].StartedAt == nil || a.FinishedAt != nil || !fault.lost.Load() {
		t.Fatal("live telemetry lost timing/binary bytes or certified completion")
	}
	resume := len(a.Logs)
	h.store = ci.NewPostgresStore(h.pool)
	h.backend.Store = h.store
	h.backend.Output = ci.SealedJobsOutput{Store: h.store, Blobs: h.objects}
	current.Store(h.store)
	close(executor.release)
	select {
	case out := <-done:
		if out.err != nil || out.result.Status != ci.StatusSucceeded {
			t.Fatalf("runner finalize: %v", out.err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("runner did not finalize")
	}
	h.tick(t)
	if h.row.Status != ci.StatusRunning {
		t.Fatal("seal bypassed native stop")
	}
	h.nativeExit(t, ci.StatusSucceeded, 0, true)
	h.tick(t)
	page, err := ci.ExecutionLogs(t.Context(), h.store, h.account, h.row.ID, h.proof.RunID, resume, api.CILogPageMaxBytes)
	if err != nil || !page.Complete || string(page.Data) != "tail\n" || h.row.Jobs[0].Attempt.OutputIncomplete {
		t.Fatalf("resumed final logs: %+v %v", page, err)
	}
}

func TestCILiveProgressHTTPStrictBodyAndScope(t *testing.T) {
	h := fixture(t)
	steps := []ci.StepResult{{Name: "prepare", Status: ci.StatusRunning}, {Name: "unit", Status: ci.StatusQueued}}
	valid, _ := json.Marshal(ci.ProgressUpdate{Sequence: 1, Steps: steps, Logs: []byte("prefix")})
	for _, tc := range []struct {
		name   string
		body   []byte
		modify func(*http.Request)
		want   int
	}{
		{"missing_sequence", []byte(`{}`), nil, 400},
		{"unknown", []byte(`{"sequence":1,"log_offset":0,"steps":[],"logs":"","status":"succeeded"}`), nil, 400},
		{"duplicate", []byte(`{"sequence":1,"sequence":2}`), nil, 400},
		{"array", []byte(`[]`), nil, 400},
		{"oversize", bytes.Repeat([]byte("x"), api.CIJobsProgressRequestMaxBytes+1), nil, 413},
		{"wrong_instance", valid, func(r *http.Request) { r.Header.Set(ci.RuntimeInstanceHeader, "wrong") }, 404},
		{"account_auth", valid, func(r *http.Request) { r.Header.Set("Authorization", "Bearer forbidden") }, 404},
		{"query", valid, func(r *http.Request) { r.URL.RawQuery = "override=true" }, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := http.NewRequestWithContext(t.Context(), http.MethodPut, h.server.URL+"/v1/internal/ci/runtime/"+h.proof.RunID+"/progress", bytes.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set(ci.RuntimeInstanceHeader, h.proof.InstanceID)
			r.Header.Set(ci.RuntimeCapabilityHeader, h.proof.Capability)
			if tc.modify != nil {
				tc.modify(r)
			}
			response, err := h.server.Client().Do(r)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode != tc.want {
				t.Fatalf("status=%d want=%d", response.StatusCode, tc.want)
			}
		})
	}
	var sequence int32
	if err := h.pool.QueryRow(t.Context(), "SELECT progress_sequence FROM ci_job_attempts WHERE attempt_id=$1", h.proof.RunID).Scan(&sequence); err != nil || sequence != 0 {
		t.Fatal("rejected HTTP request changed progress")
	}
}
