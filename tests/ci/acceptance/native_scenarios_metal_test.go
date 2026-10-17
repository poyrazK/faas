//go:build metal

package acceptance

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
)

func TestCINativeAcceptance(t *testing.T) {
	f := newNativeFixture(t)
	for _, name := range ScenarioNames() {
		if !t.Run(name, func(t *testing.T) { f.scenario(t, name) }) {
			return // Drain the failed case before attempting any more native work.
		}
	}
}

func (f *nativeFixture) scenario(t *testing.T, name string) {
	write := "mkdir -p reports; printf 'native CI artifact\\n' > reports/result.txt; dd if=/dev/zero of=reports/large.bin bs=1024 count=300 2>/dev/null; sleep 3"
	var wakeCheck func(ci.Execution, *Scenario)
	if name == "wake_during_ci" {
		wakeCheck = f.prepareWake(t)
	}
	first := f.nativeStep("result", write)
	jobs := map[string]ci.Job{"unit": first}
	expected := ci.StatusSucceeded
	switch name {
	case "pipeline":
		second := f.nativeStep("result", write)
		second.Needs = []string{"unit"}
		jobs["package"] = second
	case "step_failure":
		jobs["unit"] = f.nativeStep("failure", write+"; exit 17")
		expected = ci.StatusFailed
	case "timeout":
		first = f.nativeStep("timeout", "sleep 120")
		first.TimeoutSeconds = 45
		jobs["unit"] = first
		expected = ci.StatusFailed
	case "cancellation", "restart":
		jobs["unit"] = f.nativeStep("hold", "sleep 45; "+write)
		if name == "cancellation" {
			expected = ci.StatusCancelled
		}
	case "wake_during_ci":
		// Five real 1-vCPU guests contend with the tenant's cold wake. The
		// workload is bounded by Pro concurrency and each immutable deadline.
		jobs = make(map[string]ci.Job)
		for i := range 5 {
			jobs[fmt.Sprintf("busy-%d", i)] = f.nativeStep("busy", "while :; do :; done")
		}
		expected = ci.StatusCancelled
	}
	reads := f.sourceReads.Load()
	row := f.admit(t, name, jobs)
	entry := f.journal.Scenarios[name]
	f.reconnect() // Separate observer; only the subprocess worker reconciles intent.
	row = f.tick(t, row)
	if name == "cancellation" || name == "restart" || name == "wake_during_ci" {
		f.waitRunning(t, row, reads)
	}
	switch name {
	case "cancellation":
		row = f.cancel(t, row)
	case "restart":
		before := f.tick(t, row)
		f.lostAckArmed.Store(true)
		mustNative(t, f.h.KillAPID())
		f.apidKilled = true
		mustNative(t, f.h.KillSchedd())
		f.scheddKilled = true
		mustNative(t, f.h.RestartAPID())
		f.apidKilled = false
		mustNative(t, f.h.RestartSchedd())
		f.scheddKilled = false
		f.reconnect()
		after := f.tick(t, row)
		if before.Jobs[0].Attempt.ID != after.Jobs[0].Attempt.ID || before.Plan.Jobs[0].Image != after.Plan.Jobs[0].Image {
			t.Fatal("restart replaced the attempt or pinned image")
		}
		entry.Checks["apid_restarted"], entry.Checks["schedd_restarted"], entry.Checks["controller_restarted"] = true, true, true
	case "wake_during_ci":
		wakeCheck(row, &entry)
		row = f.cancel(t, row)
	}
	nativeEventually(t, 4*time.Minute, "terminal CI result and native stop", func() bool {
		row = f.tick(t, row)
		return row.Status == ci.StatusSucceeded || row.Status == ci.StatusFailed || row.Status == ci.StatusCancelled
	})
	if row.Status != expected {
		t.Fatalf("native %s result=%s, expected=%s", name, row.Status, expected)
	}
	if name == "step_failure" && (row.Jobs[0].Attempt.ExitCode == nil || *row.Jobs[0].Attempt.ExitCode != 17 || row.Jobs[0].Attempt.Steps[1].Status != ci.StatusFailed) {
		t.Fatal("native user failure was replaced by a platform failure")
	}
	if name == "timeout" && row.Jobs[0].Reason != ci.ReasonTimeout {
		t.Fatal("native timeout did not retain its CI deadline reason")
	}
	entry.Checks["expected_result"] = true
	for _, job := range row.Jobs {
		if job.Attempt == nil {
			t.Fatal("worker did not persist its attempt")
		}
		var page ci.LogPage
		f.ciRequest(t, http.MethodGet, "executions/"+row.ID+"/attempts/"+job.Attempt.ID+"/logs", nil, "", &page)
		if page.Offset != 0 || page.NextOffset != len(page.Data) {
			t.Fatal("service log cursor is inconsistent")
		}
	}
	entry.Checks["http_admission"], entry.Checks["http_status"], entry.Checks["http_logs"], entry.Checks["worker_progress"] = true, true, true, true
	f.assertNative(t, row, &entry)
	if name == "pipeline" {
		if len(row.Jobs) != 2 || row.Jobs[1].Attempt.StartedAt.Before(*row.Jobs[0].Attempt.FinishedAt) {
			t.Fatal("dependent native job started before its prerequisite completed")
		}
		entry.Checks["two_jobs"], entry.Checks["ordered_dependencies"] = true, true
		f.downloadArtifacts(t, row, &entry)
		f.assertBilling(t, row, &entry)
	}
	if name == "restart" {
		if !f.lostAck.Load() {
			t.Fatal("restart qualification did not inject the lost upload acknowledgement")
		}
		entry.Checks["lost_ack"] = true
		f.downloadArtifacts(t, row, &entry)
	}
	entry.Status = "passed"
	f.journal.Scenarios[name] = entry
	f.save(t)
}

