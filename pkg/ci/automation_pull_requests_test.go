// ADR-1583: PR execution uses fresh source evidence and existing native owners.
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

func pullRequestFixture(t *testing.T) (*jobsHarness, ExecutionProfile, Config) {
	t.Helper()
	h, _, cfg := automationFixture(t)
	req := h.binding.GitHubBindingRequest
	req.PullRequestBranches = []string{"main", "release"}
	var err error
	h.binding, err = h.store.PutGitHubBinding(t.Context(), h.account, h.binding.Name, req)
	if err != nil {
		t.Fatal(err)
	}
	profile := h.profile(t, "pull-requests")
	if _, err := SaveAutomationPolicy(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID,
		AutomationRequest{ProfileID: profile.ID, PushBranches: []string{"main"}, PullRequestBranches: req.PullRequestBranches}); err != nil {
		t.Fatal(err)
	}
	return h, profile, cfg
}

func pullRequestDelivery(h *jobsHarness, number int, sha, branch, action string) map[string]any {
	repo := map[string]any{"id": h.binding.RepositoryID, "full_name": h.binding.Repository}
	return map[string]any{"action": action, "number": number, "repository": repo, "installation": map[string]any{"id": h.binding.InstallationID},
		"pull_request": map[string]any{"state": "open", "draft": false, "head": map[string]any{"sha": sha, "repo": repo}, "base": map[string]any{"ref": branch, "repo": repo}}}
}

func capturePullRequest(t *testing.T, h *jobsHarness, cfg Config, number int, sha string) {
	t.Helper()
	raw, _ := json.Marshal(pullRequestDelivery(h, number, sha, "main", "synchronize"))
	if err := RecordVerifiedGitHubDelivery(t.Context(), cfg, h.store, uuid.NewString(), "pull_request", raw); err != nil {
		t.Fatal(err)
	}
}

func currentPullRequest(h *jobsHarness, number int, sha string) PullRequestState {
	return PullRequestState{Number: int64(number), State: "open", HeadSHA: sha, BaseBranch: "main", HeadRepository: h.binding.Repository,
		BaseRepository: h.binding.Repository, HeadRepositoryID: h.binding.RepositoryID, BaseRepositoryID: h.binding.RepositoryID}
}

func pullRequestResolver(state PullRequestState) AutomationPullRequestVerifier {
	return func(context.Context, GitHubBinding, int) (PullRequestState, error) { return state, nil }
}

func pollPullRequest(t *testing.T, h *jobsHarness, cfg Config, resolve AutomationPullRequestVerifier) {
	t.Helper()
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_automation_events SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE status='pending'"); err != nil {
		t.Fatal(err)
	}
	if _, err := AdmitAutomationWithResolvers(t.Context(), cfg, h.store, h.verify, AutomationResolvers{PullRequest: resolve}); err != nil {
		t.Fatal(err)
	}
}

func pullRequestEvent(t *testing.T, h *jobsHarness, number int, sha string) AutomationEventSummary {
	t.Helper()
	rows, err := h.store.AutomationEvents(t.Context(), h.account, h.binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Kind == "pull_request" && row.PRNumber == int64(number) && row.CommitSHA == sha {
			return row
		}
	}
	t.Fatal("missing PR automation source")
	return AutomationEventSummary{}
}

