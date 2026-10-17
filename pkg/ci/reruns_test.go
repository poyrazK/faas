package ci

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func rerunCancelledParent(t *testing.T, h *jobsHarness, key string) Execution {
	t.Helper()
	parent := h.admit(t, key)
	parent, err := h.store.CancelExecution(t.Context(), h.account, parent.ID)
	if err != nil || !terminal(parent.Status) {
		t.Fatalf("cancel queued parent: %v", err)
	}
	return parent
}

func TestCIRerunWholePipelineHasFreshResultsAndChecks(t *testing.T) {
	h := newJobsHarness(t)
	def := executionDefinition()
	job := def.Jobs["test"]
	job.JUnit = []string{"reports/unit.xml"}
	def.Jobs["test"] = job
	revision, err := h.store.PutRevision(t.Context(), h.account, "rerun-reports", def)
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
	h.backend.Config.ExecutionChecksEnabled, h.backend.Config.GitHubChecksEnabled = true, true
	parent := h.tick(t, h.admit(t, "failed-original"))
	h.publish(t, parent, "test", StatusFailed, 7, []byte("original failure"), true, true)
	for range 4 {
		parent = h.tick(t, parent)
		if terminal(parent.Status) {
			break
		}
	}
	if parent.Status != StatusFailed || parent.Jobs[1].Status != StatusSkipped || len(parent.Jobs[0].Attempt.TestReports) != 1 {
		t.Fatal("failed pipeline fixture did not retain its original result")
	}
	child, replay, err := RerunJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, parent.ID, "rerun-full")
	if err != nil || replay || child.ID == parent.ID || child.Rerun == nil || child.Rerun.ParentExecutionID != parent.ID || child.Rerun.RootExecutionID != parent.ID ||
		!sameRerunConfiguration(child, parent) || child.Automation != nil || child.CancelRequested {
		t.Fatalf("rerun lost frozen source or fresh identity: %v", err)
	}
	for _, item := range child.Jobs {
		if item.Status != StatusWaiting || item.Attempt != nil {
			t.Fatal("rerun reused an old attempt or skipped a pipeline job")
		}
	}
	if h.count(t, "SELECT count(*) FROM job_runs") != 1 {
		t.Fatal("rerun admission bypassed normal dispatch")
	}
	child = h.tick(t, child)
	if child.Jobs[0].Attempt.ID == parent.Jobs[0].Attempt.ID {
		t.Fatal("rerun reused a native/source/output identity")
	}
	h.publish(t, child, "test", StatusSucceeded, 0, []byte("new passing test"), true, true)
	for range 3 {
		child = h.tick(t, child)
		if child.Jobs[1].Attempt != nil {
			break
		}
	}
	h.publish(t, child, "build", StatusSucceeded, 0, []byte("new build"), false, true)
	for range 4 {
		child = h.tick(t, child)
		if terminal(child.Status) {
			break
		}
	}
	old, err := h.store.Execution(t.Context(), h.account, parent.ID)
	if err != nil || !reflect.DeepEqual(old, parent) || child.Status != StatusSucceeded || h.count(t, "SELECT count(*) FROM job_runs") != 3 || h.count(t, "SELECT count(*) FROM ci_execution_checks") != 2 {
		t.Fatal("rerun changed original results, omitted pipeline work or shared Check intent")
	}
	oldReports, err := ExecutionReports(old)
	if err != nil {
		t.Fatal(err)
	}
	newReports, err := ExecutionReports(child)
	if err != nil || oldReports.ExecutionID == newReports.ExecutionID || newReports.Jobs[0].AttemptID == oldReports.Jobs[0].AttemptID {
		t.Fatal("rerun reused JUnit report identity")
	}
	oldCheck, err := executionCheck(executionRecord{Execution: old, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	newCheck, err := executionCheck(executionRecord{Execution: child, Version: 1})
	if err != nil || oldCheck.ExternalID() == newCheck.ExternalID() {
		t.Fatal("rerun overwrote the original remote Check identity")
	}
	var prefixes int
	for key := range h.blobs.data {
		if strings.Contains(key, "/executions/"+child.ID+"/") && strings.HasSuffix(key, "artifacts/reports/unit.xml") {
			prefixes++
		}
	}
	if prefixes != 1 {
		t.Fatal("rerun artifact did not use its own execution prefix")
	}
}

func TestCIRerunConcurrentReplayLineageAndArchival(t *testing.T) {
	h := newJobsHarness(t)
	parent := rerunCancelledParent(t, h, "parent")
	var wg sync.WaitGroup
	type outcome struct {
		row    Execution
		replay bool
		err    error
	}
	results := make(chan outcome, 8)
	for range cap(results) {
		wg.Go(func() {
			row, replay, err := RerunJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, parent.ID, "one-rerun")
			results <- outcome{row, replay, err}
		})
	}
	wg.Wait()
	close(results)
	var child Execution
	fresh := 0
	for result := range results {
		if result.err != nil || child.ID != "" && child.ID != result.row.ID {
			t.Fatalf("concurrent rerun changed identity: %v", result.err)
		}
		child = result.row
		if !result.replay {
			fresh++
		}
	}
	if fresh != 1 || h.count(t, "SELECT count(*) FROM ci_executions") != 2 || h.count(t, "SELECT count(*) FROM job_runs") != 0 {
		t.Fatal("request retries admitted duplicate reruns or native work")
	}
	if _, _, err := RerunJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, child.ID, "one-rerun"); !errors.Is(err, ErrConflict) {
		t.Fatal("rerun key was reused for a different parent")
	}
	if _, _, err := AdmitJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, "one-rerun", h.req); !errors.Is(err, ErrConflict) {
		t.Fatal("ordinary submit replayed an execution with rerun lineage")
	}
	child, err := h.store.CancelExecution(t.Context(), h.account, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	grandchild, _, err := RerunJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, child.ID, "next-rerun")
	if err != nil || grandchild.Rerun.ParentExecutionID != child.ID || grandchild.Rerun.RootExecutionID != parent.ID {
		t.Fatalf("rerun chain lost its root: %v", err)
	}
	if _, _, err := h.store.ArchiveExecution(t.Context(), h.account, parent.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RerunJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, parent.ID, "new-after-archive"); !errors.Is(err, ErrExecutionArchived) {
		t.Fatal("compacted parent granted fresh rerun authority")
	}
	h.restart()
	called := false
	verify := func(context.Context, string, int64, int64, string) error { called = true; return ErrGitHubUnavailable }
	if row, replay, err := RerunJobs(t.Context(), h.backend.Config, h.store, verify, h.account, parent.ID, "one-rerun"); err != nil || !replay || row.ID != child.ID || called {
		t.Fatal("parent archival/restart/provider outage lost the existing rerun receipt")
	}
	archive, _, err := h.store.ArchiveExecution(t.Context(), h.account, child.ID)
	if err != nil || !reflect.DeepEqual(archive.Rerun, child.Rerun) {
		t.Fatalf("archive lost rerun audit: %v", err)
	}
	if _, _, err := RerunJobs(t.Context(), h.backend.Config, h.store, verify, h.account, parent.ID, "one-rerun"); !errors.Is(err, ErrExecutionArchived) {
		t.Fatal("archived rerun identity was reused")
	}
	rows, err := h.store.ListJobsExecutions(t.Context(), h.account)
	if err != nil || len(rows) != 1 || !reflect.DeepEqual(rows[0].Rerun, grandchild.Rerun) {
		t.Fatal("live history lost rerun lineage")
	}
	data, err := os.ReadFile("../../migrations/20261017095313973_ci_execution_reruns.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), strings.Split(string(data), "-- +goose Down")[1]); err == nil || !strings.Contains(err.Error(), "retain CI execution rerun") {
		t.Fatal("rollback discarded rerun lineage")
	}
	if _, err := h.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", h.account); err != nil || h.count(t, "SELECT count(*) FROM ci_executions") != 0 {
		t.Fatal("rerun lineage prevented owned account deletion")
	}
}

