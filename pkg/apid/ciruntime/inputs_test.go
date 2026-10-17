package ciruntime_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/guest/cirunner"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/ciruntime"
	"github.com/onebox-faas/faas/pkg/ci"
)

type inputExecutor struct{ calls atomic.Int32 }

func (e *inputExecutor) Run(ctx context.Context, launch cirunner.StepExecution, logs io.Writer) (int, error) {
	dir := launch.Directory.Name()
	e.calls.Add(1)
	raw, err := os.ReadFile(filepath.Join(dir, ci.ArtifactInputsDirectory, "report"))
	if err != nil || string(raw) != "synthetic test report" {
		return 125, errors.New("dependency input missing")
	}
	_, _ = logs.Write([]byte("verified dependency input\n"))
	return 0, nil
}

func consumerFixture(t *testing.T) *runtimeHarness {
	return consumerFixtureJob(t, nil)
}

func consumerFixtureJob(t *testing.T, configure func(*ci.Job)) *runtimeHarness {
	t.Helper()
	image := "registry.example/ci@sha256:" + strings.Repeat("a", 64)
	def := ci.Definition{Version: 1, Repository: "gregale/runtime", Jobs: map[string]ci.Job{
		"test": {Image: image, TimeoutSeconds: 60, Steps: []ci.Step{{Name: "prepare", Run: "synthetic prepare"}, {Name: "unit", Run: "synthetic unit"}}, Artifacts: []string{"reports/**"}},
		"use":  {Image: image, TimeoutSeconds: 60, Needs: []string{"test"}, Inputs: []ci.ArtifactInput{{Name: "report", Job: "test", Path: "reports/unit.xml"}}, Steps: []ci.Step{{Name: "consume", Run: "synthetic consume"}}},
	}}
	if configure != nil {
		job := def.Jobs["use"]
		configure(&job)
		def.Jobs["use"] = job
	}
	h := fixtureDefinition(t, &def)
	result, err := h.run(t, &fakeExecutor{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Fake provider receipts stay in the real managed journal used by dispatch.
	artifact := result.Artifacts[0]
	if _, err := h.pool.Exec(t.Context(), `INSERT INTO ci_managed_objects(object_key,id,account_id,execution_id,attempt_id,job_id,kind,digest,bytes,state,created_at,expires_at,ready_at) VALUES($1,$2,$3,$4,$5,$6,'artifact',$7,$8,'ready',clock_timestamp(),clock_timestamp()+interval '7 days',clock_timestamp())`, artifact.Key, uuid.NewString(), h.account, h.row.ID, h.proof.RunID, h.row.Compute.Templates["test"].ID, artifact.Digest, artifact.Bytes); err != nil {
		t.Fatal(err)
	}
	h.nativeExit(t, ci.StatusSucceeded, 0, false)
	h.tick(t)
	if h.row.Jobs[1].Attempt != nil {
		t.Fatal("sealed producer started consumer before native stop")
	}
	h.nativeExit(t, ci.StatusSucceeded, 0, true)
	h.tick(t)
	h.tick(t)
	var node string
	if err := h.pool.QueryRow(t.Context(), "SELECT node_id::text FROM instances WHERE id=$1", h.proof.InstanceID).Scan(&node); err != nil {
		t.Fatal(err)
	}
	run := h.row.Jobs[1].Attempt.ID
	instance, lease := uuid.NewString(), uuid.NewString()
	if _, err := h.pool.Exec(t.Context(), `INSERT INTO instances(id,node_id,state,ram_mb,kind,job_id,mode) VALUES($1,$2,'running',$3,'job_task',$4,'job')`, instance, node, api.CIJobsRAMMB, h.row.Compute.Templates["use"].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), `UPDATE job_tasks SET status='claimed',instance_id=$2,started_at=clock_timestamp(),lease_token=$3,lease_expires_at=clock_timestamp()+interval '2 minutes',last_lease_node=$4 WHERE run_id=$1`, run, instance, lease, node); err != nil {
		t.Fatal(err)
	}
	h.proof, err = h.store.IssueJobsRuntime(t.Context(), h.cfg, ci.RuntimeHostProof{RunID: run, InstanceID: instance, LeaseToken: lease})
	if err != nil {
		t.Fatal(err)
	}
	h.view, err = h.store.AuthorizeJobsRuntime(t.Context(), h.cfg, h.proof, ci.RuntimeControl)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func runInputConsumer(t *testing.T, h *runtimeHarness, e *inputExecutor) (ci.JobsResultManifest, error) {
	t.Helper()
	transport, err := cirunner.NewTransport(h.server.URL, h.proof, h.server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return (cirunner.Runner{Manifest: h.view.Manifest, Transport: transport, Executor: e, WorkParent: t.TempDir()}).Run(t.Context())
}

func TestCIArtifactInputsHTTPSRunnerRestartAndFailure(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "restart-and-success", true: "corrupt-input-no-commands"}[corrupt], func(t *testing.T) {
			h := consumerFixture(t)
			// Recreate API/controller objects against the durable run command and state.
			h.store = ci.NewPostgresStore(h.pool)
			h.backend.Store = h.store
			h.backend.Output = ci.SealedJobsOutput{Store: h.store, Blobs: h.objects}
			h.server.Close()
			h.server = httptest.NewTLSServer((ciruntime.Handler{Config: h.cfg, Store: h.store, Blobs: h.objects}).Routes())
			t.Cleanup(h.server.Close)
			if corrupt {
				input := h.view.Manifest.Inputs[0]
				key := "ci/accounts/" + h.account + "/executions/" + h.row.ID + "/attempts/" + input.AttemptID + "/output/artifacts/" + input.Path
				h.objects.mu.Lock()
				h.objects.data[key] = []byte("corrupt")
				h.objects.mu.Unlock()
			}
			executor := &inputExecutor{}
			result, err := runInputConsumer(t, h, executor)
			want := ci.StatusSucceeded
			if corrupt {
				want = ci.StatusFailed
			}
			if err != nil || result.Status != want || (corrupt && executor.calls.Load() != 0) || (!corrupt && executor.calls.Load() != 1) {
				t.Fatalf("consumer result: %s calls=%d err=%v", result.Status, executor.calls.Load(), err)
			}
			h.tick(t)
			if h.row.Status != ci.StatusRunning {
				t.Fatal("guest seal certified native consumer completion")
			}
			code := 0
			if corrupt {
				code = 125
			}
			h.nativeExit(t, want, code, true)
			h.tick(t)
			if h.row.Status != want {
				t.Fatalf("final consumer outcome: %s", h.row.Status)
			}
		})
	}
}

