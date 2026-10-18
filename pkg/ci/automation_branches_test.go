// ADR-1581: branch slots and supersession preserve source and native ownership.
package ci

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func branchFixture(t *testing.T) (*jobsHarness, ExecutionProfile, Config) {
	t.Helper()
	h, profile, cfg := automationFixture(t)
	if _, err := SaveAutomationPolicy(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, AutomationRequest{ProfileID: profile.ID, PushBranches: []string{"main"}, LatestOnly: true}); err != nil {
		t.Fatal(err)
	}
	return h, profile, cfg
}

func branchHead(sha string) AutomationHeadVerifier {
	return func(context.Context, GitHubBinding, string) (string, bool, error) { return sha, sha != "", nil }
}

func branchEvent(t *testing.T, h *jobsHarness, sha string) AutomationEventSummary {
	t.Helper()
	rows, err := h.store.AutomationEvents(t.Context(), h.account, h.binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.CommitSHA == sha {
			return row
		}
	}
	t.Fatal("missing branch source event")
	return AutomationEventSummary{}
}

func pollBranch(t *testing.T, h *jobsHarness, cfg Config, head string) {
	t.Helper()
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_automation_events SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE status='pending'"); err != nil {
		t.Fatal(err)
	}
	if _, err := AdmitAutomationOnce(t.Context(), cfg, h.store, h.verify, branchHead(head)); err != nil {
		t.Fatal(err)
	}
}

func TestCIAutomationBranchSerializesWithoutBurningRetries(t *testing.T) {
	h, _, cfg := automationFixture(t) // latest-only defaults off
	for _, c := range []string{"a", "b", "c"} {
		captureAutomationTest(t, h, cfg, strings.Repeat(c, 40))
	}
	var wg sync.WaitGroup
	errorsCh := make(chan error, 6)
	for range 6 {
		wg.Go(func() { _, err := AdmitAutomationOnce(t.Context(), cfg, h.store, h.verify); errorsCh <- err })
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	if h.count(t, "SELECT count(*) FROM ci_executions") != api.CIAutomationActivePerBranch {
		t.Fatal("concurrent workers admitted more than one branch execution")
	}
	for range api.CIGitHubMaxAttempts + 2 {
		pollBranch(t, h, cfg, "")
	}
	if h.count(t, "SELECT count(*) FROM ci_automation_events WHERE status='pending' AND attempts=0") != 2 {
		t.Fatal("branch waiting consumed failure budgets")
	}
	manual := h.admit(t, "manual-independent")
	var first string
	if err := h.pool.QueryRow(t.Context(), "SELECT execution_id FROM ci_automation_events WHERE status='admitted'").Scan(&first); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.CancelExecution(t.Context(), h.account, first); err != nil {
		t.Fatal(err)
	}
	h.restart()
	pollBranch(t, h, cfg, "")
	if h.count(t, "SELECT count(*) FROM ci_automation_events WHERE status='admitted'") != 2 {
		t.Fatal("confirmed terminal branch did not release its slot after restart")
	}
	current, err := h.store.Execution(t.Context(), h.account, manual.ID)
	if err != nil || current.CancelRequested {
		t.Fatal("automatic branch serialization cancelled manual execution")
	}
}

func TestCIAutomationBranchVerifiedHeadIgnoresDeliveryOrder(t *testing.T) {
	h, _, cfg := branchFixture(t)
	a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	for _, sha := range []string{a, b, c} {
		captureAutomationTest(t, h, cfg, sha)
	}
	pollBranch(t, h, cfg, b) // a is stale; c was delivered last but is not head.
	pollBranch(t, h, cfg, b)
	head := branchEvent(t, h, b)
	if head.Status != "admitted" || head.ExecutionID == "" {
		t.Fatal("verified branch head was not admitted")
	}
	for _, sha := range []string{a, c} {
		old := branchEvent(t, h, sha)
		if old.Status != "ignored" || old.LastError != "head_changed" || old.SupersededBy != head.ID || old.SupersededSHA != b || old.SupersededAt == nil || old.Retryable {
			t.Fatalf("supersession lost durable identity: %+v", old)
		}
		if _, _, err := RequestAutomationRetry(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, old.ID, uuid.NewString(), AutomationRetryRequest{}); !errors.Is(err, ErrAutomationRetryConflict) {
			t.Fatal("recovery revived an obsolete source")
		}
	}
	h.restart()
	captureAutomationTest(t, h, cfg, a)
	pollBranch(t, h, cfg, a) // force-pushing an already superseded SHA cannot revive it.
	if h.count(t, "SELECT count(*) FROM ci_executions") != 1 {
		t.Fatal("redelivery or force push revived obsolete retained identity")
	}
	data, err := os.ReadFile("../../migrations/20261017074525544_ci_automation_branch_concurrency.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), strings.Split(string(data), "-- +goose Down")[1]); err == nil || !strings.Contains(err.Error(), "retain CI branch") {
		t.Fatal("rollback discarded branch serialization evidence")
	}
	if _, err := h.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", h.account); err != nil {
		t.Fatal(err)
	}
	if h.count(t, "SELECT count(*) FROM ci_automation_branches") != 0 {
		t.Fatal("account deletion retained branch authority")
	}
}

