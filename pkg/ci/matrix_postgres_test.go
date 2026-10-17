package ci

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func matrixJobsHarness(t *testing.T) *jobsHarness {
	t.Helper()
	h := newJobsHarness(t)
	def := matrixDefinition()
	revision, err := h.store.PutRevision(t.Context(), h.account, "matrix", def)
	if err != nil {
		t.Fatal(err)
	}
	binding := h.binding.GitHubBindingRequest
	binding.RevisionID = revision.ID
	h.binding, err = h.store.PutGitHubBinding(t.Context(), h.account, h.binding.Name, binding)
	if err != nil {
		t.Fatal(err)
	}
	testTemplate, buildTemplate := h.req.Templates["test"], h.req.Templates["build"]
	h.req.RevisionID = revision.ID
	h.req.Templates = map[string]string{"test-alpha": testTemplate, "test-beta": buildTemplate, "build": buildTemplate}
	return h
}

func TestCIMatrixProfilesTemplatesRestartAndRerun(t *testing.T) {
	h := matrixJobsHarness(t)
	bad := h.req
	bad.Templates = map[string]string{"test": h.req.Templates["test-alpha"], "build": h.req.Templates["build"]}
	if _, _, err := AdmitJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, "logical-template", bad); err == nil {
		t.Fatal("logical template map admitted expanded jobs")
	}
	bad.Templates = clone(h.req.Templates)
	bad.Templates["test-beta"] = h.req.Templates["test-alpha"]
	if _, _, err := AdmitJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, "wrong-image", bad); err == nil {
		t.Fatal("variant accepted the wrong pinned image")
	}
	if h.count(t, "SELECT count(*) FROM ci_executions") != 0 || h.count(t, "SELECT count(*) FROM job_runs") != 0 {
		t.Fatal("invalid matrix template admission left compute intent")
	}
	profile := h.profile(t, "matrix")
	req := JobsSubmitRequest{ProfileID: profile.ID, SubmitRequest: SubmitRequest{CommitSHA: h.req.CommitSHA}}
	row, replay, err := AdmitJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, "matrix-profile", req)
	if err != nil || replay || len(row.Jobs) != 3 {
		t.Fatalf("matrix profile admission: %v", err)
	}
	row = h.tick(t, row)
	if row.Jobs[0].Name != "test-alpha" || row.Jobs[1].Name != "test-beta" || row.Jobs[2].Attempt != nil || h.count(t, "SELECT count(*) FROM job_runs") != 2 {
		t.Fatal("matrix fanout did not preserve its dependency barrier")
	}
	identities := []string{row.Jobs[0].Attempt.ID, row.Jobs[1].Attempt.ID}
	if identities[0] == identities[1] {
		t.Fatal("matrix variants share an attempt")
	}
	var command []string
	if err := h.pool.QueryRow(t.Context(), "SELECT command FROM job_runs WHERE id=$1", identities[1]).Scan(&command); err != nil {
		t.Fatal(err)
	}
	manifest, err := DecodeJobsManifest(command[2])
	if err != nil || manifest.Job.Name != "test-beta" || manifest.Job.Image != row.Plan.Jobs[1].Image || manifest.Job.Env["VARIANT"] != "beta" || manifest.Job.Steps[0].Env["MODE"] != "step" || manifest.Job.Matrix != nil {
		t.Fatal("variant manifest lost its frozen configuration")
	}
	before, _ := json.Marshal(manifest)
	h.restart()
	if err := h.pool.QueryRow(t.Context(), "SELECT command FROM job_runs WHERE id=$1", identities[1]).Scan(&command); err != nil {
		t.Fatal(err)
	}
	manifest, err = DecodeJobsManifest(command[2])
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(manifest)
	if !bytes.Equal(before, after) {
		t.Fatal("restart changed variant command")
	}
	row = h.tick(t, row)
	if row.Jobs[0].Attempt.ID != identities[0] || row.Jobs[1].Attempt.ID != identities[1] || h.count(t, "SELECT count(*) FROM job_runs") != 2 {
		t.Fatal("restart submitted fresh matrix attempts")
	}
	h.publish(t, row, "test-alpha", StatusSucceeded, 0, []byte("alpha logs"), true, true)
	betaInstance := h.publish(t, row, "test-beta", StatusSucceeded, 0, []byte("beta logs"), true, false)
	for range 2 {
		row = h.tick(t, row)
	}
	if row.Jobs[0].Status != StatusSucceeded || row.Jobs[1].Status != StatusRunning || row.Jobs[2].Attempt != nil {
		t.Fatal("aggregate started before every variant stopped")
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE instances SET state='stopped' WHERE id=$1", betaInstance); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		row = h.tick(t, row)
	}
	if row.Jobs[2].Attempt == nil || h.count(t, "SELECT count(*) FROM job_runs") != 3 {
		t.Fatal("all-variant dependency did not release aggregate")
	}
	for index, text := range []string{"alpha logs", "beta logs"} {
		page, err := ExecutionLogs(t.Context(), h.store, h.account, row.ID, identities[index], 0, 64)
		if err != nil || string(page.Data) != text || !page.Complete {
			t.Fatal("variant logs crossed attempt identity")
		}
	}
	h.publish(t, row, "build", StatusSucceeded, 0, nil, false, true)
	for range 3 {
		row = h.tick(t, row)
	}
	check, err := executionCheck(executionRecord{Execution: row, Version: 1})
	if err != nil || row.Status != StatusSucceeded || !bytes.Contains([]byte(check.Summary), []byte("test-alpha: succeeded")) || !bytes.Contains([]byte(check.Summary), []byte("test-beta: succeeded")) {
		t.Fatal("matrix results were lost from GitHub Check projection")
	}
	child, replay, err := RerunJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, row.ID, "matrix-rerun")
	if err != nil || replay || !reflect.DeepEqual(child.Plan, row.Plan) || !reflect.DeepEqual(child.Compute.Templates, row.Compute.Templates) {
		t.Fatalf("matrix rerun changed frozen configuration: %v", err)
	}
	child = h.tick(t, child)
	if child.Jobs[0].Attempt.ID == identities[0] || child.Jobs[1].Attempt.ID == identities[1] {
		t.Fatal("matrix rerun reused prior attempts")
	}
	if _, err := h.store.Execution(t.Context(), h.other, row.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("matrix execution crossed account")
	}
}