func TestCIRerunFreshAuthorityAndFrozenImages(t *testing.T) {
	for _, mode := range []string{"profile", "binding", "generation", "image", "image_without_profile", "account", "provider", "provider_revoked", "archive_during_lookup"} {
		t.Run(mode, func(t *testing.T) {
			h := newJobsHarness(t)
			profile := h.profile(t, "rerun")
			req := JobsSubmitRequest{ProfileID: profile.ID, SubmitRequest: SubmitRequest{CommitSHA: h.req.CommitSHA}}
			if mode == "image_without_profile" {
				req = h.req
			}
			parent, _, err := AdmitJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, "parent", req)
			if err != nil {
				t.Fatal(err)
			}
			parent, err = h.store.CancelExecution(t.Context(), h.account, parent.ID)
			if err != nil {
				t.Fatal(err)
			}
			verify := func(ctx context.Context, account string, installation, repository int64, name string) error {
				if err := h.verify(ctx, account, installation, repository, name); err != nil {
					return err
				}
				switch mode {
				case "profile":
					_, err = h.store.DisableExecutionProfile(ctx, h.account, profile.ID)
				case "binding":
					err = h.store.DisableGitHubBinding(ctx, h.account, h.binding.Name)
				case "generation":
					bindingReq := h.binding.GitHubBindingRequest
					bindingReq.PushBranches = []string{"release"}
					_, err = h.store.PutGitHubBinding(ctx, h.account, h.binding.Name, bindingReq)
				case "image", "image_without_profile":
					_, err = h.pool.Exec(ctx, "UPDATE jobs SET image_storage_key='private/rematerialized.ext4' WHERE account_id=$1", h.account)
				case "account":
					_, err = h.pool.Exec(ctx, "UPDATE accounts SET status='suspended' WHERE id=$1", h.account)
				case "provider":
					return errors.New("private-provider-credential")
				case "provider_revoked":
					return ErrGitHubAccess
				case "archive_during_lookup":
					_, _, err = h.store.ArchiveExecution(ctx, h.account, parent.ID)
				}
				return err
			}
			_, _, err = RerunJobs(t.Context(), h.backend.Config, h.store, verify, h.account, parent.ID, "fresh-rerun")
			if err == nil || h.count(t, "SELECT count(*) FROM ci_executions") != 1 || h.count(t, "SELECT count(*) FROM job_runs") != 0 {
				t.Fatal("rerun admitted with revoked authority or changed frozen configuration")
			}
			if mode == "provider" && (!errors.Is(err, ErrGitHubUnavailable) || strings.Contains(err.Error(), "private-provider")) {
				t.Fatal("rerun exposed provider failure details")
			}
		})
	}
}