func TestCIAutomationBranchRejectsStaleHeadObservations(t *testing.T) {
	for _, mode := range []string{"capture", "metadata_capture", "observation", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			h, profile, cfg := branchFixture(t)
			a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
			captureAutomationTest(t, h, cfg, a)
			old, err := h.store.ClaimAutomationEvent(t.Context(), h.account)
			if err != nil {
				t.Fatal(err)
			}
			observed, err := prepareAutomationHead(t.Context(), h.store, h.verify, old, []AutomationHeadVerifier{branchHead(a)})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "capture" {
				captureAutomationTest(t, h, cfg, b)
			} else if mode == "metadata_capture" {
				off := cfg
				off.AutomationEnabled = false
				captureAutomationTest(t, h, off, b)
			} else if mode == "duplicate" {
				captureAutomationTest(t, h, cfg, a)
			} else if _, err := h.store.beginAutomationHead(t.Context(), old); err != nil {
				t.Fatal(err)
			}
			req := JobsSubmitRequest{ProfileID: profile.ID, SubmitRequest: SubmitRequest{CommitSHA: a}, automation: &old, automationHead: observed}
			if mode == "duplicate" {
				if _, _, err := AdmitJobs(t.Context(), cfg, h.store, h.verify, h.account, "github-auto:"+old.ID, req); err != nil {
					t.Fatalf("duplicate delivery invalidated a current observation: %v", err)
				}
				return
			}
			if _, _, err := AdmitJobs(t.Context(), cfg, h.store, h.verify, h.account, "github-auto:"+old.ID, req); !errors.Is(err, errAutomationDeferred) {
				t.Fatalf("stale %s was not deferred: %v", mode, err)
			}
			if h.count(t, "SELECT count(*) FROM ci_executions") != 0 || h.count(t, "SELECT count(*) FROM ci_automation_events WHERE status='ignored'") != 0 {
				t.Fatal("stale head response admitted or superseded work")
			}
			if row := branchEvent(t, h, a); row.Status != "pending" || row.Attempts != 0 {
				t.Fatal("stale response consumed admission budget")
			}
		})
	}
}

func TestCIAutomationBranchCancellationWaitsForNativeStop(t *testing.T) {
	h, _, cfg := branchFixture(t)
	cfg.ExecutionChecksEnabled, cfg.GitHubChecksEnabled = true, true
	testAutomationReplacementNativeStop(t, h,
		func(sha string) { captureAutomationTest(t, h, cfg, sha) },
		func(sha string) { pollBranch(t, h, cfg, sha) },
		func(sha string) AutomationEventSummary { return branchEvent(t, h, sha) })
}