func TestCIArtifactInputsHTTPAuthority(t *testing.T) {
	h := consumerFixture(t)
	for _, test := range []struct {
		name, suffix string
		mutate       func(*http.Request)
		status       int
	}{
		{name: "declared", suffix: "report", status: http.StatusOK},
		{name: "undeclared", suffix: "other", status: http.StatusNotFound},
		{name: "query-override", suffix: "report?path=reports/unit.xml", status: http.StatusNotFound},
		{name: "account-auth", suffix: "report", mutate: func(r *http.Request) { r.Header.Set("Authorization", "Bearer unrelated") }, status: http.StatusNotFound},
		{name: "wrong-instance", suffix: "report", mutate: func(r *http.Request) { r.Header.Set(ci.RuntimeInstanceHeader, uuid.NewString()) }, status: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, h.server.URL+"/v1/internal/ci/runtime/"+h.proof.RunID+"/inputs/"+test.suffix, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set(ci.RuntimeInstanceHeader, h.proof.InstanceID)
			request.Header.Set(ci.RuntimeCapabilityHeader, h.proof.Capability)
			if test.mutate != nil {
				test.mutate(request)
			}
			response, err := h.server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil || response.StatusCode != test.status {
				t.Fatalf("input HTTP response: %d %v", response.StatusCode, err)
			}
			if test.status == http.StatusOK && (!bytes.Equal(raw, []byte("synthetic test report")) || response.Header.Get("Cache-Control") != "no-store") {
				t.Fatal("declared input response lost verified content or no-store")
			}
		})
	}
	if _, err := h.store.CancelExecution(t.Context(), h.account, h.row.ID); err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, h.server.URL+"/v1/internal/ci/runtime/"+h.proof.RunID+"/inputs/report", nil)
	request.Header.Set(ci.RuntimeInstanceHeader, h.proof.InstanceID)
	request.Header.Set(ci.RuntimeCapabilityHeader, h.proof.Capability)
	response, err := h.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatal("cancelled consumer retained input access")
	}
}