func TestCIRerunAtomicInsertAndPersistentIdentityConstraints(t *testing.T) {
	h := newJobsHarness(t)
	parent := rerunCancelledParent(t, h, "original")
	h.backend.Config.ExecutionChecksEnabled, h.backend.Config.GitHubChecksEnabled = true, true
	if _, err := h.pool.Exec(t.Context(), `CREATE FUNCTION reject_rerun_check() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 RAISE EXCEPTION 'synthetic Check insertion failure'; END $$;
 CREATE TRIGGER reject_rerun_check BEFORE INSERT ON ci_execution_checks FOR EACH ROW EXECUTE FUNCTION reject_rerun_check()`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RerunJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, parent.ID, "atomic-rerun"); err == nil || h.count(t, "SELECT count(*) FROM ci_executions") != 1 {
		t.Fatal("failed Check insertion retained a partial rerun")
	}
	if _, err := h.pool.Exec(t.Context(), "DROP TRIGGER reject_rerun_check ON ci_execution_checks"); err != nil {
		t.Fatal(err)
	}
	child, replay, err := RerunJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, parent.ID, "atomic-rerun")
	if err != nil || replay {
		t.Fatalf("failed transaction consumed rerun request identity: %v", err)
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_executions SET snapshot=snapshot-'rerun' WHERE id=$1", child.ID); err == nil {
		t.Fatal("older writer silently discarded rerun lineage")
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_executions SET rerun_root_id=NULL WHERE id=$1", child.ID); err == nil {
		t.Fatal("partial rerun lineage passed database constraints")
	}
	foreignRevision, err := h.store.PutRevision(t.Context(), h.other, "foreign", executionDefinition())
	if err != nil {
		t.Fatal(err)
	}
	foreign, _, err := h.store.SubmitQualification(t.Context(), h.other, "foreign", SubmitRequest{RevisionID: foreignRevision.ID, CommitSHA: parent.CommitSHA})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(foreign.ID)
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_executions SET rerun_root_id=$2,snapshot=jsonb_set(snapshot,'{rerun,root_execution_id}',$3::jsonb) WHERE id=$1", child.ID, foreign.ID, string(encoded)); err == nil {
		t.Fatal("rerun root crossed an account boundary")
	}
}