func testAutomationReplacementNativeStop(t *testing.T, h *jobsHarness, capture, poll func(string), lookup func(string) AutomationEventSummary) {
	t.Helper()
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	capture(a)
	poll(a)
	first := lookup(a)
	old, err := h.store.Execution(t.Context(), h.account, first.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	old = h.tick(t, old)
	instance := h.fakeInstance(t, old, "test")
	var lease string
	if err := h.pool.QueryRow(t.Context(), "SELECT lease_token::text FROM job_tasks WHERE run_id=$1", old.Jobs[0].Attempt.ID).Scan(&lease); err != nil {
		t.Fatal(err)
	}
	proof, err := h.store.IssueJobsRuntime(t.Context(), h.backend.Config, RuntimeHostProof{RunID: old.Jobs[0].Attempt.ID, InstanceID: instance, LeaseToken: lease})
	if err != nil {
		t.Fatal(err)
	}
	h.backend.Output = SealedJobsOutput{Store: h.store, Blobs: h.blobs}
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_job_runtime SET transfer_token=gen_random_uuid(),transfer_until=clock_timestamp()+interval '1 minute' WHERE attempt_id=$1", proof.RunID); err != nil {
		t.Fatal(err)
	}
	capture(b)
	poll(b)
	if _, err := h.store.AuthorizeJobsRuntime(t.Context(), h.backend.Config, proof, RuntimeSource); !errors.Is(err, ErrRuntimeAuthority) {
		t.Fatal("superseded execution retained source access")
	}
	if control, err := h.store.AuthorizeJobsRuntime(t.Context(), h.backend.Config, proof, RuntimeControl); err != nil || !control.CancelRequested {
		t.Fatal("guest could not observe its cancellation request")
	}
	view, err := h.store.Execution(t.Context(), h.account, old.ID)
	if err != nil || !view.CancelRequested || terminal(view.Status) {
		t.Fatal("supersession bypassed ordinary cancellation")
	}
	for range 3 {
		poll(b)
	}
	if h.count(t, "SELECT count(*) FROM ci_executions") != 1 || h.count(t, "SELECT count(*) FROM job_runs") != 1 {
		t.Fatal("replacement started before native cleanup")
	}
	h.publish(t, old, "test", StatusCancelled, 130, []byte("cancelled"), false, false)
	old = h.tick(t, old)
	poll(b)
	if h.count(t, "SELECT count(*) FROM ci_executions") != 1 {
		t.Fatal("terminal task receipt substituted for stopped native instance")
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE instances SET state='stopped' WHERE id=$1", instance); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		old = h.tick(t, old)
		if terminal(old.Status) {
			break
		}
	}
	if !terminal(old.Status) {
		t.Fatal("ordinary cancellation did not finish after confirmed stop")
	}
	h.restart()
	poll(b)
	if h.count(t, "SELECT count(*) FROM ci_executions") != 1 {
		t.Fatal("replacement bypassed an in-flight transfer after native stop")
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_job_runtime SET transfer_token=NULL,transfer_until=NULL WHERE attempt_id=$1", proof.RunID); err != nil {
		t.Fatal(err)
	}
	poll(b)
	if h.count(t, "SELECT count(*) FROM ci_executions") != 2 || lookup(b).Status != "admitted" {
		t.Fatal("confirmed cleanup failed to release replacement")
	}
	if event := lookup(a); event.SupersededBy != lookup(b).ID || event.Status != "admitted" {
		t.Fatal("supersession rewrote admitted provenance")
	}
	if h.count(t, "SELECT count(*) FROM ci_execution_checks") != 2 {
		t.Fatal("supersession bypassed existing execution Check intent")
	}
}

