// ADR-1654 / ADR-1655 / ADR-1656: authenticated admission and worker recovery.
package apidserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/state"
)

type ciServiceGitHub struct{ apidserver.StubGithubdClient }

func (ciServiceGitHub) ListInstallableRepos(context.Context, string, int64) ([]apidserver.Repo, error) {
	return []apidserver.Repo{{ID: 34, FullName: "gregale/service", DefaultBranch: "main"}}, nil
}
func (ciServiceGitHub) GetBranchHead(context.Context, string, int64, string, string) (string, bool, error) {
	return strings.Repeat("a", 40), true, nil
}

func (ciServiceGitHub) GetCIPullRequest(_ context.Context, _ string, installation int64, repository string, number int64) (ci.PullRequestState, error) {
	if installation != 12 || repository != "gregale/service" || number != 9 {
		return ci.PullRequestState{}, ci.ErrGitHubAccess
	}
	return ci.PullRequestState{Number: number, State: "open", HeadSHA: strings.Repeat("a", 40), BaseSHA: strings.Repeat("b", 40), BaseBranch: "main", HeadRepositoryID: 34, BaseRepositoryID: 34, HeadRepository: repository, BaseRepository: repository}, nil
}
func (ciServiceGitHub) GetCIChangedFiles(_ context.Context, _ string, installation int64, repository, base, head string) (ci.ChangedFiles, error) {
	if installation != 12 || repository != "gregale/service" || base != strings.Repeat("b", 40) || head != strings.Repeat("a", 40) {
		return ci.ChangedFiles{}, ci.ErrGitHubAccess
	}
	return ci.ChangedFiles{BaseSHA: base, HeadSHA: head, MergeBaseSHA: base, Complete: true, Files: []ci.ChangedFile{{Path: "docs/README.md", Status: "modified"}}}, nil
}
func (ciServiceGitHub) StreamSourceRef(_ context.Context, _ string, _ int64, _ string, sha string, _ int64) (*apidserver.StreamSourceRefResult, error) {
	stats := &apidserver.StreamSourceRefStats{}
	return &apidserver.StreamSourceRefResult{Body: &ciServiceStream{Reader: strings.NewReader("opaque synthetic archive, never extracted"), receipt: func() { stats.ResolvedCommitSHA = sha }}, Stats: stats}, nil
}

type ciServiceStream struct {
	io.Reader
	receipt func()
	once    sync.Once
}

func (b *ciServiceStream) Close() error { b.once.Do(b.receipt); return nil }

func TestInternalCIExecutionServiceWorkerAndHTTPSGates(t *testing.T) {
	testInternalCIService(t, false, false, false)
}

func TestInternalCIAutomationWorkerAndHTTPSGates(t *testing.T) {
	testInternalCIService(t, true, false, false)
}

func TestInternalCIPullRequestWorkerAndHTTPSGates(t *testing.T) {
	testInternalCIService(t, true, true, false)
}

func TestInternalCIPathFilteredPullRequestWorkerAndHTTPS(t *testing.T) {
	testInternalCIService(t, true, true, true)
}

