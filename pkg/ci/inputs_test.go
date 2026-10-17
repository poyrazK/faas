package ci

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCIArtifactInputDefinitionAndManifest(t *testing.T) {
	def := executionDefinition()
	job := def.Jobs["build"]
	job.Inputs = []ArtifactInput{{Name: "report", Job: "test", Path: "reports/managed.xml"}}
	def.Jobs["build"] = job
	before, _ := json.Marshal(def)
	plan, canonical, err := Compile(def)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(def)
	if !bytes.Equal(before, after) {
		t.Fatal("input normalization mutated caller")
	}
	parsed, err := Parse(canonical)
	if err != nil || !reflect.DeepEqual(parsed.Jobs["build"].Inputs, job.Inputs) {
		t.Fatal("input declaration did not round trip")
	}
	for _, change := range []func(*Job){
		func(j *Job) { j.Inputs[0].Name = "../escape" },
		func(j *Job) { j.Inputs[0].Job = "build" },
		func(j *Job) { j.Needs = nil },
		func(j *Job) { j.Inputs[0].Path = "reports/**" },
		func(j *Job) { j.Inputs[0].Path = "../escape" },
		func(j *Job) { j.Inputs[0].Path = "other/file" },
		func(j *Job) { j.Inputs = append(j.Inputs, j.Inputs[0]) },
		func(j *Job) { j.Inputs = make([]ArtifactInput, api.CIMaxArtifactInputsPerJob+1) },
	} {
		bad := clone(def)
		j := bad.Jobs["build"]
		change(&j)
		bad.Jobs["build"] = j
		if _, _, err := Compile(bad); err == nil {
			t.Fatal("invalid artifact input accepted")
		}
	}
	// Use real admission/producer identity for the runtime schema tests below.
	_, _, _, view, _ := artifactInputFixture(t, false)
	if view.Manifest.Version != 2 || len(view.Manifest.Inputs) != 1 || view.Manifest.Job.Name != plan.Jobs[1].Name {
		t.Fatal("input jobs did not use the required manifest version")
	}
	raw, _ := json.Marshal(view.Manifest)
	if _, err := DecodeJobsManifest(base64.StdEncoding.EncodeToString(raw)); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*JobsManifest){
		func(m *JobsManifest) { m.Version = 1 },
		func(m *JobsManifest) { m.Inputs = nil },
		func(m *JobsManifest) { m.Inputs[0].AttemptID = m.AttemptID },
		func(m *JobsManifest) { m.Inputs[0].Digest = "sha256:invalid" },
		func(m *JobsManifest) { m.Inputs[0].Bytes = api.CIArtifactMaxBytes + 1 },
		func(m *JobsManifest) { m.Inputs[0].Name = "other" },
	} {
		bad := clone(view.Manifest)
		change(&bad)
		if ValidateJobsManifest(bad) == nil {
			t.Fatal("invalid resolved manifest accepted")
		}
	}
}

func inputProof(t *testing.T, h *jobsHarness, row Execution, name string) (RuntimeProof, RuntimeView) {
	t.Helper()
	instance := h.fakeInstance(t, row, name)
	var run, lease string
	for _, job := range row.Jobs {
		if job.Name == name {
			run = job.Attempt.ID
		}
	}
	if err := h.pool.QueryRow(t.Context(), "SELECT lease_token::text FROM job_tasks WHERE run_id=$1", run).Scan(&lease); err != nil {
		t.Fatal(err)
	}
	proof, err := h.store.IssueJobsRuntime(t.Context(), h.backend.Config, RuntimeHostProof{RunID: run, InstanceID: instance, LeaseToken: lease})
	if err != nil {
		t.Fatal(err)
	}
	view, err := h.store.AuthorizeJobsRuntime(t.Context(), h.backend.Config, proof, RuntimeControl)
	if err != nil {
		t.Fatal(err)
	}
	return proof, view
}