func TestCIAutomationBranchProviderAndPolicyFailuresDoNotCancel(t *testing.T) {
	for _, name := range []string{"missing", "provider", "invalid", "deleted", "revoked"} {
		t.Run(name, func(t *testing.T) {
			h, _, cfg := branchFixture(t)
			a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
			captureAutomationTest(t, h, cfg, a)
			pollBranch(t, h, cfg, a)
			first := branchEvent(t, h, a)
			captureAutomationTest(t, h, cfg, b)
			head := func(ctx context.Context, _ GitHubBinding, _ string) (string, bool, error) {
				switch name {
				case "provider":
					return "", false, errors.New("private-provider-detail")
				case "invalid":
					return "not-a-sha", true, nil
				case "revoked":
					_, err := h.store.DisableAutomationPolicy(ctx, h.account, h.binding.ID)
					return b, true, err
				}
				return "", false, nil
			}
			var err error
			if name == "missing" {
				_, err = AdmitAutomationOnce(t.Context(), cfg, h.store, h.verify)
			} else {
				_, err = AdmitAutomationOnce(t.Context(), cfg, h.store, h.verify, head)
			}
			if err != nil && strings.Contains(err.Error(), "private-provider-detail") {
				t.Fatal("head lookup leaked provider response")
			}
			old, readErr := h.store.Execution(t.Context(), h.account, first.ExecutionID)
			if readErr != nil || old.CancelRequested || h.count(t, "SELECT count(*) FROM ci_executions") != 1 {
				t.Fatal("missing or revoked head authority cancelled existing execution")
			}
			if name == "deleted" && branchEvent(t, h, b).LastError != "branch_deleted" {
				t.Fatal("missing branch was treated as a successful current head")
			}
		})
	}
}

