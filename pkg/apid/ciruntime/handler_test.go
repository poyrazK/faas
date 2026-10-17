package ciruntime_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/guest/cirunner"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/ciruntime"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

type blobs struct {
	mu     sync.Mutex
	data   map[string][]byte
	writes map[string]int
}

func (b *blobs) Put(ctx context.Context, key string, reader io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data[key] = bytes.Clone(raw)
	b.writes[key]++
	return nil
}
func (b *blobs) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	raw, ok := b.data[key]
	if !ok {
		return nil, errors.New("synthetic object absent")
	}
	return io.NopCloser(bytes.NewReader(bytes.Clone(raw))), nil
}

type archiveSource struct {
	objects *blobs
	raw     []byte
}

func (s archiveSource) Stage(ctx context.Context, req ci.JobsSourceRequest) (ci.SourceArchive, error) {
	sum := sha256.Sum256(s.raw)
	digest := hex.EncodeToString(sum[:])
	key := "ci/accounts/" + req.Key.AccountID + "/executions/" + req.Key.ExecutionID + "/attempts/" + req.Key.AttemptID + "/source/" + digest + ".tar.gz"
	if err := s.objects.Put(ctx, key, bytes.NewReader(s.raw)); err != nil {
		return ci.SourceArchive{}, err
	}
	return ci.SourceArchive{Key: key, Digest: "sha256:" + digest, Bytes: int64(len(s.raw))}, nil
}

type runtimeHarness struct {
	pool    *pgxpool.Pool
	store   *ci.PostgresStore
	cfg     ci.Config
	backend ci.JobsBackend
	account string
	row     ci.Execution
	proof   ci.RuntimeProof
	view    ci.RuntimeView
	objects *blobs
	server  *httptest.Server
}

func fixture(t *testing.T) *runtimeHarness { return fixtureDefinition(t, nil) }