func artifactInputFixture(t *testing.T, missing bool) (*jobsHarness, Execution, RuntimeProof, RuntimeView, *ManagedBlobs) {
	t.Helper()
	h := newJobsHarness(t)
	def := executionDefinition()
	job := def.Jobs["build"]
	name := "reports/managed.xml"
	if missing {
		name = "reports/missing.xml"
	}
	job.Inputs = []ArtifactInput{{Name: "report", Job: "test", Path: name}}
	def.Jobs["build"] = job
	revision, err := h.store.PutRevision(t.Context(), h.account, "input-pipeline", def)
	if err != nil {
		t.Fatal(err)
	}
	request := h.binding.GitHubBindingRequest
	request.RevisionID = revision.ID
	h.binding, err = h.store.PutGitHubBinding(t.Context(), h.account, h.binding.Name, request)
	if err != nil {
		t.Fatal(err)
	}
	h.req.RevisionID = revision.ID
	row := h.tick(t, h.admit(t, "inputs"))
	proof, view := inputProof(t, h, row, "test")
	managed := &ManagedBlobs{Store: h.store, Physical: newStoragePhysicalFake()}
	if err := managed.Put(t.Context(), view.Manifest.Source.Key, bytes.NewReader(h.stream.body)); err != nil {
		t.Fatal(err)
	}
	result := managedRuntimeResult(t, h, managed, proof, view)
	raw, _ := json.Marshal(result)
	if err := h.store.SealJobsRuntime(t.Context(), proof, raw); err != nil {
		t.Fatal(err)
	}
	h.backend.Source = GitHubArchiveSource{Verify: h.verify, Stream: h.stream, Blobs: managed}
	h.backend.Output = SealedJobsOutput{Store: h.store, Blobs: managed}
	if _, err := h.pool.Exec(t.Context(), "UPDATE job_tasks SET status='succeeded',exit_code=0,finished_at=clock_timestamp(),lease_token=NULL,lease_expires_at=NULL WHERE run_id=$1", proof.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE instances SET state='stopped' WHERE id=$1", proof.InstanceID); err != nil {
		t.Fatal(err)
	}
	row = h.tick(t, row)
	row = h.tick(t, row)
	if missing {
		return h, row, RuntimeProof{}, RuntimeView{}, managed
	}
	proof, view = inputProof(t, h, row, "build")
	return h, row, proof, view, managed
}

func TestCIArtifactInputsRuntimeRestartScopeAndRetention(t *testing.T) {
	h, row, proof, view, managed := artifactInputFixture(t, false)
	input := view.Manifest.Inputs[0]
	artifact, err := h.store.JobsRuntimeInput(t.Context(), h.backend.Config, proof, "report")
	if err != nil || artifact.Digest != input.Digest || artifact.Bytes != input.Bytes || !strings.Contains(artifact.Key, "/attempts/"+input.AttemptID+"/") {
		t.Fatalf("input descriptor: %+v %v", artifact, err)
	}
	h.restart()
	managed.Store = h.store
	again, err := h.store.JobsRuntimeInput(t.Context(), h.backend.Config, proof, "report")
	if err != nil || again != artifact {
		t.Fatal("restart changed input identity")
	}
	// Expired objects remain readable and cannot retire while any consumer runs.
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_managed_objects SET created_at=$2,expires_at=$3 WHERE object_key=$1", artifact.Key, h.clock.now().Add(-48*time.Hour), h.clock.now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	body, err := managed.Get(t.Context(), artifact.Key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil || string(raw) != "managed report" {
		t.Fatal("active consumer lost its producer bytes")
	}
	if _, err := h.store.claimStorageRetirement(t.Context(), artifact.Key); !errors.Is(err, ErrConflict) {
		t.Fatal("active execution allowed input retirement")
	}
	for _, badName := range []string{"other", "../escape", "reports/managed.xml"} {
		if _, err := h.store.JobsRuntimeInput(t.Context(), h.backend.Config, proof, badName); err == nil {
			t.Fatal("undeclared input accepted")
		}
	}
	disabled := h.backend.Config
	disabled.TransportEnabled = false
	if _, err := h.store.JobsRuntimeInput(t.Context(), disabled, proof, "report"); err == nil {
		t.Fatal("disabled transport exposed input")
	}
	foreign := proof
	foreign.RunID = input.AttemptID
	if _, err := h.store.JobsRuntimeInput(t.Context(), h.backend.Config, foreign, "report"); err == nil {
		t.Fatal("foreign attempt accepted consumer capability")
	}
	if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.JobsRuntimeInput(t.Context(), h.backend.Config, proof, "report"); err == nil {
		t.Fatal("cancelled consumer accepted new downloads")
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE job_tasks SET status='cancelled',exit_code=143,finished_at=clock_timestamp(),lease_token=NULL,lease_expires_at=NULL WHERE run_id=$1", proof.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE instances SET state='stopped' WHERE id=$1", proof.InstanceID); err != nil {
		t.Fatal(err)
	}
	row = h.tick(t, row)
	if !terminal(row.Status) {
		t.Fatal("cancelled consumer did not settle")
	}
	if _, err := h.store.claimStorageRetirement(t.Context(), artifact.Key); err != nil {
		t.Fatalf("finished consumers blocked normal retirement: %v", err)
	}
}

