package ci

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func progressUpdate(view RuntimeView, sequence int32, offset int, logs []byte, status string) ProgressUpdate {
	steps := initialJobsProgress(view.Manifest.Job).Steps
	if status == StatusSucceeded {
		for i := range steps {
			steps[i].Status = status
			steps[i].ExitCode = intPtr(0)
		}
	} else {
		steps[0].Status = status
	}
	return ProgressUpdate{Sequence: sequence, LogOffset: offset, Logs: logs, Steps: steps}
}

func TestCIJobsProgressLiveRestartAndFinalAuthority(t *testing.T) {
	h, row, proof, view := runtimeFixture(t)
	if !view.ProgressEnabled {
		t.Fatal("current runner was not offered progress")
	}
	first := progressUpdate(view, 1, 0, []byte("runtime "), StatusRunning)
	start := time.Now().UTC()
	first.Steps[0].StartedAt = &start
	if receipt, err := h.store.AppendJobsRuntimeProgress(t.Context(), h.backend.Config, proof, first); err != nil || receipt.NextOffset != 8 {
		t.Fatalf("first progress: %+v %v", receipt, err)
	}
	row = h.tick(t, row)
	if row.Status != StatusRunning || row.Jobs[0].Attempt.Steps[0].Status != StatusRunning || string(row.Jobs[0].Attempt.Logs) != "runtime " {
		t.Fatal("live progress did not reach the execution")
	}
	page, err := ExecutionLogs(t.Context(), h.store, h.account, row.ID, proof.RunID, 0, 3)
	if err != nil || page.Complete || page.NextOffset != 3 || string(page.Data) != "run" {
		t.Fatal("live log cursor was not resumable")
	}
	h.restart()
	if receipt, err := h.store.AppendJobsRuntimeProgress(t.Context(), h.backend.Config, proof, first); err != nil || receipt.Sequence != 1 || receipt.NextOffset != 8 {
		t.Fatal("lost progress ACK did not replay after restart")
	}
	last := progressUpdate(view, 2, 8, []byte("logs"), StatusSucceeded)
	last.Steps[0].StartedAt = &start
	end := start.Add(time.Millisecond)
	last.Steps[0].FinishedAt = &end
	if _, err := h.store.AppendJobsRuntimeProgress(t.Context(), h.backend.Config, proof, last); err != nil {
		t.Fatal(err)
	}
	row = h.tick(t, row)
	if row.Status != StatusRunning || row.Jobs[0].Attempt.Steps[0].Status != StatusSucceeded {
		t.Fatal("guest progress certified native completion")
	}
	result := runtimeResult(t, h, proof, view)
	result.Steps = last.Steps
	raw, _ := json.Marshal(result)
	if err := h.store.SealJobsRuntime(t.Context(), proof, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.AppendJobsRuntimeProgress(t.Context(), h.backend.Config, proof, last); !errors.Is(err, ErrRuntimeAuthority) {
		t.Fatal("sealed output retained live write authority")
	}
	h.backend.Output = SealedJobsOutput{Store: h.store, Blobs: h.blobs}
	if _, err := h.pool.Exec(t.Context(), "UPDATE job_tasks SET status='succeeded',exit_code=0,finished_at=clock_timestamp(),lease_token=NULL,lease_expires_at=NULL WHERE run_id=$1", proof.RunID); err != nil {
		t.Fatal(err)
	}
	row = h.tick(t, row)
	if row.Status != StatusRunning {
		t.Fatal("final seal bypassed confirmed native stop")
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE instances SET state='stopped' WHERE id=$1", proof.InstanceID); err != nil {
		t.Fatal(err)
	}
	row = h.tick(t, row)
	if row.Jobs[0].Status != StatusSucceeded || row.Jobs[0].Attempt.OutputIncomplete {
		t.Fatal("sealed native result did not finish")
	}
	page, err = ExecutionLogs(t.Context(), h.store, h.account, row.ID, proof.RunID, 3, api.CILogPageMaxBytes)
	if err != nil || !page.Complete || string(page.Data) != "time logs" || !row.Jobs[0].Attempt.Steps[0].FinishedAt.Equal(end) {
		t.Fatal("final result changed the log prefix or step times")
	}
}

func TestCIJobsProgressConcurrentReplayOrderingAndBounds(t *testing.T) {
	h, _, proof, view := runtimeFixture(t)
	first := progressUpdate(view, 1, 0, []byte{0, 255, 'a'}, StatusRunning)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			receipt, err := h.store.AppendJobsRuntimeProgress(t.Context(), h.backend.Config, proof, first)
			if err != nil || receipt.Sequence != 1 || receipt.NextOffset != 3 {
				t.Errorf("concurrent replay: %+v %v", receipt, err)
			}
		})
	}
	wg.Wait()
	for _, change := range []func(*ProgressUpdate){
		func(p *ProgressUpdate) { p.Logs = []byte("conflict") }, func(p *ProgressUpdate) { p.Sequence = 3; p.LogOffset = 3 },
		func(p *ProgressUpdate) { p.Sequence = 2; p.LogOffset = 2 }, func(p *ProgressUpdate) { p.Sequence = 2; p.LogOffset = 3; p.Steps = nil },
		func(p *ProgressUpdate) {
			p.Sequence = 2
			p.LogOffset = 3
			p.Logs = bytes.Repeat([]byte("x"), api.CILogPageMaxBytes+1)
		},
		func(p *ProgressUpdate) { p.Sequence = 2; p.LogOffset = 3; p.LogTruncated = true },
		func(p *ProgressUpdate) { p.Sequence = 2; p.LogOffset = 3; p.Steps[0].Status = StatusQueued },
	} {
		bad := clone(first)
		change(&bad)
		if _, err := h.store.AppendJobsRuntimeProgress(t.Context(), h.backend.Config, proof, bad); err == nil {
			t.Fatal("invalid/conflicting progress accepted")
		}
	}
	var raw []byte
	var sequence int32
	if err := h.pool.QueryRow(t.Context(), "SELECT progress,progress_sequence FROM ci_job_attempts WHERE attempt_id=$1", proof.RunID).Scan(&raw, &sequence); err != nil || sequence != 1 {
		t.Fatal("failed append changed the sequence")
	}
	var snapshot JobsProgress
	if json.Unmarshal(raw, &snapshot) != nil || !bytes.Equal(snapshot.Logs, first.Logs) {
		t.Fatal("concurrent replay duplicated binary bytes")
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_job_attempts SET progress_sequence=$2 WHERE attempt_id=$1", proof.RunID, api.CIJobsProgressMaxUpdates); err != nil {
		t.Fatal(err)
	}
	first.Sequence = api.CIJobsProgressMaxUpdates + 1
	first.LogOffset = 3
	if _, err := h.store.AppendJobsRuntimeProgress(t.Context(), h.backend.Config, proof, first); !errors.Is(err, ErrLimit) {
		t.Fatal("progress request ceiling bypassed")
	}
}

