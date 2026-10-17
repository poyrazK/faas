// ADR-1656: only App-signed HTTP ingress can capture automated execution intent.
package githubd

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestCIAutomationSignedHTTPPushCapturesExactlyOneExecution(t *testing.T) {
	testCIAutomationSignedSource(t, false)
}

func TestCIAutomationSignedHTTPPullRequestCapturesExactlyOneExecution(t *testing.T) {
	testCIAutomationSignedSource(t, true)
}

func testCIAutomationSignedSource(t *testing.T, pullRequest bool) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	store := ci.NewPostgresStore(pool)
	account, jobID := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(t.Context(), "INSERT INTO accounts(id,email,plan) VALUES($1,$2,'pro')", account, account+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	image := "registry.example/ci@sha256:" + strings.Repeat("a", 64)
	if _, err := pool.Exec(t.Context(), `INSERT INTO jobs(id,account_id,kind,name,image_ref,ram_mb,task_timeout_s,max_parallelism,retry_max,env_overrides,command,image_materialization_status,image_resolved_digest,image_storage_key)
 VALUES($1,$2,'batch','signed-ci',$3,$4,600,1,0,'{}',$5,'ready',$6,'private/signed.ext4')`, jobID, account, image, api.CIJobsRAMMB, []string{ci.JobsRunnerPath, ci.JobsRunnerProtocol}, strings.Split(image, "@")[1]); err != nil {
		t.Fatal(err)
	}
	revision, err := store.PutRevision(t.Context(), account, "checks", ci.Definition{Version: 1, Repository: "gregale/ci", Jobs: map[string]ci.Job{"test": {Image: image, Steps: []ci.Step{{Name: "unit", Run: "synthetic, never executed"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	bindingRequest := ci.GitHubBindingRequest{RevisionID: revision.ID, InstallationID: 12, RepositoryID: 34, PushBranches: []string{"main"}}
	if pullRequest {
		bindingRequest.PullRequestBranches = []string{"main"}
	}
	binding, err := store.PutGitHubBinding(t.Context(), account, "source", bindingRequest)
	if err != nil {
		t.Fatal(err)
	}
	cfg := ci.ConfigFromEnv(func(key string) string {
		if key == ci.AccountsEnv {
			return account
		}
		switch key {
		case ci.InternalEnv, ci.GitHubEnv, ci.AutomationEnv, ci.DispatchEnv, ci.WorkerEnv, ci.TransportEnv, ci.GitHubChecksEnv, ci.ExecutionChecksEnv:
			return "1"
		}
		return ""
	})
	verify := func(_ context.Context, owner string, installation, repository int64, name string) error {
		if owner != account || installation != 12 || repository != 34 || name != "gregale/ci" {
			return ci.ErrGitHubAccess
		}
		return nil
	}
	profile, err := ci.SaveExecutionProfile(t.Context(), cfg, store, verify, account, "checks", ci.ExecutionProfileRequest{RevisionID: revision.ID, BindingID: binding.ID, Templates: map[string]string{"test": jobID}})
	if err != nil {
		t.Fatal(err)
	}
	policy := ci.AutomationRequest{ProfileID: profile.ID, PushBranches: []string{"main"}}
	if pullRequest {
		policy.PushBranches, policy.PullRequestBranches = nil, []string{"main"}
	}
	if _, err := ci.SaveAutomationPolicy(t.Context(), cfg, store, verify, account, binding.ID, policy); err != nil {
		t.Fatal(err)
	}
	secret := []byte("synthetic app webhook secret")
	body := []byte(`{"ref":"refs/heads/main","after":"` + strings.Repeat("b", 40) + `","installation":{"id":12},"repository":{"id":34,"full_name":"gregale/ci"}}`)
	kind := "push"
	if pullRequest {
		kind = "pull_request"
		body = []byte(`{"action":"opened","number":9,"installation":{"id":12},"repository":{"id":34,"full_name":"gregale/ci"},"pull_request":{"state":"open","draft":false,"head":{"sha":"` + strings.Repeat("b", 40) + `","repo":{"id":34,"full_name":"gregale/ci"}},"base":{"ref":"main","repo":{"id":34,"full_name":"gregale/ci"}}}}`)
	}
	srv := &Server{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), WebhookSecret: secret, Deliveries: &recordingDeliveryStore{}, RecordCI: func(ctx context.Context, d WebhookDelivery) error {
		return ci.RecordVerifiedGitHubDelivery(ctx, cfg, store, d.DeliveryID, d.EventType, d.Payload)
	}}
	for _, signed := range []bool{false, true, true, true} {
		r := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(string(body)))
		r.Header.Set("X-GitHub-Delivery", uuid.NewString())
		r.Header.Set("X-GitHub-Event", kind)
		if signed {
			r.Header.Set("X-Hub-Signature-256", SignForTest(body, secret))
		}
		w := httptest.NewRecorder()
		srv.WebhookLoopbackHandler().ServeHTTP(w, r)
		if signed && w.Code != http.StatusAccepted || !signed && w.Code < http.StatusBadRequest {
			t.Fatalf("App authentication boundary: signed=%v status=%d", signed, w.Code)
		}
		if !signed {
			events, err := store.AutomationEvents(t.Context(), account, binding.ID)
			if err != nil || len(events) != 0 {
				t.Fatal("unsigned source entered the automation journal")
			}
		}
	}
	resolvers := ci.AutomationResolvers{PullRequest: func(_ context.Context, b ci.GitHubBinding, number int) (ci.PullRequestState, error) {
		return ci.PullRequestState{Number: int64(number), State: "open", HeadSHA: strings.Repeat("b", 40), BaseBranch: "main", HeadRepositoryID: b.RepositoryID, BaseRepositoryID: b.RepositoryID, HeadRepository: b.Repository, BaseRepository: b.Repository}, nil
	}}
	if worked, err := ci.AdmitAutomationWithResolvers(t.Context(), cfg, store, verify, resolvers); err != nil || !worked {
		t.Fatalf("signed source admission: %v", err)
	}
	events, err := store.AutomationEvents(t.Context(), account, binding.ID)
	if err != nil || len(events) != 1 || events[0].Status != "admitted" {
		t.Fatal("signed redelivery created duplicate automatic execution")
	}
	var executions, nativeRuns, checks int
	if err := pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM ci_executions),(SELECT count(*) FROM job_runs),(SELECT count(*) FROM ci_github_checks)").Scan(&executions, &nativeRuns, &checks); err != nil {
		t.Fatal(err)
	}
	if executions != 1 || nativeRuns != 0 || checks != 0 {
		t.Fatal("ingress bypassed the dispatcher or published a blocked Check for execution")
	}
	verifyAutomatedExecutionChecks(t, pool, store, cfg, verify, account, events[0].ExecutionID)
}