func TestCIAutomationPullRequestPolicyIsExplicitAndImmutable(t *testing.T) {
	h, profile, cfg := pullRequestFixture(t)
	req := AutomationRequest{ProfileID: profile.ID, PullRequestBranches: []string{"release", "main"}}
	before := clone(req)
	normalized, err := NormalizeAutomationRequest(req)
	if err != nil || !reflect.DeepEqual(req, before) || normalized.PullRequestBranches[0] != "main" {
		t.Fatal("normalizing PR filters mutated the caller or rejected PR-only policy")
	}
	first, err := SaveAutomationPolicy(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, req)
	if err != nil || len(first.PushBranches) != 0 || first.LatestOnly {
		t.Fatal("PR-only policy enabled push latest-only or required pushes")
	}
	again, err := SaveAutomationPolicy(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, req)
	if err != nil || again.Generation != first.Generation {
		t.Fatal("same PR policy changed generation")
	}
	for _, branches := range [][]string{nil, {"main*"}, {"main", "main"}, {"unbound"}, make([]string, api.CIGitHubMaxBranches+1)} {
		if _, err := SaveAutomationPolicy(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, AutomationRequest{ProfileID: profile.ID, PullRequestBranches: branches}); err == nil {
			t.Fatalf("invalid PR filters accepted: %v", branches)
		}
	}
	if h.count(t, "SELECT count(*) FROM ci_executions") != 0 || h.count(t, "SELECT count(*) FROM job_runs") != 0 || h.count(t, "SELECT count(*) FROM ci_automation_pull_requests") != 0 {
		t.Fatal("saving PR policy created execution, native work or observation authority")
	}
}

func TestCIAutomationPullRequestFreshCaptureAndProvenance(t *testing.T) {
	h, profile, cfg := pullRequestFixture(t)
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	off := cfg
	off.AutomationEnabled = false
	capturePullRequest(t, h, off, 7, a)
	capturePullRequest(t, h, cfg, 7, a)
	if h.count(t, "SELECT count(*) FROM ci_automation_events") != 0 || h.count(t, "SELECT count(*) FROM ci_github_triggers") != 1 {
		t.Fatal("enabling PR automation promoted a blocked source")
	}
	for _, action := range []string{"opened", "reopened", "synchronize", "ready_for_review"} {
		raw, _ := json.Marshal(pullRequestDelivery(h, 7, b, "main", action))
		if err := RecordVerifiedGitHubDelivery(t.Context(), cfg, h.store, uuid.NewString(), "pull_request", raw); err != nil {
			t.Fatal(err)
		}
	}
	if h.count(t, "SELECT count(*) FROM ci_automation_events") != 1 || h.count(t, "SELECT count(*) FROM ci_executions") != 0 {
		t.Fatal("capture duplicated PR actions or admitted execution in githubd")
	}
	if _, err := AdmitAutomationOnce(t.Context(), cfg, h.store, h.verify); err == nil {
		t.Fatal("PR source admitted without a current PR resolver")
	}
	pollPullRequest(t, h, cfg, func(_ context.Context, binding GitHubBinding, number int) (PullRequestState, error) {
		if binding.AccountID != h.account || binding.RepositoryID != 34 || number != 7 {
			t.Fatal("current PR lookup lost account/repository/PR scope")
		}
		return currentPullRequest(h, number, b), nil
	})
	event := pullRequestEvent(t, h, 7, b)
	row, err := h.store.Execution(t.Context(), h.account, event.ExecutionID)
	if err != nil || row.CommitSHA != b || row.ProfileID != profile.ID || row.Automation == nil || row.Automation.EventID != event.ID || row.Automation.Kind != "pull_request" || row.Automation.PRNumber != 7 {
		t.Fatalf("PR provenance lost: %+v %v", row.Automation, err)
	}
	h.restart()
	capturePullRequest(t, h, cfg, 7, b)
	if worked, err := AdmitAutomationWithResolvers(t.Context(), cfg, h.store, h.verify, AutomationResolvers{}); err != nil || worked {
		t.Fatal("redelivery/restart repeated an admitted PR")
	}
	if h.count(t, "SELECT count(*) FROM job_runs") != 0 {
		t.Fatal("automatic admission bypassed the dispatcher")
	}
}