func TestCIAutomationBranchSupersessionAtomicityAndPolicyGeneration(t *testing.T) {
	h, profile, cfg := branchFixture(t)
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	captureAutomationTest(t, h, cfg, a)
	pollBranch(t, h, cfg, a)
	old := branchEvent(t, h, a)
	captureAutomationTest(t, h, cfg, b)
	if _, err := h.pool.Exec(t.Context(), `CREATE FUNCTION reject_branch_cancel() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.snapshot->>'cancel_requested'='true' THEN RAISE EXCEPTION 'synthetic cancellation failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_branch_cancel BEFORE UPDATE ON ci_executions FOR EACH ROW EXECUTE FUNCTION reject_branch_cancel()`); err != nil {
		t.Fatal(err)
	}
	if _, err := AdmitAutomationOnce(t.Context(), cfg, h.store, h.verify, branchHead(b)); err == nil {
		t.Fatal("failed cancellation committed supersession")
	}
	if branchEvent(t, h, a).SupersededAt != nil {
		t.Fatal("cancellation rollback retained partial supersession evidence")
	}
	if _, err := h.pool.Exec(t.Context(), "DROP TRIGGER reject_branch_cancel ON ci_executions"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.DisableAutomationPolicy(t.Context(), h.account, h.binding.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveAutomationPolicy(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, AutomationRequest{ProfileID: profile.ID, PushBranches: []string{"main"}, LatestOnly: true}); err != nil {
		t.Fatal(err)
	}
	c := strings.Repeat("c", 40)
	captureAutomationTest(t, h, cfg, c)
	pollBranch(t, h, cfg, c)
	prior, err := h.store.Execution(t.Context(), h.account, old.ExecutionID)
	if err != nil || prior.CancelRequested || branchEvent(t, h, c).Status != "pending" {
		t.Fatal("new policy generation cancelled frozen prior-generation work")
	}
	if _, err := h.store.CancelExecution(t.Context(), h.account, prior.ID); err != nil {
		t.Fatal(err)
	}
	pollBranch(t, h, cfg, c)
	if branchEvent(t, h, c).Status != "admitted" {
		t.Fatal("prior-generation cleanup did not release branch slot")
	}
}

func TestCIAutomationBranchDifferentBranchesHaveIndependentSlots(t *testing.T) {
	h, _, cfg := automationFixture(t)
	req := h.binding.GitHubBindingRequest
	req.PushBranches = []string{"main", "release"}
	var err error
	h.binding, err = h.store.PutGitHubBinding(t.Context(), h.account, h.binding.Name, req)
	if err != nil {
		t.Fatal(err)
	}
	profile := h.profile(t, "branches")
	policy, err := SaveAutomationPolicy(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, AutomationRequest{ProfileID: profile.ID, PushBranches: req.PushBranches})
	if err != nil || policy.LatestOnly {
		t.Fatal("branch policy lost safe defaults")
	}
	for i, branch := range req.PushBranches {
		if err := RecordVerifiedGitHubDelivery(t.Context(), cfg, h.store, uuid.NewString(), "push", automationDelivery(h, fmt.Sprintf("%040x", i+1), branch)); err != nil {
			t.Fatal(err)
		}
		pollBranch(t, h, cfg, "")
	}
	if h.count(t, "SELECT count(*) FROM ci_executions") != 2 {
		t.Fatal("independent branches shared an execution slot")
	}
}

func TestCIAutomationBranchExpiredFinalClaimRemainsRecoverable(t *testing.T) {
	h, _, cfg := automationFixture(t)
	sha := strings.Repeat("a", 40)
	captureAutomationTest(t, h, cfg, sha)
	claim, err := h.store.ClaimAutomationEvent(t.Context(), h.account)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_automation_events SET attempts=$2,last_error='branch_wait',lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", claim.ID, api.CIGitHubMaxAttempts); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.ClaimAutomationEvent(t.Context(), h.account); !errors.Is(err, ErrNoGitHubWork) {
		t.Fatal("exhausted abandoned branch claim was not retired")
	}
	event := branchEvent(t, h, sha)
	if event.Status != "dead" || event.LastError != "unavailable" || !event.Retryable {
		t.Fatal("abandoned claim inherited branch waiting instead of availability failure")
	}
	if _, _, err := RequestAutomationRetry(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, event.ID, "abandoned", AutomationRetryRequest{}); err != nil {
		t.Fatal(err)
	}
}

func TestCIAutomationBranchSupersessionFencesInFlightSourceStart(t *testing.T) {
	h, _, cfg := branchFixture(t)
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	captureAutomationTest(t, h, cfg, a)
	pollBranch(t, h, cfg, a)
	old := branchEvent(t, h, a)
	entered, resume := make(chan struct{}), make(chan struct{})
	var released sync.Once
	release := func() { released.Do(func() { close(resume) }) }
	defer release()
	h.stream.hook = func() { close(entered); <-resume }
	result := make(chan error, 1)
	go func() {
		_, err := (Engine{Store: h.store, Backend: h.backend}).Tick(t.Context(), h.account, old.ExecutionID)
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("source fetch never reached the external boundary")
	}
	captureAutomationTest(t, h, cfg, b)
	pollBranch(t, h, cfg, b)
	if h.count(t, "SELECT count(*) FROM job_runs") != 0 || branchEvent(t, h, b).Status != "pending" {
		t.Fatal("supersession admitted replacement before cancelling the start intent")
	}
	release()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("old controller acknowledged a start after cancellation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("old source fetch did not return")
	}
	if h.count(t, "SELECT count(*) FROM job_runs") != 0 {
		t.Fatal("late source fetch created native intent after supersession")
	}
	current, err := h.store.Execution(t.Context(), h.account, old.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (Engine{Store: h.store, Backend: h.backend}).Tick(t.Context(), h.account, current.ID); !errors.Is(err, ErrLeaseBusy) {
		t.Fatal("cancellation bypassed the old controller lease")
	}
	h.clock.advance(api.CIExecutionLeaseSeconds + 1)
	for range 3 {
		current = h.tick(t, current)
		if terminal(current.Status) {
			break
		}
	}
	pollBranch(t, h, cfg, b)
	if branchEvent(t, h, b).Status != "admitted" {
		t.Fatal("settled cancelled start did not release its branch slot")
	}
}
