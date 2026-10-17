package ci

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCIAutomationPathPatternSemantics(t *testing.T) {
	for _, tc := range []struct {
		pattern, name string
		match         bool
	}{
		{"**/*.go", "main.go", true}, {"**/*.go", "pkg/ci/main.go", true}, {"*.go", "pkg/main.go", false},
		{"pkg/**", "pkg/main.go", true}, {"pkg/**", "pkg", true}, {"pkg/**/test?.go", "pkg/ci/test1.go", true},
		{"pkg/**/test?.go", "pkg/test1.go", true}, {"docs/*", "docs/a/b.md", false}, {"**", ".github/workflows/check.yml", true},
		{"docs/**", "Docs/a.md", false}, {"src/a?.go", "src/ab.go", true}, {"a/**/b/**/c", "a/b/c", true},
	} {
		if got := automationPathMatches(tc.pattern, tc.name); got != tc.match {
			t.Errorf("%q / %q = %v", tc.pattern, tc.name, got)
		}
	}
	for _, pattern := range []string{"", "/docs/**", "docs/", "docs//x", "../x", "./x", "docs/**.md", "docs/[ab]", "!docs/**", "docs/\\x", "x\n", "x/***/y", strings.Repeat("a", api.CIAutomationPathPatternMaxBytes+1)} {
		if validAutomationPath(pattern, true) {
			t.Errorf("accepted invalid pattern %q", pattern)
		}
	}
	req := AutomationRequest{ProfileID: uuid.NewString(), PushBranches: []string{"main"}, IncludePaths: []string{"src/**", "go.mod"}, ExcludePaths: []string{"**/*_test.go"}}
	got, err := NormalizeAutomationRequest(req)
	if err != nil || got.IncludePaths[0] != "go.mod" || req.IncludePaths[0] != "src/**" {
		t.Fatal("normalization mutated caller or failed to sort")
	}
	got.IncludePaths[0] = "changed"
	if req.IncludePaths[1] != "go.mod" {
		t.Fatal("path filters alias caller memory")
	}
	for _, patterns := range [][]string{{"src/**", "src/**"}, make([]string, api.CIAutomationMaxPathFilters+1)} {
		req.IncludePaths = patterns
		if _, err := NormalizeAutomationRequest(req); err == nil {
			t.Fatal("invalid filter set accepted")
		}
	}
}

