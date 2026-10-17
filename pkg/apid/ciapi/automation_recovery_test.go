// ADR-1580: operator recovery is scoped, idempotent and independent of compute.
package ciapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/ciapi"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type automationRecoveryAPI struct {
	handler                          ciapi.Handler
	pool                             *pgxpool.Pool
	account, other, binding, eventID string
}

func newAutomationRecoveryAPI(t *testing.T) *automationRecoveryAPI {
	t.Helper()
	f := &automationRecoveryAPI{pool: pgtest.OpenMigrated(t), account: uuid.NewString(), other: uuid.NewString()}
	store := ci.NewPostgresStore(f.pool)
	for _, id := range []string{f.account, f.other} {
		if _, err := f.pool.Exec(t.Context(), "INSERT INTO accounts(id,email,plan) VALUES ($1,$2,'pro')", id, id+"@example.invalid"); err != nil {
			t.Fatal(err)
		}
	}
	image, job := "registry.example/ci@sha256:"+strings.Repeat("a", 64), uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO jobs(id,account_id,kind,name,image_ref,ram_mb,task_timeout_s,max_parallelism,retry_max,env_overrides,command,image_materialization_status,image_resolved_digest,image_storage_key)
 VALUES ($1,$2,'batch','recovery-test',$3,$4,600,1,0,'{}',$5,'ready',$6,'private/recovery-rootfs.ext4')`, job, f.account, image, api.CIJobsRAMMB, []string{ci.JobsRunnerPath, ci.JobsRunnerProtocol}, strings.Split(image, "@")[1]); err != nil {
		t.Fatal(err)
	}
	revision, err := store.PutRevision(t.Context(), f.account, "recovery", ci.Definition{Version: 1, Repository: "private/automation-recovery", Jobs: map[string]ci.Job{"test": {Image: image, Steps: []ci.Step{{Name: "unit", Run: "private synthetic command; never executed"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := store.PutGitHubBinding(t.Context(), f.account, "recovery", ci.GitHubBindingRequest{RevisionID: revision.ID, InstallationID: 12, RepositoryID: 34, PushBranches: []string{"main"}})
	if err != nil {
		t.Fatal(err)
	}
	f.binding = binding.ID
	cfg := ci.ConfigFromEnv(func(name string) string {
		switch name {
		case ci.AccountsEnv:
			return f.account + "," + f.other
		case ci.InternalEnv, ci.GitHubEnv, ci.AutomationEnv:
			return "1"
		}
		return ""
	})
	verify := func(context.Context, string, int64, int64, string) error { return nil }
	profile, err := ci.SaveExecutionProfile(t.Context(), cfg, store, verify, f.account, "recovery", ci.ExecutionProfileRequest{RevisionID: revision.ID, BindingID: binding.ID, Templates: map[string]string{"test": job}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ci.SaveAutomationPolicy(t.Context(), cfg, store, verify, f.account, binding.ID, ci.AutomationRequest{ProfileID: profile.ID, PushBranches: []string{"main"}}); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"ref": "refs/heads/main", "after": strings.Repeat("b", 40),
		"installation": map[string]any{"id": 12}, "repository": map[string]any{"id": 34, "full_name": "private/automation-recovery"}})
	if err := ci.RecordVerifiedGitHubDelivery(t.Context(), cfg, store, uuid.NewString(), "push", body); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(t.Context(), "UPDATE ci_automation_events SET status='dead',attempts=$2,last_error='unavailable' WHERE account_id=$1 RETURNING id", f.account, api.CIGitHubMaxAttempts).Scan(&f.eventID); err != nil {
		t.Fatal(err)
	}
	f.handler = ciapi.Handler{Config: cfg, Store: store, VerifyGitHub: verify}
	return f
}

func (f *automationRecoveryAPI) call(t *testing.T, action ciapi.Action, account, binding, event, key string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
	r.SetPathValue("binding", binding)
	r.SetPathValue("id", event)
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	action(f.handler, w, r, state.Account{ID: account})
	return w
}

func TestCIAutomationRecoveryAPIValidationScopeAndIdempotency(t *testing.T) {
	f := newAutomationRecoveryAPI(t)
	for _, owner := range []struct{ account, binding, event string }{{f.other, f.binding, f.eventID}, {f.account, uuid.NewString(), f.eventID}, {f.account, f.binding, uuid.NewString()}} {
		for _, action := range []ciapi.Action{ciapi.Handler.AutomationEventStatus, ciapi.Handler.RetryAutomationEvent} {
			if w := f.call(t, action, owner.account, owner.binding, owner.event, "foreign", ci.AutomationRetryRequest{}); w.Code != http.StatusNotFound {
				t.Fatalf("source ownership escaped: %d %s", w.Code, w.Body)
			}
		}
	}
	for _, body := range []any{map[string]any{}, map[string]any{"expected_retry_generation": -1}, map[string]any{"expected_retry_generation": 4}, map[string]any{"expected_retry_generation": 0, "commit_sha": strings.Repeat("c", 40)}} {
		if w := f.call(t, ciapi.Handler.RetryAutomationEvent, f.account, f.binding, f.eventID, "invalid", body); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid retry body accepted: %d %s", w.Code, w.Body)
		}
	}
	if w := f.call(t, ciapi.Handler.RetryAutomationEvent, f.account, f.binding, f.eventID, "", ci.AutomationRetryRequest{}); w.Code != http.StatusBadRequest {
		t.Fatal("retry omitted idempotency identity")
	}
	w := f.call(t, ciapi.Handler.AutomationEventStatus, f.account, f.binding, f.eventID, "", nil)
	var view ci.AutomationEventSummary
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &view) != nil || !view.Retryable || view.RetryGeneration != 0 {
		t.Fatalf("exhausted status unavailable: %d %s", w.Code, w.Body)
	}
	audits := 0
	f.handler.Audit = func(_ context.Context, kind string, owner *string, data map[string]any) {
		if kind != "ci.automation.retry_requested" || owner == nil || *owner != f.account || data["event_id"] != f.eventID || data["binding_id"] != f.binding {
			t.Error("recovery audit lost source scope")
		}
		audits++
	}
	first := f.call(t, ciapi.Handler.RetryAutomationEvent, f.account, f.binding, f.eventID, "response-loss", ci.AutomationRetryRequest{})
	replay := f.call(t, ciapi.Handler.RetryAutomationEvent, f.account, f.binding, f.eventID, "response-loss", ci.AutomationRetryRequest{})
	if first.Code != http.StatusAccepted || replay.Code != http.StatusAccepted || replay.Header().Get("Idempotency-Replayed") != "true" || !bytes.Equal(first.Body.Bytes(), replay.Body.Bytes()) || audits != 1 {
		t.Fatalf("recovery response replay: %d %s / %d %s audits=%d", first.Code, first.Body, replay.Code, replay.Body, audits)
	}
	if w := f.call(t, ciapi.Handler.RetryAutomationEvent, f.account, f.binding, f.eventID, "another-key", ci.AutomationRetryRequest{}); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "ci_automation_retry_conflict") {
		t.Fatal("pending recovery accepted another request")
	}
	w = f.call(t, ciapi.Handler.AutomationEventStatus, f.account, f.binding, f.eventID, "", nil)
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.Status != "pending" || view.RetryGeneration != 1 || view.LastRetry == nil || view.Attempts != 0 {
		t.Fatal("status omitted the committed recovery")
	}
	for _, private := range []string{"snapshot", "key_digest", "lease_token", "installation_id", "private/", "command"} {
		if strings.Contains(w.Body.String(), private) || strings.Contains(first.Body.String(), private) {
			t.Fatalf("recovery exposed %s", private)
		}
	}
	var executions, jobs int
	if err := f.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM ci_executions),(SELECT count(*) FROM job_runs)").Scan(&executions, &jobs); err != nil || executions != 0 || jobs != 0 {
		t.Fatal("API recovery bypassed disabled compute")
	}
}

func TestCIAutomationRecoveryAPIHonorsGatesAndRedactsProviderErrors(t *testing.T) {
	f := newAutomationRecoveryAPI(t)
	cfg := f.handler.Config
	for _, name := range []string{"internal", "github", "automation"} {
		f.handler.Config = cfg
		switch name {
		case "internal":
			f.handler.Config.Enabled = false
		case "github":
			f.handler.Config.GitHubEnabled = false
		case "automation":
			f.handler.Config.AutomationEnabled = false
		}
		if w := f.call(t, ciapi.Handler.RetryAutomationEvent, f.account, f.binding, f.eventID, name, ci.AutomationRetryRequest{}); w.Code != http.StatusNotFound {
			t.Fatalf("retry escaped %s gate", name)
		}
		if name != "internal" {
			if w := f.call(t, ciapi.Handler.AutomationEventStatus, f.account, f.binding, f.eventID, "", nil); w.Code != http.StatusOK {
				t.Fatal("diagnostics require new admission authority")
			}
		}
	}
	f.handler.Config = cfg
	for _, denied := range []bool{true, false} {
		f.handler.VerifyGitHub = func(context.Context, string, int64, int64, string) error {
			if denied {
				return ci.ErrGitHubAccess
			}
			return errors.New("private-provider-secret")
		}
		w := f.call(t, ciapi.Handler.RetryAutomationEvent, f.account, f.binding, f.eventID, "provider", ci.AutomationRetryRequest{})
		want := http.StatusServiceUnavailable
		if denied {
			want = http.StatusForbidden
		}
		if w.Code != want || strings.Contains(w.Body.String(), "private-provider-secret") {
			t.Fatalf("provider authority/redaction: %d %s", w.Code, w.Body)
		}
	}
}