func TestCIAutomationPullRequestCurrentHeadIgnoresDeliveryOrder(t *testing.T) {
	h, _, cfg := pullRequestFixture(t)
	a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	for _, sha := range []string{a, b, c} {
		capturePullRequest(t, h, cfg, 7, sha)
	}
	pollPullRequest(t, h, cfg, pullRequestResolver(currentPullRequest(h, 7, b)))
	pollPullRequest(t, h, cfg, pullRequestResolver(currentPullRequest(h, 7, b)))
	head := pullRequestEvent(t, h, 7, b)
	if head.Status != "admitted" {
		t.Fatal("fresh PR head was not admitted")
	}
	for _, sha := range []string{a, c} {
		old := pullRequestEvent(t, h, 7, sha)
		if old.Status != "ignored" || old.SupersededSHA != b || old.SupersededBy != head.ID || old.Retryable {
			t.Fatalf("obsolete PR source remained eligible: %+v", old)
		}
		if _, _, err := RequestAutomationRetry(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, old.ID, uuid.NewString(), AutomationRetryRequest{}); !errors.Is(err, ErrAutomationRetryConflict) {
			t.Fatal("recovery revived superseded PR source")
		}
	}
	data, err := os.ReadFile("../../migrations/20261017085604854_ci_pull_request_automation.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), strings.Split(string(data), "-- +goose Down")[1]); err == nil || !strings.Contains(err.Error(), "retain CI pull request") {
		t.Fatal("rollback discarded PR authority")
	}
	if _, err := h.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", h.account); err != nil || h.count(t, "SELECT count(*) FROM ci_automation_pull_requests") != 0 {
		t.Fatal("account deletion retained PR authority")
	}
}

func TestCIAutomationPullRequestIneligibleAndUnavailableNeverCancel(t *testing.T) {
	for _, mode := range []string{"closed", "draft", "fork", "wrong_base_repo", "wrong_base", "invalid", "missing_id", "wrong_number", "provider"} {
		t.Run(mode, func(t *testing.T) {
			h, _, cfg := pullRequestFixture(t)
			a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
			capturePullRequest(t, h, cfg, 7, a)
			pollPullRequest(t, h, cfg, pullRequestResolver(currentPullRequest(h, 7, a)))
			old := pullRequestEvent(t, h, 7, a)
			capturePullRequest(t, h, cfg, 7, b)
			state, reason := currentPullRequest(h, 7, b), "unavailable"
			switch mode {
			case "closed":
				state.State, reason = "closed", "pr_closed"
			case "draft":
				state.Draft, reason = true, "pr_draft"
			case "fork":
				state.HeadRepositoryID, state.HeadRepository, reason = 99, "foreign/repo", "pr_changed"
			case "wrong_base_repo":
				state.BaseRepositoryID, reason = 99, "pr_changed"
			case "wrong_base":
				state.BaseBranch, reason = "release", "pr_changed"
			case "invalid":
				state.HeadSHA = "not-a-sha"
			case "missing_id":
				state.HeadRepositoryID = 0
			case "wrong_number":
				state.Number++
			}
			_, err := AdmitAutomationWithResolvers(t.Context(), cfg, h.store, h.verify, AutomationResolvers{PullRequest: func(context.Context, GitHubBinding, int) (PullRequestState, error) {
				if mode == "provider" {
					return PullRequestState{}, errors.New("private-provider-credential")
				}
				return state, nil
			}})
			if reason == "unavailable" && err == nil || reason != "unavailable" && err != nil || err != nil && strings.Contains(err.Error(), "private-provider") {
				t.Fatalf("provider classification: %v", err)
			}
			candidate := pullRequestEvent(t, h, 7, b)
			row, err := h.store.Execution(t.Context(), h.account, old.ExecutionID)
			if err != nil || row.CancelRequested || h.count(t, "SELECT count(*) FROM ci_executions") != 1 || candidate.LastError != reason {
				t.Fatal("ineligible/unavailable PR observation cancelled work or admitted a candidate")
			}
		})
	}
}

