package ciruntime_test

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/guest/cirunner"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/ciruntime"
	"github.com/onebox-faas/faas/pkg/ci"
)

type matrixExecutor struct {
	variant  string
	consumer bool
	base     fakeExecutor
}

func (e *matrixExecutor) Run(ctx context.Context, launch cirunner.StepExecution, logs io.Writer) (int, error) {
	if e.consumer {
		raw, err := os.ReadFile(filepath.Join(launch.Directory.Name(), ci.ArtifactInputsDirectory, "report"))
		if err != nil || string(raw) != "beta report" {
			return 125, errors.New("consumer received the wrong matrix producer")
		}
		_, err = io.WriteString(logs, "consumed beta report\n")
		return 0, err
	}
	values := make(map[string]string)
	for _, entry := range launch.Environment {
		name, value, _ := strings.Cut(entry, "=")
		values[name] = value
	}
	want := e.variant
	if launch.Step.Name == "prepare" {
		want = "step"
	}
	if values["VARIANT"] != e.variant || values["MODE"] != want || values["BASE"] != "shared" {
		return 125, errors.New("variant environment or step precedence was lost")
	}
	code, err := e.base.Run(ctx, launch, logs)
	if err == nil && code == 0 && launch.Step.Name == "prepare" {
		err = os.WriteFile(filepath.Join(launch.Directory.Name(), "reports", "unit.xml"), []byte(e.variant+" report"), 0o600)
	}
	_, _ = io.WriteString(logs, e.variant+" log\n")
	return code, err
}

func activateMatrixJob(t *testing.T, h *runtimeHarness, name string) {
	t.Helper()
	var node string
	if err := h.pool.QueryRow(t.Context(), "SELECT node_id::text FROM instances WHERE id=$1", h.proof.InstanceID).Scan(&node); err != nil {
		t.Fatal(err)
	}
	var run string
	for _, job := range h.row.Jobs {
		if job.Name == name && job.Attempt != nil {
			run = job.Attempt.ID
		}
	}
	if run == "" {
		t.Fatal("matrix job is not ready")
	}
	instance, lease := uuid.NewString(), uuid.NewString()
	if _, err := h.pool.Exec(t.Context(), `INSERT INTO instances(id,node_id,state,ram_mb,kind,job_id,mode) VALUES($1,$2,'running',$3,'job_task',$4,'job')`, instance, node, api.CIJobsRAMMB, h.row.Compute.Templates[name].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), `UPDATE job_tasks SET status='claimed',instance_id=$2,started_at=clock_timestamp(),lease_token=$3,lease_expires_at=clock_timestamp()+interval '2 minutes',last_lease_node=$4 WHERE run_id=$1`, run, instance, lease, node); err != nil {
		t.Fatal(err)
	}
	var err error
	h.proof, err = h.store.IssueJobsRuntime(t.Context(), h.cfg, ci.RuntimeHostProof{RunID: run, InstanceID: instance, LeaseToken: lease})
	if err != nil {
		t.Fatal(err)
	}
	h.view, err = h.store.AuthorizeJobsRuntime(t.Context(), h.cfg, h.proof, ci.RuntimeControl)
	if err != nil {
		t.Fatal(err)
	}
}