func testInternalCIService(t *testing.T, automatic, pullRequest, filtered bool) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	store := (*e.Store()).(*state.PgStore)
	pool := *e.Pool()
	account := e.Acct().ID
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := apidserver.NewServerWithDeps(store, log, "gregale.dev", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, ciServiceGitHub{}, nil, nil, 0, "")
	image := "registry.example/ci@sha256:" + strings.Repeat("a", 64)
	job, err := store.JobCreate(t.Context(), account, "ci-service", "batch", image, []string{ci.JobsRunnerPath, ci.JobsRunnerProtocol}, api.CIJobsRAMMB, 600, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE jobs SET image_materialization_status='ready',image_resolved_digest=$2,image_storage_key='private/runner.ext4' WHERE id=$1", job.ID, strings.Split(image, "@")[1]); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertGitHubInstall(t.Context(), state.GitHubInstall{AccountID: account, InstallationID: 12, DefaultBranch: "main", AuditGithubLogin: "gregale-ci-fixture", SealedToken: []byte("synthetic-seal-not-a-credential"), TokenExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	intent := ci.NewPostgresStore(pool)
	revision, err := intent.PutRevision(t.Context(), account, "service", ci.Definition{Version: 1, Repository: "gregale/service", Jobs: map[string]ci.Job{"test": {Image: image, Steps: []ci.Step{{Name: "unit", Run: "synthetic, never executed"}}, Artifacts: []string{"reports/**"}, JUnit: []string{"reports/unit.xml"}}}})
	if err != nil {
		t.Fatal(err)
	}
	bindingReq := ci.GitHubBindingRequest{RevisionID: revision.ID, InstallationID: 12, RepositoryID: 34, PushBranches: []string{"main"}}
	if pullRequest {
		bindingReq.PullRequestBranches = []string{"main"}
	}
	binding, err := intent.PutGitHubBinding(t.Context(), account, "service", bindingReq)
	if err != nil {
		t.Fatal(err)
	}
	cfg := ci.ConfigFromEnv(func(key string) string {
		if key == ci.AccountsEnv {
			return account
		}
		switch key {
		case ci.InternalEnv, ci.GitHubEnv, ci.DispatchEnv, ci.TransportEnv, ci.WorkerEnv, ci.GitHubChecksEnv, ci.ExecutionChecksEnv:
			return "1"
		}
		return ""
	})
	cfg.AutomationEnabled = automatic
	blobs := &ci.ManagedBlobs{Store: intent, Physical: &apiCIPhysical{data: map[string][]byte{}, retired: map[string]bool{}}}
	tlsServer := httptest.NewUnstartedServer(nil)
	origin := "https://" + tlsServer.Listener.Addr().String()
	server.WithInternalCIConfig(cfg).WithInternalCIStorage(blobs, origin)
	handler := server.Handler()
	tlsServer.Config.Handler = handler
	tlsServer.StartTLS()
	defer tlsServer.Close()
	request := ci.JobsSubmitRequest{SubmitRequest: ci.SubmitRequest{RevisionID: revision.ID, CommitSHA: strings.Repeat("a", 40)}, BindingID: binding.ID, Templates: map[string]string{"test": job.ID}}
	do := func(method, path, key string, body []byte, out any) int {
		t.Helper()
		r, err := http.NewRequestWithContext(t.Context(), method, origin+"/v1/internal/ci/"+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+*e.Key())
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		response, err := tlsServer.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("execution response is cacheable")
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 && out != nil {
			if err := json.NewDecoder(response.Body).Decode(out); err != nil {
				t.Fatal(err)
			}
		}
		return response.StatusCode
	}
	profileRequest := ci.ExecutionProfileRequest{RevisionID: revision.ID, BindingID: binding.ID, Templates: request.Templates}
	profileRaw, _ := json.Marshal(profileRequest)
	var profile ci.ExecutionProfile
	if got := do(http.MethodPut, "profiles/service", "", profileRaw, &profile); got != http.StatusOK {
		t.Fatalf("HTTP profile setup: %d", got)
	}
	if got := do(http.MethodGet, "profiles/"+profile.ID, "", nil, nil); got != http.StatusOK {
		t.Fatalf("HTTP profile read: %d", got)
	}
	var row ci.Execution
	var stop func()
	if automatic {
		policy := ci.AutomationRequest{ProfileID: profile.ID, PushBranches: []string{"main"}, LatestOnly: true}
		policy.IncludePaths = []string{"**"}
		if filtered {
			policy.IncludePaths = []string{"src/**"}
		}
		if pullRequest {
			policy.PushBranches, policy.PullRequestBranches, policy.LatestOnly = nil, []string{"main"}, false
		}
		policyRaw, _ := json.Marshal(policy)
		if got := do(http.MethodPut, "github/automation/"+binding.ID, "", policyRaw, nil); got != http.StatusOK {
			t.Fatalf("HTTP automation setup: %d", got)
		}
		body := []byte(`{"ref":"refs/heads/main","before":"` + strings.Repeat("b", 40) + `","after":"` + request.CommitSHA + `","installation":{"id":12},"repository":{"id":34,"full_name":"gregale/service"}}`)
		kind := "push"
		if pullRequest {
			kind = "pull_request"
			body = []byte(`{"action":"opened","number":9,"installation":{"id":12},"repository":{"id":34,"full_name":"gregale/service"},"pull_request":{"state":"open","draft":false,"head":{"sha":"` + request.CommitSHA + `","repo":{"id":34,"full_name":"gregale/service"}},"base":{"ref":"main","repo":{"id":34,"full_name":"gregale/service"}}}}`)
		}
		if err := ci.RecordVerifiedGitHubDelivery(t.Context(), cfg, intent, uuid.NewString(), kind, body); err != nil {
			t.Fatal(err)
		}
		// ADR-1580: recover the original source over authenticated HTTPS before
		// the normal worker can create its single execution and Jobs intent.
		var eventID string
		if err := pool.QueryRow(t.Context(), "UPDATE ci_automation_events SET status='dead',attempts=$2,last_error='unavailable' WHERE account_id=$1 RETURNING id", account, api.CIGitHubMaxAttempts).Scan(&eventID); err != nil {
			t.Fatal(err)
		}
		eventPath := "github/automation/" + binding.ID + "/events/" + eventID
		var event ci.AutomationEventSummary
		if got := do(http.MethodGet, eventPath, "", nil, &event); got != http.StatusOK || !event.Retryable {
			t.Fatalf("authenticated source diagnostics: %d %+v", got, event)
		}
		for range 2 {
			var retry ci.AutomationRetry
			if got := do(http.MethodPost, eventPath+"/retry", "service-automation-retry", []byte(`{"expected_retry_generation":0}`), &retry); got != http.StatusAccepted || retry.EventID != eventID || retry.Generation != 1 {
				t.Fatalf("authenticated source recovery: %d %+v", got, retry)
			}
		}
		var retryCount int
		if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM ci_automation_event_retries").Scan(&retryCount); err != nil || retryCount != 1 {
			t.Fatal("HTTPS response retry duplicated source recovery")
		}
		stop = server.StartInternalCIWorker(t.Context(), log)
		defer stop()
		until := time.Now().Add(10 * time.Second)
		for row.ID == "" {
			events, err := intent.AutomationEvents(t.Context(), account, binding.ID)
			if err != nil || len(events) != 1 {
				t.Fatalf("automation history: %v", err)
			}
			if filtered && events[0].LastError == "paths_filtered" {
				stop()
				if got := do(http.MethodGet, eventPath, "", nil, &event); got != http.StatusOK || event.PathDecision == nil || event.PathDecision.Outcome != "skip" || event.Retryable || event.ExecutionID != "" {
					t.Fatalf("HTTPS skipped source diagnostics: %d %+v", got, event)
				}
				if got := do(http.MethodPost, eventPath+"/retry", "must-not-promote-filtered-source", []byte(`{"expected_retry_generation":1}`), nil); got != http.StatusConflict {
					t.Fatalf("HTTPS recovery promoted skipped source: %d", got)
				}
				var executions, runs, instances int
				if err := pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM ci_executions),(SELECT count(*) FROM job_runs),(SELECT count(*) FROM instances)").Scan(&executions, &runs, &instances); err != nil || executions != 0 || runs != 0 || instances != 0 {
					t.Fatal("skipped source allocated compute")
				}
				checkPath := eventPath + "/check"
				var feedback ci.SourceCheckDelivery
				if got := do(http.MethodGet, checkPath, "", nil, &feedback); got != http.StatusOK || feedback.DeliveryStatus != "pending" {
					t.Fatalf("HTTPS source Check status: %d %+v", got, feedback)
				}
				if _, err := pool.Exec(t.Context(), "UPDATE ci_automation_path_checks SET status='dead',attempts=8,last_error='unavailable',check_id=77 WHERE event_id=$1", event.ID); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					var retry ci.SourceCheckRetry
					if got := do(http.MethodPost, checkPath+"/retry", "source-check-response-loss", []byte(`{"expected_retry_generation":0}`), &retry); got != http.StatusAccepted || retry.EventID != event.ID || retry.Generation != 1 {
						t.Fatalf("HTTPS source Check retry: %d %+v", got, retry)
					}
				}
				if got := do(http.MethodGet, eventPath, "", nil, &event); got != http.StatusOK || event.CheckDelivery == nil || !event.CheckDelivery.RetryPending || event.Retryable || event.ExecutionID != "" {
					t.Fatal("source retry changed skip decision or lost pending feedback")
				}
				if worked, err := ci.PublishExecutionCheckOnce(t.Context(), cfg, intent, func(context.Context, string, int64, int64, string) error { return nil }, func(_ context.Context, check ci.ExecutionCheck) (int64, error) {
					if check.AutomationEventID != event.ID || check.ExecutionID != "" || check.Conclusion != "skipped" || check.CheckID != 77 || check.ExternalID() != "gregale-ci-source:"+event.ID {
						t.Fatal("skip reported a native execution")
					}
					return 77, nil
				}); err != nil || !worked {
					t.Fatal("skip Check not queued atomically")
				}
				if got := do(http.MethodGet, checkPath, "", nil, &feedback); got != http.StatusOK || feedback.DeliveryStatus != "sent" || feedback.RetryGeneration != 1 || feedback.LastRetry == nil || feedback.LastRetry.AppliedAt == nil {
					t.Fatalf("HTTPS source Check application: %d %+v", got, feedback)
				}
				var receipts int
				if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM ci_source_check_retries").Scan(&receipts); err != nil || receipts != 1 {
					t.Fatal("HTTPS duplicate retry created multiple source receipts")
				}
				if err := pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM ci_executions),(SELECT count(*) FROM job_runs),(SELECT count(*) FROM instances)").Scan(&executions, &runs, &instances); err != nil || executions != 0 || runs != 0 || instances != 0 {
					t.Fatal("source Check recovery allocated compute")
				}
				return
			}
			if events[0].ExecutionID != "" {
				if got := do(http.MethodGet, "executions/"+events[0].ExecutionID, "", nil, &row); got != http.StatusOK {
					t.Fatalf("automated execution read: %d", got)
				}
				if pullRequest && (row.Automation == nil || row.Automation.Kind != "pull_request" || row.Automation.PRNumber != 9) {
					t.Fatal("worker lost freshly verified PR provenance")
				}
			}
			if time.Now().After(until) {
				t.Fatal("service did not admit captured automation")
			}
			time.Sleep(20 * time.Millisecond)
		}
		if row.Automation == nil {
			t.Fatal("automatic service admission lost provenance")
		}
		if got := do(http.MethodDelete, "github/automation/"+binding.ID, "", nil, nil); got != http.StatusOK {
			t.Fatal("HTTP automation disable failed")
		}
	} else {
		raw, _ := json.Marshal(ci.JobsSubmitRequest{ProfileID: profile.ID, SubmitRequest: ci.SubmitRequest{CommitSHA: request.CommitSHA}})
		if got := do(http.MethodPost, "executions", "service-fresh", raw, &row); got != http.StatusAccepted {
			t.Fatalf("HTTP admission: %d", got)
		}
		stop = server.StartInternalCIWorker(t.Context(), log)
		defer stop()
	}
	if row.Compute != nil {
		t.Fatal("HTTP response exposed internal native image keys")
	}
	if row.ProfileID != profile.ID {
		t.Fatal("HTTP execution lost saved profile identity")
	}
	if automatic {
		if got := do(http.MethodDelete, "profiles/"+profile.ID, "", nil, nil); got != http.StatusOK {
			t.Fatalf("HTTP profile disable: %d", got)
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		current, err := intent.Execution(t.Context(), account, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		var runs int
		if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM ci_job_attempts WHERE execution_id=$1 AND run_id IS NOT NULL", row.ID).Scan(&runs); err != nil {
			t.Fatal(err)
		}
		if runs == 1 && current.Jobs[0].Attempt != nil {
			row = current
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("service worker did not dispatch its durable intent")
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop() // Restart recovery must reuse the same attempt/run.
	stop = server.StartInternalCIWorker(t.Context(), log)
	defer stop()
	if got := do(http.MethodPost, "executions/"+row.ID+"/cancel", "", nil, nil); got != http.StatusOK {
		t.Fatalf("cancel: %d", got)
	}
	// Stopping during a tick intentionally retains its 30-second fenced lease.
	// Cancellation must recover the same attempt after that lease can expire.
	deadline = time.Now().Add(time.Duration(api.CIExecutionLeaseSeconds+10) * time.Second)
	for {
		var current ci.Execution
		if got := do(http.MethodGet, "executions/"+row.ID, "", nil, &current); got != http.StatusOK {
			t.Fatalf("status: %d", got)
		}
		if current.Status == ci.StatusCancelled {
			if current.Jobs[0].Attempt.ID != row.Jobs[0].Attempt.ID {
				t.Fatal("restart replaced attempt")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("service worker did not drain cancellation after lease recovery: status=%s", current.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	var runs, instances int
	if err := pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM job_runs),(SELECT count(*) FROM instances)").Scan(&runs, &instances); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || instances != 0 {
		t.Fatal("service duplicated intent or allocated native compute in portable test")
	}
	if !automatic {
		stop()
		var rerun ci.Execution
		for range 2 {
			var current ci.Execution
			if got := do(http.MethodPost, "executions/"+row.ID+"/rerun", "service-execution-rerun", []byte(`{}`), &current); got != http.StatusAccepted || current.Rerun == nil || current.Rerun.ParentExecutionID != row.ID || current.Rerun.RootExecutionID != row.ID || current.Compute != nil {
				t.Fatalf("HTTPS execution rerun: %d", got)
			}
			if rerun.ID != "" && rerun.ID != current.ID {
				t.Fatal("HTTPS response retry duplicated a rerun")
			}
			rerun = current
		}
		if got := do(http.MethodPost, "executions/"+rerun.ID+"/cancel", "", nil, nil); got != http.StatusOK {
			t.Fatalf("cancel queued rerun: %d", got)
		}
		if got := do(http.MethodDelete, "profiles/"+profile.ID, "", nil, nil); got != http.StatusOK {
			t.Fatalf("HTTP profile disable after rerun: %d", got)
		}
	}
	var reports ci.ExecutionTestReports
	if got := do(http.MethodGet, "executions/"+row.ID+"/reports", "", nil, &reports); got != http.StatusOK || reports.ExecutionID != row.ID || reports.Totals.Expected != 1 || reports.Totals.Problems != 1 || len(reports.Jobs) != 1 || reports.Jobs[0].Reports[0].Status != ci.ReportMissing {
		t.Fatalf("HTTPS report diagnostics: %d %+v", got, reports)
	}
	if got := do(http.MethodGet, "checks/"+row.ID, "", nil, nil); got != http.StatusOK {
		t.Fatalf("delivery diagnostics: %d", got)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE ci_execution_checks SET status='dead',attempts=$2,last_error='unavailable' WHERE execution_id=$1", row.ID, api.CIGitHubMaxAttempts); err != nil {
		t.Fatal(err)
	}
	retryRaw := []byte(`{"expected_retry_generation":0}`)
	var retry ci.CheckRetry
	for range 2 {
		if got := do(http.MethodPost, "checks/"+row.ID+"/retry", "service-check-retry", retryRaw, &retry); got != http.StatusAccepted || retry.Generation != 1 {
			t.Fatalf("authenticated retry: %d", got)
		}
	}
	var retryCount int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM ci_execution_check_retries").Scan(&retryCount); err != nil || retryCount != 1 {
		t.Fatal("HTTP retry repeated audit intent")
	}
	stop()
	for range 2 {
		if _, err := ci.PublishExecutionCheckOnce(t.Context(), cfg, intent, func(context.Context, string, int64, int64, string) error { return nil }, func(_ context.Context, check ci.ExecutionCheck) (int64, error) {
			if check.ExecutionID == row.ID {
				return 77, nil
			}
			return 78, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if got := do(http.MethodPost, "executions/"+row.ID+"/archive", "", nil, nil); got != http.StatusConflict {
		t.Fatalf("ready source allowed archive: %d", got)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE ci_managed_objects SET expires_at=created_at+interval '1 microsecond' WHERE execution_id=$1", row.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := blobs.Sweep(t.Context()); err != nil || n != 1 {
		t.Fatalf("queued-cancel source retirement: %d %v", n, err)
	}
	var archived ci.ExecutionArchive
	for range 2 {
		if got := do(http.MethodPost, "executions/"+row.ID+"/archive", "", nil, &archived); got != http.StatusOK || archived.ExecutionID != row.ID {
			t.Fatalf("authenticated archive: %d", got)
		}
	}
	if got := do(http.MethodGet, "archives/"+row.ID, "", nil, nil); got != http.StatusOK {
		t.Fatalf("archive receipt: %d", got)
	}
	if got := do(http.MethodGet, "executions/"+row.ID, "", nil, nil); got != http.StatusGone {
		t.Fatalf("compacted detail: %d", got)
	}
	// Untrusted forwarded headers cannot make a remote plaintext request secure.
	protected := "executions"
	if automatic {
		protected = "github/automation"
	}
	r := httptest.NewRequest(http.MethodGet, origin+"/v1/internal/ci/"+protected, nil)
	r.TLS = nil
	r.RemoteAddr = "203.0.113.10:5555"
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("Authorization", "Bearer "+*e.Key())
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatal("remote plaintext caller spoofed execution HTTPS")
	}
	r = httptest.NewRequest(http.MethodGet, origin+"/v1/internal/ci/"+protected, nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatal("execution route omitted account authentication")
	}
	readKey, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(t.Context(), account, hash, "ci-read-only", []string{"apps:read"}); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest(http.MethodGet, origin+"/v1/internal/ci/"+protected, nil)
	r.Header.Set("Authorization", "Bearer "+readKey)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal("execution route omitted admin scope")
	}
	testInternalCICheckHTTPGuards(t, handler, origin, *e.Key(), readKey, row.ID)
	var health struct {
		Health ci.Health `json:"health"`
	}
	if got := do(http.MethodGet, "health", "", nil, &health); got != http.StatusOK || len(health.Health.Queues) != 7 || len(health.Health.Workers) != 6 {
		t.Fatalf("CI health route: %d %+v", got, health)
	}
	cfg.Enabled = false
	server.WithInternalCIConfig(cfg)
	if got := do(http.MethodGet, protected, "", nil, nil); got != http.StatusNotFound {
		t.Fatal("disabled internal access exposed executions")
	}
}

func testInternalCICheckHTTPGuards(t *testing.T, handler http.Handler, origin, key, readKey, id string) {
	t.Helper()
	for routeIndex, route := range []struct{ method, path string }{{http.MethodGet, "github/automation/" + id + "/events/" + id}, {http.MethodPost, "github/automation/" + id + "/events/" + id + "/retry"}, {http.MethodGet, "github/automation/" + id + "/checks"}, {http.MethodGet, "github/automation/" + id + "/events/" + id + "/check"}, {http.MethodPost, "github/automation/" + id + "/events/" + id + "/check/retry"}, {http.MethodGet, "executions/" + id + "/reports"}, {http.MethodGet, "health"}, {http.MethodGet, "checks"}, {http.MethodGet, "checks/" + id}, {http.MethodPost, "checks/" + id + "/retry"}, {http.MethodGet, "archives"}, {http.MethodGet, "archives/" + id}, {http.MethodPost, "executions/" + id + "/archive"}, {http.MethodPost, "executions/" + id + "/rerun"}} {
		for modeIndex, mode := range []string{"plaintext", "anonymous", "read_scope", "mfa_pending"} {
			r := httptest.NewRequest(route.method, origin+"/v1/internal/ci/"+route.path, strings.NewReader(`{"expected_retry_generation":0}`))
			// Isolate the route guards from the shared unauthenticated IP budget.
			r.RemoteAddr = fmt.Sprintf("203.0.113.%d:5555", 20+routeIndex*4+modeIndex)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Idempotency-Key", "guarded-retry")
			want := http.StatusNotFound
			switch mode {
			case "plaintext":
				r.TLS = nil
				r.RemoteAddr = "203.0.113.10:5555"
				r.Header.Set("X-Forwarded-Proto", "https")
				r.Header.Set("Authorization", "Bearer "+key)
			case "anonymous":
				want = http.StatusUnauthorized
			case "mfa_pending":
				r = r.WithContext(middleware.WithMFAPending(r.Context(), true))
				r.Header.Set("Authorization", "Bearer "+key)
				want = http.StatusForbidden
			case "read_scope":
				r.Header.Set("Authorization", "Bearer "+readKey)
				want = http.StatusForbidden
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if mode == "mfa_pending" && !strings.Contains(w.Body.String(), api.CodeMFARequired) {
				t.Fatal("protected CI route bypassed pending MFA")
			}
			if w.Code != want {
				t.Fatalf("Check route %s %s guard %s: got %d want %d", route.method, route.path, mode, w.Code, want)
			}
		}
	}
}