type cancellingInputBlobs struct {
	base   *blobs
	cancel func()
}

func (b cancellingInputBlobs) Put(ctx context.Context, key string, body io.Reader) error {
	return b.base.Put(ctx, key, body)
}
func (b cancellingInputBlobs) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	body, err := b.base.Get(ctx, key)
	if err == nil {
		b.cancel()
	}
	return body, err
}

func TestCIArtifactInputsCancellationDuringSpoolingReturnsNoBytes(t *testing.T) {
	h := consumerFixture(t)
	handler := ciruntime.Handler{Config: h.cfg, Store: h.store, Blobs: cancellingInputBlobs{base: h.objects, cancel: func() {
		if _, err := h.store.CancelExecution(t.Context(), h.account, h.row.ID); err != nil {
			t.Error(err)
		}
	}}}
	server := httptest.NewTLSServer(handler.Routes())
	defer server.Close()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/v1/internal/ci/runtime/"+h.proof.RunID+"/inputs/report", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(ci.RuntimeInstanceHeader, h.proof.InstanceID)
	request.Header.Set(ci.RuntimeCapabilityHeader, h.proof.Capability)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusNotFound || bytes.Contains(raw, []byte("synthetic test report")) {
		t.Fatal("late cancellation leaked input payload")
	}
}

type cancelAfterInputTransport struct {
	base   http.RoundTripper
	cancel func() error
	calls  atomic.Int32
}

func (t *cancelAfterInputTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(r)
	if err == nil && strings.Contains(r.URL.Path, "/inputs/") && response.StatusCode == http.StatusOK {
		t.calls.Add(1)
		if err := t.cancel(); err != nil {
			_ = response.Body.Close()
			return nil, err
		}
	}
	return response, err
}

func TestCIArtifactInputsGuestRechecksCancellationAfterDownload(t *testing.T) {
	h := consumerFixture(t)
	client := *h.server.Client()
	fault := &cancelAfterInputTransport{base: client.Transport, cancel: func() error {
		_, err := h.store.CancelExecution(t.Context(), h.account, h.row.ID)
		if err != nil {
			t.Error(err)
		}
		return err
	}}
	client.Transport = fault
	transport, err := cirunner.NewTransport(h.server.URL, h.proof, &client)
	if err != nil {
		t.Fatal(err)
	}
	executor := &inputExecutor{}
	result, err := (cirunner.Runner{Manifest: h.view.Manifest, Transport: transport, Executor: executor, WorkParent: t.TempDir()}).Run(t.Context())
	if err != nil || result.Status != ci.StatusFailed || fault.calls.Load() != 1 || executor.calls.Load() != 0 {
		t.Fatalf("cancellation after input headers: result=%s commands=%d downloads=%d err=%v", result.Status, executor.calls.Load(), fault.calls.Load(), err)
	}
	h.nativeExit(t, ci.StatusCancelled, 143, true)
	h.tick(t)
	if h.row.Status != ci.StatusCancelled {
		t.Fatal("native cancellation did not retain precedence")
	}
}