func TestCIJobsProgressAuthorityGatesAndRollback(t *testing.T) {
	h, _, proof, view := runtimeFixture(t)
	update := progressUpdate(view, 1, 0, []byte("prefix"), StatusRunning)
	for _, mode := range []string{"internal", "account", "github", "dispatch", "transport"} {
		cfg := h.backend.Config
		switch mode {
		case "internal":
			cfg.Enabled = false
		case "account":
			cfg.accounts = nil
		case "github":
			cfg.GitHubEnabled = false
		case "dispatch":
			cfg.DispatchEnabled = false
		case "transport":
			cfg.TransportEnabled = false
		}
		if _, err := h.store.AppendJobsRuntimeProgress(t.Context(), cfg, proof, update); !errors.Is(err, ErrRuntimeAuthority) {
			t.Fatalf("gate %s accepted live progress: %v", mode, err)
		}
	}
	for _, change := range []func(*RuntimeProof){func(p *RuntimeProof) { p.RunID = uuid.NewString() }, func(p *RuntimeProof) { p.InstanceID = uuid.NewString() }, func(p *RuntimeProof) { p.Capability = strings.Repeat("f", 64) }} {
		bad := proof
		change(&bad)
		if _, err := h.store.AppendJobsRuntimeProgress(t.Context(), h.backend.Config, bad, update); !errors.Is(err, ErrRuntimeAuthority) {
			t.Fatal("foreign/stale runtime proof accepted")
		}
	}
	if _, err := h.pool.Exec(t.Context(), "ALTER TABLE ci_job_attempts ADD CONSTRAINT refuse_progress CHECK(progress IS NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.AppendJobsRuntimeProgress(t.Context(), h.backend.Config, proof, update); err == nil {
		t.Fatal("failed storage append accepted")
	}
	if _, err := h.pool.Exec(t.Context(), "ALTER TABLE ci_job_attempts DROP CONSTRAINT refuse_progress"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.AppendJobsRuntimeProgress(t.Context(), h.backend.Config, proof, update); err != nil {
		t.Fatal("failed transaction reserved the sequence")
	}
	data, err := os.ReadFile("../../migrations/20261017121648916_ci_live_progress.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), strings.Split(string(data), "-- +goose Down")[1]); err == nil || !strings.Contains(err.Error(), "retain CI live progress") {
		t.Fatal("rollback discarded progress evidence")
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE job_tasks SET lease_token=$2 WHERE run_id=$1", proof.RunID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.AppendJobsRuntimeProgress(t.Context(), h.backend.Config, proof, update); !errors.Is(err, ErrRuntimeAuthority) {
		t.Fatal("replaced native lease retained progress authority")
	}
	// Account teardown must first retire the stopped instance's runtime grant.
	if _, err := h.pool.Exec(t.Context(), "UPDATE instances SET state='stopped' WHERE id=$1", proof.InstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), "DELETE FROM ci_job_runtime WHERE attempt_id=$1", proof.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE job_tasks SET status='cancelled',exit_code=143,finished_at=clock_timestamp(),lease_token=NULL,lease_expires_at=NULL,instance_id=NULL WHERE run_id=$1", proof.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), "DELETE FROM instances WHERE id=$1 AND state='stopped'", proof.InstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", h.account); err != nil || h.count(t, "SELECT count(*) FROM ci_job_attempts WHERE progress IS NOT NULL") != 0 {
		t.Fatalf("account deletion retained live output: %v", err)
	}
}