func (f *nativeFixture) admit(t *testing.T, name string, jobs map[string]ci.Job) ci.Execution {
	t.Helper()
	slug := strings.ReplaceAll(name, "_", "-")
	definition, err := json.Marshal(ci.Definition{Version: 1, Repository: fixtureRepository, Jobs: jobs})
	mustNative(t, err)
	var revision ci.Revision
	f.ciRequest(t, http.MethodPut, "pipelines/"+slug, map[string]string{"definition": string(definition)}, "", &revision)
	var binding ci.GitHubBinding
	f.ciRequest(t, http.MethodPut, "github/bindings/"+slug, ci.GitHubBindingRequest{RevisionID: revision.ID, InstallationID: 12, RepositoryID: 34, PushBranches: []string{"main"}}, "", &binding)
	templates := make(map[string]string)
	for name := range jobs {
		templates[name] = f.job.ID
	}
	request := ci.JobsSubmitRequest{SubmitRequest: ci.SubmitRequest{RevisionID: revision.ID, CommitSHA: strings.Repeat("a", 40)}, BindingID: binding.ID, Templates: templates}
	var row ci.Execution
	if f.ciRequest(t, http.MethodPost, "executions", request, name, &row) {
		t.Fatal("fresh native execution replayed")
	}
	var replay ci.Execution
	if !f.ciRequest(t, http.MethodPost, "executions", request, name, &replay) || row.ID != replay.ID {
		t.Fatal("native admission lost its idempotency identity")
	}
	f.journal.Scenarios[name] = Scenario{ExecutionID: row.ID, Status: "running", Checks: make(map[string]bool), Attempts: make(map[string]string), Instances: make(map[string]string)}
	f.save(t) // Schema ownership already permits recovery if the admission acknowledgement is lost.
	return row
}

func (f *nativeFixture) tick(t *testing.T, row ci.Execution) ci.Execution {
	t.Helper()
	var current ci.Execution
	f.ciRequest(t, http.MethodGet, "executions/"+row.ID, nil, "", &current)
	return current
}

func (f *nativeFixture) cancel(t *testing.T, row ci.Execution) ci.Execution {
	t.Helper()
	var current ci.Execution
	f.ciRequest(t, http.MethodPost, "executions/"+row.ID+"/cancel", nil, "", &current)
	return current
}