func runMatrixGuest(t *testing.T, h *runtimeHarness, e *matrixExecutor) ci.JobsResultManifest {
	t.Helper()
	transport, err := cirunner.NewTransport(h.server.URL, h.proof, h.server.Client())
	if err != nil {
		t.Fatal(err)
	}
	result, err := (cirunner.Runner{Manifest: h.view.Manifest, Transport: transport, Executor: e, WorkParent: t.TempDir()}).Run(t.Context())
	if err != nil || result.Status != ci.StatusSucceeded {
		t.Fatalf("matrix guest result: %s %v", result.Status, err)
	}
	if !e.consumer {
		artifact := result.Artifacts[0]
		if _, err := h.pool.Exec(t.Context(), `INSERT INTO ci_managed_objects(object_key,id,account_id,execution_id,attempt_id,job_id,kind,digest,bytes,state,created_at,expires_at,ready_at) VALUES($1,$2,$3,$4,$5,$6,'artifact',$7,$8,'ready',clock_timestamp(),clock_timestamp()+interval '7 days',clock_timestamp())`, artifact.Key, uuid.NewString(), h.account, h.row.ID, h.proof.RunID, h.row.Compute.Templates[h.view.Manifest.Job.Name].ID, artifact.Digest, artifact.Bytes); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func TestCIMatrixHTTPSVariantsExplicitInputAndRestart(t *testing.T) {
	image := "registry.example/ci@sha256:" + strings.Repeat("a", 64)
	def := ci.Definition{Version: 1, Repository: "gregale/runtime", Jobs: map[string]ci.Job{
		"test": {Image: image, TimeoutSeconds: 60, Env: map[string]string{"MODE": "job", "BASE": "shared"}, Matrix: map[string]ci.MatrixVariant{
			"alpha": {Env: map[string]string{"VARIANT": "alpha", "MODE": "alpha"}},
			"beta":  {Env: map[string]string{"VARIANT": "beta", "MODE": "beta"}},
		}, Steps: []ci.Step{{Name: "prepare", Run: "synthetic prepare", Env: map[string]string{"MODE": "step"}}, {Name: "unit", Run: "synthetic unit"}}, Artifacts: []string{"reports/**"}},
		"use": {Image: image, TimeoutSeconds: 60, Needs: []string{"test"}, Inputs: []ci.ArtifactInput{{Name: "report", Job: "test-beta", Path: "reports/unit.xml"}}, Steps: []ci.Step{{Name: "consume", Run: "synthetic consume"}}},
	}}
	h := fixtureDefinition(t, &def)
	if h.view.Manifest.Job.Name != "test-alpha" || h.view.Manifest.Job.Matrix != nil {
		t.Fatal("first variant did not receive a concrete manifest")
	}
	alpha := runMatrixGuest(t, h, &matrixExecutor{variant: "alpha"})
	h.nativeExit(t, ci.StatusSucceeded, 0, true)
	for range 2 {
		h.tick(t)
	}
	if h.row.Jobs[2].Attempt != nil {
		t.Fatal("one variant released whole-matrix dependency")
	}
	activateMatrixJob(t, h, "test-beta")
	beta := runMatrixGuest(t, h, &matrixExecutor{variant: "beta"})
	if alpha.AttemptID == beta.AttemptID || alpha.Artifacts[0].Key == beta.Artifacts[0].Key || alpha.Artifacts[0].Digest == beta.Artifacts[0].Digest {
		t.Fatal("variant outputs share ownership or content")
	}
	h.nativeExit(t, ci.StatusSucceeded, 0, false)
	h.tick(t)
	if h.row.Jobs[2].Attempt != nil {
		t.Fatal("producer seal bypassed native stop")
	}
	h.nativeExit(t, ci.StatusSucceeded, 0, true)
	for range 3 {
		h.tick(t)
	}
	h.store = ci.NewPostgresStore(h.pool)
	h.backend.Store = h.store
	h.backend.Output = ci.SealedJobsOutput{Store: h.store, Blobs: h.objects}
	h.server.Close()
	h.server = httptest.NewTLSServer((ciruntime.Handler{Config: h.cfg, Store: h.store, Blobs: h.objects}).Routes())
	t.Cleanup(h.server.Close)
	activateMatrixJob(t, h, "use")
	if len(h.view.Manifest.Inputs) != 1 || h.view.Manifest.Inputs[0].AttemptID != beta.AttemptID || h.view.Manifest.Inputs[0].Digest != beta.Artifacts[0].Digest {
		t.Fatal("restart changed the explicitly selected variant")
	}
	runMatrixGuest(t, h, &matrixExecutor{consumer: true})
	h.nativeExit(t, ci.StatusSucceeded, 0, true)
	for range 3 {
		h.tick(t)
	}
	if h.row.Status != ci.StatusSucceeded {
		t.Fatalf("matrix pipeline did not settle: %s", h.row.Status)
	}
}
