package ci

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCIJUnitSealedReportsRestartStopAndArchive(t *testing.T) {
	h := newJobsHarness(t)
	def := executionDefinition()
	delete(def.Jobs, "build")
	delete(h.req.Templates, "build")
	job := def.Jobs["test"]
	job.JUnit = []string{"reports/unit.xml"}
	def.Jobs["test"] = job
	revision, err := h.store.PutRevision(t.Context(), h.account, "report-checks", def)
	if err != nil {
		t.Fatal(err)
	}
	bindingReq := h.binding.GitHubBindingRequest
	bindingReq.RevisionID = revision.ID
	h.binding, err = h.store.PutGitHubBinding(t.Context(), h.account, h.binding.Name, bindingReq)
	if err != nil {
		t.Fatal(err)
	}
	h.req.RevisionID = revision.ID
	row := h.tick(t, h.admit(t, "reports"))
	instance := h.fakeInstance(t, row, "test")
	var lease string
	if err := h.pool.QueryRow(t.Context(), "SELECT lease_token::text FROM job_tasks WHERE run_id=$1", row.Jobs[0].Attempt.ID).Scan(&lease); err != nil {
		t.Fatal(err)
	}
	proof, err := h.store.IssueJobsRuntime(t.Context(), h.backend.Config, RuntimeHostProof{RunID: row.Jobs[0].Attempt.ID, InstanceID: instance, LeaseToken: lease})
	if err != nil {
		t.Fatal(err)
	}
	view, err := h.store.AuthorizeJobsRuntime(t.Context(), h.backend.Config, proof, RuntimeControl)
	if err != nil || len(view.Manifest.Job.JUnit) != 0 {
		t.Fatalf("guest received controller selectors: %v", err)
	}
	physical := newStoragePhysicalFake()
	blobs := &ManagedBlobs{Store: h.store, Physical: physical}
	zero := 0
	result := JobsResultManifest{Version: 1, AccountID: h.account, ExecutionID: row.ID, AttemptID: proof.RunID, CommitSHA: row.CommitSHA, Status: StatusSucceeded, ExitCode: &zero}
	for _, step := range job.Steps {
		result.Steps = append(result.Steps, StepResult{Name: step.Name, Status: StatusSucceeded, ExitCode: &zero})
	}
	var artifactKey string
	for name, data := range map[string][]byte{"logs.bin": bytes.Repeat([]byte{'L'}, api.CILogPageMaxBytes*3), "artifacts/reports/unit.xml": []byte(junitExample)} {
		object := RuntimeObject{Name: name, Digest: jobsDigest(data), Bytes: int64(len(data))}
		key, _, err := h.store.ReserveJobsRuntimeObject(t.Context(), proof, object)
		if err != nil {
			t.Fatal(err)
		}
		if err := blobs.Put(t.Context(), key, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		if err := h.store.CommitJobsRuntimeObject(t.Context(), proof, object); err != nil {
			t.Fatal(err)
		}
		if name == "logs.bin" {
			result.Logs = SourceArchive{Key: key, Digest: object.Digest, Bytes: object.Bytes}
		} else {
			artifactKey = key
			result.Artifacts = []JobsArtifact{{ArtifactRef: ArtifactRef{Path: "reports/unit.xml", Digest: object.Digest, Bytes: object.Bytes}, Key: key}}
		}
	}
	raw, _ := json.Marshal(result)
	if err := h.store.SealJobsRuntime(t.Context(), proof, raw); err != nil {
		t.Fatal(err)
	}
	counting := &junitCountingBlobs{store: blobs, gets: map[string]int{}}
	h.backend.Output = SealedJobsOutput{Store: h.store, Blobs: counting}
	if _, err := h.pool.Exec(t.Context(), "UPDATE job_tasks SET status='succeeded',exit_code=0,finished_at=clock_timestamp(),lease_token=NULL,lease_expires_at=NULL WHERE run_id=$1", proof.RunID); err != nil {
		t.Fatal(err)
	}
	row = h.tick(t, row)
	if len(counting.gets) != 0 || len(row.Jobs[0].Attempt.TestReports) != 0 || row.Status != StatusRunning {
		t.Fatal("reports collected before confirmed native stop")
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE instances SET state='stopped' WHERE id=$1", instance); err != nil {
		t.Fatal(err)
	}
	row = h.tick(t, row)
	if row.Status != StatusRunning || len(row.Jobs[0].Attempt.TestReports) != 1 {
		t.Fatal("report diagnostics were not persisted during log drain")
	}
	h.restart()
	blobs.Store = h.store
	h.backend.Output = SealedJobsOutput{Store: h.store, Blobs: counting}
	for i := 0; !terminal(row.Status) && i < 5; i++ {
		row = h.tick(t, row)
	}
	if row.Status != StatusSucceeded || counting.gets[artifactKey] != 1 || len(row.Jobs[0].Attempt.Logs) != api.CILogPageMaxBytes*3 {
		t.Fatal("restart reread reports or report failure changed native success")
	}
	reports, err := ExecutionReports(row)
	if err != nil || reports.Totals != (TestReportTotals{Expected: 1, Parsed: 1, TestCounts: TestCounts{Tests: 4, Passed: 1, Failed: 1, Errors: 1, Skipped: 1}}) || reports.Jobs[0].AttemptID != proof.RunID || reports.Jobs[0].Reports[0].ArtifactRef != result.Artifacts[0].ArtifactRef {
		t.Fatalf("durable report identity/totals lost: %+v %v", reports, err)
	}
	check, err := executionCheck(executionRecord{Execution: row, Version: 1})
	if err != nil || check.Conclusion != "success" || !strings.Contains(check.Summary, "tests=4, passed=1, failed=1, errors=1, skipped=1") {
		t.Fatalf("Check totals changed native conclusion: %+v %v", check, err)
	}
	for _, private := range []string{"private-", "reports/", "assertion", "evidence"} {
		if strings.Contains(check.Summary, private) {
			t.Fatal("Check exposed report details")
		}
	}
	// Provider-free reads and persisted output survive admission gates being off.
	h.backend.Config = Config{}
	h.restart()
	stored, err := h.store.Execution(t.Context(), h.account, row.ID)
	if err != nil || !reflect.DeepEqual(stored.Jobs[0].Attempt.TestReports, row.Jobs[0].Attempt.TestReports) {
		t.Fatal("report snapshot lost after restart")
	}
	if _, _, err := h.store.ArchiveExecution(t.Context(), h.account, row.ID); !errors.Is(err, ErrArchiveUnsafe) {
		t.Fatal("report payload bypassed retention safety")
	}
	h.clock.advance(api.CIStorageRetentionDays*24*3600 + 1)
	blobs.Store = h.store
	if _, err := blobs.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	archive, _, err := h.store.ArchiveExecution(t.Context(), h.account, row.ID)
	if err != nil || len(archive.Jobs) != 1 || archive.Jobs[0].TestReports == nil || *archive.Jobs[0].TestReports != reports.Totals {
		t.Fatalf("archive lost aggregate report history: %+v %v", archive, err)
	}
	raw, _ = json.Marshal(archive)
	for _, private := range []string{"private-", "reports/", "assertion", "evidence", "failures"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("archive retained detailed report data")
		}
	}
}