func pathPolicy(t *testing.T, h *jobsHarness, profile ExecutionProfile, cfg Config, pr, latest bool) AutomationPolicy {
	t.Helper()
	req := AutomationRequest{ProfileID: profile.ID, PushBranches: []string{"main"}, IncludePaths: []string{"src/**", "go.mod"}, ExcludePaths: []string{"src/docs/**"}, LatestOnly: latest}
	if pr {
		req.PullRequestBranches = []string{"main"}
	}
	row, err := SaveAutomationPolicy(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func capturePathPush(t *testing.T, h *jobsHarness, cfg Config, base, head string) {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(automationDelivery(h, head, "main"), &payload); err != nil {
		t.Fatal(err)
	}
	payload["before"] = base
	raw, _ := json.Marshal(payload)
	if err := RecordVerifiedGitHubDelivery(t.Context(), cfg, h.store, uuid.NewString(), "push", raw); err != nil {
		t.Fatal(err)
	}
}

func pathChanges(base, head string, files ...ChangedFile) ChangedFiles {
	return ChangedFiles{BaseSHA: base, HeadSHA: head, MergeBaseSHA: base, Complete: true, Files: files}
}
func resolvePathChanges(changes ChangedFiles) AutomationChangesResolver {
	return func(context.Context, GitHubBinding, string, string) (ChangedFiles, error) { return changes, nil }
}
func pathEvent(t *testing.T, h *jobsHarness, head string) AutomationEventSummary {
	t.Helper()
	rows, err := h.store.AutomationEvents(t.Context(), h.account, h.binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.CommitSHA == head {
			return row
		}
	}
	t.Fatal("source event missing")
	return AutomationEventSummary{}
}

func TestCIAutomationPathDecisionsConservativeAndExact(t *testing.T) {
	for _, mode := range []string{"docs", "excluded", "code", "rename_out", "rename_in", "removed", "empty", "unavailable", "incomplete", "wrong_base", "wrong_head", "diverged_push", "invalid_path", "unknown_status", "missing_before", "zero_before", "no_resolver", "cap", "missing_previous"} {
		t.Run(mode, func(t *testing.T) {
			h, profile, cfg := automationFixture(t)
			pathPolicy(t, h, profile, cfg, false, false)
			base, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
			changes := pathChanges(base, head, ChangedFile{Path: "docs/readme.md", Status: "modified"})
			want, reason := "run", "changes_unavailable"
			switch mode {
			case "docs":
				want, reason = "skip", "paths_filtered"
			case "excluded":
				changes.Files[0].Path = "src/docs/README.md"
				want, reason = "skip", "paths_filtered"
			case "code":
				changes.Files[0].Path = "src/main.go"
				reason = "paths_matched"
			case "rename_out":
				changes.Files = []ChangedFile{{Path: "docs/main.go", PreviousPath: "src/main.go", Status: "renamed"}}
				reason = "paths_matched"
			case "rename_in":
				changes.Files = []ChangedFile{{Path: "src/main.go", PreviousPath: "docs/main.go", Status: "renamed"}}
				reason = "paths_matched"
			case "removed":
				changes.Files = []ChangedFile{{Path: "src/main.go", Status: "removed"}}
				reason = "paths_matched"
			case "empty":
				changes.Files = nil
				want, reason = "skip", "paths_filtered"
			case "incomplete":
				changes.Complete = false
			case "wrong_base":
				changes.BaseSHA = strings.Repeat("c", 40)
			case "wrong_head":
				changes.HeadSHA = strings.Repeat("c", 40)
			case "diverged_push":
				changes.MergeBaseSHA = strings.Repeat("c", 40)
			case "invalid_path":
				changes.Files[0].Path = "../src/main.go"
			case "unknown_status":
				changes.Files[0].Status = "future-status"
			case "missing_before":
				base = ""
			case "zero_before":
				base = strings.Repeat("0", 40)
			case "cap":
				for range api.CIAutomationCompareMaxFiles {
					changes.Files = append(changes.Files, ChangedFile{Path: "docs/capped", Status: "added"})
				}
			case "missing_previous":
				changes.Files[0].Status = "renamed"
			}
			capturePathPush(t, h, cfg, base, head)
			resolve := resolvePathChanges(changes)
			if mode == "unavailable" {
				resolve = func(context.Context, GitHubBinding, string, string) (ChangedFiles, error) {
					return ChangedFiles{}, errors.New("private-provider-token")
				}
			}
			if mode == "no_resolver" {
				resolve = nil
			}
			if worked, err := AdmitAutomationWithResolvers(t.Context(), cfg, h.store, h.verify, AutomationResolvers{Changes: resolve}); err != nil || !worked {
				t.Fatalf("admit: %v %v", worked, err)
			}
			event := pathEvent(t, h, head)
			if event.PathDecision == nil || event.PathDecision.Outcome != want || event.PathDecision.Reason != reason {
				t.Fatalf("decision: %+v", event)
			}
			if want == "skip" {
				if event.Status != "ignored" || event.LastError != "paths_filtered" || event.ExecutionID != "" || event.Retryable || h.count(t, "SELECT count(*) FROM ci_executions") != 0 {
					t.Fatal("skip created work or remained recoverable")
				}
			} else if event.Status != "admitted" || event.ExecutionID == "" {
				t.Fatal("uncertain/matching diff did not run")
			}
			if h.count(t, "SELECT count(*) FROM job_runs") != 0 {
				t.Fatal("path evaluation started native work")
			}
		})
	}
}

func TestCIAutomationPathsSkipDedupChecksAndRollback(t *testing.T) {
	h, profile, cfg := automationFixture(t)
	cfg.ExecutionChecksEnabled, cfg.GitHubChecksEnabled = true, true
	policy := pathPolicy(t, h, profile, cfg, false, false)
	same := pathPolicy(t, h, profile, cfg, false, false)
	if same.Generation != policy.Generation {
		t.Fatal("equivalent path policy changed generation")
	}
	base, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
	capturePathPush(t, h, cfg, base, head)
	resolve := resolvePathChanges(pathChanges(base, head, ChangedFile{Path: "docs/private-name.md", Status: "removed"}))
	if _, err := AdmitAutomationWithResolvers(t.Context(), cfg, h.store, h.verify, AutomationResolvers{Changes: resolve}); err != nil {
		t.Fatal(err)
	}
	event := pathEvent(t, h, head)
	if h.count(t, "SELECT count(*) FROM ci_automation_path_checks") != 1 || h.count(t, "SELECT count(*) FROM ci_execution_checks") != 0 {
		t.Fatal("skip Check used an execution identity")
	}
	h.restart()
	// Changed before evidence and disabled capture mode must not alter source identity.
	off := cfg
	off.AutomationEnabled = false
	capturePathPush(t, h, off, strings.Repeat("c", 40), head)
	if worked, err := AdmitAutomationWithResolvers(t.Context(), cfg, h.store, h.verify, AutomationResolvers{Changes: func(context.Context, GitHubBinding, string, string) (ChangedFiles, error) {
		t.Fatal("settled source was evaluated again")
		return ChangedFiles{}, nil
	}}); err != nil || worked {
		t.Fatal("redelivery revived skip")
	}
	var first ExecutionCheck
	if worked, err := PublishExecutionCheckOnce(t.Context(), cfg, h.store, h.verify, func(_ context.Context, c ExecutionCheck) (int64, error) { first = c; return 0, ErrGitHubUnavailable }); err == nil || !worked {
		t.Fatal("lost provider acknowledgement not retried")
	}
	if first.ExecutionID != "" || first.AutomationEventID != event.ID || first.ExternalID() != "gregale-ci-source:"+event.ID || first.Conclusion != "skipped" || !first.Valid() || strings.Contains(first.Summary, "private-name") {
		t.Fatalf("invalid/private skip Check: %+v", first)
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_automation_path_checks SET next_attempt_at=clock_timestamp()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	h.restart()
	if worked, err := PublishExecutionCheckOnce(t.Context(), cfg, h.store, h.verify, func(_ context.Context, c ExecutionCheck) (int64, error) {
		if !reflect.DeepEqual(first, c) {
			t.Fatal("retry changed immutable projection")
		}
		return 77, nil
	}); err != nil || !worked {
		t.Fatal("skip Check retry unavailable")
	}
	if worked, err := PublishExecutionCheckOnce(t.Context(), cfg, h.store, h.verify, func(context.Context, ExecutionCheck) (int64, error) {
		t.Fatal("terminal skip republished")
		return 77, nil
	}); err != nil || worked {
		t.Fatal("settled Check remained due")
	}
	if _, _, err := RequestAutomationRetry(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, event.ID, uuid.NewString(), AutomationRetryRequest{}); !errors.Is(err, ErrAutomationRetryConflict) {
		t.Fatal("recovery promoted filtered source")
	}
	data, err := os.ReadFile("../../migrations/20261017103328494_ci_automation_path_filters.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), strings.Split(string(data), "-- +goose Down")[1]); err == nil || !strings.Contains(err.Error(), "retain CI path filter") {
		t.Fatal("rollback discarded path authority")
	}
	if _, err := h.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", h.account); err != nil || h.count(t, "SELECT count(*) FROM ci_automation_path_checks") != 0 {
		t.Fatal("account deletion retained source Check authority")
	}
}

func TestCIAutomationPathsNoFiltersAndGatesDoNotLookup(t *testing.T) {
	h, profile, cfg := automationFixture(t)
	base, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
	resolve := func(context.Context, GitHubBinding, string, string) (ChangedFiles, error) {
		t.Fatal("disabled/unfiltered automation called diff provider")
		return ChangedFiles{}, nil
	}
	capturePathPush(t, h, cfg, base, head)
	if _, err := AdmitAutomationWithResolvers(t.Context(), cfg, h.store, h.verify, AutomationResolvers{Changes: resolve}); err != nil {
		t.Fatal(err)
	}
	if pathEvent(t, h, head).PathDecision != nil {
		t.Fatal("legacy policy gained a path decision")
	}
	pathPolicy(t, h, profile, cfg, false, false)
	head = strings.Repeat("c", 40)
	capturePathPush(t, h, cfg, base, head)
	for _, mode := range []string{"internal", "github", "automation", "dispatch", "worker", "transport", "accounts"} {
		off := cfg
		switch mode {
		case "internal":
			off.Enabled = false
		case "github":
			off.GitHubEnabled = false
		case "automation":
			off.AutomationEnabled = false
		case "dispatch":
			off.DispatchEnabled = false
		case "worker":
			off.WorkerEnabled = false
		case "transport":
			off.TransportEnabled = false
		case "accounts":
			off.accounts = nil
		}
		if worked, err := AdmitAutomationWithResolvers(t.Context(), off, h.store, h.verify, AutomationResolvers{Changes: resolve}); err != nil || worked {
			t.Fatalf("gate %s admitted paths", mode)
		}
	}
}

func TestCIAutomationPathsSkipAndCheckAreAtomicAndConcurrent(t *testing.T) {
	h, profile, cfg := automationFixture(t)
	cfg.ExecutionChecksEnabled, cfg.GitHubChecksEnabled = true, true
	pathPolicy(t, h, profile, cfg, false, false)
	base, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
	capturePathPush(t, h, cfg, base, head)
	resolvers := AutomationResolvers{Changes: resolvePathChanges(pathChanges(base, head, ChangedFile{Path: "docs/readme", Status: "modified"}))}
	if _, err := h.pool.Exec(t.Context(), `CREATE FUNCTION fail_ci_path_check() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic check insert failure'; END $$;
CREATE TRIGGER fail_ci_path_check BEFORE INSERT ON ci_automation_path_checks FOR EACH ROW EXECUTE FUNCTION fail_ci_path_check()`); err != nil {
		t.Fatal(err)
	}
	if _, err := AdmitAutomationWithResolvers(t.Context(), cfg, h.store, h.verify, resolvers); err == nil {
		t.Fatal("failed atomic Check write reported success")
	}
	event := pathEvent(t, h, head)
	if event.PathDecision != nil || event.Status != "pending" || h.count(t, "SELECT count(*) FROM ci_automation_path_checks") != 0 || h.count(t, "SELECT count(*) FROM ci_executions") != 0 {
		t.Fatal("failed outbox write partially settled source")
	}
	if _, err := h.pool.Exec(t.Context(), "DROP TRIGGER fail_ci_path_check ON ci_automation_path_checks; UPDATE ci_automation_events SET next_attempt_at=clock_timestamp()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			_, err := AdmitAutomationWithResolvers(t.Context(), cfg, h.store, h.verify, resolvers)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if pathEvent(t, h, head).LastError != "paths_filtered" || h.count(t, "SELECT count(*) FROM ci_automation_path_checks") != 1 {
		t.Fatal("concurrent admission duplicated or lost source feedback")
	}
	var publications atomic.Int32
	for range 8 {
		wg.Go(func() {
			_, err := PublishExecutionCheckOnce(t.Context(), cfg, h.store, h.verify, func(context.Context, ExecutionCheck) (int64, error) { publications.Add(1); return 77, nil })
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if publications.Load() != 1 || h.count(t, "SELECT count(*) FROM ci_automation_path_checks WHERE status='sent' AND check_id=77") != 1 {
		t.Fatal("concurrent publishers lost feedback")
	}
}

func TestCIAutomationPathsCheckGatesLeaseExpiryAndExhaustion(t *testing.T) {
	h, profile, cfg := automationFixture(t)
	cfg.ExecutionChecksEnabled, cfg.GitHubChecksEnabled = true, true
	pathPolicy(t, h, profile, cfg, false, false)
	base, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
	capturePathPush(t, h, cfg, base, head)
	if _, err := AdmitAutomationWithResolvers(t.Context(), cfg, h.store, h.verify, AutomationResolvers{Changes: resolvePathChanges(pathChanges(base, head, ChangedFile{Path: "docs/readme", Status: "modified"}))}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"execution_checks", "github_checks", "internal", "accounts"} {
		off := cfg
		switch mode {
		case "execution_checks":
			off.ExecutionChecksEnabled = false
		case "github_checks":
			off.GitHubChecksEnabled = false
		case "internal":
			off.Enabled = false
		case "accounts":
			off.accounts = nil
		}
		if worked, err := PublishExecutionCheckOnce(t.Context(), off, h.store, h.verify, func(context.Context, ExecutionCheck) (int64, error) {
			t.Fatal("disabled Check worker published skip")
			return 0, nil
		}); err != nil || worked {
			t.Fatal("disabled publication acquired claim")
		}
	}
	old, err := h.store.claimExecutionCheck(t.Context(), h.account)
	if err != nil || !old.source {
		t.Fatal("skip claim unavailable")
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_automation_path_checks SET lease_until=clock_timestamp()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	newClaim, err := h.store.claimExecutionCheck(t.Context(), h.account)
	if err != nil || newClaim.token == old.token {
		t.Fatal("expired source Check did not re-fence")
	}
	if err := h.store.deliverExecutionCheck(t.Context(), old, h.verify, func(context.Context, ExecutionCheck) (int64, error) {
		t.Fatal("stale lease reached provider")
		return 0, nil
	}); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("stale source publisher retained authority")
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_automation_path_checks SET attempts=$1,lease_until=clock_timestamp()-interval '1 second'", api.CIGitHubMaxAttempts); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.claimExecutionCheck(t.Context(), h.account); !errors.Is(err, ErrNoGitHubWork) {
		t.Fatal("exhausted skip Check was reclaimed")
	}
	if h.count(t, "SELECT count(*) FROM ci_automation_path_checks WHERE status='dead' AND last_error='attempts_exhausted'") != 1 || pathEvent(t, h, head).LastError != "paths_filtered" {
		t.Fatal("publication exhaustion altered source decision")
	}
}

func TestCIAutomationPathsPushSkipPreservesOlderCodeAndCapacity(t *testing.T) {
	h, profile, cfg := automationFixture(t)
	pathPolicy(t, h, profile, cfg, false, true)
	a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	capturePathPush(t, h, cfg, a, b)
	resolvers := AutomationResolvers{Branch: branchHead(b), Changes: resolvePathChanges(pathChanges(a, b, ChangedFile{Path: "src/main.go", Status: "modified"}))}
	if _, err := AdmitAutomationWithResolvers(t.Context(), cfg, h.store, h.verify, resolvers); err != nil {
		t.Fatal(err)
	}
	old := pathEvent(t, h, b)
	// Fill ordinary active capacity. A skip needs no execution slot.
	for i := 1; i < api.CIActiveExecutionsPerAccount; i++ {
		h.admit(t, uuid.NewString())
	}
	capturePathPush(t, h, cfg, b, c)
	resolvers.Branch = branchHead(c)
	resolvers.Changes = resolvePathChanges(pathChanges(b, c, ChangedFile{Path: "docs/readme", Status: "modified"}))
	if _, err := AdmitAutomationWithResolvers(t.Context(), cfg, h.store, h.verify, resolvers); err != nil {
		t.Fatal(err)
	}
	row, err := h.store.Execution(t.Context(), h.account, old.ExecutionID)
	if err != nil || row.CancelRequested || pathEvent(t, h, c).LastError != "paths_filtered" || pathEvent(t, h, b).SupersededBy != "" {
		t.Fatal("incremental skip cancelled older untested code or consumed capacity")
	}
}

func TestCIAutomationPathsLatestPushCannotStrandUnadmittedCode(t *testing.T) {
	for _, exhausted := range []bool{false, true} {
		t.Run(map[bool]string{false: "queued", true: "exhausted"}[exhausted], func(t *testing.T) {
			h, profile, cfg := automationFixture(t)
			pathPolicy(t, h, profile, cfg, false, true)
			a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
			capturePathPush(t, h, cfg, a, b)
			if exhausted {
				if _, err := h.pool.Exec(t.Context(), "UPDATE ci_automation_events SET status='dead',attempts=$1,last_error='unavailable'", api.CIGitHubMaxAttempts); err != nil {
					t.Fatal(err)
				}
			}
			capturePathPush(t, h, cfg, b, c)
			// Put the current head first even though capture ordering differed.
			if _, err := h.pool.Exec(t.Context(), "UPDATE ci_automation_events SET next_attempt_at=clock_timestamp()+interval '1 day' WHERE snapshot->'source'->>'commit_sha'=$1", b); err != nil {
				t.Fatal(err)
			}
			if _, err := AdmitAutomationWithResolvers(t.Context(), cfg, h.store, h.verify, AutomationResolvers{Branch: branchHead(c), Changes: resolvePathChanges(pathChanges(b, c, ChangedFile{Path: "docs/readme", Status: "modified"}))}); err != nil {
				t.Fatal(err)
			}
			event := pathEvent(t, h, c)
			if event.Status != "admitted" || event.PathDecision == nil || event.PathDecision.Outcome != "run" || event.PathDecision.Reason != "prior_source_unsettled" {
				t.Fatalf("docs skip stranded queued code: %+v", event)
			}
		})
	}
}

func TestCIAutomationPathsObsoletePushStillNeedsReplacementCoverage(t *testing.T) {
	for _, missingHead := range []bool{false, true} {
		t.Run(map[bool]string{false: "old_source_first", true: "replacement_delivery_missing"}[missingHead], func(t *testing.T) {
			h, profile, cfg := automationFixture(t)
			pathPolicy(t, h, profile, cfg, false, true)
			a, b, c, d := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40), strings.Repeat("d", 40)
			capturePathPush(t, h, cfg, a, b)
			if !missingHead {
				capturePathPush(t, h, cfg, b, c)
			}
			resolvers := AutomationResolvers{Branch: branchHead(c), Changes: resolvePathChanges(pathChanges(a, b, ChangedFile{Path: "src/main.go", Status: "modified"}))}
			if _, err := AdmitAutomationWithResolvers(t.Context(), cfg, h.store, h.verify, resolvers); err != nil {
				t.Fatal(err)
			}
			if old := pathEvent(t, h, b); old.Status != "ignored" || old.LastError != "head_changed" {
				t.Fatal("older source was not observed obsolete")
			}
			base, head := b, c
			if missingHead {
				base, head = c, d
				capturePathPush(t, h, cfg, base, head)
			}
			resolvers.Branch = branchHead(head)
			resolvers.Changes = resolvePathChanges(pathChanges(base, head, ChangedFile{Path: "docs/readme", Status: "modified"}))
			// Use the production DB clock so the observation/admission ordering is
			// meaningful even when the originally observed replacement never arrived.
			h.store.executionClock = nil
			if _, err := AdmitAutomationWithResolvers(t.Context(), cfg, h.store, h.verify, resolvers); err != nil {
				t.Fatal(err)
			}
			covered := pathEvent(t, h, head)
			if covered.Status != "admitted" || covered.PathDecision.Reason != "prior_source_unsettled" {
				t.Fatalf("obsolete source was left untested: %+v", covered)
			}
			next := strings.Repeat("e", 40)
			capturePathPush(t, h, cfg, head, next)
			resolvers.Branch = branchHead(next)
			resolvers.Changes = resolvePathChanges(pathChanges(head, next, ChangedFile{Path: "docs/next", Status: "modified"}))
			if _, err := AdmitAutomationWithResolvers(t.Context(), cfg, h.store, h.verify, resolvers); err != nil {
				t.Fatal(err)
			}
			if event := pathEvent(t, h, next); event.LastError != "paths_filtered" {
				t.Fatal("covered obsolete source permanently disabled filtering")
			}
			row, err := h.store.Execution(t.Context(), h.account, covered.ExecutionID)
			if err != nil || row.CancelRequested {
				t.Fatal("subsequent docs-only skip cancelled code coverage")
			}
		})
	}
}

func TestCIAutomationPathsPullRequestCumulativeAndSupersession(t *testing.T) {
	h, profile, cfg := pullRequestFixture(t)
	pathPolicy(t, h, profile, cfg, true, false)
	base, a, b, merge := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40), strings.Repeat("d", 40)
	capturePullRequest(t, h, cfg, 7, a)
	state := currentPullRequest(h, 7, a)
	state.BaseSHA = base
	changes := pathChanges(base, a, ChangedFile{Path: "src/main.go", Status: "modified"})
	changes.MergeBaseSHA = merge
	resolvers := AutomationResolvers{PullRequest: pullRequestResolver(state), Changes: resolvePathChanges(changes)}
	if _, err := AdmitAutomationWithResolvers(t.Context(), cfg, h.store, h.verify, resolvers); err != nil {
		t.Fatal(err)
	}
	old := pathEvent(t, h, a)
	if old.PathDecision.Reason != "paths_matched" || old.PathDecision.MergeBaseSHA != merge {
		t.Fatal("PR did not use pinned cumulative comparison")
	}
	capturePullRequest(t, h, cfg, 7, b)
	state.HeadSHA = b
	changes = pathChanges(base, b, ChangedFile{Path: "docs/readme", Status: "modified"})
	changes.MergeBaseSHA = merge
	resolvers.PullRequest = pullRequestResolver(state)
	resolvers.Changes = resolvePathChanges(changes)
	if _, err := AdmitAutomationWithResolvers(t.Context(), cfg, h.store, h.verify, resolvers); err != nil {
		t.Fatal(err)
	}
	row, err := h.store.Execution(t.Context(), h.account, old.ExecutionID)
	if err != nil || !row.CancelRequested || pathEvent(t, h, b).Status != "pending" {
		t.Fatal("filtered PR bypassed normal supersession/drain")
	}
	row = h.tick(t, row)
	if !terminal(row.Status) {
		t.Fatal("queued superseded execution did not settle")
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_automation_events SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE status='pending'"); err != nil {
		t.Fatal(err)
	}
	if _, err := AdmitAutomationWithResolvers(t.Context(), cfg, h.store, h.verify, resolvers); err != nil {
		t.Fatal(err)
	}
	if event := pathEvent(t, h, b); event.Status != "ignored" || event.LastError != "paths_filtered" || event.PathDecision.MergeBaseSHA != merge {
		t.Fatalf("PR skip lost cumulative decision: %+v", event)
	}
	if h.count(t, "SELECT count(*) FROM ci_executions") != 1 {
		t.Fatal("filtered PR created a compute execution")
	}
}

func TestCIAutomationPathsChangesCannotOverrideAuthorityOrFreshHead(t *testing.T) {
	for _, mode := range []string{"policy", "profile", "binding", "image", "account", "push_fence", "pr_fence", "closed_pr", "changed_pr", "missing_pr_base"} {
		t.Run(mode, func(t *testing.T) {
			pr := strings.HasPrefix(mode, "pr_") || strings.HasSuffix(mode, "_pr") || mode == "missing_pr_base"
			var h *jobsHarness
			var profile ExecutionProfile
			var cfg Config
			if pr {
				h, profile, cfg = pullRequestFixture(t)
			} else {
				h, profile, cfg = automationFixture(t)
			}
			pathPolicy(t, h, profile, cfg, pr, true)
			base, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
			if pr {
				capturePullRequest(t, h, cfg, 7, head)
			} else {
				capturePathPush(t, h, cfg, base, head)
			}
			state := currentPullRequest(h, 7, head)
			state.BaseSHA = base
			if mode == "closed_pr" {
				state.State = "closed"
			}
			if mode == "changed_pr" {
				state.HeadSHA = strings.Repeat("c", 40)
			}
			if mode == "missing_pr_base" {
				state.BaseSHA = ""
			}
			resolve := func(ctx context.Context, _ GitHubBinding, _, _ string) (ChangedFiles, error) {
				var err error
				switch mode {
				case "policy":
					_, err = h.store.DisableAutomationPolicy(ctx, h.account, h.binding.ID)
				case "profile":
					_, err = h.store.DisableExecutionProfile(ctx, h.account, profile.ID)
				case "binding":
					err = h.store.DisableGitHubBinding(ctx, h.account, h.binding.Name)
				case "image":
					_, err = h.pool.Exec(ctx, "UPDATE jobs SET image_storage_key='replacement/image.ext4' WHERE id=$1", h.req.Templates["test"])
				case "account":
					_, err = h.pool.Exec(ctx, "UPDATE accounts SET status='suspended' WHERE id=$1", h.account)
				case "push_fence":
					err = RecordVerifiedGitHubDelivery(ctx, cfg, h.store, uuid.NewString(), "push", automationDelivery(h, strings.Repeat("c", 40), "main"))
				case "pr_fence":
					raw, _ := json.Marshal(pullRequestDelivery(h, 7, strings.Repeat("c", 40), "main", "synchronize"))
					err = RecordVerifiedGitHubDelivery(ctx, cfg, h.store, uuid.NewString(), "pull_request", raw)
				default:
					t.Fatal("ineligible PR or missing base reached comparison provider")
				}
				if err != nil {
					t.Fatal(err)
				}
				return pathChanges(base, head, ChangedFile{Path: "docs/README", Status: "modified"}), nil
			}
			if _, err := AdmitAutomationWithResolvers(t.Context(), cfg, h.store, h.verify, AutomationResolvers{Branch: branchHead(head), PullRequest: pullRequestResolver(state), Changes: resolve}); err != nil {
				t.Fatal(err)
			}
			event := pathEvent(t, h, head)
			if mode == "missing_pr_base" {
				if event.Status != "admitted" || event.PathDecision.Reason != "changes_unavailable" {
					t.Fatal("missing PR comparison pin skipped CI")
				}
			} else if event.LastError == "paths_filtered" || event.Status == "admitted" || event.PathDecision != nil {
				t.Fatalf("stale authority committed path decision: %+v", event)
			}
		})
	}
}