func TestCIAutomationPullRequestFencesStaleObservations(t *testing.T) {
	for _, mode := range []string{"capture", "metadata", "observation", "unlisted_base", "duplicate", "other_pr", "push"} {
		t.Run(mode, func(t *testing.T) {
			h, profile, cfg := pullRequestFixture(t)
			a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
			capturePullRequest(t, h, cfg, 7, a)
			claim, err := h.store.ClaimAutomationEvent(t.Context(), h.account)
			if err != nil {
				t.Fatal(err)
			}
			observation, err := prepareAutomationPullRequest(t.Context(), h.store, h.verify, claim, pullRequestResolver(currentPullRequest(h, 7, a)))
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "capture":
				capturePullRequest(t, h, cfg, 7, b)
			case "metadata":
				off := cfg
				off.AutomationEnabled = false
				capturePullRequest(t, h, off, 7, b)
			case "observation":
				if _, err := h.store.beginAutomationPullRequest(t.Context(), claim); err != nil {
					t.Fatal(err)
				}
			case "unlisted_base":
				payload := pullRequestDelivery(h, 7, a, "unlisted", "edited")
				payload["changes"] = map[string]any{"base": map[string]any{"ref": map[string]any{"from": "main"}}}
				raw, _ := json.Marshal(payload)
				if err := RecordVerifiedGitHubDelivery(t.Context(), cfg, h.store, uuid.NewString(), "pull_request", raw); err != nil {
					t.Fatal(err)
				}
			case "duplicate":
				capturePullRequest(t, h, cfg, 7, a)
			case "other_pr":
				capturePullRequest(t, h, cfg, 8, b)
			case "push":
				captureAutomationTest(t, h, cfg, b)
			}
			req := JobsSubmitRequest{ProfileID: profile.ID, SubmitRequest: SubmitRequest{CommitSHA: a}, automation: &claim, automationPR: observation}
			_, _, err = AdmitJobs(t.Context(), cfg, h.store, h.verify, h.account, "github-auto:"+claim.ID, req)
			stale := mode == "capture" || mode == "metadata" || mode == "observation" || mode == "unlisted_base"
			if stale {
				if !errors.Is(err, errAutomationDeferred) || h.count(t, "SELECT count(*) FROM ci_executions") != 0 || pullRequestEvent(t, h, 7, a).Attempts != 0 {
					t.Fatalf("stale PR observation admitted work or consumed budget: %v", err)
				}
			} else if err != nil || h.count(t, "SELECT count(*) FROM ci_executions") != 1 {
				t.Fatalf("independent or duplicate source invalidated current PR observation: %v", err)
			}
		})
	}
}

func TestCIAutomationPullRequestIndependentSlotsAndConcurrentClaims(t *testing.T) {
	h, _, cfg := pullRequestFixture(t)
	a := strings.Repeat("a", 40)
	for _, number := range []int{7, 8} {
		capturePullRequest(t, h, cfg, number, a)
	}
	captureAutomationTest(t, h, cfg, a)
	var wg sync.WaitGroup
	errorsCh := make(chan error, 6)
	for range 6 {
		wg.Go(func() {
			_, err := AdmitAutomationWithResolvers(t.Context(), cfg, h.store, h.verify, AutomationResolvers{PullRequest: func(_ context.Context, _ GitHubBinding, number int) (PullRequestState, error) {
				return currentPullRequest(h, number, a), nil
			}})
			errorsCh <- err
		})
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil && !errors.Is(err, ErrLeaseLost) {
			t.Fatal(err)
		}
	}
	if h.count(t, "SELECT count(*) FROM ci_executions") != 3 {
		t.Fatal("PRs or pushes shared a slot or concurrent claims duplicated admission")
	}
	manual := h.admit(t, "manual-PR-independent")
	b := strings.Repeat("b", 40)
	capturePullRequest(t, h, cfg, 7, b)
	pollPullRequest(t, h, cfg, pullRequestResolver(currentPullRequest(h, 7, b)))
	for _, id := range []string{manual.ID, pullRequestEvent(t, h, 8, a).ExecutionID} {
		row, err := h.store.Execution(t.Context(), h.account, id)
		if err != nil || row.CancelRequested {
			t.Fatal("PR supersession cancelled manual work or another PR")
		}
	}
}