func fixtureDefinition(t *testing.T, override *ci.Definition) *runtimeHarness {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	h := &runtimeHarness{pool: pool, store: ci.NewPostgresStore(pool), account: uuid.NewString(), objects: &blobs{data: map[string][]byte{}, writes: map[string]int{}}}
	node, job, instance, lease := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	ctx := t.Context()
	if _, err := pool.Exec(ctx, "INSERT INTO accounts(id,email,plan) VALUES ($1,$2,'pro')", h.account, h.account+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO compute_nodes(id,name,target_url,vpcpus,mem_mb,max_concurrency,admission_ceiling_mb) VALUES ($1,$2,'unix:///ci-test-no-vmmd.sock',4,8192,8,8192)`, node, "ci-"+node); err != nil {
		t.Fatal(err)
	}
	image := "registry.example/ci@sha256:" + strings.Repeat("a", 64)
	if _, err := pool.Exec(ctx, `INSERT INTO jobs(id,account_id,kind,name,image_ref,ram_mb,task_timeout_s,max_parallelism,retry_max,env_overrides,command,image_materialization_status,image_resolved_digest,image_storage_key) VALUES ($1,$2,'batch','ci-runtime',$3,$4,60,1,0,'{}',$5,'ready',$6,$7)`, job, h.account, image, api.CIJobsRAMMB, []string{ci.JobsRunnerPath, ci.JobsRunnerProtocol}, "sha256:"+strings.Repeat("a", 64), "jobs/"+job+"/image.ext4"); err != nil {
		t.Fatal(err)
	}
	definition := ci.Definition{Version: 1, Repository: "gregale/runtime", Jobs: map[string]ci.Job{"test": {Image: image, TimeoutSeconds: 60, Steps: []ci.Step{{Name: "prepare", Run: "synthetic prepare"}, {Name: "unit", Run: "synthetic unit"}}, Artifacts: []string{"reports/**"}}}}
	if override != nil {
		definition = *override
	}
	templates := make(map[string]string)
	plan, _, err := ci.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	for _, planned := range plan.Jobs {
		templates[planned.Name] = job
	}
	revision, err := h.store.PutRevision(ctx, h.account, "runtime", definition)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := h.store.PutGitHubBinding(ctx, h.account, "source", ci.GitHubBindingRequest{RevisionID: revision.ID, InstallationID: 12, RepositoryID: 34, PushBranches: []string{"main"}})
	if err != nil {
		t.Fatal(err)
	}
	h.cfg = ci.ConfigFromEnv(func(name string) string {
		switch name {
		case ci.InternalEnv, ci.GitHubEnv, ci.DispatchEnv, ci.TransportEnv:
			return "1"
		case ci.AccountsEnv:
			return h.account
		}
		return ""
	})
	verify := func(context.Context, string, int64, int64, string) error { return nil }
	h.row, _, err = ci.AdmitJobs(ctx, h.cfg, h.store, verify, h.account, "fresh", ci.JobsSubmitRequest{SubmitRequest: ci.SubmitRequest{RevisionID: revision.ID, CommitSHA: strings.Repeat("a", 40)}, BindingID: binding.ID, Templates: templates})
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	writer := tar.NewWriter(gz)
	body := []byte("pinned repository source\n")
	if err := writer.WriteHeader(&tar.Header{Name: "repository/source.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	h.backend = ci.JobsBackend{Config: h.cfg, Store: h.store, Source: archiveSource{objects: h.objects, raw: archive.Bytes()}, Output: ci.SealedJobsOutput{Store: h.store, Blobs: h.objects}}
	h.row, err = (ci.Engine{Store: h.store, Backend: h.backend}).Tick(ctx, h.account, h.row.ID)
	if err != nil {
		t.Fatal(err)
	}
	run := h.row.Jobs[0].Attempt.ID
	if _, err := pool.Exec(ctx, `INSERT INTO instances(id,node_id,state,ram_mb,kind,job_id,mode) VALUES ($1,$2,'running',$3,'job_task',$4,'job')`, instance, node, api.CIJobsRAMMB, job); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE job_tasks SET status='claimed',instance_id=$2,started_at=clock_timestamp(),lease_token=$3,lease_expires_at=clock_timestamp()+interval '2 minutes',last_lease_node=$4 WHERE run_id=$1`, run, instance, lease, node); err != nil {
		t.Fatal(err)
	}
	h.proof, err = h.store.IssueJobsRuntime(ctx, h.cfg, ci.RuntimeHostProof{RunID: run, InstanceID: instance, LeaseToken: lease})
	if err != nil {
		t.Fatal(err)
	}
	h.view, err = h.store.AuthorizeJobsRuntime(ctx, h.cfg, h.proof, ci.RuntimeControl)
	if err != nil {
		t.Fatal(err)
	}
	h.server = httptest.NewTLSServer((ciruntime.Handler{Config: h.cfg, Store: h.store, Blobs: h.objects}).Routes())
	t.Cleanup(h.server.Close)
	return h
}

type fakeExecutor struct {
	failure  bool
	blocking bool
	ready    chan struct{}
	calls    []string
	logs     int
}

func (e *fakeExecutor) Run(ctx context.Context, launch cirunner.StepExecution, logs io.Writer) (int, error) {
	step, dir := launch.Step, launch.Directory.Name()
	e.calls = append(e.calls, step.Name)
	if step.Name == "prepare" {
		raw, err := os.ReadFile(filepath.Join(dir, "source.txt"))
		if err != nil || string(raw) != "pinned repository source\n" {
			return 125, errors.New("source checkout missing")
		}
		if err := os.MkdirAll(filepath.Join(dir, "reports"), 0o755); err != nil {
			return 125, err
		}
		if err := os.WriteFile(filepath.Join(dir, "reports", "unit.xml"), []byte("synthetic test report"), 0o600); err != nil {
			return 125, err
		}
		_, _ = logs.Write(bytes.Repeat([]byte("x"), e.logs))
		return 0, nil
	}
	if e.blocking {
		close(e.ready)
		<-ctx.Done()
		return 143, ctx.Err()
	}
	if e.failure {
		_, _ = logs.Write([]byte("synthetic unit failed\n"))
		return 2, nil
	}
	return 0, nil
}