func TestCIArtifactInputsMissingFailsWithoutNativeRun(t *testing.T) {
	h, row, _, _, _ := artifactInputFixture(t, true)
	if row.Status != StatusFailed || row.Jobs[1].Status != StatusFailed || row.Jobs[1].Attempt.ExitCode == nil || *row.Jobs[1].Attempt.ExitCode != 125 || !strings.Contains(string(row.Jobs[1].Attempt.Logs), "artifact inputs are unavailable") {
		t.Fatalf("missing input did not fail explicitly: %+v", row.Jobs[1])
	}
	if h.count(t, "SELECT count(*) FROM job_runs") != 1 {
		t.Fatal("missing input created a native consumer run")
	}
	before := clone(row)
	h.restart()
	row = h.tick(t, row)
	if !reflect.DeepEqual(row, before) {
		t.Fatal("missing input receipt changed after restart")
	}
}

func TestCIArtifactInputsRejectChangedProducerAndStaleLease(t *testing.T) {
	h, row, proof, view, _ := artifactInputFixture(t, false)
	input := view.Manifest.Inputs[0]
	for _, scope := range [][2]string{{h.other, row.ID}, {h.account, "11111111-1111-4111-8111-111111111111"}} {
		if _, err := h.store.verifiedInput(t.Context(), h.pool, scope[0], scope[1], input); err == nil {
			t.Fatal("producer artifact crossed account/execution scope")
		}
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_managed_objects SET digest=$2 WHERE attempt_id=$1 AND kind='artifact'", input.AttemptID, jobsDigest([]byte("changed"))); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.JobsRuntimeInput(t.Context(), h.backend.Config, proof, "report"); err == nil {
		t.Fatal("changed producer descriptor accepted")
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_managed_objects SET digest=$2 WHERE attempt_id=$1 AND kind='artifact'", input.AttemptID, input.Digest); err != nil {
		t.Fatal(err)
	}
	var seal []byte
	if err := h.pool.QueryRow(t.Context(), "SELECT result FROM ci_job_runtime WHERE attempt_id=$1", input.AttemptID).Scan(&seal); err != nil {
		t.Fatal(err)
	}
	var changed JobsResultManifest
	if err := json.Unmarshal(seal, &changed); err != nil {
		t.Fatal(err)
	}
	changed.Artifacts[0].Digest = jobsDigest([]byte("changed seal"))
	changedRaw, _ := json.Marshal(changed)
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_job_runtime SET result=$2 WHERE attempt_id=$1", input.AttemptID, changedRaw); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.JobsRuntimeInput(t.Context(), h.backend.Config, proof, "report"); err == nil {
		t.Fatal("artifact not named by producer seal accepted")
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_job_runtime SET sealed_at=NULL,result=NULL WHERE attempt_id=$1", input.AttemptID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.JobsRuntimeInput(t.Context(), h.backend.Config, proof, "report"); err == nil {
		t.Fatal("unsealed producer accepted")
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_job_runtime SET sealed_at=clock_timestamp(),result=$2 WHERE attempt_id=$1", input.AttemptID, seal); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE job_tasks SET lease_token='22222222-2222-4222-8222-222222222222' WHERE run_id=$1", proof.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.JobsRuntimeInput(t.Context(), h.backend.Config, proof, "report"); err == nil {
		t.Fatal("replaced native lease accepted")
	}
}

func TestCIArtifactInputAggregateBytesAndFreshProducer(t *testing.T) {
	producer := Attempt{ID: "11111111-1111-4111-8111-111111111111", Status: StatusSucceeded}
	job := PlannedJob{Name: "consume"}
	for _, name := range []string{"one", "two", "three"} {
		producer.Artifacts = append(producer.Artifacts, ArtifactRef{Path: "dist/" + name, Digest: jobsDigest([]byte(name)), Bytes: api.CIArtifactMaxBytes})
		job.Inputs = append(job.Inputs, ArtifactInput{Name: name, Job: "build", Path: "dist/" + name})
	}
	row := Execution{Jobs: []JobExecution{{Name: "build", Status: StatusSucceeded, Attempt: &producer}}}
	if _, err := resolveArtifactInputs(row, job); err == nil {
		t.Fatal("combined input byte allowance exceeded")
	}
	job.Inputs = job.Inputs[:2]
	inputs, err := resolveArtifactInputs(row, job)
	if err != nil || len(inputs) != 2 {
		t.Fatal("valid aggregate bound rejected")
	}
	row = clone(row)
	row.Jobs[0].Attempt.ID = "22222222-2222-4222-8222-222222222222"
	fresh, err := resolveArtifactInputs(row, job)
	if err != nil || fresh[0].AttemptID == inputs[0].AttemptID {
		t.Fatal("fresh execution reused parent producer identity")
	}
	row.Jobs[0].Attempt.OutputIncomplete = true
	if _, err := resolveArtifactInputs(row, job); err == nil {
		t.Fatal("incomplete producer output accepted")
	}
}