func TestCIAutomationPullRequestReplacementWaitsForNativeStop(t *testing.T) {
	h, _, cfg := pullRequestFixture(t)
	// Check publication is captured by the closures as well as the shared
	// native/transfer drain harness. No real provider or VM is involved.
	cfg.ExecutionChecksEnabled, cfg.GitHubChecksEnabled = true, true
	testAutomationReplacementNativeStop(t, h,
		func(sha string) { capturePullRequest(t, h, cfg, 7, sha) },
		func(sha string) { pollPullRequest(t, h, cfg, pullRequestResolver(currentPullRequest(h, 7, sha))) },
		func(sha string) AutomationEventSummary { return pullRequestEvent(t, h, 7, sha) })
}

func TestCIAutomationPullRequestRetargetSharesItsExistingSlot(t *testing.T) {
	h, _, cfg := pullRequestFixture(t)
	a := strings.Repeat("a", 40)
	capturePullRequest(t, h, cfg, 7, a)
	pollPullRequest(t, h, cfg, pullRequestResolver(currentPullRequest(h, 7, a)))
	old := pullRequestEvent(t, h, 7, a)
	payload := pullRequestDelivery(h, 7, a, "release", "edited")
	payload["changes"] = map[string]any{"base": map[string]any{"ref": map[string]any{"from": "main"}}}
	raw, _ := json.Marshal(payload)
	for range 2 {
		if err := RecordVerifiedGitHubDelivery(t.Context(), cfg, h.store, uuid.NewString(), "pull_request", raw); err != nil {
			t.Fatal(err)
		}
	}
	state := currentPullRequest(h, 7, a)
	state.BaseBranch = "release"
	pollPullRequest(t, h, cfg, pullRequestResolver(state))
	row, err := h.store.Execution(t.Context(), h.account, old.ExecutionID)
	if err != nil || !row.CancelRequested || h.count(t, "SELECT count(*) FROM ci_executions") != 1 {
		t.Fatal("retargeted PR escaped its existing slot or bypassed committed cancellation")
	}
	pollPullRequest(t, h, cfg, pullRequestResolver(state))
	if h.count(t, "SELECT count(*) FROM ci_executions") != 2 || h.count(t, "SELECT count(*) FROM ci_automation_pull_requests") != 1 {
		t.Fatal("retargeting duplicated the PR scope or failed to admit after cleanup")
	}
}

func TestCIAutomationPullRequestRecoveryRechecksCurrentState(t *testing.T) {
	h, _, cfg := pullRequestFixture(t)
	a := strings.Repeat("a", 40)
	capturePullRequest(t, h, cfg, 7, a)
	event := pullRequestEvent(t, h, 7, a)
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_automation_events SET status='dead',attempts=$2,last_error='unavailable' WHERE id=$1", event.ID, api.CIGitHubMaxAttempts); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RequestAutomationRetry(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, event.ID, "PR-recovery", AutomationRetryRequest{}); err != nil {
		t.Fatal(err)
	}
	state := currentPullRequest(h, 7, a)
	state.State = "closed"
	pollPullRequest(t, h, cfg, pullRequestResolver(state))
	if row := pullRequestEvent(t, h, 7, a); row.Status != "ignored" || row.LastError != "pr_closed" || h.count(t, "SELECT count(*) FROM ci_executions") != 0 {
		t.Fatal("recovery reused stale PR state or created compute intent")
	}
}