func TestCIMatrixFailureAndCancellationArePerVariant(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "cancellation"}[cancelled], func(t *testing.T) {
			h := matrixJobsHarness(t)
			row := h.tick(t, h.admit(t, "matrix"))
			if cancelled {
				var err error
				row, err = h.store.CancelExecution(t.Context(), h.account, row.ID)
				if err != nil {
					t.Fatal(err)
				}
				row = h.tick(t, row)
				// Both variants were queued without native claims. Cancellation
				// must stop both task intents and never start the waiting aggregate.
				for range 4 {
					row = h.tick(t, row)
				}
				if row.Status != StatusCancelled || h.count(t, "SELECT count(*) FROM job_runs") != 2 || row.Jobs[2].Attempt != nil {
					t.Fatal("matrix cancellation admitted additional work")
				}
				return
			}
			h.publish(t, row, "test-alpha", StatusFailed, 7, []byte("alpha failed"), true, true)
			for range 2 {
				row = h.tick(t, row)
			}
			if row.Jobs[1].Status != StatusRunning || row.Jobs[2].Status != StatusSkipped {
				t.Fatal("variant failure cancelled a sibling or admitted dependent")
			}
			h.publish(t, row, "test-beta", StatusSucceeded, 0, []byte("beta succeeded"), true, true)
			for range 3 {
				row = h.tick(t, row)
			}
			if row.Status != StatusFailed || row.Jobs[0].Status != StatusFailed || row.Jobs[1].Status != StatusSucceeded || row.Jobs[2].Attempt != nil {
				t.Fatal("matrix result mixed independent variant outcomes")
			}
		})
	}
}

func TestCIMatrixResolvedInputPinsNamedProducer(t *testing.T) {
	def := matrixDefinition()
	job := def.Jobs["build"]
	job.Inputs = []ArtifactInput{{Name: "report", Job: "test-beta", Path: "reports/unit.xml"}}
	def.Jobs["build"] = job
	plan, _, err := Compile(def)
	if err != nil {
		t.Fatal(err)
	}
	row := Execution{Plan: plan}
	for _, name := range []string{"test-alpha", "test-beta"} {
		row.Jobs = append(row.Jobs, JobExecution{Name: name, Status: StatusSucceeded, Attempt: &Attempt{ID: uuid.NewString(), Status: StatusSucceeded, Artifacts: []ArtifactRef{{Path: "reports/unit.xml", Digest: jobsDigest([]byte(name)), Bytes: 1}}}})
	}
	inputs, err := resolveArtifactInputs(row, plan.Jobs[2])
	if err != nil || len(inputs) != 1 || inputs[0].AttemptID != row.Jobs[1].Attempt.ID || inputs[0].Digest != row.Jobs[1].Attempt.Artifacts[0].Digest {
		t.Fatal("input selected a different matrix producer")
	}
}