func (h *runtimeHarness) run(t *testing.T, executor *fakeExecutor, client *http.Client) (ci.JobsResultManifest, error) {
	t.Helper()
	if client == nil {
		client = h.server.Client()
	}
	transport, err := cirunner.NewTransport(h.server.URL, h.proof, client)
	if err != nil {
		return ci.JobsResultManifest{}, err
	}
	return (cirunner.Runner{Manifest: h.view.Manifest, Transport: transport, Executor: executor, WorkParent: t.TempDir()}).Run(t.Context())
}

func (h *runtimeHarness) nativeExit(t *testing.T, status string, code int, stopped bool) {
	t.Helper()
	if _, err := h.pool.Exec(t.Context(), "UPDATE job_tasks SET status=$2,exit_code=$3,finished_at=clock_timestamp(),lease_token=NULL,lease_expires_at=NULL WHERE run_id=$1", h.proof.RunID, status, code); err != nil {
		t.Fatal(err)
	}
	if stopped {
		if _, err := h.pool.Exec(t.Context(), "UPDATE instances SET state='stopped' WHERE id=$1", h.proof.InstanceID); err != nil {
			t.Fatal(err)
		}
	}
}
func (h *runtimeHarness) tick(t *testing.T) {
	t.Helper()
	row, err := (ci.Engine{Store: h.store, Backend: h.backend}).Tick(t.Context(), h.account, h.row.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.row = row
}

func TestCIRunnerTransport(t *testing.T) {
	probe := pgtest.Open(t)
	probe.Close()
	if os.Getenv(pgtest.UseTemplateDatabase) == "" {
		t.Setenv(pgtest.UseTemplateDatabase, "1")
	}
	for _, failure := range []bool{false, true} {
		name := "success"
		if failure {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			h := fixture(t)
			executor := &fakeExecutor{failure: failure, logs: api.CILogMaxBytesPerAttempt + 7}
			result, err := h.run(t, executor, nil)
			if err != nil {
				t.Fatal(err)
			}
			expected := ci.StatusSucceeded
			code := 0
			if failure {
				expected = ci.StatusFailed
				code = 2
			}
			if result.Status != expected || len(result.Artifacts) != 1 || !result.LogTruncated || len(executor.calls) != 2 {
				t.Fatalf("runner result: %+v", result)
			}
			if _, exists := h.objects.data[h.view.Manifest.OutputPrefix+"result.json"]; exists {
				t.Fatal("result was published outside the seal transaction")
			}
			h.nativeExit(t, expected, code, false)
			h.tick(t)
			if h.row.Status != ci.StatusRunning {
				t.Fatal("CI certified cleanup before native stop")
			}
			if _, err := h.pool.Exec(t.Context(), "UPDATE instances SET state='stopped' WHERE id=$1", h.proof.InstanceID); err != nil {
				t.Fatal(err)
			}
			for range 5 {
				h.tick(t)
				if h.row.Status == expected {
					break
				}
			}
			if h.row.Status != expected || len(h.row.Jobs[0].Attempt.Artifacts) != 1 || !h.row.Jobs[0].Attempt.LogTruncated {
				t.Fatal("guest result did not reach the engine with artifacts/logs")
			}
		})
	}
	t.Run("cancel_finalizes_failure_artifacts", func(t *testing.T) {
		h := fixture(t)
		executor := &fakeExecutor{blocking: true, ready: make(chan struct{})}
		type completed struct {
			result ci.JobsResultManifest
			err    error
		}
		done := make(chan completed, 1)
		go func() { result, err := h.run(t, executor, nil); done <- completed{result, err} }()
		select {
		case <-executor.ready:
		case <-time.After(10 * time.Second):
			t.Fatal("synthetic guest did not reach unit step")
		}
		if _, err := h.store.CancelExecution(t.Context(), h.account, h.row.ID); err != nil {
			t.Fatal(err)
		}
		h.tick(t)
		var result ci.JobsResultManifest
		select {
		case out := <-done:
			if out.err != nil {
				t.Fatal(out.err)
			}
			result = out.result
		case <-time.After(10 * time.Second):
			t.Fatal("guest did not cancel and finalize")
		}
		if result.Status != ci.StatusCancelled || len(result.Artifacts) != 1 {
			t.Fatal("cancellation lost report")
		}
		if _, err := h.pool.Exec(t.Context(), "UPDATE instances SET state='stopped' WHERE id=$1", h.proof.InstanceID); err != nil {
			t.Fatal(err)
		}
		h.tick(t)
		if h.row.Status != ci.StatusCancelled || len(h.row.Jobs[0].Attempt.Artifacts) != 1 {
			t.Fatal("cancelled result did not reach CI")
		}
	})
	t.Run("lost_upload_ack_reuses_object", func(t *testing.T) {
		h := fixture(t)
		client := *h.server.Client()
		fault := &lostAck{base: client.Transport}
		client.Transport = fault
		result, err := h.run(t, &fakeExecutor{}, &client)
		if err != nil || result.Status != ci.StatusSucceeded || !fault.lost.Load() {
			t.Fatalf("retry did not recover: %v", err)
		}
		if h.objects.writes[h.view.Manifest.OutputPrefix+"logs.bin"] != 1 {
			t.Fatal("lost acknowledgement rewrote a retained object")
		}
	})
	t.Run("lost_seal_ack_reuses_transactional_result", func(t *testing.T) {
		h := fixture(t)
		client := *h.server.Client()
		fault := &lostAck{base: client.Transport, suffix: "/result"}
		client.Transport = fault
		result, err := h.run(t, &fakeExecutor{}, &client)
		if err != nil || result.Status != ci.StatusSucceeded || !fault.lost.Load() {
			t.Fatalf("seal retry failed: %v", err)
		}
		var sealed int
		if err := h.pool.QueryRow(t.Context(), "SELECT count(*) FROM ci_job_runtime WHERE result IS NOT NULL").Scan(&sealed); err != nil || sealed != 1 {
			t.Fatalf("seal replay changed publication: %d %v", sealed, err)
		}
	})
	t.Run("invalid_headers_and_flags_do_not_fetch", func(t *testing.T) {
		h := fixture(t)
		for _, mutate := range []func(*http.Request){func(r *http.Request) { r.Header.Set(ci.RuntimeCapabilityHeader, strings.Repeat("f", 64)) }, func(r *http.Request) { r.Header.Add(ci.RuntimeCapabilityHeader, h.proof.Capability) }, func(r *http.Request) { r.Header.Set("Authorization", "Bearer ignored") }, func(r *http.Request) { r.URL.RawQuery = "capability=forbidden" }} {
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, h.server.URL+"/v1/internal/ci/runtime/"+h.proof.RunID+"/source", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set(ci.RuntimeInstanceHeader, h.proof.InstanceID)
			request.Header.Set(ci.RuntimeCapabilityHeader, h.proof.Capability)
			mutate(request)
			response, err := h.server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != http.StatusNotFound {
				t.Fatal("invalid authority was accepted")
			}
		}
		disabled := httptest.NewTLSServer((ciruntime.Handler{Config: ci.Config{}, Store: h.store, Blobs: h.objects}).Routes())
		defer disabled.Close()
		transport, err := cirunner.NewTransport(disabled.URL, h.proof, disabled.Client())
		if err != nil {
			t.Fatal(err)
		}
		view, err := transport.Control(t.Context())
		if err != nil || !view.CancelRequested {
			t.Fatal("disabled dispatch failed to cancel runtime control")
		}
	})
}

type lostAck struct {
	base   http.RoundTripper
	lost   atomic.Bool
	suffix string
}

func (l *lostAck) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := l.base.RoundTrip(request)
	suffix := l.suffix
	if suffix == "" {
		suffix = "/objects"
	}
	if err == nil && request.Method == http.MethodPut && strings.HasSuffix(request.URL.Path, suffix) && !l.lost.Swap(true) {
		_ = response.Body.Close()
		return nil, errors.New("synthetic upload response lost after commit")
	}
	return response, err
}