func TestCIAutomationPullRequestPolicyChangesFenceCurrentLookup(t *testing.T) {
	for _, mode := range []string{"policy", "profile", "binding"} {
		t.Run(mode, func(t *testing.T) {
			h, profile, cfg := pullRequestFixture(t)
			a := strings.Repeat("a", 40)
			capturePullRequest(t, h, cfg, 7, a)
			_, _ = AdmitAutomationWithResolvers(t.Context(), cfg, h.store, h.verify, AutomationResolvers{PullRequest: func(ctx context.Context, _ GitHubBinding, number int) (PullRequestState, error) {
				var err error
				switch mode {
				case "policy":
					_, err = h.store.DisableAutomationPolicy(ctx, h.account, h.binding.ID)
				case "profile":
					_, err = h.store.DisableExecutionProfile(ctx, h.account, profile.ID)
				case "binding":
					err = h.store.DisableGitHubBinding(ctx, h.account, h.binding.Name)
				}
				return currentPullRequest(h, number, a), err
			}})
			if h.count(t, "SELECT count(*) FROM ci_executions") != 0 || pullRequestEvent(t, h, 7, a).Status != "ignored" {
				t.Fatal("PR lookup committed with revoked policy/profile/binding authority")
			}
		})
	}
}

func TestCIAutomationPullRequestCancellationIsAtomicAndGenerationScoped(t *testing.T) {
	h, profile, cfg := pullRequestFixture(t)
	a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	capturePullRequest(t, h, cfg, 7, a)
	pollPullRequest(t, h, cfg, pullRequestResolver(currentPullRequest(h, 7, a)))
	old := pullRequestEvent(t, h, 7, a)
	capturePullRequest(t, h, cfg, 7, b)
	capturePullRequest(t, h, cfg, 7, c)
	if _, err := h.pool.Exec(t.Context(), `CREATE FUNCTION reject_pr_cancel() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.snapshot->>'cancel_requested'='true' THEN RAISE EXCEPTION 'synthetic cancellation failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_pr_cancel BEFORE UPDATE ON ci_executions FOR EACH ROW EXECUTE FUNCTION reject_pr_cancel()`); err != nil {
		t.Fatal(err)
	}
	if _, err := AdmitAutomationWithResolvers(t.Context(), cfg, h.store, h.verify, AutomationResolvers{PullRequest: pullRequestResolver(currentPullRequest(h, 7, b))}); err == nil {
		t.Fatal("failed native cancellation committed PR supersession")
	}
	if row := pullRequestEvent(t, h, 7, c); row.Status != "pending" || row.SupersededAt != nil || pullRequestEvent(t, h, 7, a).SupersededAt != nil {
		t.Fatal("cancellation rollback left partial PR queue/supersession changes")
	}
	if _, err := h.pool.Exec(t.Context(), "DROP TRIGGER reject_pr_cancel ON ci_executions"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.DisableAutomationPolicy(t.Context(), h.account, h.binding.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveAutomationPolicy(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID,
		AutomationRequest{ProfileID: profile.ID, PullRequestBranches: []string{"main"}}); err != nil {
		t.Fatal(err)
	}
	d := strings.Repeat("d", 40)
	capturePullRequest(t, h, cfg, 7, d)
	pollPullRequest(t, h, cfg, pullRequestResolver(currentPullRequest(h, 7, d)))
	prior, err := h.store.Execution(t.Context(), h.account, old.ExecutionID)
	if err != nil || prior.CancelRequested || pullRequestEvent(t, h, 7, d).LastError != "pr_wait" {
		t.Fatal("new policy generation cancelled prior-generation PR work or bypassed its slot")
	}
	if _, err := h.store.CancelExecution(t.Context(), h.account, prior.ID); err != nil {
		t.Fatal(err)
	}
	h.restart()
	pollPullRequest(t, h, cfg, pullRequestResolver(currentPullRequest(h, 7, d)))
	if pullRequestEvent(t, h, 7, d).Status != "admitted" {
		t.Fatal("settled prior-generation PR slot did not recover after restart")
	}
}