func TestCIRerunOfPullRequestExecutionIsExplicitManualWork(t *testing.T) {
	h, _, cfg := pullRequestFixture(t)
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	capturePullRequest(t, h, cfg, 7, a)
	pollPullRequest(t, h, cfg, pullRequestResolver(currentPullRequest(h, 7, a)))
	source := pullRequestEvent(t, h, 7, a)
	parent, err := h.store.CancelExecution(t.Context(), h.account, source.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	manual, _, err := RerunJobs(t.Context(), cfg, h.store, h.verify, h.account, parent.ID, "manual-PR-rerun")
	if err != nil || manual.Automation != nil || manual.Rerun.ParentExecutionID != parent.ID {
		t.Fatalf("rerun reused automation admission authority: %v", err)
	}
	capturePullRequest(t, h, cfg, 7, a)
	capturePullRequest(t, h, cfg, 7, b)
	pollPullRequest(t, h, cfg, pullRequestResolver(currentPullRequest(h, 7, b)))
	manual, err = h.store.Execution(t.Context(), h.account, manual.ID)
	if err != nil || manual.CancelRequested || pullRequestEvent(t, h, 7, a).ID != source.ID || h.count(t, "SELECT count(*) FROM ci_automation_events") != 2 || h.count(t, "SELECT count(*) FROM ci_executions") != 3 {
		t.Fatal("PR automation duplicated its source, cancelled manual rerun or shared its automatic slot")
	}
}

func TestCIRerunRequiresNativeOutputAndTransferDrain(t *testing.T) {
	for _, mode := range []string{"instance", "task", "output", "transfer", "clean"} {
		t.Run(mode, func(t *testing.T) {
			h, parent, proof, view := runtimeFixture(t)
			result := runtimeResult(t, h, proof, view)
			raw, _ := json.Marshal(result)
			if err := h.store.SealJobsRuntime(t.Context(), proof, raw); err != nil {
				t.Fatal(err)
			}
			h.backend.Output = SealedJobsOutput{Store: h.store, Blobs: h.blobs}
			parent = stopManagedRuntime(t, h, parent)
			var err error
			switch mode {
			case "instance":
				_, err = h.pool.Exec(t.Context(), "UPDATE instances SET state='running' WHERE id=$1", proof.InstanceID)
			case "task":
				_, err = h.pool.Exec(t.Context(), "UPDATE job_tasks SET attempt=2 WHERE run_id=$1", proof.RunID)
			case "output":
				_, err = h.pool.Exec(t.Context(), "UPDATE ci_job_attempts SET output=NULL WHERE attempt_id=$1", proof.RunID)
			case "transfer":
				_, err = h.pool.Exec(t.Context(), "UPDATE ci_job_runtime SET transfer_token=$2,transfer_until=clock_timestamp()+interval '1 minute' WHERE attempt_id=$1", proof.RunID, uuid.NewString())
			}
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = RerunJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, parent.ID, "drained-rerun")
			if mode == "clean" {
				if err != nil {
					t.Fatalf("retained artifacts blocked a drained rerun: %v", err)
				}
			} else if !errors.Is(err, ErrRerunNotReady) || h.count(t, "SELECT count(*) FROM ci_executions") != 1 {
				t.Fatalf("%s cleanup allowed rerun: %v", mode, err)
			}
		})
	}
}

func TestCIRerunOwnershipGatesAndQuota(t *testing.T) {
	h := newJobsHarness(t)
	parent := h.admit(t, "active-parent")
	if _, _, err := RerunJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, parent.ID, "active"); !errors.Is(err, ErrRerunNotReady) {
		t.Fatal("active parent was rerun")
	}
	parent, err := h.store.CancelExecution(t.Context(), h.account, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct{ account, parent, key string }{{h.other, parent.ID, "foreign"}, {h.account, uuid.NewString(), "missing"}, {h.account, parent.ID, ""}, {h.account, "../invalid", "invalid"}} {
		if _, _, err := RerunJobs(t.Context(), h.backend.Config, h.store, h.verify, request.account, request.parent, request.key); err == nil {
			t.Fatal("invalid rerun scope accepted")
		}
	}
	qualification, _, err := h.store.SubmitQualification(t.Context(), h.account, "qualification", h.req.SubmitRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := RerunJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, qualification.ID, "qualification-rerun"); !errors.Is(err, ErrNotFound) {
		t.Fatal("qualification receipt granted compute")
	}
	off := ConfigFromEnv(func(string) string { return "" })
	if _, _, err := RerunJobs(t.Context(), off, h.store, h.verify, h.account, parent.ID, "off"); !errors.Is(err, ErrDispatchDisabled) {
		t.Fatal("dormant defaults admitted rerun")
	}
	first, _, err := RerunJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, parent.ID, "first")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < api.CIActiveExecutionsPerAccount; i++ {
		h.admit(t, uuid.NewString())
	}
	var quota *LimitError
	if _, _, err := RerunJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, parent.ID, "full"); !errors.As(err, &quota) {
		t.Fatal("rerun bypassed active execution quota")
	}
	if row, replay, err := RerunJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, parent.ID, "first"); err != nil || !replay || row.ID != first.ID {
		t.Fatal("full quota blocked idempotent receipt replay")
	}
}
