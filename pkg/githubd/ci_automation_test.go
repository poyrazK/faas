// ADR-1341: only App-signed HTTP ingress can capture automated execution intent.
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
	binding, err := store.PutGitHubBinding(t.Context(), account, "source", ci.GitHubBindingRequest{RevisionID: revision.ID, InstallationID: 12, RepositoryID: 34, PushBranches: []string{"main"}})
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
	if _, err := ci.SaveAutomationPolicy(t.Context(), cfg, store, verify, account, binding.ID, ci.AutomationRequest{ProfileID: profile.ID, PushBranches: []string{"main"}}); err != nil {
		t.Fatal(err)
	}
	secret := []byte("synthetic app webhook secret")
	body := []byte(`{"ref":"refs/heads/main","after":"` + strings.Repeat("b", 40) + `","installation":{"id":12},"repository":{"id":34,"full_name":"gregale/ci"}}`)
	srv := &Server{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), WebhookSecret: secret, Deliveries: &recordingDeliveryStore{}, RecordCI: func(ctx context.Context, d WebhookDelivery) error {
		return ci.RecordVerifiedGitHubDelivery(ctx, cfg, store, d.DeliveryID, d.EventType, d.Payload)
	}}
	for _, signed := range []bool{false, true, true, true} {
		r := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(string(body)))
		r.Header.Set("X-GitHub-Delivery", uuid.NewString())
		r.Header.Set("X-GitHub-Event", "push")
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
	if worked, err := ci.AdmitAutomationOnce(t.Context(), cfg, store, verify); err != nil || !worked {
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