func TestCIJobsProgressPrefixTruncationAndSkewedClockCleanup(t *testing.T) {
	job := PlannedJob{Name: "test", Job: Job{Steps: []Step{{Name: "unit"}}}}
	before := initialJobsProgress(job)
	for sequence := int32(1); sequence <= 4; sequence++ {
		next, err := appendJobsProgress(job, before, ProgressUpdate{Sequence: sequence, LogOffset: len(before.Logs), Logs: bytes.Repeat([]byte{0, 255}, api.CILogPageMaxBytes/2), Steps: []StepResult{{Name: "unit", Status: StatusRunning}}})
		if err != nil {
			t.Fatal(err)
		}
		before = next
	}
	truncated := ProgressUpdate{Sequence: 5, LogOffset: api.CILogMaxBytesPerAttempt, Steps: before.Steps, LogTruncated: true}
	next, err := appendJobsProgress(job, before, truncated)
	if err != nil || !next.LogTruncated || !bytes.Equal(before.Logs, next.Logs) {
		t.Fatal("truncation changed the retained binary prefix")
	}
	truncated.Sequence++
	truncated.LogTruncated = false
	if _, err := appendJobsProgress(job, next, truncated); err == nil {
		t.Fatal("truncation regressed")
	}
	start := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	next.Steps[0].StartedAt = &start
	output := incompleteProgressOutput(job, jobsTask{Status: StatusSucceeded}, &next, time.Now().UTC())
	if !output.OutputIncomplete || output.Status != StatusFailed || !output.MoreLogs || !bytes.Equal(output.Logs, next.Logs) || validateSteps(next.Steps, output) != nil {
		t.Fatal("skewed diagnostic clock blocked native cleanup or replaced the full prefix")
	}
	for _, step := range []StepResult{
		{Name: "unit", Status: StatusQueued, StartedAt: &start},
		{Name: "unit", Status: StatusRunning, StartedAt: &start, FinishedAt: &start},
		{Name: "unit", Status: StatusSucceeded, ExitCode: intPtr(0), StartedAt: &start},
	} {
		if validateStepTiming(StepResult{Name: "unit", Status: StatusQueued}, step) == nil {
			t.Fatal("invalid diagnostic timing accepted")
		}
	}
}

func TestCIJobsProgressArchiveClearsPayloadAndRetainsSequence(t *testing.T) {
	h, row, proof, view := runtimeFixture(t)
	update := progressUpdate(view, 1, 0, []byte("runtime "), StatusRunning)
	if _, err := h.store.AppendJobsRuntimeProgress(t.Context(), h.backend.Config, proof, update); err != nil {
		t.Fatal(err)
	}
	result := runtimeResult(t, h, proof, view)
	raw, _ := json.Marshal(result)
	if err := h.store.SealJobsRuntime(t.Context(), proof, raw); err != nil {
		t.Fatal(err)
	}
	h.backend.Output = SealedJobsOutput{Store: h.store, Blobs: h.blobs}
	row = stopManagedRuntime(t, h, row)
	if _, _, err := h.store.ArchiveExecution(t.Context(), h.account, row.ID); err != nil {
		t.Fatal(err)
	}
	var hasPayload bool
	var sequence int32
	var digest string
	if err := h.pool.QueryRow(t.Context(), "SELECT progress IS NOT NULL,progress_sequence,progress_digest FROM ci_job_attempts WHERE attempt_id=$1", proof.RunID).Scan(&hasPayload, &sequence, &digest); err != nil || hasPayload || sequence != 1 || len(digest) != 64 {
		t.Fatal("archive lost sequence evidence or retained raw live output")
	}
}