func (f *nativeFixture) waitRunning(t *testing.T, row ci.Execution, reads int32) {
	t.Helper()
	nativeEventually(t, time.Minute, "native running identity and HTTPS source", func() bool {
		var running int
		err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM ci_job_attempts a JOIN job_tasks t ON t.run_id=a.run_id
 JOIN instances i ON i.id=t.instance_id WHERE a.execution_id=$1 AND i.state='running' AND t.status='claimed'`, row.ID).Scan(&running)
		return err == nil && running > 0 && f.sourceReads.Load() > reads
	})
}

func (f *nativeFixture) assertNative(t *testing.T, row ci.Execution, entry *Scenario) {
	t.Helper()
	pinned, err := f.store.Execution(t.Context(), row.AccountID, row.ID)
	mustNative(t, err)
	for _, job := range row.Jobs {
		if job.Attempt == nil || job.Attempt.Number != 1 {
			t.Fatal("native fixture lost its single durable attempt")
		}
		task, err := f.state.JobTaskGet(t.Context(), job.Attempt.ID, 0)
		mustNative(t, err)
		if task.Attempt != 1 || task.InstanceID == nil || task.StartedAt == nil || task.FinishedAt == nil {
			t.Fatal("native task was retried or has no real terminal execution receipt")
		}
		var count int
		var nativeState, image, digest, key string
		mustNative(t, f.pool.QueryRow(t.Context(), `SELECT i.state,r.image_ref_snapshot,r.image_resolved_digest_snapshot,r.image_storage_key_snapshot,
 (SELECT count(*) FROM job_task_attempts a WHERE a.run_id=r.id) FROM job_runs r JOIN job_tasks t ON t.run_id=r.id JOIN instances i ON i.id=t.instance_id WHERE r.id=$1`, job.Attempt.ID).Scan(&nativeState, &image, &digest, &key, &count))
		if nativeState != "stopped" || count != 1 || image != f.config.Image || digest != f.job.ImageResolvedDigest || key != f.job.ImageStorageKey || key != pinned.Compute.Templates[job.Name].StorageKey {
			t.Fatal("native stop, immutable image or one-attempt evidence disagrees")
		}
		entry.Attempts[job.Name], entry.Instances[job.Name] = job.Attempt.ID, *task.InstanceID
	}
	entry.Checks["native_stop"], entry.Checks["one_attempt"], entry.Checks["image_snapshot"] = true, true, true
}

func (f *nativeFixture) downloadArtifacts(t *testing.T, row ci.Execution, entry *Scenario) {
	t.Helper()
	client := &http.Client{Timeout: 20 * time.Second}
	large := false
	for _, job := range row.Jobs {
		if job.Attempt.Steps[0].Status != ci.StatusSucceeded || len(job.Attempt.Artifacts) != 2 {
			t.Fatal("candidate runner identity/source validation or artifact selection failed")
		}
		for _, artifact := range job.Attempt.Artifacts {
			path := f.config.Origin + "/v1/internal/ci/executions/" + row.ID + "/jobs/" + job.Name + "/artifacts/" + artifact.Path
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
			mustNative(t, err)
			req.Header.Set("Authorization", "Bearer "+f.key)
			response, err := client.Do(req)
			mustNative(t, err)
			raw, err := io.ReadAll(io.LimitReader(response.Body, artifact.Bytes+1))
			_ = response.Body.Close()
			mustNative(t, err)
			if response.StatusCode != http.StatusOK || int64(len(raw)) != artifact.Bytes || nativePayloadDigest(raw) != artifact.Digest || response.Header.Get("Cache-Control") != "no-store" {
				t.Fatal("authenticated native artifact download did not match verified bytes")
			}
			if artifact.Path == "reports/large.bin" && len(raw) == 300*1024 {
				large = true // Above the real GCS driver's resumable chunk size.
			}
		}
	}
	entry.Checks["verified_download"], entry.Checks["large_artifact"] = true, large
}

func (f *nativeFixture) assertBilling(t *testing.T, row ci.Execution, entry *Scenario) {
	t.Helper()
	var expected int64
	from := row.CreatedAt.Truncate(time.Minute)
	until := time.Now().Truncate(time.Minute).Add(time.Minute)
	seconds, err := f.state.InstanceBillingSeconds(t.Context(), from, until)
	mustNative(t, err)
	for _, instance := range entry.Instances {
		if seconds[instance] <= 0 {
			t.Fatal("native billing residency journal is missing")
		}
		expected += int64(api.BillableRAMMBWithSidecars(api.CIJobsRAMMB, nil)) * seconds[instance]
	}
	nativeEventually(t, 2*time.Minute, "native CI billing reconciliation", func() bool {
		var observed int64
		err := f.pool.QueryRow(t.Context(), "SELECT coalesce(sum(mb_seconds),0) FROM usage_minutes WHERE account_id=$1 AND job_id=$2 AND meter_kind='job'", row.AccountID, f.job.ID).Scan(&observed)
		entry.MBSeconds = observed
		return err == nil && observed == expected && observed > 0
	})
	entry.Checks["billing"] = true
}