func TestCIJobsProgressUnsealedStopRetainsExplicitPartialFailure(t *testing.T) {
	for _, test := range []struct {
		name          string
		cancel        bool
		clearInstance bool
	}{
		{name: "unsealed"},
		{name: "cancelled", cancel: true},
		{name: "cancelled-after-instance-cleanup", cancel: true, clearInstance: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cancel := test.cancel
			h, row, proof, view := runtimeFixture(t)
			update := progressUpdate(view, 1, 0, []byte("partial guest bytes\x00\xff"), StatusRunning)
			if _, err := h.store.AppendJobsRuntimeProgress(t.Context(), h.backend.Config, proof, update); err != nil {
				t.Fatal(err)
			}
			row = h.tick(t, row)
			h.backend.Output = SealedJobsOutput{Store: h.store, Blobs: h.blobs}
			status, code := StatusSucceeded, 0
			if cancel {
				status, code = StatusCancelled, 143
				if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := h.store.AppendJobsRuntimeProgress(t.Context(), h.backend.Config, proof, update); !errors.Is(err, ErrRuntimeAuthority) {
					t.Fatal("cancelled attempt accepted progress")
				}
			}
			if _, err := h.pool.Exec(t.Context(), "UPDATE job_tasks SET status=$2,exit_code=$3,finished_at=clock_timestamp(),lease_token=NULL,lease_expires_at=NULL WHERE run_id=$1", proof.RunID, status, code); err != nil {
				t.Fatal(err)
			}
			if _, err := h.pool.Exec(t.Context(), "UPDATE instances SET state='stopped' WHERE id=$1", proof.InstanceID); err != nil {
				t.Fatal(err)
			}
			if test.clearInstance {
				if _, err := h.pool.Exec(t.Context(), "UPDATE job_tasks SET instance_id=NULL WHERE run_id=$1", proof.RunID); err != nil {
					t.Fatal(err)
				}
			}
			row = h.tick(t, row)
			a := row.Jobs[0].Attempt
			want := StatusFailed
			if cancel {
				want = StatusCancelled
			}
			if row.Jobs[0].Status != want || !a.OutputIncomplete || !bytes.HasPrefix(a.Logs, update.Logs) || a.Steps[0].Status != StatusCancelled {
				t.Fatalf("partial cleanup result: %+v", a)
			}
		})
	}
}

func TestCIJobsProgressStepTimesAndFinalPrefixCannotChange(t *testing.T) {
	h, _, proof, view := runtimeFixture(t)
	update := progressUpdate(view, 1, 0, []byte("runtime "), StatusRunning)
	start := time.Now().UTC()
	update.Steps[0].StartedAt = &start
	if _, err := h.store.AppendJobsRuntimeProgress(t.Context(), h.backend.Config, proof, update); err != nil {
		t.Fatal(err)
	}
	bad := clone(update)
	bad.Sequence = 2
	bad.LogOffset = len(update.Logs)
	bad.Logs = nil
	changed := start.Add(time.Second)
	bad.Steps[0].StartedAt = &changed
	if _, err := h.store.AppendJobsRuntimeProgress(t.Context(), h.backend.Config, proof, bad); err == nil {
		t.Fatal("step start time changed")
	}
	result := runtimeResult(t, h, proof, view)
	if raw, _ := json.Marshal(result); h.store.SealJobsRuntime(t.Context(), proof, raw) == nil {
		t.Fatal("final seal erased step timing")
	}
	result.Steps[0].StartedAt = &start
	end := start.Add(time.Second)
	result.Steps[0].FinishedAt = &end
	if raw, _ := json.Marshal(result); h.store.SealJobsRuntime(t.Context(), proof, raw) != nil {
		t.Fatal("consistent final timing refused")
	}
	progress := JobsProgress{Steps: result.Steps, Logs: []byte("original prefix")}
	if _, err := reconcileProgressOutput(&progress, Observation{Status: StatusSucceeded, Steps: result.Steps, Logs: []byte("different prefix")}); err == nil {
		t.Fatal("final logs overwrote a streamed prefix")
	}
}
